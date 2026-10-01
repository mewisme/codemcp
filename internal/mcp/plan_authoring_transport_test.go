package mcp

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

func TestCreatePlanProfileAndBoundWorkspaceProjectionStayCanonical(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := tools.NewRuntime()
	schema, ok := runtime.Registry.Schema(tools.CreatePlanToolName)
	if !ok {
		t.Fatal("create_plan schema missing")
	}
	descriptor := DescribeTool(schema)
	base, err := ProjectSDKTool(BaseProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	openai, err := ProjectSDKTool(OpenAIProfile(), descriptor, ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertSDKToolCanonicalContract(t, base, openai)

	bound, err := ProjectSDKTool(BaseProfile(), descriptor, ToolProjectionOptions{BoundWorkspace: true})
	if err != nil {
		t.Fatal(err)
	}
	boundInput, ok := bound.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("bound create_plan schema type=%T", bound.InputSchema)
	}
	properties, _ := boundInput["properties"].(map[string]any)
	if _, exists := properties["workspace_id"]; exists {
		t.Fatalf("bound create_plan still exposes workspace_id: %#v", boundInput)
	}
	if required, ok := boundInput["required"].([]any); ok {
		for _, item := range required {
			if item == "workspace_id" {
				t.Fatalf("bound create_plan still requires workspace_id: %#v", boundInput)
			}
		}
	}
}

func TestCreatePlanContractEquivalentAcrossHTTPAndStdio(t *testing.T) {
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
	httpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "plan-http-contract-test", Version: "1.0.0"}, nil)
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
	stdioClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "plan-stdio-contract-test", Version: "1.0.0"}, nil)
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

	schema, ok := runtime.Registry.Schema(tools.CreatePlanToolName)
	if !ok {
		t.Fatal("create_plan schema missing")
	}
	expected, err := ProjectSDKTool(BaseProfile(), DescribeTool(schema), ToolProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	httpTool := findSDKTool(t, httpTools.Tools, tools.CreatePlanToolName)
	stdioTool := findSDKTool(t, stdioTools.Tools, tools.CreatePlanToolName)
	assertSDKToolCanonicalContract(t, expected, httpTool)
	assertSDKToolCanonicalContract(t, expected, stdioTool)
	assertSDKToolCanonicalContract(t, httpTool, stdioTool)
}
