package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/workspace"
)

const (
	maxProcessLogChars       = 400_000
	maxFinishedProcesses     = 100
	maxRunningProcesses      = 32
	maxWorkspaceProcesses    = 8
	finishedProcessRetention = 24 * time.Hour
)

var (
	ErrProcessRunning = errors.New("process is still running")
	ErrProcessLimit   = errors.New("background process limit reached")
)

type ProcessInfo struct {
	ID          string  `json:"id"`
	ExecutionID string  `json:"execution_id,omitempty"`
	PID         int     `json:"pid"`
	Command     string  `json:"command"`
	CWD         string  `json:"cwd"`
	StartedAt   string  `json:"started_at"`
	Running     bool    `json:"running"`
	ExitCode    *int    `json:"exit_code"`
	Signal      *string `json:"signal"`
}

type StartResult struct {
	ID          string `json:"id"`
	ExecutionID string `json:"execution_id,omitempty"`
	PID         int    `json:"pid"`
	Command     string `json:"command"`
	CWD         string `json:"cwd"`
	StartedAt   string `json:"started_at"`
}

type OutputResult struct {
	ID       string  `json:"id"`
	Running  bool    `json:"running"`
	ExitCode *int    `json:"exit_code"`
	Signal   *string `json:"signal"`
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
}

type StopResult struct {
	ID            string `json:"id"`
	Force         bool   `json:"force,omitempty"`
	AlreadyExited bool   `json:"already_exited,omitempty"`
}

type managedProcess struct {
	mu         sync.Mutex
	workspace  string
	id         string
	command    string
	cwd        string
	startedAt  string
	cmd        *exec.Cmd
	stdout     *logBuffer
	stderr     *logBuffer
	exitCode   *int
	signal     *string
	finishedAt time.Time
	execution  *ExecutionRun
	done       chan struct{}
}

type ProcessManager struct {
	workspaces          *workspace.Manager
	shell               *Manager
	mu                  sync.RWMutex
	processes           map[string]*managedProcess
	order               []string
	maxFinished         int
	maxRunning          int
	maxWorkspaceRunning int
	retention           time.Duration
	executions          *ExecutionHub
}

type logBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func NewProcessManager(workspaces *workspace.Manager, shell *Manager) *ProcessManager {
	return &ProcessManager{workspaces: workspaces, shell: shell, processes: map[string]*managedProcess{}, maxFinished: maxFinishedProcesses, maxRunning: maxRunningProcesses, maxWorkspaceRunning: maxWorkspaceProcesses, retention: finishedProcessRetention}
}

func NewProcessManagerWithExecutions(workspaces *workspace.Manager, shell *Manager, executions *ExecutionHub) *ProcessManager {
	manager := NewProcessManager(workspaces, shell)
	manager.executions = executions
	return manager
}

func (m *ProcessManager) Start(ctx context.Context, workspaceID, command string) (StartResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workspaceItem, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return StartResult{}, err
	}
	workspaceID = workspaceItem.ID
	cwd, plan, err := m.shell.prepareBackgroundCommand(ctx, workspaceID, command)
	if err != nil {
		return StartResult{}, err
	}
	processCtx := context.WithoutCancel(ctx)
	cmd, err := commandForPlatform(processCtx, plan.Effective)
	if err != nil {
		return StartResult{}, err
	}
	cmd.Dir = cwd
	cmd.Env = shellEnvironment(ctx, commandSearchPath(plan, m.workspaces.ShellPath()))
	configureCommandLifecycle(cmd)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return StartResult{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdoutPipe.Close()
		return StartResult{}, err
	}
	closePipes := func() {
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
	}
	id, err := processID()
	if err != nil {
		closePipes()
		return StartResult{}, err
	}
	process := &managedProcess{
		workspace: workspaceID, id: id, command: plan.Effective, cwd: cwd,
		startedAt: time.Now().UTC().Format(time.RFC3339Nano), cmd: cmd, stdout: &logBuffer{}, stderr: &logBuffer{}, done: make(chan struct{}),
	}
	m.mu.Lock()
	m.pruneLocked(time.Now().UTC())
	if err := m.checkStartLimitLocked(workspaceID); err != nil {
		m.mu.Unlock()
		closePipes()
		return StartResult{}, err
	}
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		closePipes()
		return StartResult{}, err
	}
	m.processes[id] = process
	m.order = append(m.order, id)
	m.mu.Unlock()
	var execution *ExecutionRun
	if m.executions != nil {
		metadata := executionMetadata(ctx)
		execution = m.executions.Begin(ExecutionInput{WorkspaceID: workspaceID, Tool: "start_process", Command: plan.Effective, RequestedCommand: command, EffectiveCommand: plan.Effective, SecurityCommand: plan.Security, CWD: cwd, Shell: commandShellLanguage(ctx), Source: metadata.Source, CallID: metadata.CallID, SessionHash: metadata.SessionHash, ReceivedByInstanceID: metadata.ReceivedByInstanceID, ExecutedByInstanceID: metadata.ExecutedByInstanceID})
		process.mu.Lock()
		process.execution = execution
		process.mu.Unlock()
	}

	var outputWG sync.WaitGroup
	outputWG.Add(2)
	go copyProcessLog(&outputWG, process.stdout, execution, "stdout", stdoutPipe)
	go copyProcessLog(&outputWG, process.stderr, execution, "stderr", stderrPipe)
	go func() {
		outputWG.Wait()
		waitErr := cmd.Wait()
		process.mu.Lock()
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code := exitErr.ExitCode()
			process.exitCode = &code
		} else if waitErr == nil {
			code := 0
			process.exitCode = &code
		} else {
			code := -1
			process.exitCode = &code
		}
		if state := cmd.ProcessState; state != nil {
			if text := signalFromState(state.String()); text != "" {
				process.signal = &text
			}
		}
		process.finishedAt = time.Now().UTC()
		exitCode := cloneInt(process.exitCode)
		signal := cloneString(process.signal)
		process.mu.Unlock()
		if execution != nil {
			status := ExecutionStatusSuccess
			if signal != nil {
				status = ExecutionStatusCancelled
			} else if exitCode == nil || *exitCode != 0 {
				status = ExecutionStatusFailed
			}
			execution.Finish(status, exitCode, false)
		}
		m.mu.Lock()
		m.pruneLocked(time.Now().UTC())
		m.mu.Unlock()
		close(process.done)
	}()

	executionID := ""
	if execution != nil {
		executionID = execution.ID()
	}
	return StartResult{ID: id, ExecutionID: executionID, PID: cmd.Process.Pid, Command: plan.Effective, CWD: cwd, StartedAt: process.startedAt}, nil
}

func (m *ProcessManager) Status(workspaceID, id string) ([]ProcessInfo, error) {
	item, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID = item.ID
	m.mu.Lock()
	m.pruneLocked(time.Now().UTC())
	items := make([]*managedProcess, 0, len(m.processes))
	for _, item := range m.processes {
		if m.processWorkspaceMatches(item.workspace, workspaceID) && (id == "" || item.id == id) {
			items = append(items, item)
		}
	}
	m.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].startedAt < items[j].startedAt })
	result := make([]ProcessInfo, 0, len(items))
	for _, item := range items {
		result = append(result, item.info())
	}
	return result, nil
}

func (m *ProcessManager) Output(workspaceID, id string, tailChars int) (OutputResult, error) {
	item, err := m.get(workspaceID, id)
	if err != nil {
		return OutputResult{}, err
	}
	if tailChars <= 0 {
		tailChars = 40_000
	}
	if tailChars > 200_000 {
		return OutputResult{}, errors.New("tail_chars must be <= 200000")
	}
	item.mu.Lock()
	running := item.exitCode == nil
	exitCode := cloneInt(item.exitCode)
	signal := cloneString(item.signal)
	item.mu.Unlock()
	return OutputResult{
		ID: id, Running: running, ExitCode: exitCode, Signal: signal,
		Stdout: item.stdout.tail(tailChars), Stderr: item.stderr.tail(tailChars),
	}, nil
}

func (m *ProcessManager) Stop(workspaceID, id string, force bool) (StopResult, error) {
	item, err := m.get(workspaceID, id)
	if err != nil {
		return StopResult{}, err
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.exitCode != nil {
		return StopResult{ID: id, AlreadyExited: true}, nil
	}
	if item.cmd.Process == nil {
		return StopResult{}, errors.New("process handle is unavailable")
	}
	if err := signalCommandTree(item.cmd, force); err != nil {
		return StopResult{}, err
	}
	return StopResult{ID: id, Force: force}, nil
}

func (m *ProcessManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.RLock()
	items := make([]*managedProcess, 0, len(m.processes))
	for _, item := range m.processes {
		item.mu.Lock()
		running := item.exitCode == nil
		item.mu.Unlock()
		if running {
			items = append(items, item)
		}
	}
	m.mu.RUnlock()
	if len(items) == 0 {
		return nil
	}
	var shutdownErr error
	for _, item := range items {
		if err := signalCommandTree(item.cmd, false); err != nil && !errors.Is(err, os.ErrProcessDone) {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	if waitProcesses(ctx, items) {
		return shutdownErr
	}
	for _, item := range items {
		item.mu.Lock()
		running := item.exitCode == nil
		item.mu.Unlock()
		if running {
			if err := signalCommandTree(item.cmd, true); err != nil && !errors.Is(err, os.ErrProcessDone) {
				shutdownErr = errors.Join(shutdownErr, err)
			}
		}
	}
	forceCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !waitProcesses(forceCtx, items) {
		shutdownErr = errors.Join(shutdownErr, errors.New("background processes did not stop"))
	}
	return shutdownErr
}

func waitProcesses(ctx context.Context, items []*managedProcess) bool {
	for _, item := range items {
		if item.done == nil {
			continue
		}
		select {
		case <-item.done:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

func (m *ProcessManager) Clear(workspaceID string) (int, error) {
	item, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return 0, err
	}
	workspaceID = item.ID
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(time.Now().UTC())
	cleared := 0
	for id, item := range m.processes {
		if !m.processWorkspaceMatches(item.workspace, workspaceID) {
			continue
		}
		item.mu.Lock()
		finished := item.exitCode != nil
		item.mu.Unlock()
		if finished {
			delete(m.processes, id)
			cleared++
		}
	}
	m.compactOrderLocked()
	return cleared, nil
}

func (m *ProcessManager) ClearFinished(workspaceID, id string) error {
	item, err := m.get(workspaceID, id)
	if err != nil {
		return err
	}
	item.mu.Lock()
	running := item.exitCode == nil
	item.mu.Unlock()
	if running {
		return ErrProcessRunning
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.processes[id]; current != item {
		return fmt.Errorf("unknown process id: %s", id)
	}
	delete(m.processes, id)
	m.compactOrderLocked()
	return nil
}

func (m *ProcessManager) get(workspaceID, id string) (*managedProcess, error) {
	workspace, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID = workspace.ID
	m.mu.Lock()
	m.pruneLocked(time.Now().UTC())
	item := m.processes[id]
	m.mu.Unlock()
	if item == nil || !m.processWorkspaceMatches(item.workspace, workspaceID) {
		return nil, fmt.Errorf("unknown process id: %s", id)
	}
	return item, nil
}

func (m *ProcessManager) processWorkspaceMatches(processWorkspaceID, workspaceID string) bool {
	if processWorkspaceID == workspaceID {
		return true
	}
	if m == nil || m.workspaces == nil {
		return false
	}
	canonical, err := m.workspaces.CanonicalID(processWorkspaceID)
	return err == nil && canonical == workspaceID
}

func (m *ProcessManager) checkStartLimitLocked(workspaceID string) error {
	maxRunning := m.maxRunning
	if maxRunning <= 0 {
		maxRunning = maxRunningProcesses
	}
	maxWorkspace := m.maxWorkspaceRunning
	if maxWorkspace <= 0 {
		maxWorkspace = maxWorkspaceProcesses
	}
	running, workspaceRunning := 0, 0
	for _, item := range m.processes {
		item.mu.Lock()
		active := item.exitCode == nil
		itemWorkspace := item.workspace
		item.mu.Unlock()
		if !active {
			continue
		}
		running++
		if m.processWorkspaceMatches(itemWorkspace, workspaceID) {
			workspaceRunning++
		}
	}
	if running >= maxRunning {
		return fmt.Errorf("%w: runtime allows at most %d running processes", ErrProcessLimit, maxRunning)
	}
	if workspaceRunning >= maxWorkspace {
		return fmt.Errorf("%w: workspace allows at most %d running processes", ErrProcessLimit, maxWorkspace)
	}
	return nil
}

func (m *ProcessManager) pruneLocked(now time.Time) {
	if m == nil || len(m.order) == 0 {
		return
	}
	retention := m.retention
	if retention <= 0 {
		retention = finishedProcessRetention
	}
	maxFinished := m.maxFinished
	if maxFinished <= 0 {
		maxFinished = maxFinishedProcesses
	}
	finished := 0
	for _, id := range m.order {
		item := m.processes[id]
		if item == nil {
			continue
		}
		item.mu.Lock()
		isFinished := item.exitCode != nil
		finishedAt := item.finishedAt
		item.mu.Unlock()
		if isFinished && !finishedAt.IsZero() && now.Sub(finishedAt) > retention {
			delete(m.processes, id)
			continue
		}
		if isFinished {
			finished++
		}
	}
	remove := finished - maxFinished
	if remove > 0 {
		for _, id := range m.order {
			if remove == 0 {
				break
			}
			item := m.processes[id]
			if item == nil {
				continue
			}
			item.mu.Lock()
			isFinished := item.exitCode != nil
			item.mu.Unlock()
			if isFinished {
				delete(m.processes, id)
				remove--
			}
		}
	}
	m.compactOrderLocked()
}

func (m *ProcessManager) compactOrderLocked() {
	kept := m.order[:0]
	for _, id := range m.order {
		if m.processes[id] != nil {
			kept = append(kept, id)
		}
	}
	m.order = kept
}

func (p *managedProcess) info() ProcessInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	pid := 0
	if p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	return ProcessInfo{
		ID: p.id, ExecutionID: processExecutionID(p.execution), PID: pid, Command: p.command, CWD: p.cwd, StartedAt: p.startedAt,
		Running: p.exitCode == nil, ExitCode: cloneInt(p.exitCode), Signal: cloneString(p.signal),
	}
}

func copyProcessLog(wg *sync.WaitGroup, target *logBuffer, execution *ExecutionRun, stream string, reader io.Reader) {
	defer wg.Done()
	writers := []io.Writer{target}
	if execution != nil {
		writers = append(writers, execution.Writer(stream))
	}
	_, _ = io.Copy(io.MultiWriter(writers...), reader)
}

func processExecutionID(execution *ExecutionRun) string {
	if execution == nil {
		return ""
	}
	return execution.ID()
}

func (b *logBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if len(b.data) > maxProcessLogChars {
		b.truncated = true
		b.data = append([]byte(nil), b.data[len(b.data)-maxProcessLogChars:]...)
	}
	return len(data), nil
}

func (b *logBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data), b.truncated
}

func (b *logBuffer) tail(chars int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if chars >= len(b.data) {
		return string(b.data)
	}
	return string(b.data[len(b.data)-chars:])
}

func processID() (string, error) {
	return idgen.New("proc", 8)
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func signalFromState(value string) string {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "interrupt"):
		return "interrupt"
	case strings.Contains(lower, "killed"):
		return "killed"
	default:
		return ""
	}
}
