package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

func FuzzDecodeParamsNoPanic(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`null`,
		`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}`,
		`[]`,
		`{"name":"x","arguments":{"nested":[1,true,null]}}`,
		`{"_meta":"bad"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = DecodeParams(json.RawMessage(raw))
	})
}

func FuzzHTTPRuntimeMalformedRequestNoPanic(f *testing.F) {
	for _, seed := range []string{
		``,
		`{`,
		`[]`,
		`null`,
		`{"jsonrpc":"2.0"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}{}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 1<<20 {
			t.Skip()
		}
		runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: tools.NewRegistry()})
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set(ProtocolVersionHeader, SupportedProtocolVersion)
		req.Header.Set(MethodHeader, "tools/list")
		res := httptest.NewRecorder()
		runtime.ServeHTTP(res, req)
	})
}
