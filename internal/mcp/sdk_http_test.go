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

func TestSDKHTTPTransportsOfficialClientInterop(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("transport_probe", tools.Schema{Name: "transport_probe", Description: "Return MCP call source.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		return tools.JSONResult(map[string]any{"source": tools.CallSource(ctx)}), nil
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
	}{
		{name: "streamable", transport: &sdkmcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true}, want: "http"},
		{name: "legacy-sse", transport: &sdkmcp.SSEClientTransport{Endpoint: server.URL + "/mcp/sse"}, want: "sse"},
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
		})
	}
}
