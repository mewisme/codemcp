package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/tools"
)

type transportConfigReadProvider struct {
	setting mcpconfigwire.Setting
}

func (provider transportConfigReadProvider) List(context.Context, string) ([]mcpconfigwire.Setting, mcpconfigwire.ErrorCode) {
	return []mcpconfigwire.Setting{provider.setting}, ""
}

func (provider transportConfigReadProvider) Get(context.Context, string) (mcpconfigwire.Setting, mcpconfigwire.ErrorCode) {
	return provider.setting, ""
}

func TestConfigReadToolsProjectStableReadOnlySchemas(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := tools.NewRuntime()
	for _, tc := range []struct {
		name   string
		output json.RawMessage
	}{
		{mcpconfigwire.ListToolName, mcpconfigwire.ListOutputSchema},
		{mcpconfigwire.GetToolName, mcpconfigwire.GetOutputSchema},
	} {
		schema, ok := runtime.Registry.Schema(tc.name)
		if !ok {
			t.Fatalf("missing tool %s", tc.name)
		}
		projected, err := ProjectTool(BaseProfile(), DescribeTool(schema), ToolProjectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if projected.Name != tc.name || projected.Title == "" || projected.Description == "" {
			t.Fatalf("projection=%#v", projected)
		}
		if projected.Annotations["readOnlyHint"] != true || projected.Annotations["idempotentHint"] != true ||
			projected.Annotations["destructiveHint"] != false || projected.Annotations["openWorldHint"] != false {
			t.Fatalf("%s annotations=%#v", tc.name, projected.Annotations)
		}
		if string(projected.OutputSchema) != string(tc.output) {
			t.Fatalf("%s output schema=%s want=%s", tc.name, projected.OutputSchema, tc.output)
		}
	}
}

func TestConfigGetSanitizedResultEquivalentAcrossHTTPAndStdio(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	value := "41001"
	want := mcpconfigwire.GetResult{Setting: mcpconfigwire.Setting{
		Key: "server.port", Label: "MCP port", Section: "server", Kind: "int",
		Readable: true, Writable: true, Value: &value,
	}}
	runtime := tools.NewRuntime()
	runtime.SetConfigReadProvider(transportConfigReadProvider{setting: want.Setting})

	httpHandler, err := NewSDKHTTPHandler(runtime, "", false)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(httpHandler)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "config-http-test", Version: "1.0.0"}, nil)
	httpSession, err := httpClient.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpResult := callConfigGet(t, ctx, httpSession)
	if err := httpSession.Close(); err != nil {
		t.Fatal(err)
	}

	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	stdioServer, err := NewStdioRuntime(runtime, clientToServerReader, serverToClientWriter)
	if err != nil {
		t.Fatal(err)
	}
	stdioDone := make(chan error, 1)
	go func() { stdioDone <- stdioServer.Run(ctx) }()
	stdioClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "config-stdio-test", Version: "1.0.0"}, nil)
	stdioSession, err := stdioClient.Connect(ctx, &sdkmcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stdioResult := callConfigGet(t, ctx, stdioSession)
	if err := stdioSession.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stdioDone:
		if err != nil && err != context.Canceled {
			t.Fatalf("stdio server: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
	}

	if !reflect.DeepEqual(httpResult, want) || !reflect.DeepEqual(stdioResult, want) || !reflect.DeepEqual(httpResult, stdioResult) {
		t.Fatalf("transport mismatch\nhttp=%#v\nstdio=%#v\nwant=%#v", httpResult, stdioResult, want)
	}
}

func TestConfigToolContractsEquivalentAcrossHTTPAndStdio(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := tools.NewRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	httpHandler, err := NewSDKHTTPHandler(runtime, "", false)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(httpHandler)
	defer httpServer.Close()
	httpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "config-http-contract-test", Version: "1.0.0"}, nil)
	httpSession, err := httpClient.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpTools, err := httpSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpSession.Close(); err != nil {
		t.Fatal(err)
	}

	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	stdioServer, err := NewStdioRuntime(runtime, clientToServerReader, serverToClientWriter)
	if err != nil {
		t.Fatal(err)
	}
	stdioDone := make(chan error, 1)
	go func() { stdioDone <- stdioServer.Run(ctx) }()
	stdioClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "config-stdio-contract-test", Version: "1.0.0"}, nil)
	stdioSession, err := stdioClient.Connect(ctx, &sdkmcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stdioTools, err := stdioSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdioSession.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stdioDone:
		if err != nil && err != context.Canceled {
			t.Fatalf("stdio server: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
	}

	for _, name := range []string{mcpconfigwire.ListToolName, mcpconfigwire.GetToolName, mcpconfigwire.SetToolName} {
		schema, ok := runtime.Registry.Schema(name)
		if !ok {
			t.Fatalf("missing canonical schema %q", name)
		}
		expected, err := ProjectSDKTool(BaseProfile(), DescribeTool(schema), ToolProjectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		httpTool := findSDKTool(t, httpTools.Tools, name)
		stdioTool := findSDKTool(t, stdioTools.Tools, name)
		assertSDKToolCanonicalContract(t, expected, httpTool)
		assertSDKToolCanonicalContract(t, expected, stdioTool)
		assertSDKToolCanonicalContract(t, httpTool, stdioTool)
	}
}

func callConfigGet(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) mcpconfigwire.GetResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: mcpconfigwire.GetToolName, Arguments: map[string]any{"key": "server.port"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("config_get result=%#v", result)
	}
	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("config_get content=%#v", result.Content)
	}
	var decoded mcpconfigwire.GetResult
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("decode config_get: %v", err)
	}
	return decoded
}

func findSDKTool(t *testing.T, values []*sdkmcp.Tool, name string) *sdkmcp.Tool {
	t.Helper()
	for _, value := range values {
		if value != nil && value.Name == name {
			return value
		}
	}
	t.Fatalf("tool %q missing from discovery", name)
	return nil
}

func assertSDKToolCanonicalContract(t *testing.T, expected, actual *sdkmcp.Tool) {
	t.Helper()
	if expected == nil || actual == nil {
		t.Fatalf("tool contract expected=%#v actual=%#v", expected, actual)
	}
	if expected.Name != actual.Name ||
		!sdkSchemaSemanticEqual(expected.InputSchema, actual.InputSchema) ||
		!sdkSchemaSemanticEqual(expected.OutputSchema, actual.OutputSchema) ||
		!reflect.DeepEqual(expected.Annotations, actual.Annotations) {
		t.Fatalf("tool contract drift for %q\nexpected=%#v\nactual=%#v", expected.Name, expected, actual)
	}
}

func sdkSchemaSemanticEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftJSON, &leftValue) != nil || json.Unmarshal(rightJSON, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
