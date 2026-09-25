package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

func TestResponseMarshalStampsServerInfoWithoutLosingRawNumbersOrMeta(t *testing.T) {
	response := Response{
		JSONRPC: "2.0",
		ID:      1,
		Result: map[string]any{
			"value": json.Number("9007199254740993"),
			"_meta": map[string]any{"traceparent": "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"},
		},
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(data, &outer); err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(outer["result"], &result); err != nil {
		t.Fatal(err)
	}
	if string(result["value"]) != "9007199254740993" {
		t.Fatalf("large integer changed: %s", result["value"])
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(result["_meta"], &meta); err != nil {
		t.Fatal(err)
	}
	var traceparent string
	if err := json.Unmarshal(meta["traceparent"], &traceparent); err != nil {
		t.Fatal(err)
	}
	if traceparent != "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" {
		t.Fatalf("traceparent = %q", traceparent)
	}
	assertServerInfoRaw(t, meta[serverInfoMetaKey], "codemcp")
}

func TestResponseMarshalPreservesHandlerAuthoredServerInfo(t *testing.T) {
	response := Response{
		JSONRPC: "2.0",
		ID:      "custom",
		Result: map[string]any{
			"resultType": "complete",
			"_meta": map[string]any{
				serverInfoMetaKey: map[string]any{"name": "custom-server", "version": "9.9.9"},
			},
		},
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	meta, _ := decoded.Result["_meta"].(map[string]any)
	info, _ := meta[serverInfoMetaKey].(map[string]any)
	if info["name"] != "custom-server" || info["version"] != "9.9.9" {
		t.Fatalf("handler-authored serverInfo was overwritten: %#v", info)
	}
}

func TestHTTPRuntimeToolsListStampsServerInfo(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("tools/list", `{"jsonrpc":"2.0","id":20,"method":"tools/list","params":{}}`)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	response := decodeResponse(t, res)
	result, _ := response.Result.(map[string]any)
	assertServerInfoValue(t, result["_meta"])
}

func TestHTTPRuntimeCompleteToolResultStampsServerInfoAndPreservesMeta(t *testing.T) {
	toolRuntime := &tools.Runtime{Registry: tools.NewRegistry()}
	toolRuntime.Registry.MustRegister("meta_probe", tools.Schema{Name: "meta_probe"}, func(context.Context, map[string]any) (tools.Result, error) {
		result := tools.TextResult("ok")
		result.Meta = map[string]any{"traceparent": "trace-value"}
		return result, nil
	})
	runtime := NewHTTPRuntimeWithTools(toolRuntime)
	req := modernRequest("tools/call", `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"meta_probe","arguments":{}}}`)
	req.Header.Set(NameHeader, "meta_probe")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	response := decodeResponse(t, res)
	result, _ := response.Result.(map[string]any)
	meta, _ := result["_meta"].(map[string]any)
	if meta["traceparent"] != "trace-value" {
		t.Fatalf("tool metadata was lost: %#v", meta)
	}
	assertServerInfoValue(t, meta)
}

func TestHTTPRuntimeInputRequiredResultStampsServerInfo(t *testing.T) {
	toolRuntime := &tools.Runtime{Registry: tools.NewRegistry()}
	toolRuntime.Registry.MustRegister("input_probe", tools.Schema{Name: "input_probe"}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.Result{
			ResultType:   "input_required",
			RequestState: "opaque-state",
			Meta:         map[string]any{"traceparent": "trace-value"},
			InputRequests: map[string]any{
				"confirm": map[string]any{"type": "elicitation", "message": "Continue?", "schema": map[string]any{"type": "boolean"}},
			},
		}, nil
	})
	runtime := NewHTTPRuntimeWithTools(toolRuntime)
	req := modernRequest("tools/call", `{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"input_probe","arguments":{}}}`)
	req.Header.Set(NameHeader, "input_probe")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), `"content"`) {
		t.Fatalf("input_required unexpectedly contains CallToolResult content: %s", res.Body.String())
	}
	response := decodeResponse(t, res)
	result, _ := response.Result.(map[string]any)
	if result["resultType"] != "input_required" || result["requestState"] != "opaque-state" {
		t.Fatalf("input_required result = %#v", result)
	}
	meta, _ := result["_meta"].(map[string]any)
	if meta["traceparent"] != "trace-value" {
		t.Fatalf("input_required metadata was lost: %#v", meta)
	}
	assertServerInfoValue(t, meta)
}

func TestResponseMarshalDoesNotAddResultToProtocolError(t *testing.T) {
	data, err := json.Marshal(Response{JSONRPC: "2.0", ID: 23, Error: &Error{Code: ErrInvalidParams, Message: "bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"result"`) || strings.Contains(string(data), serverInfoMetaKey) {
		t.Fatalf("protocol error was rewritten as successful result: %s", data)
	}
}

func assertServerInfoValue(t *testing.T, raw any) {
	t.Helper()
	meta, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("_meta = %#v", raw)
	}
	info, ok := meta[serverInfoMetaKey].(map[string]any)
	if !ok || info["name"] != "codemcp" {
		t.Fatalf("serverInfo = %#v", meta[serverInfoMetaKey])
	}
	if version, _ := info["version"].(string); version == "" {
		t.Fatalf("serverInfo version = %#v", info["version"])
	}
}

func assertServerInfoRaw(t *testing.T, raw json.RawMessage, wantName string) {
	t.Helper()
	var info map[string]any
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if info["name"] != wantName {
		t.Fatalf("serverInfo = %#v", info)
	}
	if version, _ := info["version"].(string); version == "" {
		t.Fatalf("serverInfo version = %#v", info["version"])
	}
}
