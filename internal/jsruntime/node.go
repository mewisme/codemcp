package jsruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type EvalResult struct {
	Output               string  `json:"output"`
	Value                *string `json:"value,omitempty"`
	ComputerUseAvailable bool    `json:"computer_use_available"`
}

type StatusResult struct {
	Persistent           bool   `json:"persistent"`
	ComputerUseAvailable bool   `json:"computer_use_available"`
	ComputerUseError     string `json:"computer_use_error,omitempty"`
}

type Manager struct {
	mu       sync.Mutex
	nodePath string
	sessions map[string]*worker
}

type worker struct {
	mu       sync.Mutex
	stderrMu sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	stderr   strings.Builder
	nextID   int64
}

type request struct {
	ID        int64  `json:"id"`
	Action    string `json:"action"`
	Code      string `json:"code,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type response struct {
	ID    int64           `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

const helperSource = `
const readline = require("node:readline");
const vm = require("node:vm");
const util = require("node:util");
const path = require("node:path");
const Module = require("node:module");
const realFs = require("node:fs");
const realFsp = require("node:fs/promises");
const { createRequire } = Module;

const workspaceRoot = realFs.realpathSync.native(path.resolve(process.env.CM_WORKSPACE_ROOT));
const projectRequire = createRequire(path.join(workspaceRoot, "package.json"));
const output = [];

function normalize(value) {
  return process.platform === "win32" ? value.toLowerCase() : value;
}

function withinWorkspace(candidate) {
  const relative = path.relative(normalize(workspaceRoot), normalize(candidate));
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

function canonicalForContainment(input, mustExist = false, base = process.cwd()) {
  const absolute = path.resolve(base, String(input));
  try {
    const resolved = realFs.realpathSync.native(absolute);
    if (!withinWorkspace(resolved)) throw new Error("path escapes registered workspace: " + resolved);
    return absolute;
  } catch (error) {
    if (mustExist) {
      if (error instanceof Error && error.message.startsWith("path escapes registered workspace:")) throw error;
      throw error;
    }
  }

  let current = absolute;
  const suffix = [];
  while (true) {
    try {
      realFs.lstatSync(current);
      break;
    } catch {
      const parent = path.dirname(current);
      if (parent === current) throw new Error("cannot resolve path ancestor: " + absolute);
      suffix.unshift(path.basename(current));
      current = parent;
    }
  }
  let resolved = realFs.realpathSync.native(current);
  for (const item of suffix) resolved = path.join(resolved, item);
  if (!withinWorkspace(resolved)) throw new Error("path escapes registered workspace: " + resolved);
  return absolute;
}

function guardOne(fn, mustExist = false) {
  return function(input, ...args) {
    canonicalForContainment(input, mustExist);
    return fn.call(this, input, ...args);
  };
}

function guardTwo(fn, sourceMustExist = true) {
  return function(source, destination, ...args) {
    canonicalForContainment(source, sourceMustExist);
    canonicalForContainment(destination, false);
    return fn.call(this, source, destination, ...args);
  };
}

function guardSymlink(fn) {
  return function(target, linkPath, ...args) {
    const linkAbsolute = canonicalForContainment(linkPath, false);
    canonicalForContainment(target, false, path.dirname(linkAbsolute));
    return fn.call(this, target, linkPath, ...args);
  };
}

const onePathExisting = new Set([
  "access", "accessSync", "chmod", "chmodSync", "chown", "chownSync", "lchmod", "lchmodSync",
  "lchown", "lchownSync", "lstat", "lstatSync", "openAsBlob", "opendir", "opendirSync",
  "readFile", "readFileSync", "readdir", "readdirSync", "readlink", "readlinkSync",
  "realpath", "realpathSync", "stat", "statSync", "statfs", "statfsSync", "truncate", "truncateSync",
  "unlink", "unlinkSync", "utimes", "utimesSync", "watch", "watchFile"
]);
const onePathCreate = new Set([
  "appendFile", "appendFileSync", "createReadStream", "createWriteStream", "existsSync",
  "mkdir", "mkdirSync", "mkdtemp", "mkdtempSync", "open", "openSync", "rm", "rmSync",
  "rmdir", "rmdirSync", "writeFile", "writeFileSync"
]);
const twoPath = new Set([
  "copyFile", "copyFileSync", "cp", "cpSync", "link", "linkSync", "rename", "renameSync"
]);
const deniedFs = new Set(["glob", "globSync"]);

function buildSafeFs(target) {
  return new Proxy(target, {
    get(object, property, receiver) {
      if (property === "promises") return safeFsp;
      if (typeof property !== "string") return Reflect.get(object, property, receiver);
      if (deniedFs.has(property)) return () => { throw new Error("fs." + property + " denied by workspace policy"); };
      const value = Reflect.get(object, property, receiver);
      if (typeof value !== "function") return value;
      if (onePathExisting.has(property)) return guardOne(value, true);
      if (onePathCreate.has(property)) return guardOne(value, false);
      if (twoPath.has(property)) return guardTwo(value, true);
      if (property === "symlink" || property === "symlinkSync") return guardSymlink(value);
      return value.bind(object);
    }
  });
}

function buildSafeFsp(target) {
  return new Proxy(target, {
    get(object, property, receiver) {
      if (typeof property !== "string") return Reflect.get(object, property, receiver);
      if (property === "glob") return () => Promise.reject(new Error("fs.promises.glob denied by workspace policy"));
      const value = Reflect.get(object, property, receiver);
      if (typeof value !== "function") return value;
      if (onePathExisting.has(property)) return guardOne(value, true);
      if (onePathCreate.has(property)) return guardOne(value, false);
      if (twoPath.has(property)) return guardTwo(value, true);
      if (property === "symlink") return guardSymlink(value);
      return value.bind(object);
    }
  });
}

let safeFsp;
const safeFs = buildSafeFs(realFs);
safeFsp = buildSafeFsp(realFsp);

const blocked = new Set([
  "child_process", "node:child_process",
  "cluster", "node:cluster",
  "worker_threads", "node:worker_threads",
  "module", "node:module",
  "vm", "node:vm",
  "inspector", "node:inspector"
]);

const originalLoad = Module._load;
Module._load = function(request, parent, isMain) {
  if (request === "fs" || request === "node:fs") return safeFs;
  if (request === "fs/promises" || request === "node:fs/promises") return safeFsp;
  if (request === "process" || request === "node:process") return processView;
  if (blocked.has(request)) throw new Error("require denied by workspace policy: " + request);
  return originalLoad.call(this, request, parent, isMain);
};

const originalChdir = process.chdir.bind(process);
function guardedChdir(target) {
  const resolved = canonicalForContainment(target, true);
  const info = realFs.statSync(resolved);
  if (!info.isDirectory()) throw new Error("process.chdir target is not a directory");
  originalChdir(resolved);
}
process.chdir = guardedChdir;

const processView = Object.freeze({
  argv: Object.freeze([...process.argv]),
  arch: process.arch,
  env: Object.freeze({ ...process.env }),
  pid: process.pid,
  platform: process.platform,
  release: Object.freeze({ ...process.release }),
  version: process.version,
  versions: Object.freeze({ ...process.versions }),
  cwd: () => process.cwd(),
  chdir: guardedChdir
});

function safeRequire(specifier) {
  const name = String(specifier);
  if (name === "process" || name === "node:process") return processView;
  if (name === "fs" || name === "node:fs") return safeFs;
  if (name === "fs/promises" || name === "node:fs/promises") return safeFsp;
  if (blocked.has(name)) throw new Error("require denied by workspace policy: " + name);
  return projectRequire(name);
}

const nodeRepl = { write: (value) => output.push(String(value)) };
const sandbox = {
  Buffer,
  process: processView,
  setTimeout,
  clearTimeout,
  setInterval,
  clearInterval,
  fetch,
  require: safeRequire,
  console: {
    log: (...values) => output.push(values.map((value) => util.inspect(value, { depth: 4 })).join(" "))
  },
  nodeRepl,
  __localCoderOutput: output
};

const context = vm.createContext(sandbox);
const computerUseAvailable = false;
const computerUseError = process.platform === "win32"
  ? "Computer Use is disabled inside the workspace-locked Go node_repl worker"
  : "Computer Use is only available on Windows";

function send(value) {
  process.stdout.write(JSON.stringify(value) + "\n");
}

async function handle(message) {
  if (message.action === "status") {
    return {
      persistent: true,
      computer_use_available: computerUseAvailable,
      computer_use_error: computerUseError
    };
  }
  if (message.action !== "eval") throw new Error("unknown node_repl worker action: " + message.action);
  if (!String(message.code || "").trim()) throw new Error("code is required for node_repl eval");

  output.length = 0;
  const timeout = Math.max(100, Math.min(60000, Number(message.timeout_ms || 30000)));
  const value = await vm.runInContext(
    "(async () => { " + message.code + "\n})()",
    context,
    { timeout }
  );
  return {
    output: output.join("\n"),
    ...(value === undefined ? {} : { value: util.inspect(value, { depth: 5, maxArrayLength: 100 }) }),
    computer_use_available: computerUseAvailable
  };
}

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", async (line) => {
  let message;
  try {
    message = JSON.parse(line);
    const data = await handle(message);
    send({ id: message.id, ok: true, data });
  } catch (error) {
    send({ id: message?.id ?? 0, ok: false, error: error instanceof Error ? error.message : String(error) });
  }
});
`

func NewManager() *Manager {
	return &Manager{sessions: map[string]*worker{}}
}

func (m *Manager) Eval(ctx context.Context, workspaceID, workspaceRoot, code string, timeout time.Duration) (EvalResult, error) {
	if strings.TrimSpace(code) == "" {
		return EvalResult{}, errors.New("code is required for node_repl eval")
	}
	if timeout < 100*time.Millisecond || timeout > 60*time.Second {
		return EvalResult{}, errors.New("timeout must be between 100ms and 60s")
	}
	current, err := m.session(workspaceID, workspaceRoot)
	if err != nil {
		return EvalResult{}, err
	}
	var value EvalResult
	if err := current.call(ctx, request{Action: "eval", Code: code, TimeoutMS: int(timeout / time.Millisecond)}, &value); err != nil {
		m.drop(workspaceID, current)
		return EvalResult{}, err
	}
	return value, nil
}

func (m *Manager) Status(ctx context.Context, workspaceID, workspaceRoot string) (StatusResult, error) {
	current, err := m.session(workspaceID, workspaceRoot)
	if err != nil {
		return StatusResult{}, err
	}
	var value StatusResult
	if err := current.call(ctx, request{Action: "status"}, &value); err != nil {
		m.drop(workspaceID, current)
		return StatusResult{}, err
	}
	return value, nil
}

func (m *Manager) Reset(workspaceID string) error {
	m.mu.Lock()
	current := m.sessions[workspaceID]
	delete(m.sessions, workspaceID)
	m.mu.Unlock()
	if current == nil {
		return nil
	}
	return current.close()
}

func (m *Manager) Close() error {
	m.mu.Lock()
	values := make([]*worker, 0, len(m.sessions))
	for _, current := range m.sessions {
		values = append(values, current)
	}
	m.sessions = map[string]*worker{}
	m.mu.Unlock()
	var first error
	for _, current := range values {
		if err := current.close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) session(workspaceID, workspaceRoot string) (*worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.sessions[workspaceID]; current != nil {
		return current, nil
	}
	node, err := m.node()
	if err != nil {
		return nil, err
	}
	current, err := startWorker(node, workspaceRoot)
	if err != nil {
		return nil, err
	}
	m.sessions[workspaceID] = current
	return current, nil
}

func (m *Manager) drop(workspaceID string, current *worker) {
	m.mu.Lock()
	if m.sessions[workspaceID] == current {
		delete(m.sessions, workspaceID)
	}
	m.mu.Unlock()
	_ = current.close()
}

func (m *Manager) node() (string, error) {
	if m.nodePath != "" {
		return m.nodePath, nil
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return "", errors.New("node not found")
	}
	help, err := exec.Command(node, "--help").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect node permission support: %w", err)
	}
	text := string(help)
	if !strings.Contains(text, "--permission") && !strings.Contains(text, "--experimental-permission") {
		return "", errors.New("node_repl requires a Node.js runtime with the permission model")
	}
	m.nodePath = node
	return node, nil
}

func startWorker(node, workspaceRoot string) (*worker, error) {
	root, err := filepath.EvalSymlinks(filepath.Clean(workspaceRoot))
	if err != nil {
		return nil, err
	}
	permissionFlag, err := nodePermissionFlag(node)
	if err != nil {
		return nil, err
	}
	descendants := filepath.Join(root, "*")
	args := []string{
		permissionFlag,
		"--allow-fs-read=" + root,
		"--allow-fs-read=" + descendants,
		"--allow-fs-write=" + root,
		"--allow-fs-write=" + descendants,
		"-e", helperSource,
	}
	cmd := exec.Command(node, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CM_WORKSPACE_ROOT="+root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	current := &worker{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdoutPipe)}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		buffer := make([]byte, 64*1024)
		scanner.Buffer(buffer, 512*1024)
		for scanner.Scan() {
			current.mu.Lock()
			if current.stderr.Len() < 512*1024 {
				current.stderr.WriteString(scanner.Text())
				current.stderr.WriteByte('\n')
			}
			current.mu.Unlock()
		}
	}()
	return current, nil
}

func nodePermissionFlag(node string) (string, error) {
	help, err := exec.Command(node, "--help").CombinedOutput()
	if err != nil {
		return "", err
	}
	text := string(help)
	if strings.Contains(text, "--permission") {
		return "--permission", nil
	}
	if strings.Contains(text, "--experimental-permission") {
		return "--experimental-permission", nil
	}
	return "", errors.New("node permission model is unavailable")
}

func (w *worker) call(ctx context.Context, value request, target any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextID++
	value.ID = w.nextID
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := w.stdin.Write(append(data, '\n')); err != nil {
		return w.workerError(err)
	}

	type readResult struct {
		line string
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		line, err := w.stdout.ReadString('\n')
		read <- readResult{line: line, err: err}
	}()

	var result readResult
	select {
	case <-ctx.Done():
		_ = w.closeUnlocked()
		return ctx.Err()
	case result = <-read:
	}
	if result.err != nil {
		return w.workerError(result.err)
	}
	var message response
	if err := json.Unmarshal([]byte(result.line), &message); err != nil {
		return w.workerError(fmt.Errorf("decode node worker response: %w", err))
	}
	if message.ID != value.ID {
		return w.workerError(fmt.Errorf("unexpected node worker response id %d", message.ID))
	}
	if !message.OK {
		return errors.New(message.Error)
	}
	if target == nil || len(message.Data) == 0 {
		return nil
	}
	return json.Unmarshal(message.Data, target)
}

func (w *worker) workerError(cause error) error {
	w.stderrMu.Lock()
	detail := strings.TrimSpace(w.stderr.String())
	w.stderrMu.Unlock()
	if detail == "" {
		return cause
	}
	return fmt.Errorf("%w: %s", cause, detail)
}

func (w *worker) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeUnlocked()
}

func (w *worker) closeUnlocked() error {
	if w.stdin != nil {
		_ = w.stdin.Close()
	}
	if w.cmd == nil || w.cmd.Process == nil {
		return nil
	}
	if w.cmd.ProcessState != nil && w.cmd.ProcessState.Exited() {
		return nil
	}
	if runtime.GOOS == "windows" {
		killErr := w.cmd.Process.Kill()
		waitErr := w.cmd.Wait()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return killErr
		}
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			return waitErr
		}
		return nil
	}
	_ = w.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- w.cmd.Wait() }()
	select {
	case <-time.After(500 * time.Millisecond):
		return w.cmd.Process.Kill()
	case <-done:
		return nil
	}
}
