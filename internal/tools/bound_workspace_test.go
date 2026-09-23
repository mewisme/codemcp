package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/workspace"
)

func TestBoundWorkspaceInjectsAndRestrictsWorkspaceID(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.MustRegister("probe", Schema{Name: "probe", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"]}`)}, func(_ context.Context, args map[string]any) (Result, error) {
		return JSONResult(args), nil
	})
	runtime := &Runtime{Registry: registry, Workspaces: manager, SessionAccess: NewSessionWorkspaceAccessManager(), LoopGuard: NewToolLoopGuard()}
	ctx := WithMCPSessionID(WithBoundWorkspace(context.Background(), first.ID), "bound-session")
	result, err := runtime.Call(ctx, "probe", map[string]any{})
	if err != nil || result.IsError {
		t.Fatalf("injected call result=%#v err=%v", result, err)
	}
	payload, ok := result.StructuredContent.(map[string]any)
	if !ok || payload["workspace_id"] != first.ID {
		t.Fatalf("payload=%#v", result.StructuredContent)
	}
	result, err = runtime.Call(ctx, "probe", map[string]any{"workspace_id": second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("cross-workspace call unexpectedly succeeded: %#v", result)
	}
}

func TestUnboundWorkspaceStillRequiresWorkspaceID(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	registry := NewRegistry()
	registry.MustRegister("probe", Schema{Name: "probe", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"]}`)}, func(_ context.Context, args map[string]any) (Result, error) {
		return JSONResult(args), nil
	})
	runtime := &Runtime{Registry: registry, Workspaces: manager, LoopGuard: NewToolLoopGuard()}
	result, err := runtime.Call(context.Background(), "probe", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("unbound call unexpectedly succeeded: %#v", result)
	}
}
