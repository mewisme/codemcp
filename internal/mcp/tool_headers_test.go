package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

func TestHTTPRuntimeValidatesAnnotatedToolHeaders(t *testing.T) {
	var calls atomic.Int32
	runtime := NewHTTPRuntimeWithTools(annotatedToolRuntime(t, "header_probe", &calls))

	body := `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"header_probe","arguments":{"tenant":"mèo","count":7,"enabled":true}}}`
	req := modernRequest("tools/call", body)
	req.Header.Set(NameHeader, "header_probe")
	req.Header.Set("Mcp-Param-Tenant", base64Sentinel("mèo"))
	req.Header.Set("Mcp-Param-Count", "007")
	req.Header.Set("Mcp-Param-Enabled", "true")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls=%d", calls.Load())
	}
}

func TestHTTPRuntimeRejectsMissingAnnotatedHeaderBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	runtime := NewHTTPRuntimeWithTools(annotatedToolRuntime(t, "header_probe", &calls))

	body := `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"header_probe","arguments":{"tenant":"acme"}}}`
	req := modernRequest("tools/call", body)
	req.Header.Set(NameHeader, "header_probe")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)

	assertHeaderMismatch(t, res)
	if calls.Load() != 0 {
		t.Fatalf("handler was called %d times", calls.Load())
	}
}

func TestHTTPRuntimeRejectsStaleAndUnexpectedMcpParamHeaders(t *testing.T) {
	for name, header := range map[string]string{
		"stale":      "Mcp-Param-Tenant",
		"unexpected": "Mcp-Param-Other",
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			runtime := NewHTTPRuntimeWithTools(annotatedToolRuntime(t, "header_probe", &calls))
			body := `{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"header_probe","arguments":{}}}`
			req := modernRequest("tools/call", body)
			req.Header.Set(NameHeader, "header_probe")
			req.Header.Set(header, "stale")
			res := httptest.NewRecorder()
			runtime.ServeHTTP(res, req)
			assertHeaderMismatch(t, res)
			if calls.Load() != 0 {
				t.Fatalf("handler was called %d times", calls.Load())
			}
		})
	}
}

func TestHTTPRuntimeRejectsMalformedOrMismatchedAnnotatedHeaders(t *testing.T) {
	cases := map[string]map[string]string{
		"bad-base64": {
			"Mcp-Param-Tenant": "=?base64?***?=",
		},
		"string-mismatch": {
			"Mcp-Param-Tenant": "other",
		},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			runtime := NewHTTPRuntimeWithTools(annotatedToolRuntime(t, "header_probe", &calls))
			body := `{"jsonrpc":"2.0","id":33,"method":"tools/call","params":{"name":"header_probe","arguments":{"tenant":"acme"}}}`
			req := modernRequest("tools/call", body)
			req.Header.Set(NameHeader, "header_probe")
			for key, value := range headers {
				req.Header.Set(key, value)
			}
			res := httptest.NewRecorder()
			runtime.ServeHTTP(res, req)
			assertHeaderMismatch(t, res)
			if calls.Load() != 0 {
				t.Fatalf("handler was called %d times", calls.Load())
			}
		})
	}
}

func TestHTTPRuntimeAcceptsEncodedMcpName(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("töö", tools.Schema{
		Name:        "töö",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.TextResult("ok"), nil
	})
	runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: registry})

	req := modernRequest("tools/call", `{"jsonrpc":"2.0","id":34,"method":"tools/call","params":{"name":"töö","arguments":{}}}`)
	req.Header.Set(NameHeader, base64Sentinel("töö"))
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestHTTPRuntimeFiltersMalformedAnnotatedToolAndRejectsDirectCall(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("bad_header", tools.Schema{
		Name: "bad_header",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"payload":{"type":"object","x-mcp-header":"Payload"}}
		}`),
	}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.TextResult("should not run"), nil
	})
	runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: registry})

	listReq := modernRequest("tools/list", `{"jsonrpc":"2.0","id":35,"method":"tools/list","params":{}}`)
	listRes := httptest.NewRecorder()
	runtime.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	response := decodeResponse(t, listRes)
	result, _ := response.Result.(map[string]any)
	items, _ := result["tools"].([]any)
	for _, item := range items {
		tool, _ := item.(map[string]any)
		if tool["name"] == "bad_header" {
			t.Fatalf("malformed annotated tool was advertised: %#v", tool)
		}
	}

	callReq := modernRequest("tools/call", `{"jsonrpc":"2.0","id":36,"method":"tools/call","params":{"name":"bad_header","arguments":{"payload":{}}}}`)
	callReq.Header.Set(NameHeader, "bad_header")
	callRes := httptest.NewRecorder()
	runtime.ServeHTTP(callRes, callReq)
	assertHeaderMismatch(t, callRes)
}

func TestToolHeaderSchemaRejectsNestedAndDuplicateAnnotations(t *testing.T) {
	cases := []json.RawMessage{
		json.RawMessage(`{"type":"object","properties":{"top":{"type":"object","properties":{"nested":{"type":"string","x-mcp-header":"Nested"}}}}}`),
		json.RawMessage(`{"type":"object","properties":{"a":{"type":"string","x-mcp-header":"Tenant"},"b":{"type":"string","x-mcp-header":"tenant"}}}`),
		json.RawMessage(`{"type":"object","properties":{"a":{"type":"number","x-mcp-header":"Number"}}}`),
		json.RawMessage(`{"type":"object","properties":{"a":{"type":"string","x-mcp-header":"bad header"}}}`),
	}
	for index, schema := range cases {
		if _, err := toolHeaderSpecs(schema); err == nil {
			t.Fatalf("schema[%d] unexpectedly accepted", index)
		}
	}
}

func annotatedToolRuntime(t *testing.T, name string, calls *atomic.Int32) *tools.Runtime {
	t.Helper()
	registry := tools.NewRegistry()
	registry.MustRegister(name, tools.Schema{
		Name: name,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"tenant":{"type":"string","x-mcp-header":"Tenant"},
				"count":{"type":"integer","x-mcp-header":"Count"},
				"enabled":{"type":"boolean","x-mcp-header":"Enabled"}
			}
		}`),
	}, func(context.Context, map[string]any) (tools.Result, error) {
		calls.Add(1)
		return tools.TextResult("ok"), nil
	})
	return &tools.Runtime{Registry: registry}
}

func assertHeaderMismatch(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrHeaderMismatch {
		t.Fatalf("error=%#v", response.Error)
	}
}

func base64Sentinel(value string) string {
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}

func TestDecodeMCPHeaderValueRejectsUnsafeRawAndMalformedSentinel(t *testing.T) {
	for _, value := range []string{"=?base64?***?=", "=?base64?YWJj", "abc?=", "é"} {
		if _, err := decodeMCPHeaderValue(value); err == nil {
			t.Fatalf("value %q unexpectedly accepted", value)
		}
	}
	if got, err := decodeMCPHeaderValue(base64Sentinel(" mèo ")); err != nil || got != " mèo " {
		t.Fatalf("decoded=%q err=%v", got, err)
	}
}

func TestIntegerHeaderComparisonIsNumeric(t *testing.T) {
	for _, header := range []string{"7", "007", "7.0", "7e0"} {
		if err := compareToolHeaderValue("integer", float64(7), header); err != nil {
			t.Fatalf("header %q: %v", header, err)
		}
	}
	if err := compareToolHeaderValue("integer", float64(7), "8"); err == nil {
		t.Fatal("integer mismatch unexpectedly accepted")
	}
}
