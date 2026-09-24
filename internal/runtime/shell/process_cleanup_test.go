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
