package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/upstream"
)

func TestInputRoundContext(t *testing.T) {
	ctx := WithInputRound(context.Background(), "state", map[string]any{"confirm": map[string]any{"action": "accept"}})
	round := InputRoundFromContext(ctx)
	if round.RequestState != "state" || round.InputResponses == nil {
		t.Fatalf("round = %#v", round)
	}
}

func TestForwardUpstreamInputRequiredPreservesWireShape(t *testing.T) {
	result := forwardUpstreamResult(upstream.CallResult{
		ResultType:   "input_required",
		RequestState: "opaque",
		InputRequests: map[string]any{
			"confirm": map[string]any{"method": "elicitation/create", "params": map[string]any{"message": "Continue?"}},
		},
	})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{`"resultType":"input_required"`, `"requestState":"opaque"`, `"inputRequests"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("result JSON missing %s: %s", expected, text)
		}
	}
	if strings.Contains(text, `"content"`) {
		t.Fatalf("input_required must not be serialized as a complete CallToolResult: %s", text)
	}
}

func TestRuntimeStampsCompleteResultType(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister("probe", Schema{Name: "probe"}, func(context.Context, map[string]any) (Result, error) {
		return Result{Content: []Content{{Type: "text", Text: "ok"}}}, nil
	})
	runtime := &Runtime{Registry: registry}
	result, err := runtime.Call(context.Background(), "probe", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResultType != "complete" {
		t.Fatalf("resultType = %q", result.ResultType)
	}
}
