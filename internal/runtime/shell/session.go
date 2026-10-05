package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/integrations/rtk"
	statepkg "go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/workspace"
)

const (
	sessionStateVersion   = 1
	maxHistory            = 50
	defaultCommandTimeout = 120 * time.Second
)

type SessionState struct {
	Version        int      `json:"version"`
	WorkspaceID    string   `json:"workspace_id"`
	CWD            string   `json:"cwd"`
	StartedAt      string   `json:"started_at"`
	UpdatedAt      string   `json:"updated_at"`
	RecentCommands []string `json:"recent_commands"`
}

type Status struct {
	Active         bool     `json:"active"`
	CWD            string   `json:"cwd"`
	StartedAt      string   `json:"started_at"`
	RecentCommands []string `json:"recent_commands"`
}

type ExecResult struct {
	Command         string `json:"command"`
	CWD             string `json:"cwd"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	ExitCode        int    `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
}

type Manager struct {
	workspaces *workspace.Manager
	executions *ExecutionHub
	mu         sync.Mutex
	sessions   map[string]*session
	rtk        *rtk.Manager
	resolver   *ProviderResolver
	timeout    time.Duration
}

type session struct {
	mu    sync.Mutex
	state SessionState
}

var (
	setLocationPattern = regexp.MustCompile(`(?i)^(?:Set-Location|sl)\s+(.+?)(?:\s*;\s*|\s*&&\s*|$)`)
	cdPattern          = regexp.MustCompile(`(?i)^cd(?:\s+(.+?))?(?:\s*;\s*|\s*&&\s*|$)`)
	pushdPattern       = regexp.MustCompile(`(?i)^pushd\s+(.+?)(?:\s*;\s*|\s*&&\s*|$)`)
)

func DefaultStateRoot() string {
	return configformat.RootPath()
}

func NewManager(workspaces *workspace.Manager, root string) *Manager {
	return NewManagerWithExecutions(workspaces, root, NewExecutionHub())
}

func NewManagerWithExecutions(workspaces *workspace.Manager, _ string, executions *ExecutionHub) *Manager {
	if executions == nil {
		executions = NewExecutionHub()
	}
	return &Manager{workspaces: workspaces, executions: executions, sessions: map[string]*session{}, resolver: NewProviderResolver(), timeout: defaultCommandTimeout}
}

func (m *Manager) Executions() *ExecutionHub {
	if m == nil {
		return nil
	}
	return m.executions
}

func (m *Manager) Status(workspaceID string) (Status, error) {
	item, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return Status{}, err
	}
	workspaceID = item.ID
	current, err := m.session(workspaceID, item.Path)
	if err != nil {
		return Status{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	return statusFromState(current.state), nil
}

func (m *Manager) Reset(workspaceID, path string) (Status, error) {
	item, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return Status{}, err
	}
	workspaceID = item.ID
	target := item.Path
	if strings.TrimSpace(path) != "" {
		target, err = m.workspaces.ResolvePath(workspaceID, item.Path, path, true)
		if err != nil {
			return Status{}, err
		}
		info, err := os.Stat(target)
		if err != nil {
			return Status{}, err
		}
		if !info.IsDir() {
			return Status{}, fmt.Errorf("shell reset path is not a directory: %s", target)
		}
	}
	current, err := m.session(workspaceID, item.Path)
	if err != nil {
		return Status{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	current.state = SessionState{Version: sessionStateVersion, WorkspaceID: workspaceID, CWD: target, StartedAt: now, UpdatedAt: now, RecentCommands: []string{}}
	if err := m.save(current.state); err != nil {
		return Status{}, err
	}
	return statusFromState(current.state), nil
}

func (m *Manager) Exec(ctx context.Context, workspaceID, command string) (ExecResult, error) {
	item, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return ExecResult{}, err
	}
	workspaceID = item.ID
	current, err := m.session(workspaceID, item.Path)
	if err != nil {
		return ExecResult{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()

	baseCWD, err := m.resolveDirectory(workspaceID, item.Path, current.state.CWD)
	if err != nil {
		return ExecResult{}, err
	}
	current.state.CWD = baseCWD
	cwd, effective, err := m.applyCWDDirectives(workspaceID, baseCWD, command)
	if err != nil {
		return ExecResult{}, err
	}
	provider := Provider{}
	if strings.TrimSpace(effective) == "" {
		provider, err = m.resolveSessionProvider(ctx)
		if err != nil {
			return ExecResult{}, err
		}
		effective = pwdCommandForProvider(provider)
	}
	requestedEffective := effective
	plan, err := m.prepareCommand(ctx, requestedEffective)
	if err != nil {
		return ExecResult{}, err
	}
	if err := m.workspaces.ValidateShellCommandContext(ctx, workspaceID, cwd, plan.Security); err != nil {
		return ExecResult{}, err
	}
	if strings.TrimSpace(provider.Executable) == "" {
		provider, err = m.resolveSessionProvider(ctx)
		if err != nil {
			return ExecResult{}, err
		}
	}
	current.state.CWD = cwd
	current.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	current.state.RecentCommands = append(current.state.RecentCommands, tracepkg.SanitizeCommand(requestedEffective))
	if len(current.state.RecentCommands) > maxHistory {
		current.state.RecentCommands = append([]string(nil), current.state.RecentCommands[len(current.state.RecentCommands)-maxHistory:]...)
	}

	metadata := executionMetadata(ctx)
	source := metadata.Source
	if source == "" {
		source = executionSource(ctx)
	}
	run, err := m.executions.Begin(ExecutionInput{
		WorkspaceID: workspaceID, Tool: "run_command", Command: plan.Effective, RequestedCommand: command, EffectiveCommand: plan.Effective, SecurityCommand: plan.Security, CWD: cwd, Shell: providerLanguage(ctx, provider), Source: source,
		CallID: metadata.CallID, SessionHash: metadata.SessionHash, ReceivedByInstanceID: metadata.ReceivedByInstanceID, ExecutedByInstanceID: metadata.ExecutedByInstanceID,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("persist execution start: %w", err)
	}
	result, err := runOnce(ctx, plan.Effective, cwd, m.timeout, run, provider, mergeExecutablePath(provider.Path, commandSearchPath(plan, m.workspaces.ShellPath())))
	if saveErr := m.save(current.state); saveErr != nil && err == nil {
		return ExecResult{}, saveErr
	}
	return result, err
}

func (m *Manager) ValidateBackgroundCommand(ctx context.Context, workspaceID, command string) (string, error) {
	cwd, _, err := m.prepareBackgroundCommand(ctx, workspaceID, command)
	return cwd, err
}

func (m *Manager) PreviewCommand(ctx context.Context, workspaceID, command string, background bool) (CommandPreview, error) {
	if background {
		cwd, plan, err := m.prepareBackgroundCommand(ctx, workspaceID, command)
		if err != nil {
			return CommandPreview{}, err
		}
		return CommandPreview{Requested: plan.Requested, Effective: plan.Effective, Security: plan.Security, CWD: cwd}, nil
	}
	item, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return CommandPreview{}, err
	}
	workspaceID = item.ID
	current, err := m.session(workspaceID, item.Path)
	if err != nil {
		return CommandPreview{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	baseCWD, err := m.resolveDirectory(workspaceID, item.Path, current.state.CWD)
	if err != nil {
		return CommandPreview{}, err
	}
	cwd, effective, err := m.applyCWDDirectives(workspaceID, baseCWD, command)
	if err != nil {
		return CommandPreview{}, err
	}
	if strings.TrimSpace(effective) == "" {
		provider, providerErr := m.resolveSessionProvider(ctx)
		if providerErr != nil {
			return CommandPreview{}, providerErr
		}
		effective = pwdCommandForProvider(provider)
	}
	plan, err := m.prepareCommand(ctx, effective)
	if err != nil {
		return CommandPreview{}, err
	}
	if err := m.workspaces.ValidateShellCommandContext(ctx, workspaceID, cwd, plan.Security); err != nil {
		return CommandPreview{}, err
	}
	return CommandPreview{Requested: plan.Requested, Effective: plan.Effective, Security: plan.Security, CWD: cwd}, nil
}

func (m *Manager) prepareBackgroundCommand(ctx context.Context, workspaceID, command string) (string, commandPlan, error) {
	item, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return "", commandPlan{}, err
	}
	workspaceID = item.ID
	current, err := m.session(workspaceID, item.Path)
	if err != nil {
		return "", commandPlan{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()

	cwd, err := m.resolveDirectory(workspaceID, item.Path, current.state.CWD)
	if err != nil {
		return "", commandPlan{}, err
	}
	current.state.CWD = cwd
	effectiveCWD, effective, err := m.applyCWDDirectives(workspaceID, cwd, command)
	if err != nil {
		return "", commandPlan{}, err
	}
	if strings.TrimSpace(effective) != strings.TrimSpace(command) || filepath.Clean(effectiveCWD) != filepath.Clean(cwd) {
		return "", commandPlan{}, errors.New("background process command must not contain cwd-changing directives; change the shell cwd first")
	}
	plan, err := m.prepareCommand(ctx, effective)
	if err != nil {
		return "", commandPlan{}, err
	}
	if err := m.workspaces.ValidateShellCommandContext(ctx, workspaceID, cwd, plan.Security); err != nil {
		return "", commandPlan{}, err
	}
	return cwd, plan, nil
}

func (m *Manager) session(workspaceID, workspaceRoot string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[workspaceID]; existing != nil {
		return existing, nil
	}
	state, err := m.load(workspaceID, workspaceRoot)
	if err != nil {
		return nil, err
	}
	value := &session{state: state}
	m.sessions[workspaceID] = value
	return value, nil
}

func (m *Manager) load(workspaceID, workspaceRoot string) (SessionState, error) {
	path, pathErr := m.statePath(workspaceID)
	if pathErr != nil {
		return SessionState{}, pathErr
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var state SessionState
		if json.Unmarshal(data, &state) == nil && (state.Version == 0 || state.Version == sessionStateVersion) && state.WorkspaceID == workspaceID && strings.TrimSpace(state.CWD) != "" {
			legacy := state.Version == 0
			state.Version = sessionStateVersion
			resolved, resolveErr := m.resolveDirectory(workspaceID, workspaceRoot, state.CWD)
			if resolveErr == nil {
				state.CWD = resolved
				if state.RecentCommands == nil {
					state.RecentCommands = []string{}
				}
				if len(state.RecentCommands) > maxHistory {
					state.RecentCommands = append([]string(nil), state.RecentCommands[len(state.RecentCommands)-maxHistory:]...)
				}
				if legacy {
					if err := m.save(state); err != nil {
						return SessionState{}, err
					}
				}
				return state, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return SessionState{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	state := SessionState{Version: sessionStateVersion, WorkspaceID: workspaceID, CWD: workspaceRoot, StartedAt: now, UpdatedAt: now, RecentCommands: []string{}}
	if err := m.save(state); err != nil {
		return SessionState{}, err
	}
	return state, nil
}

func (m *Manager) save(state SessionState) error {
	path, err := m.statePath(state.WorkspaceID)
	if err != nil {
		return err
	}
	state.Version = sessionStateVersion
	return statepkg.WriteJSONAtomic(path, state, 0600)
}

func (m *Manager) statePath(workspaceID string) (string, error) {
	local, err := m.workspaces.LocalState(workspaceID)
	if err != nil {
		return "", err
	}
	return local.StatePath("shell.json")
}

func (m *Manager) resolveDirectory(workspaceID, workspaceRoot, input string) (string, error) {
	resolved, err := m.workspaces.ResolvePath(workspaceID, workspaceRoot, input, true)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("shell cwd is not a directory: %s", resolved)
	}
	return resolved, nil
}

func (m *Manager) applyCWDDirectives(workspaceID, currentCWD, command string) (string, string, error) {
	cwd := currentCWD
	rest := strings.TrimSpace(command)
	for i := 0; i < 8; i++ {
		var target string
		var matched string
		switch {
		case setLocationPattern.MatchString(rest):
			match := setLocationPattern.FindStringSubmatch(rest)
			target = match[1]
			matched = match[0]
		case cdPattern.MatchString(rest):
			match := cdPattern.FindStringSubmatch(rest)
			if len(match) > 1 {
				target = match[1]
			}
			matched = match[0]
		case pushdPattern.MatchString(rest):
			match := pushdPattern.FindStringSubmatch(rest)
			target = match[1]
			matched = match[0]
		default:
			return cwd, rest, nil
		}
		target = stripQuotes(target)
		if target != "" && target != "-" && target != "~" {
			resolved, err := m.workspaces.ResolvePath(workspaceID, cwd, target, true)
			if err != nil {
				return "", "", err
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return "", "", err
			}
			if !info.IsDir() {
				return "", "", fmt.Errorf("cwd target is not a directory: %s", resolved)
			}
			cwd = resolved
		}
		rest = strings.TrimSpace(strings.TrimPrefix(rest, matched))
	}
	return cwd, rest, nil
}

func statusFromState(state SessionState) Status {
	recent := append([]string(nil), state.RecentCommands...)
	for index := range recent {
		recent[index] = tracepkg.SanitizeCommand(recent[index])
	}
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	return Status{Active: true, CWD: state.CWD, StartedAt: state.StartedAt, RecentCommands: recent}
}

func runOnce(ctx context.Context, command, cwd string, timeout time.Duration, execution *ExecutionRun, provider Provider, shellPath []string) (ExecResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd, err := commandForProvider(runCtx, command, provider)
	if err != nil {
		return ExecResult{}, errors.Join(err, execution.Finish(ExecutionStatusFailed, nil, false))
	}
	cmd.Dir = cwd
	cmd.Env = shellEnvironment(ctx, shellPath)
	configureCommandLifecycle(cmd)
	stdout, stderr := &logBuffer{}, &logBuffer{}
	cmd.Stdout = io.MultiWriter(stdout, execution.Writer("stdout"))
	cmd.Stderr = io.MultiWriter(stderr, execution.Writer("stderr"))
	err = cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ExecResult{}, errors.Join(ctxErr, execution.Finish(ExecutionStatusCancelled, nil, false))
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return ExecResult{}, errors.Join(fmt.Errorf("command timed out after %s", timeout), execution.Finish(ExecutionStatusTimedOut, nil, true))
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return ExecResult{}, errors.Join(err, execution.Finish(ExecutionStatusFailed, nil, false))
		}
		exitCode = exitErr.ExitCode()
	}
	status := ExecutionStatusSuccess
	if exitCode != 0 {
		status = ExecutionStatusFailed
	}
	finishErr := execution.Finish(status, &exitCode, false)
	stdoutText, stdoutTruncated := stdout.snapshot()
	stderrText, stderrTruncated := stderr.snapshot()
	result := ExecResult{Command: tracepkg.SanitizeCommand(command), CWD: cwd, Stdout: strings.TrimSpace(stdoutText), Stderr: strings.TrimSpace(stderrText), StdoutTruncated: stdoutTruncated, StderrTruncated: stderrTruncated, ExitCode: exitCode, TimedOut: false}
	if finishErr != nil {
		return result, finishErr
	}
	return result, nil
}

func commandForProvider(ctx context.Context, command string, provider Provider) (*exec.Cmd, error) {
	if granted, ok := controlguard.ApprovalFromContext(ctx); ok {
		if strings.TrimSpace(command) != strings.TrimSpace(granted.Invocation.Command) {
			return nil, errors.New("approved control-plane command does not match shell invocation")
		}
		executable, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve approved control-plane executable: %w", err)
		}
		return exec.CommandContext(ctx, executable, granted.Invocation.Args...), nil
	}
	if strings.TrimSpace(provider.Executable) == "" {
		return nil, &ShellUnavailableError{GOOS: runtime.GOOS}
	}
	switch provider.Kind {
	case ProviderGitBash:
		return exec.CommandContext(ctx, provider.Executable, "--noprofile", "--norc", "-c", command), nil
	case ProviderPowerShell7:
		return exec.CommandContext(ctx, provider.Executable, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command), nil
	case ProviderWindowsPowerShell:
		return exec.CommandContext(ctx, provider.Executable, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", transpileCompoundOperators(command)), nil
	default:
		return exec.CommandContext(ctx, provider.Executable, "-c", command), nil
	}
}

func providerLanguage(ctx context.Context, provider Provider) string {
	if _, ok := controlguard.ApprovalFromContext(ctx); ok {
		return ""
	}
	return provider.Language
}

func (m *Manager) resolveSessionProvider(ctx context.Context) (Provider, error) {
	if _, ok := controlguard.ApprovalFromContext(ctx); ok {
		return Provider{}, nil
	}
	if m == nil || m.resolver == nil {
		return Provider{}, errors.New("shell provider resolver is unavailable")
	}
	return m.resolver.Resolve(m.workspaces.ShellPath())
}

func shellMarkdownLanguage(shell string) string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(shell)))
	switch base {
	case "bash", "bash.exe":
		return "bash"
	case "zsh", "zsh.exe":
		return "zsh"
	case "fish", "fish.exe":
		return "fish"
	case "sh", "sh.exe", "dash", "dash.exe", "ash", "ash.exe":
		return "sh"
	case "pwsh", "pwsh.exe", "powershell", "powershell.exe":
		return "powershell"
	case "cmd", "cmd.exe":
		return "batch"
	default:
		return "shell"
	}
}

func transpileCompoundOperators(command string) string {
	if !strings.Contains(command, "&&") && !strings.Contains(command, "||") {
		return command
	}
	type token struct {
		kind  string
		value string
	}
	tokens := make([]token, 0)
	var current strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(command); {
		char := command[i]
		if char == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteByte(char)
			i++
			continue
		}
		if char == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteByte(char)
			i++
			continue
		}
		if !inSingle && !inDouble && i+1 < len(command) {
			op := command[i : i+2]
			if op == "&&" || op == "||" {
				if text := strings.TrimSpace(current.String()); text != "" {
					tokens = append(tokens, token{kind: "text", value: text})
				}
				tokens = append(tokens, token{kind: op, value: op})
				current.Reset()
				i += 2
				continue
			}
		}
		current.WriteByte(char)
		i++
	}
	if text := strings.TrimSpace(current.String()); text != "" {
		tokens = append(tokens, token{kind: "text", value: text})
	}
	if len(tokens) <= 1 {
		return command
	}
	result := tokens[0].value + "; $__chatgptMcpSuccess = $?"
	for i := 1; i+1 < len(tokens); i += 2 {
		next := tokens[i+1].value
		if tokens[i].kind == "&&" {
			result += "; if ($__chatgptMcpSuccess) { " + next + "; $__chatgptMcpSuccess = $? }"
		} else {
			result += "; if (-not $__chatgptMcpSuccess) { " + next + "; $__chatgptMcpSuccess = $? }"
		}
	}
	return result
}

func stripQuotes(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'`)
}

func pwdCommandForProvider(provider Provider) string {
	if provider.Language == "powershell" {
		return "(Get-Location).Path"
	}
	return "pwd"
}
