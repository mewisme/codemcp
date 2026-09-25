package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/approval"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

func TestPhase1AConfigToolsRemainUnregistered(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	for _, name := range []string{mcpconfigwire.ListToolName, mcpconfigwire.GetToolName, mcpconfigwire.SetToolName} {
		if _, ok := runtime.Registry.Schema(name); ok {
			t.Fatalf("planned MCP config tool %q was registered before its implementation phase", name)
		}
	}
}

func TestConfigSetCallObservationUsesValueFreeArguments(t *testing.T) {
	args := map[string]any{
		"workspace_id": "ws_scope",
		"changes": []any{
			map[string]any{"key": "server.port", "value": "4000"},
			map[string]any{"key": "permissions.allow_dirs", "value": "/private/value"},
		},
	}
	ctx := WithCallDetails(context.Background(), "tools/call", map[string]any{
		"name": mcpconfigwire.SetToolName, "arguments": args,
	})
	ctx = WithCallRequest(ctx, map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params":  map[string]any{"name": mcpconfigwire.SetToolName, "arguments": args},
	})
	raw := callRaw(ctx, "http", mcpconfigwire.SetToolName, args)
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"4000", "/private/value"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("config_set observation leaked value %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{"server.port", "permissions.allow_dirs", "change_count"} {
		if !strings.Contains(text, required) {
			t.Fatalf("config_set observation lost safe summary %q: %s", required, text)
		}
	}
}

func TestConfigSetApprovalResultsUseValueFreeArguments(t *testing.T) {
	raw := json.RawMessage(`{"workspace_id":"ws_scope","changes":[{"key":"server.port","value":"4000"},{"key":"permissions.allow_dirs","value":"/private/value"}]}`)
	results := []Result{
		approvalRequiredResult(approval.Challenge{
			ID: "chg_1", WorkspaceID: "ws_scope", TargetTool: mcpconfigwire.SetToolName, Arguments: raw,
			GuardReason: "contains /private/value", Command: "set 4000",
		}),
		approvalResolutionResult(approval.Request{
			ID: "apr_1", Status: approval.StatusApproved, WorkspaceID: "ws_scope", TargetTool: mcpconfigwire.SetToolName, Arguments: raw,
		}),
		approvalMismatchResult(&approval.MismatchError{
			RequestID: "apr_1", TargetTool: mcpconfigwire.SetToolName, Expected: raw,
			Actual: json.RawMessage(`{"changes":[{"key":"server.port","value":"5000"}]}`),
		}),
	}
	for index, result := range results {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{"4000", "5000", "/private/value"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("approval result %d leaked value %q: %s", index, forbidden, text)
			}
		}
		if !strings.Contains(text, "server.port") || !strings.Contains(text, "change_count") {
			t.Fatalf("approval result %d lost value-free summary: %s", index, text)
		}
	}
}
