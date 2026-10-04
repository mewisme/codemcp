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

func TestCompletionCorrelationPrefersTrustedControllerOverSharedApprovalCaller(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	const toolName = "capture_controller_completion_correlation"
	got := make(chan AgentCompletionCorrelation, 2)
	runtime.Registry.MustRegister(toolName, Schema{
		Name:        toolName,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}, func(ctx context.Context, _ map[string]any) (Result, error) {
		got <- AgentCompletionCorrelationFromContext(ctx)
		return TextResult("ok"), nil
	})
	call := func(controller string) AgentCompletionCorrelation {
		ctx := WithTrustedControllerID(context.Background(), controller)
		ctx = WithCallSource(ctx, "tunnel")
		ctx = WithApprovalCorrelation(ctx, "apc_shared", "apr_unique")
		if _, err := runtime.Call(ctx, toolName, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		return <-got
	}
	first := call("openai:session-a")
	second := call("openai:session-b")
	if first.AgentID == second.AgentID {
		t.Fatalf("distinct trusted controllers shared completion identity: %#v / %#v", first, second)
	}
	want := agentcompletion.DeriveAgentID("controller:openai:session-a", runtime.runtimeInstanceID())
	if first.AgentID != want || first.Source != "tunnel" {
		t.Fatalf("first=%#v want_agent=%q", first, want)
	}
}

func TestRuntimeStateIdentityPrefersControllerAndSeparatesPlanKeys(t *testing.T) {
	base := WithMCPSessionID(context.Background(), "mcp-session")
	if got := RuntimeStateIdentity(base); got != "mcp:mcp-session" {
		t.Fatalf("MCP state identity=%q", got)
	}
	centralizedMCP := WithTrustedControllerID(base, "mcp:mcp-session")
	if got := RuntimeStateIdentity(centralizedMCP); got != "mcp:mcp-session" || RuntimeStateKey(centralizedMCP) != RuntimeStateKey(base) {
		t.Fatalf("centralized MCP identity diverged: identity=%q direct=%q centralized=%q", got, RuntimeStateKey(base), RuntimeStateKey(centralizedMCP))
	}
	first := WithTrustedControllerID(base, "openai:session-a")
	second := WithTrustedControllerID(base, "openai:session-b")
	if got := RuntimeStateIdentity(first); got != "controller:openai:session-a" {
		t.Fatalf("controller state identity=%q", got)
	}
	if RuntimeStateKey(first) == RuntimeStateKey(second) || planExecutionSessionKey(first) == planExecutionSessionKey(second) {
		t.Fatal("distinct trusted controllers share runtime/plan state keys")
	}
}
