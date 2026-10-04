package tools

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/integrations"
	"go.mewis.me/codemcp/internal/integrations/caveman"
	"go.mewis.me/codemcp/internal/integrations/fanout"
	"go.mewis.me/codemcp/internal/integrations/ponytail"
	"go.mewis.me/codemcp/internal/workspace"
)

func newAdvancedRuntime(t *testing.T) (*Runtime, string, string) {
	t.Helper()
	root := t.TempDir()
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterWorkspaceTools(registry, workspaces)
	RegisterAdvancedTools(registry, workspaces)
	runtime := &Runtime{Registry: registry, Workspaces: workspaces, ponytailManager: ponytail.NewManager(true, ponytail.Full), cavemanManager: caveman.NewManager(true, caveman.Full), fanoutManager: fanout.NewManager(true, fanout.Auto)}
	if err := runtime.SyncIntegrations(integrations.Default()); err != nil {
		t.Fatal(err)
	}
	return runtime, item.ID, item.Path
}

func TestAdvancedToolCatalog(t *testing.T) {
	runtime, _, _ := newAdvancedRuntime(t)
	names := map[string]bool{}
	for _, schema := range runtime.List() {
		names[schema.Name] = true
	}
	for _, name := range []string{"node_repl", "ponytail_turn", "caveman_turn", "fanout_turn"} {
		if !names[name] {
			t.Fatalf("missing tool %q", name)
		}
	}
}

func TestFanoutToolIsolatesTrustedSessionsAndWorkspaces(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	first, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctxA := WithMCPSessionID(context.Background(), "fanout-session-a")
	ctxB := WithMCPSessionID(context.Background(), "fanout-session-b")
	call := func(ctx context.Context, workspaceID, prompt string) fanout.Result {
		t.Helper()
		result, callErr := runtime.Call(ctx, "fanout_turn", map[string]any{"workspace_id": workspaceID, "prompt": prompt})
		if callErr != nil || result.IsError {
			t.Fatalf("fanout call err=%v result=%#v", callErr, result)
		}
		return result.StructuredContent.(fanout.Result)
	}
	if value := call(ctxA, first.ID, "/fanout aggressive"); value.Mode != fanout.Aggressive {
		t.Fatalf("session A first=%#v", value)
	}
	if value := call(ctxB, first.ID, "continue"); value.Mode != fanout.Auto {
		t.Fatalf("session B leaked state=%#v", value)
	}
	if value := call(ctxA, second.ID, "/fanout conservative"); value.Mode != fanout.Conservative {
		t.Fatalf("second workspace=%#v", value)
	}
	if value := call(ctxA, first.ID, "continue"); value.Mode != fanout.Aggressive {
		t.Fatalf("first workspace lost state=%#v", value)
	}
	missing, err := runtime.Call(context.Background(), "fanout_turn", map[string]any{"workspace_id": first.ID, "prompt": "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if !missing.IsError || !strings.Contains(missing.Content[0].Text, "trusted MCP session") {
		t.Fatalf("missing-session result=%#v", missing)
	}
	spoofed, err := runtime.Call(ctxA, "fanout_turn", map[string]any{"workspace_id": first.ID, "prompt": "continue", "controller_id": "caller-value"})
	if err != nil {
		t.Fatal(err)
	}
	if !spoofed.IsError || !strings.Contains(spoofed.Content[0].Text, "unsupported fanout controller argument") {
		t.Fatalf("caller-controlled identity result=%#v", spoofed)
	}
}

func TestFanoutToolReloadInactiveSchemaAndAgentLifecycle(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Agents.RegisterBackend(claimToolBackend{}); err != nil {
		t.Fatal(err)
	}
	spawned, err := runtime.Agents.Spawn(t.Context(), managedagent.OperatorController(), managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{WorkspaceID: item.ID, Prompt: "keep running", Backend: "claim-test"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := integrations.Default()
	cfg.Fanout.Active = false
	cfg.Fanout.Mode = "conservative"
	if err := runtime.SyncIntegrations(cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Registry.Schema("fanout_turn"); !ok {
		t.Fatal("fanout controller tool disappeared while inactive")
	}
	ctx := WithMCPSessionID(context.Background(), "fanout-reload")
	result, err := runtime.Call(ctx, "fanout_turn", map[string]any{"workspace_id": item.ID, "prompt": "continue"})
	if err != nil || result.IsError {
		t.Fatalf("inactive fanout call err=%v result=%#v", err, result)
	}
	if value := result.StructuredContent.(fanout.Result); value.Active || value.Mode != fanout.Off {
		t.Fatalf("inactive fanout=%#v", value)
	}
	current, err := runtime.Agents.Get(context.Background(), managedagent.OperatorController(), spawned.ID)
	if err != nil || current.State != managedagent.StateWorking {
		t.Fatalf("reload changed managed agent=%#v err=%v", current, err)
	}
	cfg.Fanout.Active = true
	if err := runtime.SyncIntegrations(cfg); err != nil {
		t.Fatal(err)
	}
	result, err = runtime.Call(ctx, "fanout_turn", map[string]any{"workspace_id": item.ID, "prompt": "continue"})
	if err != nil || result.IsError {
		t.Fatalf("reactivated fanout call err=%v result=%#v", err, result)
	}
	if value := result.StructuredContent.(fanout.Result); !value.Active || value.Mode != fanout.Conservative {
		t.Fatalf("reactivated fanout=%#v", value)
	}
}

func TestFanoutToolSuppressesClaimedChild(t *testing.T) {
	runtime, workspaceID, _, spawned, credential := newClaimToolRuntime(t)
	childCtx := WithMCPSessionID(context.Background(), "fanout-managed-child")
	claimed, err := runtime.Call(childCtx, AgentClaimToolName, map[string]any{"agent_id": string(spawned.ID), "token": credential.Token()})
	if err != nil || claimed.IsError {
		t.Fatalf("claim err=%v result=%#v", err, claimed)
	}
	result, err := runtime.Call(childCtx, "fanout_turn", map[string]any{"workspace_id": workspaceID, "prompt": "/fanout aggressive"})
	if err != nil || result.IsError {
		t.Fatalf("child fanout err=%v result=%#v", err, result)
	}
	value := result.StructuredContent.(fanout.Result)
	if value.Active || value.Mode != fanout.Off || value.ActiveInstructions != "" || value.RefreshHint != "" {
		t.Fatalf("claimed child fanout=%#v", value)
	}
}

func TestFeatureToolsStayRegisteredWhileActiveStateChanges(t *testing.T) {
	runtime, workspaceID, _ := newAdvancedRuntime(t)
	first, err := runtime.Call(context.Background(), "caveman_turn", map[string]any{"workspace_id": workspaceID, "prompt": "continue"})
	if err != nil || first.IsError {
		t.Fatalf("default caveman call = %#v %v", first, err)
	}
	if value, ok := first.StructuredContent.(caveman.Result); !ok || !value.Active {
		t.Fatalf("default caveman result = %#v", first.StructuredContent)
	}
	integrationConfig := integrations.Default()
	integrationConfig.Ponytail.Active = false
	integrationConfig.Caveman.Active = false
	if err := runtime.SyncIntegrations(integrationConfig); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Registry.Schema("ponytail_turn"); !ok {
		t.Fatal("ponytail controller tool disappeared")
	}
	if _, ok := runtime.Registry.Schema("caveman_turn"); !ok {
		t.Fatal("caveman controller tool disappeared")
	}
	second, err := runtime.Call(context.Background(), "caveman_turn", map[string]any{"workspace_id": workspaceID, "prompt": "continue"})
	if err != nil || second.IsError {
		t.Fatalf("inactive caveman call = %#v %v", second, err)
	}
	if value, ok := second.StructuredContent.(caveman.Result); !ok || value.Active {
		t.Fatalf("inactive caveman result = %#v", second.StructuredContent)
	}
	integrationConfig.Caveman.Active = true
	if err := runtime.SyncIntegrations(integrationConfig); err != nil {
		t.Fatal(err)
	}
	third, err := runtime.Call(context.Background(), "caveman_turn", map[string]any{"workspace_id": workspaceID, "prompt": "continue"})
	if err != nil || third.IsError {
		t.Fatalf("reactivated caveman call = %#v %v", third, err)
	}
	if value, ok := third.StructuredContent.(caveman.Result); !ok || !value.Active {
		t.Fatalf("reactivated caveman result = %#v", third.StructuredContent)
	}
}

func TestCavemanToolReturnsBuiltInInstructions(t *testing.T) {
	runtime, workspaceID, _ := newAdvancedRuntime(t)
	integrationConfig := integrations.Default()
	integrationConfig.Caveman.Mode = "wenyan-full"
	if err := runtime.SyncIntegrations(integrationConfig); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(context.Background(), "caveman_turn", map[string]any{"workspace_id": workspaceID, "prompt": "continue", "action": "refresh"})
	if err != nil || result.IsError {
		t.Fatalf("caveman call = %#v %v", result, err)
	}
	value, ok := result.StructuredContent.(caveman.Result)
	if !ok || !value.Available || !value.Active || value.Mode != caveman.WenyanFull || !strings.Contains(value.ActiveInstructions, "CAVEMAN MODE ACTIVE") || !strings.Contains(value.ActiveInstructions, "| **wenyan-full** |") || strings.Contains(value.ActiveInstructions, "| **ultra** |") {
		t.Fatalf("caveman result = %#v", result.StructuredContent)
	}
}

func TestPonytailToolReturnsBuiltInInstructionsAndConfiguredMode(t *testing.T) {
	runtime, workspaceID, _ := newAdvancedRuntime(t)
	integrationConfig := integrations.Default()
	integrationConfig.Ponytail.Mode = "ultra"
	if err := runtime.SyncIntegrations(integrationConfig); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(context.Background(), "ponytail_turn", map[string]any{"workspace_id": workspaceID, "prompt": "continue"})
	if err != nil || result.IsError {
		t.Fatalf("ponytail call = %#v %v", result, err)
	}
	value, ok := result.StructuredContent.(ponytail.Result)
	if !ok || !value.Available || !value.Active || value.Mode != ponytail.Ultra || !strings.Contains(value.ActiveInstructions, "PONYTAIL MODE ACTIVE") || !strings.Contains(value.ActiveInstructions, "## The ladder") {
		t.Fatalf("ponytail result = %#v", result.StructuredContent)
	}
}

func TestNodeReplToolPersistsState(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	help, _ := exec.Command(node, "--help").CombinedOutput()
	if !strings.Contains(string(help), "--permission") && !strings.Contains(string(help), "--experimental-permission") {
		t.Skip("node permission model unavailable")
	}
	runtime, workspaceID, _ := newAdvancedRuntime(t)
	t.Cleanup(func() {
		result, err := runtime.Call(context.Background(), "node_repl", map[string]any{"workspace_id": workspaceID, "action": "reset"})
		if err != nil || result.IsError {
			t.Errorf("node_repl cleanup failed: result=%#v err=%v", result, err)
		}
	})
	first, err := runtime.Call(context.Background(), "node_repl", map[string]any{
		"workspace_id": workspaceID, "code": "globalThis.x = 1; return globalThis.x",
	})
	if err != nil || first.IsError {
		t.Fatalf("first eval failed: %#v %v", first, err)
	}
	second, err := runtime.Call(context.Background(), "node_repl", map[string]any{
		"workspace_id": workspaceID, "code": "globalThis.x += 1; return globalThis.x",
	})
	if err != nil || second.IsError {
		t.Fatalf("second eval failed: %#v %v", second, err)
	}
}
