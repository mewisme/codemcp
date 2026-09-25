package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/version"
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
	initialized := session.InitializeResult()
	if initialized == nil || initialized.ServerInfo == nil || initialized.ServerInfo.Name != "codemcp" || initialized.ServerInfo.Version != version.Version {
		t.Fatalf("stdio server info = %#v", initialized)
	}
	if initialized.Instructions != ProjectServerInstructions(BaseProfile()) {
		t.Fatalf("stdio instructions drifted from base profile")
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "stdio_probe" {
		t.Fatalf("tools = %#v", list.Tools)
	}
	httpRuntime := NewHTTPRuntimeWithTools(toolRuntime)
	httpRequest := modernRequest("tools/list", `{"jsonrpc":"2.0","id":99,"method":"tools/list","params":{}}`)
	httpResponse := httptest.NewRecorder()
	httpRuntime.ServeHTTP(httpResponse, httpRequest)
	if httpResponse.Code != 200 {
		t.Fatalf("direct HTTP list status=%d body=%s", httpResponse.Code, httpResponse.Body.String())
	}
	directResponse := decodeResponse(t, httpResponse)
	directResult := directResponse.Result.(map[string]any)
	directTools := directResult["tools"].([]any)
	directJSON, _ := json.Marshal(directTools[0])
	stdioJSON, _ := json.Marshal(list.Tools[0])
	var directValue, stdioValue map[string]any
	if err := json.Unmarshal(directJSON, &directValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stdioJSON, &stdioValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(directValue, stdioValue) {
		t.Fatalf("base-profile direct/stdin tool metadata differs:\ndirect=%s\nstdio=%s", directJSON, stdioJSON)
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
