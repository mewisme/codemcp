package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

func TestOfficialGoSDKStreamableInterop(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("sdk_echo", tools.Schema{
		Name:        "sdk_echo",
		Description: "Echo text for official Go SDK conformance.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
	}, func(_ context.Context, args map[string]any) (tools.Result, error) {
		text, _ := args["text"].(string)
		return tools.TextResult("echo:" + text), nil
	})
	runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: registry})
	server := httptest.NewServer(runtime.Handler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "codemcp-conformance", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatalf("official SDK connect: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "sdk_echo", Arguments: map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatalf("official SDK tools/call: %v", err)
	}
	if result.IsError {
		t.Fatalf("official SDK received tool error: %#v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok || text.Text != "echo:hello" {
		t.Fatalf("content = %#v", result.Content[0])
	}
}

func TestModernRequestMetaRequiresProtocolVersionAndClientCapabilities(t *testing.T) {
	cases := map[string]string{
		"missing-meta":                `{"jsonrpc":"2.0","id":40,"method":"tools/list","params":{}}`,
		"missing-protocol-version":    `{"jsonrpc":"2.0","id":41,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}}}`,
		"missing-client-capabilities": `{"jsonrpc":"2.0","id":42,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: tools.NewRegistry()})
			req := rawModernRequest("tools/list", body)
			res := httptest.NewRecorder()
			runtime.ServeHTTP(res, req)
			response := decodeResponse(t, res)
			if response.Error == nil || response.Error.Code != ErrInvalidParams {
				t.Fatalf("error = %#v, want code %d", response.Error, ErrInvalidParams)
			}
		})
	}
}

func TestUnsupportedProtocolVersionCarriesNegotiationData(t *testing.T) {
	runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: tools.NewRegistry()})
	body := `{"jsonrpc":"2.0","id":43,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`
	req := rawModernRequest("tools/list", body)
	req.Header.Set(ProtocolVersionHeader, "2099-01-01")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)

	if res.Code != 400 {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrUnsupportedProtocolVersion {
		t.Fatalf("error = %#v, want code %d", response.Error, ErrUnsupportedProtocolVersion)
	}
	data, ok := response.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("error data = %#v", response.Error.Data)
	}
	if data["requested"] != "2099-01-01" {
		t.Fatalf("requested = %#v", data["requested"])
	}
	supported, ok := data["supported"].([]any)
	if !ok || len(supported) != 1 || supported[0] != SupportedProtocolVersion {
		t.Fatalf("supported = %#v", data["supported"])
	}
}

func TestModernRequestMetaHeaderVersionMismatchIsHeaderMismatch(t *testing.T) {
	runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: tools.NewRegistry()})
	body := `{"jsonrpc":"2.0","id":44,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`
	req := rawModernRequest("tools/list", body)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)

	if res.Code != 400 {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrHeaderMismatch {
		t.Fatalf("error = %#v, want code %d", response.Error, ErrHeaderMismatch)
	}
}
