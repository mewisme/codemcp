package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

func TestProjectToolProvidesStablePresentationFallbacks(t *testing.T) {
	projected, err := ProjectTool(BaseProfile(), DescribeTool(tools.Schema{
		Name:        "stable_probe",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}), ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if projected.Title != "stable_probe" || projected.Description != "stable_probe" {
		t.Fatalf("presentation fallback = title %q description %q", projected.Title, projected.Description)
	}
	if projected.Annotations["readOnlyHint"] != false ||
		projected.Annotations["destructiveHint"] != true ||
		projected.Annotations["idempotentHint"] != false ||
		projected.Annotations["openWorldHint"] != true {
		t.Fatalf("conservative annotations = %#v", projected.Annotations)
	}
}

func TestProjectionAndSDKRegistrationRejectMalformedHeaderContracts(t *testing.T) {
	schema := tools.Schema{
		Name: "bad_header_contract",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"payload":{"type":"object","x-mcp-header":"Payload"}}
		}`),
	}
	if _, err := ProjectTool(BaseProfile(), DescribeTool(schema), ToolProjectionOptions{}); err == nil {
		t.Fatal("portable projection accepted malformed x-mcp-header contract")
	}

	registry := tools.NewRegistry()
	registry.MustRegister(schema.Name, schema, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.TextResult("should not run"), nil
	})
	if _, err := NewSDKServerWithTools(&tools.Runtime{Registry: registry}, "test"); err == nil {
		t.Fatal("SDK registration accepted malformed x-mcp-header contract")
	}
}

func TestInputRequiredMetadataSurvivesWireAndSDKProjection(t *testing.T) {
	result := tools.Result{
		ResultType:   "input_required",
		RequestState: "opaque",
		InputRequests: map[string]any{
			"confirm": map[string]any{
				"method": "elicitation/create",
				"params": map[string]any{"message": "Continue?", "requestedSchema": map[string]any{"type": "object"}},
			},
		},
		Meta: map[string]any{"traceparent": "trace-value"},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var direct map[string]any
	if err := json.Unmarshal(data, &direct); err != nil {
		t.Fatal(err)
	}
	meta, _ := direct["_meta"].(map[string]any)
	if meta["traceparent"] != "trace-value" {
		t.Fatalf("direct input_required metadata = %#v", direct["_meta"])
	}
	if _, exists := direct["content"]; exists {
		t.Fatalf("input_required leaked complete result content: %s", data)
	}

	sdk, err := sdkCallToolResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if !sdk.NeedsInput() || sdk.RequestState != "opaque" || sdk.Meta["traceparent"] != "trace-value" {
		t.Fatalf("SDK input_required projection = %#v", sdk)
	}
}

func TestProtocolErrorMappingAndResourceNotFoundContract(t *testing.T) {
	notFound := ProtocolError(tools.ErrToolNotFound)
	if notFound.Code != ErrInvalidParams {
		t.Fatalf("tool not found code = %d", notFound.Code)
	}
	internal := ProtocolError(errors.New("boom"))
	if internal.Code != ErrInternal || internal.Message != "boom" {
		t.Fatalf("internal error = %#v", internal)
	}
	resource := ResourceNotFoundError(" file:///missing ")
	if resource.Code != ErrInvalidParams || resource.Message != "Resource not found" {
		t.Fatalf("resource error = %#v", resource)
	}
	data, _ := resource.Data.(map[string]any)
	if data["uri"] != "file:///missing" {
		t.Fatalf("resource error data = %#v", resource.Data)
	}
}

func TestCacheableCompleteResultUsesConservativeSharedMetadata(t *testing.T) {
	fields := map[string]any{"tools": []any{"probe"}}
	result := cacheableCompleteResult(fields)
	if result["resultType"] != "complete" || result["ttlMs"] != defaultCacheTTLMS || result["cacheScope"] != defaultCacheScope {
		t.Fatalf("cache metadata = %#v", result)
	}
	if _, exists := fields["resultType"]; exists {
		t.Fatal("cache metadata helper mutated caller fields")
	}
}

func TestProfileCannotRewriteToolFailureIntoSuccess(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("denied_probe", tools.Schema{Name: "denied_probe"}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.ErrorResult(errors.New("denied")), nil
	})
	toolRuntime := &tools.Runtime{Registry: registry}
	params := map[string]any{"name": "denied_probe", "arguments": map[string]any{}}
	for name, profile := range map[string]Profile{"base": BaseProfile(), "presentation": presentationOnlyProfile{}} {
		t.Run(name, func(t *testing.T) {
			result, err := NewRuntimeWithProfile(toolRuntime, profile).Handle(context.Background(), "tools/call", params)
			if err != nil {
				t.Fatal(err)
			}
			toolResult, ok := result.(tools.Result)
			if !ok || !toolResult.IsError {
				t.Fatalf("profile rewrote tool failure: %#v", result)
			}
		})
	}
}

func TestLegacySSEEndpointIsOptIn(t *testing.T) {
	handler, err := NewSDKHTTPHandler(&tools.Runtime{Registry: tools.NewRegistry()}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("legacy SSE status = %d, want %d", res.Code, http.StatusNotFound)
	}
}
