package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

func TestBaseAndOpenAIProfilesShareCanonicalProtocolContract(t *testing.T) {
	profiles := []struct {
		name    string
		profile Profile
	}{
		{name: "base", profile: BaseProfile()},
		{name: "openai", profile: OpenAIProfile()},
	}

	for _, tc := range profiles {
		t.Run(tc.name, func(t *testing.T) {
			registry := tools.NewRegistry()
			registry.MustRegister("matrix_probe", tools.Schema{
				Name:         "matrix_probe",
				Title:        "Matrix Probe",
				Description:  "Exercise canonical MCP profile behavior.",
				InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
				Annotations:  tools.ToolAnnotations(tools.RiskRead),
			}, func(context.Context, map[string]any) (tools.Result, error) {
				return tools.JSONResult(map[string]any{"ok": true}), nil
			})
			registry.MustRegister("matrix_input", tools.Schema{
				Name:        "matrix_input",
				Description: "Exercise MRTR input_required.",
				InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
				Annotations: tools.ToolAnnotations(tools.RiskRead),
			}, func(context.Context, map[string]any) (tools.Result, error) {
				return tools.Result{
					ResultType:   "input_required",
					RequestState: "matrix-state",
					InputRequests: map[string]any{
						"confirm": map[string]any{"method": "elicitation/create", "params": map[string]any{"message": "Continue?"}},
					},
					Meta: map[string]any{"traceparent": "matrix-trace"},
				}, nil
			})
			registry.MustRegister("matrix_error", tools.Schema{
				Name:        "matrix_error",
				Description: "Exercise canonical tool errors.",
				InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
				Annotations: tools.ToolAnnotations(tools.RiskRead),
			}, func(context.Context, map[string]any) (tools.Result, error) {
				return tools.Result{}, errors.New("matrix denied")
			})

			runtime := NewRuntimeWithProfile(&tools.Runtime{Registry: registry}, tc.profile)
			runtime.SetAuthRequirements(BearerAuthRequirement("mcp:tools"))

			discovery, err := runtime.Handle(context.Background(), "server/discover", nil)
			if err != nil {
				t.Fatal(err)
			}
			discoveryJSON, err := json.Marshal(discovery)
			if err != nil {
				t.Fatal(err)
			}
			if len(discoveryJSON) == 0 {
				t.Fatal("empty discovery result")
			}

			listed, err := runtime.Handle(context.Background(), "tools/list", nil)
			if err != nil {
				t.Fatal(err)
			}
			listedJSON, err := json.Marshal(listed)
			if err != nil {
				t.Fatal(err)
			}
			var listedWire map[string]any
			if err := json.Unmarshal(listedJSON, &listedWire); err != nil {
				t.Fatal(err)
			}
			toolsValue, _ := listedWire["tools"].([]any)
			if len(toolsValue) != 3 {
				t.Fatalf("listed tools=%#v", listedWire)
			}
			for _, value := range toolsValue {
				tool, _ := value.(map[string]any)
				security, _ := tool["securitySchemes"].([]any)
				if len(security) != 1 {
					t.Fatalf("securitySchemes drifted: %#v", tool)
				}
			}

			result, err := runtime.Handle(context.Background(), "tools/call", map[string]any{
				"name":      "matrix_input",
				"arguments": map[string]any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			input, ok := result.(tools.Result)
			if !ok || input.ResultType != "input_required" || input.RequestState != "matrix-state" || input.Meta["traceparent"] != "matrix-trace" {
				t.Fatalf("MRTR result=%#v", result)
			}

			result, err = runtime.Handle(context.Background(), "tools/call", map[string]any{
				"name":      "matrix_error",
				"arguments": map[string]any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			toolError, ok := result.(tools.Result)
			if !ok || !toolError.IsError || len(toolError.Content) != 1 || toolError.Content[0].Text != "matrix denied" {
				t.Fatalf("tool error result=%#v", result)
			}

			_, err = runtime.Handle(context.Background(), "unsupported/method", nil)
			var protocolErr *Error
			if !errors.As(err, &protocolErr) || protocolErr.Code != ErrMethodNotFound {
				t.Fatalf("method error=%#v", err)
			}
		})
	}
}

func TestBaseAndOpenAIProfilesDifferOnlyInPresentationProjection(t *testing.T) {
	descriptor := DescribeTool(tools.Schema{
		Name:         "presentation_probe",
		Title:        "Presentation Probe",
		Description:  "Verify presentation-only profile differences.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}`),
		Annotations:  tools.ToolAnnotations(tools.RiskEdit),
	})
	descriptor.Security.AuthRequirements = []AuthRequirement{BearerAuthRequirement("mcp:tools")}

	base, err := ProjectTool(BaseProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	openai, err := ProjectTool(OpenAIProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if base.Name != openai.Name ||
		base.Title != openai.Title ||
		base.Description != openai.Description ||
		!jsonSemanticEqual(base.InputSchema, openai.InputSchema) ||
		!jsonSemanticEqual(base.OutputSchema, openai.OutputSchema) ||
		!reflect.DeepEqual(base.Annotations, openai.Annotations) ||
		!reflect.DeepEqual(base.SecuritySchemes, openai.SecuritySchemes) {
		t.Fatalf("OpenAI profile changed canonical protocol truth\nbase=%#v\nopenai=%#v", base, openai)
	}
	if len(base.Meta) != 0 || len(openai.Meta) == 0 {
		t.Fatalf("presentation metadata projection base=%#v openai=%#v", base.Meta, openai.Meta)
	}
}
