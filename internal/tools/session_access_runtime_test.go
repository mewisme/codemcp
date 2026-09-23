package tools

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/workspace"
)

func newSessionAccessRuntime(t *testing.T) (*Runtime, string, string, *int) {
	t.Helper()
	manager := workspace.NewManager(t.TempDir() + "/workspaces.json")
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	registry := NewRegistry()
	registry.MustRegister("workspace_probe", Schema{Name: "workspace_probe", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"]}`)}, func(_ context.Context, args map[string]any) (Result, error) {
		calls++
		workspaceID, _ := args["workspace_id"].(string)
		return TextResult(workspaceID), nil
	})
	registry.MustRegister("global_probe", Schema{Name: "global_probe", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, map[string]any) (Result, error) {
		return TextResult("ok"), nil
	})
	return &Runtime{Registry: registry, Workspaces: manager, SessionAccess: NewSessionWorkspaceAccessManager()}, first.ID, second.ID, &calls
}

func TestRuntimeSessionCanAccessMultipleValidWorkspaces(t *testing.T) {
	runtime, first, second, calls := newSessionAccessRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "session-a")
	for index, workspaceID := range []string{first, first, second, second} {
		result, err := runtime.Call(ctx, "workspace_probe", map[string]any{"workspace_id": workspaceID})
		if err != nil || result.IsError || *calls != index+1 {
			t.Fatalf("call %d = %#v err=%v calls=%d", index, result, err, *calls)
		}
	}
	access, ok := runtime.SessionAccess.Lookup("session-a")
	if !ok || len(access.Workspaces) != 2 {
		t.Fatalf("access = %#v ok=%t", access, ok)
	}
}

func TestRuntimeAllowsManySessionsOnSameWorkspace(t *testing.T) {
	runtime, first, _, calls := newSessionAccessRuntime(t)
	for _, sessionID := range []string{"session-a", "session-b"} {
		result, err := runtime.Call(WithMCPSessionID(context.Background(), sessionID), "workspace_probe", map[string]any{"workspace_id": first})
		if err != nil || result.IsError {
			t.Fatalf("session %s = %#v err=%v", sessionID, result, err)
		}
	}
	if *calls != 2 {
		t.Fatalf("calls = %d", *calls)
	}
}

func TestRuntimeInvalidWorkspaceDoesNotGrantSessionAccess(t *testing.T) {
	runtime, _, second, calls := newSessionAccessRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "session-a")
	result, err := runtime.Call(ctx, "workspace_probe", map[string]any{"workspace_id": "ws_missing"})
	if err != nil || !result.IsError || *calls != 0 {
		t.Fatalf("invalid workspace = %#v err=%v calls=%d", result, err, *calls)
	}
	if _, ok := runtime.SessionAccess.Lookup("session-a"); ok {
		t.Fatal("invalid workspace created session access")
	}
	result, err = runtime.Call(ctx, "workspace_probe", map[string]any{"workspace_id": second})
	if err != nil || result.IsError || *calls != 1 {
		t.Fatalf("valid workspace after invalid = %#v err=%v calls=%d", result, err, *calls)
	}
}

func TestRuntimeWorkspaceSchemaRequiresExplicitWorkspaceID(t *testing.T) {
	runtime, _, _, calls := newSessionAccessRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "session-a")
	result, err := runtime.Call(ctx, "workspace_probe", map[string]any{})
	if err != nil || !result.IsError || *calls != 0 || !strings.Contains(result.Content[0].Text, "workspace_id") {
		t.Fatalf("missing workspace = %#v err=%v calls=%d", result, err, *calls)
	}
	if _, ok := runtime.SessionAccess.Lookup("session-a"); ok {
		t.Fatal("missing workspace created session access")
	}
}

func TestRuntimeIgnoresWorkspaceIDOnGlobalTool(t *testing.T) {
	runtime, first, _, _ := newSessionAccessRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "session-a")
	result, err := runtime.Call(ctx, "global_probe", map[string]any{"workspace_id": first})
	if err != nil || result.IsError {
		t.Fatalf("global tool = %#v err=%v", result, err)
	}
	if _, ok := runtime.SessionAccess.Lookup("session-a"); ok {
		t.Fatal("global tool created session access from undeclared workspace_id")
	}
}

func TestRuntimeNonWorkspaceToolDoesNotGrantSessionAccess(t *testing.T) {
	runtime, _, _, _ := newSessionAccessRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "session-a")
	result, err := runtime.Call(ctx, "global_probe", map[string]any{})
	if err != nil || result.IsError {
		t.Fatalf("global tool = %#v err=%v", result, err)
	}
	if _, ok := runtime.SessionAccess.Lookup("session-a"); ok {
		t.Fatal("global tool created session access")
	}
}

func TestRuntimeObservesSessionWorkspaceAccessWithoutRawSessionID(t *testing.T) {
	runtime, first, second, _ := newSessionAccessRuntime(t)
	observed := make(chan CallObservation, 4)
	runtime.SetCallObserver(func(value CallObservation) { observed <- value })
	ctx := WithMCPSessionID(context.Background(), "session-secret-a")
	if result, err := runtime.Call(ctx, "workspace_probe", map[string]any{"workspace_id": first}); err != nil || result.IsError {
		t.Fatalf("first call = %#v err=%v", result, err)
	}
	start, finish := <-observed, <-observed
	callHex := strings.TrimPrefix(start.CallID, "call_")
	if len(callHex) != 16 || finish.CallID != start.CallID {
		t.Fatalf("call ids = %q / %q", start.CallID, finish.CallID)
	}
	if _, err := hex.DecodeString(callHex); err != nil {
		t.Fatalf("call id is not hex: %q", start.CallID)
	}
	if start.SessionAccess != SessionWorkspaceAccessNew || finish.SessionAccess != SessionWorkspaceAccessNew || finish.SessionWorkspaceCount != 1 {
		t.Fatalf("first access observations = %#v / %#v", start, finish)
	}
	if finish.SessionHash == "" || finish.SessionHash != MCPSessionFingerprint("session-secret-a") {
		t.Fatalf("session hash = %q", finish.SessionHash)
	}
	if result, err := runtime.Call(ctx, "workspace_probe", map[string]any{"workspace_id": second}); err != nil || result.IsError {
		t.Fatalf("second workspace call = %#v err=%v", result, err)
	}
	_, secondFinish := <-observed, <-observed
	if secondFinish.SessionAccess != SessionWorkspaceAccessNew || secondFinish.SessionWorkspaceCount != 2 || secondFinish.WorkspaceID != second || secondFinish.Status != "ok" {
		t.Fatalf("second workspace observation = %#v", secondFinish)
	}
	data, err := json.Marshal(secondFinish.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session-secret-a") {
		t.Fatalf("raw observation leaked MCP session id: %s", data)
	}
}

func TestRuntimeDoesNotGrantExternalUpstreamWorkspaceID(t *testing.T) {
	runtime, _, _, _ := newSessionAccessRuntime(t)
	if err := runtime.Registry.ReplaceOwned("upstream:test", map[string]Entry{
		"external_probe": {
			Schema:  Schema{Name: "external_probe", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"]}`)},
			Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("ok"), nil },
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithMCPSessionID(context.Background(), "session-a")
	result, err := runtime.Call(ctx, "external_probe", map[string]any{"workspace_id": "external-workspace"})
	if err != nil || result.IsError {
		t.Fatalf("external tool = %#v err=%v", result, err)
	}
	if _, ok := runtime.SessionAccess.Lookup("session-a"); ok {
		t.Fatal("external upstream workspace_id created local session access")
	}
}
