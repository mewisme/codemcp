package shell

import (
	"context"
	"encoding/hex"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessStartUsesCanonicalRTKRewritePlan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script RTK fixture")
	}
	shellManager, workspaceID, _ := newShellTestManager(t)
	shellManager.ConfigureRTK(true, writeFakeRTK(t))
	manager := NewProcessManagerWithExecutions(shellManager.workspaces, shellManager, shellManager.executions)

	ctx := WithExecutionMetadata(context.Background(), ExecutionMetadata{Source: "admin"})
	result, err := manager.Start(ctx, workspaceID, "printf source-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Command != "rtk printf source-a" {
		t.Fatalf("start=%#v", result)
	}
	manager.mu.Lock()
	process := manager.processes[result.ID]
	manager.mu.Unlock()
	if process == nil {
		t.Fatal("managed process missing")
	}
	select {
	case <-process.done:
	case <-time.After(2 * time.Second):
		t.Fatal("background process did not finish")
	}
	items, err := manager.Status(workspaceID, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Command != "rtk printf source-a" {
		t.Fatalf("process status=%#v", items)
	}
	snapshot, err := shellManager.executions.Get(workspaceID, result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	info := snapshot.Execution
	if info.RequestedCommand != "printf source-a" || info.EffectiveCommand != "rtk printf source-a" || info.SecurityCommand != "printf source-a" || info.Source != "admin" {
		t.Fatalf("execution identity=%#v", info)
	}
	if strings.TrimSpace(snapshot.Stdout) != "source-a" {
		t.Fatalf("stdout=%q stderr=%q", snapshot.Stdout, snapshot.Stderr)
	}
}

func TestProcessIDUsesCompactHex(t *testing.T) {
	id, err := processID()
	if err != nil {
		t.Fatal(err)
	}
	value := strings.TrimPrefix(id, "proc_")
	if len(value) != 16 {
		t.Fatalf("process id=%q", id)
	}
	if _, err := hex.DecodeString(value); err != nil {
		t.Fatalf("process id is not hex: %q", id)
	}
}

func TestProcessManagerPrunesFinishedHistory(t *testing.T) {
	now := time.Now().UTC()
	code := 0
	manager := &ProcessManager{processes: map[string]*managedProcess{}, maxFinished: 2, retention: time.Hour}
	add := func(id string, finishedAt time.Time, running bool) {
		process := &managedProcess{id: id, finishedAt: finishedAt, stdout: &logBuffer{}, stderr: &logBuffer{}}
		if !running {
			value := code
			process.exitCode = &value
		}
		manager.processes[id] = process
		manager.order = append(manager.order, id)
	}
	add("expired", now.Add(-2*time.Hour), false)
	add("old", now.Add(-30*time.Minute), false)
	add("middle", now.Add(-20*time.Minute), false)
	add("recent", now.Add(-10*time.Minute), false)
	add("running", time.Time{}, true)

	manager.pruneLocked(now)
	for _, id := range []string{"expired", "old"} {
		if manager.processes[id] != nil {
			t.Fatalf("process %s was not pruned", id)
		}
	}
	for _, id := range []string{"middle", "recent", "running"} {
		if manager.processes[id] == nil {
			t.Fatalf("process %s was pruned unexpectedly", id)
		}
	}
	if len(manager.order) != 3 {
		t.Fatalf("order = %#v", manager.order)
	}
}
