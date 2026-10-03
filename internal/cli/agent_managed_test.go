package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tools"
)

type cliManagedAgentHandle struct {
	mu    sync.Mutex
	phase managedagent.BackendPhase
}

type cliManagedAgentBackend struct{}

func (cliManagedAgentBackend) ID() managedagent.BackendID { return "cli-agent-test" }
func (cliManagedAgentBackend) Ready(context.Context) (managedagent.Readiness, error) {
	return managedagent.Readiness{Available: true, Capacity: managedagent.Capacity{MaxParallel: 5}}, nil
}
func (cliManagedAgentBackend) Spawn(context.Context, managedagent.BackendSpawnRequest) (managedagent.Handle, error) {
	return &cliManagedAgentHandle{phase: managedagent.BackendPhaseIdle}, nil
}
func (cliManagedAgentBackend) Send(_ context.Context, raw managedagent.Handle, _ managedagent.Message) error {
	handle := raw.(*cliManagedAgentHandle)
	handle.mu.Lock()
	handle.phase = managedagent.BackendPhaseIdle
	handle.mu.Unlock()
	return nil
}
func (cliManagedAgentBackend) Snapshot(_ context.Context, raw managedagent.Handle) (managedagent.BackendSnapshot, error) {
	handle := raw.(*cliManagedAgentHandle)
	handle.mu.Lock()
	defer handle.mu.Unlock()
	return managedagent.BackendSnapshot{Phase: handle.phase}, nil
}
func (cliManagedAgentBackend) Cancel(context.Context, managedagent.Handle) error { return nil }
func (cliManagedAgentBackend) Close(context.Context, managedagent.Handle) error  { return nil }

func TestManagedAgentCLIControlsCanonicalRuntimeManager(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = configformat.SetRootPath(previous) }()

	toolRuntime := tools.NewRuntime()
	defer toolRuntime.CompletionHooks.Stop()
	if err := toolRuntime.Agents.RegisterBackend(cliManagedAgentBackend{}); err != nil {
		t.Fatal(err)
	}
	workspace, err := toolRuntime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mcpOwner, err := managedagent.NewMCPController("mcp-owned-agent")
	if err != nil {
		t.Fatal(err)
	}
	mcpOwned, err := toolRuntime.Agents.Spawn(t.Context(), mcpOwner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: workspace.ID, Prompt: "created by MCP owner", Backend: "cli-agent-test",
	}})
	if err != nil {
		t.Fatal(err)
	}

	dispatcher := application.NewDispatcher()
	if err := application.BindManagedAgentOperations(dispatcher, application.NewManagedAgentService(toolRuntime.Agents, toolRuntime.Workspaces)); err != nil {
		t.Fatal(err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{
		Operations: dispatcher,
		Reload:     func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil },
		Status:     func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} },
		Shutdown:   func() {},
		ClearLogs:  func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	listJSON := executeRequestCommand(t, root, []string{"agent", "list", "--json"})
	var listed []managedagent.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(listJSON)), &listed); err != nil {
		t.Fatalf("agent list json=%q err=%v", listJSON, err)
	}
	if len(listed) != 1 || listed[0].ID != mcpOwned.ID {
		t.Fatalf("operator CLI did not see canonical MCP-owned agent: %#v", listed)
	}

	getJSON := executeRequestCommand(t, root, []string{"agent", "get", string(mcpOwned.ID), "--json"})
	var got managedagent.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(getJSON)), &got); err != nil || got.ID != mcpOwned.ID || got.State != managedagent.StateIdle {
		t.Fatalf("agent get=%q value=%#v err=%v", getJSON, got, err)
	}
	sentJSON := executeRequestCommand(t, root, []string{"agent", "send", string(mcpOwned.ID), "operator follow-up", "--json"})
	var sent managedagent.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(sentJSON)), &sent); err != nil || sent.ID != mcpOwned.ID || sent.Turn != 2 {
		t.Fatalf("agent send=%q value=%#v err=%v", sentJSON, sent, err)
	}
	cancelJSON := executeRequestCommand(t, root, []string{"agent", "cancel", string(mcpOwned.ID), "--json"})
	var cancelled managedagent.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(cancelJSON)), &cancelled); err != nil || cancelled.State != managedagent.StateCancelled {
		t.Fatalf("agent cancel=%q value=%#v err=%v", cancelJSON, cancelled, err)
	}

	spawnJSON := executeRequestCommand(t, root, []string{
		"agent", "spawn", "--workspace", workspace.ID, "--backend", "cli-agent-test", "--json", "created by operator CLI",
	})
	var spawned managedagent.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(spawnJSON)), &spawned); err != nil || spawned.ID == "" || spawned.WorkspaceID != workspace.ID {
		t.Fatalf("agent spawn=%q value=%#v err=%v", spawnJSON, spawned, err)
	}
	waitJSON := executeRequestCommand(t, root, []string{"agent", "wait", string(spawned.ID), "--timeout", "10ms", "--json"})
	var waited managedagent.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(waitJSON)), &waited); err != nil || waited.ID != spawned.ID {
		t.Fatalf("agent wait=%q value=%#v err=%v", waitJSON, waited, err)
	}

	completion, remaining, err := newRootCommand().Find([]string{"agent", "completion", "list"})
	if err != nil || completion == nil || completion.CommandPath() != "cm agent completion list" || len(remaining) != 0 {
		t.Fatalf("agent completion subtree changed: command=%v remaining=%v err=%v", completion, remaining, err)
	}
}

func TestManagedAgentCLIRejectsUnboundedWaitAndDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	output, err := executeRequestCommandError(root, []string{"agent", "wait", "agent_0123456789abcdef", "--timeout", "11s"})
	if err == nil || !strings.Contains(err.Error(), "timeout must be between 0 and 10s") {
		t.Fatalf("unbounded wait output=%q err=%v", output, err)
	}

	output, err = executeRequestCommandError(root, []string{"agent", "list"})
	if err == nil || !strings.Contains(err.Error(), "start CodeMCP with 'cm up' or 'cm serve' first") {
		t.Fatalf("runtime-unavailable guidance output=%q err=%v", output, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".runtime-control.json")); !os.IsNotExist(statErr) {
		t.Fatalf("managed-agent CLI unexpectedly created runtime-control state: %v", statErr)
	}
}
