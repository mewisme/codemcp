package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

func TestStdioMessageReaderEnforcesPerMessageLimit(t *testing.T) {
	input := io.NopCloser(strings.NewReader("ok\n" + strings.Repeat("x", 9) + "\n"))
	reader := newLineLimitReadCloser(input, 8)
	data, err := io.ReadAll(reader)
	if !errors.Is(err, ErrStdioMessageTooLarge) {
		t.Fatalf("read error=%v", err)
	}
	if string(data) != "ok\n"+strings.Repeat("x", 8) {
		t.Fatalf("bounded data=%q", data)
	}
}

func TestStdioMessageReaderResetsLimitPerLine(t *testing.T) {
	input := io.NopCloser(strings.NewReader("12345678\nabcdefgh\n"))
	reader := newLineLimitReadCloser(input, 8)
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "12345678\nabcdefgh\n" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestStdioRuntimeOfficialSDKInterop(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("stdio_probe", tools.Schema{Name: "stdio_probe", Description: "Probe stdio transport context.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		return tools.JSONResult(map[string]any{"source": tools.CallSource(ctx), "session": tools.MCPSessionID(ctx), "request": RequestContextFromContext(ctx)}), nil
	})
	toolRuntime := &tools.Runtime{Registry: registry, LoopGuard: tools.NewToolLoopGuard()}
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	server, err := NewStdioRuntime(toolRuntime, clientToServerReader, serverToClientWriter)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "stdio-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "stdio_probe" {
		t.Fatalf("tools = %#v", list.Tools)
	}
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "stdio_probe", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %#v", result)
	}
	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("content = %#v", result.Content[0])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
		t.Fatalf("decode payload %q: %v", text.Text, err)
	}
	if payload["source"] != "stdio" {
		t.Fatalf("source = %#v", payload["source"])
	}
	if sessionID, _ := payload["session"].(string); sessionID != "" {
		t.Fatalf("modern stdio request inherited transport session = %#v", payload["session"])
	}
	request, _ := payload["request"].(map[string]any)
	if request["protocol_version"] != SupportedProtocolVersion {
		t.Fatalf("request context = %#v", request)
	}
	clientInfo, _ := request["client_info"].(map[string]any)
	if clientInfo["name"] != "stdio-test" || clientInfo["version"] != "1.0.0" {
		t.Fatalf("request client info = %#v", clientInfo)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil && err != context.Canceled {
			t.Fatalf("server run: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
	}
}
