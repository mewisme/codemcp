package mcp

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestRuntimeRelaysInputRoundContext(t *testing.T) {
	registry := tools.NewRegistry()
	var captured tools.InputRound
	var capabilities map[string]any
	registry.MustRegister("probe", tools.Schema{Name: "probe"}, func(ctx context.Context, args map[string]any) (tools.Result, error) {
		captured = tools.InputRoundFromContext(ctx)
		meta := upstream.RequestMetaFromContext(ctx)
		capabilities, _ = meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
		return tools.TextResult("ok"), nil
	})
	runtime := &Runtime{Tools: &tools.Runtime{Registry: registry}}
	result, err := runtime.Handle(context.Background(), "tools/call", map[string]any{
		"name":         "probe",
		"arguments":    map[string]any{"value": "x"},
		"requestState": "opaque",
		"inputResponses": map[string]any{
			"confirm": map[string]any{"action": "accept"},
		},
		"_meta": map[string]any{
			"io.modelcontextprotocol/clientCapabilities": map[string]any{"elicitation": map[string]any{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.RequestState != "opaque" || captured.InputResponses == nil {
		t.Fatalf("input round = %#v", captured)
	}
	if capabilities == nil {
		t.Fatal("client capabilities missing from relayed request meta")
	}
	toolResult := result.(tools.Result)
	if toolResult.ResultType != "complete" {
		t.Fatalf("resultType = %q", toolResult.ResultType)
	}
}

func TestValidateParamsRejectsInvalidInputRound(t *testing.T) {
	for key, value := range map[string]any{"requestState": 1, "inputResponses": "bad"} {
		params := map[string]any{"name": "probe", "arguments": map[string]any{}, key: value}
		if err := ValidateParams("tools/call", params); err == nil {
			t.Fatalf("%s was accepted", key)
		}
	}
}
