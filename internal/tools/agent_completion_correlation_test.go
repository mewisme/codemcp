package tools

import (
	"context"
	"encoding/json"
	"testing"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

func TestCompletionCorrelationIsDerivedFromTrustedRuntimeContext(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	const toolName = "capture_completion_correlation"
	var got AgentCompletionCorrelation
	runtime.Registry.MustRegister(toolName, Schema{
		Name:        toolName,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
	}, func(ctx context.Context, _ map[string]any) (Result, error) {
		got = AgentCompletionCorrelationFromContext(ctx)
		return JSONResult(map[string]bool{"ok": true}), nil
	})

	ctx := context.Background()
	ctx = WithCallSource(ctx, "tunnel")
	ctx = WithApprovalCorrelation(ctx, "apc_trusted", "apr_trusted")
	ctx = WithCallDetails(ctx, "tools/call", map[string]any{
		"name": toolName,
		"_meta": map[string]any{
			"caller_id":     "apc_forged",
			"generation_id": "instance_forged",
			"source":        "forged",
		},
	})
	ctx = WithAgentCompletionCorrelation(ctx, "apc_forged", "instance_forged", "forged")

	if _, err := runtime.Call(ctx, toolName, map[string]any{"caller_id": "apc_forged"}); err != nil {
		t.Fatal(err)
	}
	want := agentcompletion.DeriveAgentID("apc_trusted", runtime.runtimeInstanceID())
	if got.AgentID == "" || got.AgentID != want || got.Source != "tunnel" {
		t.Fatalf("correlation=%#v want_agent=%q", got, want)
	}
	if got.AgentID == agentcompletion.DeriveAgentID("apc_forged", "instance_forged") {
		t.Fatalf("protocol metadata/preexisting context forged completion identity: %#v", got)
	}
}
