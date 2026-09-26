package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/workspace"
)

func TestProcessManagerCloseSubscriptionsClosesTerminalSubscribers(t *testing.T) {
	manager := NewProcessManager(nil, nil)
	sub := manager.SubscribeTerminal()
	manager.CloseSubscriptions()
	select {
	case _, ok := <-sub.Events:
		if ok {
			t.Fatal("terminal subscription remained open after process subscription shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("process terminal subscription did not close")
	}
}

func TestProcessDiagnosticsExposeRunningAgeAndTerminalOverflow(t *testing.T) {
	manager := NewProcessManager(nil, nil)
	process := &managedProcess{id: "proc_diag", startedAt: time.Now().Add(-25 * time.Millisecond).UTC().Format(time.RFC3339Nano)}
	manager.mu.Lock()
	manager.processes[process.id] = process
	manager.order = append(manager.order, process.id)
	manager.mu.Unlock()
	sub := manager.SubscribeTerminal()
	defer manager.UnsubscribeTerminal(sub)
	for index := 0; index < terminalEventBuffer; index++ {
		sub.events <- BackgroundWorkTerminalEvent{ProcessID: "buffered"}
	}
	manager.publishTerminal(&managedProcess{id: "overflow", workspace: "ws_diag"}, ExecutionStatusSuccess, BackgroundTerminalExit, nil, nil, false)
	diagnostics := manager.Diagnostics()
	if diagnostics.Running != 1 || diagnostics.OldestRunningAgeMS < 1 || diagnostics.TerminalSubscribers != 1 || diagnostics.TerminalOverflowDropped != 1 {
		t.Fatalf("process diagnostics=%#v", diagnostics)
	}
}

func TestProcessManagerResolvesRelocatedWorkspaceAliases(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	parent := t.TempDir()
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	if err := os.Mkdir(oldRoot, 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	shell := NewManager(manager, filepath.Join(t.TempDir(), "shell-state"))
	processes := NewProcessManager(manager, shell)
	command := "printf relocate-process"
	if os.PathSeparator == '\\' {
		command = "Write-Output relocate-process"
	}
	started, err := processes.Start(context.Background(), item.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	relocated, err := manager.Relocate(item.ID, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if relocated.ID != item.ID {
		t.Fatalf("stable workspace id changed: got=%s want=%s", relocated.ID, item.ID)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := processes.Status(relocated.ID, started.ID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if len(status) != 1 {
			t.Fatalf("processes=%#v", status)
		}
		if !status[0].Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	output, err := processes.Output(relocated.ID, started.ID, 40000)
	if err != nil || !strings.Contains(output.Stdout, "relocate-process") {
		t.Fatalf("output=%#v err=%v", output, err)
	}
	if err := processes.ClearFinished(relocated.ID, started.ID); err != nil {
		t.Fatal(err)
	}
}

func TestClearFinishedProcessRejectsRunningAndDeletesFinished(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shell := NewManager(manager, filepath.Join(t.TempDir(), "shell-state"))
	processes := NewProcessManager(manager, shell)
	command := "sleep 0.2"
	if os.PathSeparator == '\\' {
		command = "Start-Sleep -Milliseconds 200"
	}
	started, err := processes.Start(t.Context(), item.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := processes.ClearFinished(item.ID, started.ID); !errors.Is(err, ErrProcessRunning) {
		t.Fatalf("running cleanup error=%v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := processes.Status(item.ID, started.ID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if len(status) == 1 && !status[0].Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := processes.ClearFinished(item.ID, started.ID); err != nil {
		t.Fatal(err)
	}
	status, err := processes.Status(item.ID, started.ID)
	if err != nil || len(status) != 0 {
		t.Fatalf("process still present=%#v err=%v", status, err)
	}
}

func TestProcessManagerEnforcesRunningLimits(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	processes := NewProcessManager(manager, NewManager(manager, filepath.Join(t.TempDir(), "shell-state")))
	processes.maxRunning = 2
	processes.maxWorkspaceRunning = 1
	processes.processes["first"] = &managedProcess{workspace: first.ID}
	if err := processes.checkStartLimitLocked(first.ID); !errors.Is(err, ErrProcessLimit) {
		t.Fatalf("workspace limit error=%v", err)
	}
	if err := processes.checkStartLimitLocked(second.ID); err != nil {
		t.Fatalf("second workspace unexpectedly limited: %v", err)
	}
	processes.processes["second"] = &managedProcess{workspace: second.ID}
	if err := processes.checkStartLimitLocked(second.ID); !errors.Is(err, ErrProcessLimit) {
		t.Fatalf("global limit error=%v", err)
	}
}

func TestProcessManagerShutdownStopsRunningProcess(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	processes := NewProcessManager(manager, NewManager(manager, filepath.Join(t.TempDir(), "shell-state")))
	command := "sleep 30"
	if os.PathSeparator == '\\' {
		command = "Start-Sleep -Seconds 30"
	}
	started, err := processes.Start(t.Context(), item.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := processes.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := processes.Status(item.ID, started.ID)
	if err != nil || len(status) != 1 || status[0].Running {
		t.Fatalf("process still running after shutdown: %#v err=%v", status, err)
	}
}

func TestProcessManagerPublishesCommittedNaturalTerminalTruth(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executions := NewExecutionHub()
	processes := NewProcessManagerWithExecutions(manager, NewManager(manager, filepath.Join(t.TempDir(), "shell-state")), executions)
	sub := processes.SubscribeTerminal()
	defer processes.UnsubscribeTerminal(sub)
	ctx := WithExecutionMetadata(t.Context(), ExecutionMetadata{SessionHash: "session-safe", CallID: "call-safe"})
	command := "printf terminal-ok"
	if os.PathSeparator == '\\' {
		command = "Write-Output terminal-ok"
	}
	started, err := processes.Start(ctx, item.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	var event BackgroundWorkTerminalEvent
	select {
	case event = <-sub.Events:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal event was not published")
	}
	if event.ProcessID != started.ID || event.ExecutionID != started.ExecutionID || event.Status != ExecutionStatusSuccess ||
		event.Reason != BackgroundTerminalExit || event.SessionHash != "session-safe" || event.CallID != "call-safe" ||
		event.ExitCode == nil || *event.ExitCode != 0 || event.FinishedAt == "" {
		t.Fatalf("terminal event=%#v", event)
	}
	status, err := processes.Status(item.ID, started.ID)
	if err != nil || len(status) != 1 || status[0].Running || status[0].ExitCode == nil || *status[0].ExitCode != 0 {
		t.Fatalf("process truth not committed before event: status=%#v err=%v", status, err)
	}
	snapshot, err := executions.Get(item.ID, started.ExecutionID)
	if err != nil || snapshot.Execution.Status != ExecutionStatusSuccess || snapshot.Execution.FinishedAt == "" || !strings.Contains(snapshot.Stdout, "terminal-ok") {
		t.Fatalf("execution truth not committed before event: snapshot=%#v err=%v", snapshot, err)
	}
}

func TestProcessManagerPublishesOneTerminalEventWhenStopRacesExit(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("race fixture uses POSIX shell timing")
	}
	if os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	processes := NewProcessManagerWithExecutions(manager, NewManager(manager, filepath.Join(t.TempDir(), "shell-state")), NewExecutionHub())
	sub := processes.SubscribeTerminal()
	defer processes.UnsubscribeTerminal(sub)
	started, err := processes.Start(t.Context(), item.ID, "sleep 0.05")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = processes.Stop(item.ID, started.ID, false) }()
	process, err := processes.get(item.ID, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		t.Fatal("process did not finish")
	}
	var event BackgroundWorkTerminalEvent
	select {
	case event = <-sub.Events:
	case <-time.After(time.Second):
		t.Fatal("terminal event was not published")
	}
	select {
	case duplicate := <-sub.Events:
		t.Fatalf("duplicate terminal event=%#v", duplicate)
	default:
	}
	if event.ProcessID != started.ID || (event.Reason != BackgroundTerminalStopped && event.Reason != BackgroundTerminalExit) {
		t.Fatalf("terminal event=%#v", event)
	}
}

func TestProcessManagerShutdownPublishesShutdownTerminalReason(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	processes := NewProcessManagerWithExecutions(manager, NewManager(manager, filepath.Join(t.TempDir(), "shell-state")), NewExecutionHub())
	sub := processes.SubscribeTerminal()
	defer processes.UnsubscribeTerminal(sub)
	command := "sleep 30"
	if os.PathSeparator == '\\' {
		command = "Start-Sleep -Seconds 30"
	}
	started, err := processes.Start(t.Context(), item.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := processes.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.Events:
		if event.ProcessID != started.ID || event.Status != ExecutionStatusCancelled || event.Reason != BackgroundTerminalShutdown {
			t.Fatalf("shutdown terminal event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown terminal event was not published")
	}
	select {
	case duplicate := <-sub.Events:
		t.Fatalf("duplicate terminal event=%#v", duplicate)
	default:
	}
}

func TestProcessManagerTerminalStreamDropsSlowSubscriberWithoutBlockingCompletion(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executions := NewExecutionHub()
	processes := NewProcessManagerWithExecutions(manager, NewManager(manager, filepath.Join(t.TempDir(), "shell-state")), executions)
	sub := processes.SubscribeTerminal()
	defer processes.UnsubscribeTerminal(sub)
	for index := 0; index < terminalEventBuffer; index++ {
		sub.events <- BackgroundWorkTerminalEvent{ProcessID: "blocked"}
	}
	command := "printf stream-done"
	if os.PathSeparator == '\\' {
		command = "Write-Output stream-done"
	}
	started, err := processes.Start(t.Context(), item.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	process, err := processes.get(item.ID, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		t.Fatal("slow subscriber blocked process completion")
	}
	if sub.Dropped() != 1 {
		t.Fatalf("dropped=%d, want 1", sub.Dropped())
	}
	select {
	case <-sub.Overflow:
	default:
		t.Fatal("slow subscriber drop was not observable")
	}
	snapshot, err := executions.Get(item.ID, started.ExecutionID)
	if err != nil || snapshot.Execution.Status != ExecutionStatusSuccess || !strings.Contains(snapshot.Stdout, "stream-done") {
		t.Fatalf("execution snapshot=%#v err=%v", snapshot, err)
	}
}

func TestProcessTerminalStatusClassifiesAllTerminalReasons(t *testing.T) {
	zero, one := 0, 1
	signal := "killed"
	tests := []struct {
		name   string
		timed  bool
		signal *string
		exit   *int
		intent BackgroundTerminalReason
		status string
		reason BackgroundTerminalReason
	}{
		{name: "exit", exit: &zero, status: ExecutionStatusSuccess, reason: BackgroundTerminalExit},
		{name: "failure", exit: &one, status: ExecutionStatusFailed, reason: BackgroundTerminalFailure},
		{name: "timeout", timed: true, exit: &one, status: ExecutionStatusTimedOut, reason: BackgroundTerminalTimeout},
		{name: "signal", signal: &signal, exit: &one, status: ExecutionStatusCancelled, reason: BackgroundTerminalSignal},
		{name: "stop", signal: &signal, exit: &one, intent: BackgroundTerminalStopped, status: ExecutionStatusCancelled, reason: BackgroundTerminalStopped},
		{name: "shutdown", signal: &signal, exit: &one, intent: BackgroundTerminalShutdown, status: ExecutionStatusCancelled, reason: BackgroundTerminalShutdown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, reason := processTerminalStatus(tc.timed, tc.signal, tc.exit, tc.intent)
			if status != tc.status || reason != tc.reason {
				t.Fatalf("status=%q reason=%q want status=%q reason=%q", status, reason, tc.status, tc.reason)
			}
		})
	}
}
