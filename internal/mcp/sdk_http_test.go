package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

func TestSDKHTTPTransportsOfficialClientInterop(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("transport_probe", tools.Schema{Name: "transport_probe", Description: "Return MCP call source.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		return tools.JSONResult(map[string]any{"source": tools.CallSource(ctx), "session": tools.MCPSessionID(ctx), "request": RequestContextFromContext(ctx)}), nil
	})
	runtime := &tools.Runtime{Registry: registry, LoopGuard: tools.NewToolLoopGuard()}
	handler, err := NewSDKHTTPHandler(runtime, "", true)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	cases := []struct {
		name      string
		transport sdkmcp.Transport
		want      string
		modern    bool
	}{
		{name: "streamable", transport: &sdkmcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true}, want: "http", modern: true},
		{name: "legacy-sse", transport: &sdkmcp.SSEClientTransport{Endpoint: server.URL + "/mcp/sse"}, want: "sse", modern: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "transport-test", Version: "1.0.0"}, nil)
			session, err := client.Connect(ctx, tc.transport, nil)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer session.Close()
			list, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatalf("list tools: %v", err)
			}
			if len(list.Tools) != 1 || list.Tools[0].Name != "transport_probe" {
				t.Fatalf("tools=%#v", list.Tools)
			}
			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "transport_probe", Arguments: map[string]any{}})
			if err != nil {
				t.Fatalf("call tool: %v", err)
			}
			if result.IsError || len(result.Content) != 1 {
				t.Fatalf("result=%#v", result)
			}
			text, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok {
				t.Fatalf("content=%#v", result.Content[0])
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload["source"] != tc.want {
				t.Fatalf("source=%#v want=%q", payload["source"], tc.want)
			}
			sessionID, _ := payload["session"].(string)
			request, _ := payload["request"].(map[string]any)
			if tc.modern {
				if sessionID != "" {
					t.Fatalf("modern Streamable HTTP inherited transport session=%q", sessionID)
				}
				if request["protocol_version"] != SupportedProtocolVersion {
					t.Fatalf("modern Streamable HTTP request context=%#v", request)
				}
				clientInfo, _ := request["client_info"].(map[string]any)
				if clientInfo["name"] != "transport-test" || clientInfo["version"] != "1.0.0" {
					t.Fatalf("modern Streamable HTTP client info=%#v", clientInfo)
				}
			}
		})
	}
}

func TestSDKHTTPTransportsPreserveToolErrorSemantics(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("transport_error", tools.Schema{Name: "transport_error"}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.Result{}, errors.New("denied")
	})
	handler, err := NewSDKHTTPHandler(&tools.Runtime{Registry: registry}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, tc := range []struct {
		name      string
		transport sdkmcp.Transport
	}{
		{name: "streamable", transport: &sdkmcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true}},
		{name: "legacy-sse", transport: &sdkmcp.SSEClientTransport{Endpoint: server.URL + "/mcp/sse"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "transport-error-test", Version: "1.0.0"}, nil)
			session, err := client.Connect(ctx, tc.transport, nil)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer session.Close()
			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "transport_error", Arguments: map[string]any{}})
			if err != nil {
				t.Fatalf("call tool: %v", err)
			}
			if !result.IsError || len(result.Content) != 1 {
				t.Fatalf("tool error result = %#v", result)
			}
			text, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok || text.Text != "denied" {
				t.Fatalf("tool error content = %#v", result.Content)
			}
		})
	}
}
