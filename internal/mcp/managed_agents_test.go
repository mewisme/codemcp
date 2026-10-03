package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/tools"
)

type managedAgentMCPHandle struct {
	mu    sync.Mutex
	phase managedagent.BackendPhase
}

type managedAgentMCPBackend struct{}

func (managedAgentMCPBackend) ID() managedagent.BackendID { return "mcp-agent-test" }
func (managedAgentMCPBackend) Ready(context.Context) (managedagent.Readiness, error) {
	return managedagent.Readiness{Available: true, Capacity: managedagent.Capacity{MaxParallel: 5}}, nil
}
func (managedAgentMCPBackend) Spawn(context.Context, managedagent.BackendSpawnRequest) (managedagent.Handle, error) {
	return &managedAgentMCPHandle{phase: managedagent.BackendPhaseIdle}, nil
}
func (managedAgentMCPBackend) Send(_ context.Context, raw managedagent.Handle, _ managedagent.Message) error {
	handle := raw.(*managedAgentMCPHandle)
	handle.mu.Lock()
	handle.phase = managedagent.BackendPhaseIdle
	handle.mu.Unlock()
	return nil
}
func (managedAgentMCPBackend) Snapshot(_ context.Context, raw managedagent.Handle) (managedagent.BackendSnapshot, error) {
	handle := raw.(*managedAgentMCPHandle)
	handle.mu.Lock()
	defer handle.mu.Unlock()
	return managedagent.BackendSnapshot{Phase: handle.phase}, nil
}
func (managedAgentMCPBackend) Cancel(context.Context, managedagent.Handle) error { return nil }
func (managedAgentMCPBackend) Close(context.Context, managedagent.Handle) error  { return nil }

func TestManagedAgentMCPToolsIsolateSessionsAndRejectNestedSpawn(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	toolRuntime := tools.NewRuntime()
	defer toolRuntime.CompletionHooks.Stop()
	if err := toolRuntime.Agents.RegisterBackend(managedAgentMCPBackend{}); err != nil {
		t.Fatal(err)
	}
	workspace, err := toolRuntime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntimeWithProfile(toolRuntime, BaseProfile())
	parentA := tools.WithMCPSessionID(context.Background(), "parent-a")
	parentB := tools.WithMCPSessionID(context.Background(), "parent-b")

	spawn := callManagedAgentMCP(t, runtime, parentA, tools.AgentSpawnToolName, map[string]any{
		"workspace_id": workspace.ID,
		"prompt":       "inspect one independent subsystem",
		"backend":      "mcp-agent-test",
	})
	if spawn.IsError {
		t.Fatalf("session A spawn failed: %#v", spawn)
	}
	var child managedagent.Snapshot
	decodeManagedAgentStructured(t, spawn.StructuredContent, &child)
	if child.ID == "" || child.WorkspaceID != workspace.ID {
		t.Fatalf("spawned child=%#v", child)
	}

	listA := callManagedAgentMCP(t, runtime, parentA, tools.AgentListToolName, map[string]any{})
	if listA.IsError {
		t.Fatalf("session A list failed: %#v", listA)
	}
	var owned []managedagent.Snapshot
	decodeManagedAgentStructured(t, listA.StructuredContent, &owned)
	if len(owned) != 1 || owned[0].ID != child.ID {
		t.Fatalf("session A list=%#v", owned)
	}

	listB := callManagedAgentMCP(t, runtime, parentB, tools.AgentListToolName, map[string]any{})
	if listB.IsError {
		t.Fatalf("session B list failed: %#v", listB)
	}
	var foreign []managedagent.Snapshot
	decodeManagedAgentStructured(t, listB.StructuredContent, &foreign)
	if len(foreign) != 0 {
		t.Fatalf("session B enumerated session A agents: %#v", foreign)
	}
	for _, name := range []string{tools.AgentWaitToolName, tools.AgentCancelToolName} {
		args := map[string]any{"agent_id": string(child.ID)}
		if name == tools.AgentWaitToolName {
			args["timeout_ms"] = 0
		}
		result := callManagedAgentMCP(t, runtime, parentB, name, args)
		if !result.IsError || !strings.Contains(result.Content[0].Text, managedagent.ErrAgentNotFound.Error()) {
			t.Fatalf("session B %s foreign agent result=%#v", name, result)
		}
	}

	// Refresh through the owner so the idle backend state becomes visible,
	// then prove send works only through the owning MCP session.
	wait := callManagedAgentMCP(t, runtime, parentA, tools.AgentWaitToolName, map[string]any{
		"agent_id": string(child.ID), "after_revision": int(child.Revision), "timeout_ms": 10,
	})
	if wait.IsError {
		t.Fatalf("owner wait failed: %#v", wait)
	}
	sent := callManagedAgentMCP(t, runtime, parentA, tools.AgentSendToolName, map[string]any{
		"agent_id": string(child.ID), "message": "follow up",
	})
	if sent.IsError {
		t.Fatalf("owner send failed: %#v", sent)
	}

	credential, err := toolRuntime.Agents.IssueClaim(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	childContext := tools.WithMCPSessionID(context.Background(), "child-session")
	claim := callManagedAgentMCP(t, runtime, childContext, tools.AgentClaimToolName, map[string]any{
		"agent_id": string(child.ID), "token": credential.Token(),
	})
	if claim.IsError {
		t.Fatalf("child claim failed: %#v", claim)
	}
	nested := callManagedAgentMCP(t, runtime, childContext, tools.AgentSpawnToolName, map[string]any{
		"workspace_id": workspace.ID,
		"prompt":       "attempt grandchild",
		"backend":      "mcp-agent-test",
	})
	if !nested.IsError || !strings.Contains(nested.Content[0].Text, "nested managed-agent delegation is disabled") {
		t.Fatalf("claimed child spawned nested agent: %#v", nested)
	}
}

func TestManagedAgentMCPToolsRequireTrustedSessionAndBoundWait(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	toolRuntime := tools.NewRuntime()
	defer toolRuntime.CompletionHooks.Stop()
	if err := toolRuntime.Agents.RegisterBackend(managedAgentMCPBackend{}); err != nil {
		t.Fatal(err)
	}
	workspace, err := toolRuntime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntimeWithProfile(toolRuntime, BaseProfile())

	noSession := callManagedAgentMCP(t, runtime, context.Background(), tools.AgentSpawnToolName, map[string]any{
		"workspace_id": workspace.ID, "prompt": "no session", "backend": "mcp-agent-test",
	})
	if !noSession.IsError || !strings.Contains(noSession.Content[0].Text, "trusted MCP session identity") {
		t.Fatalf("untrusted spawn=%#v", noSession)
	}

	trusted := tools.WithMCPSessionID(context.Background(), "bounded-wait")
	spawn := callManagedAgentMCP(t, runtime, trusted, tools.AgentSpawnToolName, map[string]any{
		"workspace_id": workspace.ID, "prompt": "bounded wait", "backend": "mcp-agent-test",
	})
	var child managedagent.Snapshot
	decodeManagedAgentStructured(t, spawn.StructuredContent, &child)
	tooLong := callManagedAgentMCP(t, runtime, trusted, tools.AgentWaitToolName, map[string]any{
		"agent_id": string(child.ID), "timeout_ms": 10001,
	})
	if !tooLong.IsError {
		t.Fatalf("wait accepted timeout above 10 seconds: %#v", tooLong)
	}
}

func callManagedAgentMCP(t *testing.T, runtime *Runtime, ctx context.Context, name string, args map[string]any) tools.Result {
	t.Helper()
	value, err := runtime.Handle(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatalf("%s protocol call failed: %v", name, err)
	}
	result, ok := value.(tools.Result)
	if !ok {
		t.Fatalf("%s returned %T, want tools.Result", name, value)
	}
	return result
}

func decodeManagedAgentStructured(t *testing.T, value any, target any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode managed agent structured content: %v data=%s", err, data)
	}
}
