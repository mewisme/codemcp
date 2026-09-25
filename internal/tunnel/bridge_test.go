package tunnel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/tunnel-client/pkg/tunnelctx"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestSDKBridgePropagatesTunnelSessionID(t *testing.T) {
	registry := tools.NewRegistry()
	seen := make(chan string, 1)
	registry.MustRegister("session_probe", tools.Schema{Name: "session_probe", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		seen <- tools.MCPSessionID(ctx)
		return tools.TextResult("ok"), nil
	})
	bridge, err := newSDKBridge(&tools.Runtime{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	handler := bridge.toolHandler("session_probe")
	ctx := tunnelctx.ContextWithSessionID(context.Background(), "session-a")
	if _, err := handler(ctx, &sdkmcp.CallToolRequest{Params: &sdkmcp.CallToolParamsRaw{Name: "session_probe", Arguments: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "session-a" {
		t.Fatalf("session id = %q", got)
	}
}

func TestSDKBridgeConsumesInternalSessionMetaWithoutLoggingIt(t *testing.T) {
	registry := tools.NewRegistry()
	seen := make(chan string, 1)
	registry.MustRegister("session_probe", tools.Schema{Name: "session_probe", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		seen <- tools.MCPSessionID(ctx)
		return tools.TextResult("ok"), nil
	})
	runtime := &tools.Runtime{Registry: registry}
	observed := make(chan tools.CallObservation, 2)
	runtime.SetCallObserver(func(value tools.CallObservation) { observed <- value })
	bridge, err := newSDKBridge(runtime)
	if err != nil {
		t.Fatal(err)
	}
	handler := bridge.toolHandler("session_probe")
	request := &sdkmcp.CallToolRequest{Params: &sdkmcp.CallToolParamsRaw{Name: "session_probe", Arguments: json.RawMessage(`{}`), Meta: sdkmcp.Meta{sessionMetaKey: "session-meta", "client": "keep"}}}
	if _, err := handler(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "session-meta" {
		t.Fatalf("session id = %q", got)
	}
	if _, exists := request.Params.Meta[sessionMetaKey]; exists {
		t.Fatal("internal session metadata was not consumed")
	}
	for i := 0; i < 2; i++ {
		value := <-observed
		data, err := json.Marshal(value.Raw)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "session-meta") || strings.Contains(string(data), sessionMetaKey) {
			t.Fatalf("raw activity leaked session metadata: %s", data)
		}
	}
}

func TestSDKBridgeCallsSharedToolsRuntime(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister("echo", tools.Schema{
		Name: "echo", Description: "Echo text.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Annotations: tools.ToolAnnotations(tools.RiskRead),
	}, func(_ context.Context, args map[string]any) (tools.Result, error) {
		text, _ := args["text"].(string)
		return tools.TextResult("echo:" + text), nil
	})
	runtime := &tools.Runtime{Registry: registry}
	bridge, err := newSDKBridge(runtime)
	if err != nil {
		t.Fatal(err)
	}

	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- bridge.Run(ctx, serverTransport) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "bridge-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %#v", result)
	}
	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok || text.Text != "echo:hello" {
		t.Fatalf("content = %#v", result.Content)
	}

	registry.MustRegister("later", tools.Schema{Name: "later", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.TextResult("later-ok"), nil
	})
	deadline := time.Now().Add(time.Second)
	for {
		result, err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "later", Arguments: map[string]any{}})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dynamic tool did not reach SDK server: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	text, ok = result.Content[0].(*sdkmcp.TextContent)
	if !ok || text.Text != "later-ok" {
		t.Fatalf("dynamic content = %#v", result.Content)
	}

	cancel()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("bridge server did not stop")
	}
}

func TestSDKBridgeAcceptsIntegerArgumentsForBuiltInTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.ts"), []byte("ValkeyRedis\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoints"))
	registry := tools.NewRegistry()
	tools.RegisterFilesystemTools(registry, workspaces, checkpoints)
	bridge, err := newSDKBridge(&tools.Runtime{Registry: registry, Workspaces: workspaces, Checkpoints: checkpoints})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(map[string]any{
		"workspace_id": item.ID,
		"path":         root,
		"pattern":      "ValkeyRedis",
		"glob":         "*.ts",
		"output_mode":  "files_with_matches",
		"head_limit":   200,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := bridge.toolHandler("grep")(context.Background(), &sdkmcp.CallToolRequest{Params: &sdkmcp.CallToolParamsRaw{Name: "grep", Arguments: arguments}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned tool error: %#v", result)
	}
}

func TestSDKBridgeModernRequestsDoNotUseServerSessionAsApplicationIdentity(t *testing.T) {
	registry := tools.NewRegistry()
	type observed struct {
		session     string
		correlation tools.ApprovalCorrelation
	}
	seen := make(chan observed, 2)
	registry.MustRegister("session_probe", tools.Schema{Name: "session_probe", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		seen <- observed{session: tools.MCPSessionID(ctx), correlation: tools.ApprovalCorrelationFromContext(ctx)}
		return tools.TextResult("ok"), nil
	})
	bridge, err := newSDKBridge(&tools.Runtime{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- bridge.Run(ctx, serverTransport) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "bridge-session-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for range 2 {
		if _, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_probe", Arguments: map[string]any{}}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := <-seen, <-seen
	if first.session != "" || second.session != "" {
		t.Fatalf("modern SDK requests inherited server session ids = %q, %q", first.session, second.session)
	}
	if first.correlation.CallerID == "" || first.correlation.CallerID != second.correlation.CallerID || first.correlation.RequestID == second.correlation.RequestID {
		t.Fatalf("modern approval correlations = %#v / %#v", first.correlation, second.correlation)
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("bridge server did not stop")
	}
}

func TestSDKBridgeApprovalFlowUsesStatelessCallerScope(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.Instance()
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	runtime := &tools.Runtime{Registry: registry, Workspaces: manager, SessionAccess: tools.NewSessionWorkspaceAccessManager(), Approvals: approval.NewManager(identity.ID)}
	registry.MustRegister("guarded_action", tools.Schema{Name: "guarded_action", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`)}, func(ctx context.Context, args map[string]any) (tools.Result, error) {
		if requestID := tools.ApprovalRequestID(ctx); requestID != "" {
			return tools.JSONResult(map[string]any{"approved_request": requestID}), nil
		}
		command, _ := args["command"].(string)
		return tools.Result{}, controlguard.New(controlguard.CodeControlPlaneMutation, "guarded action requires approval", true, &controlguard.Invocation{Program: "cm", Args: []string{"update"}, Command: command})
	})
	tools.RegisterApprovalTools(registry, runtime)
	bridge, err := newSDKBridge(runtime)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- bridge.Run(ctx, serverTransport) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "bridge-approval-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	args := map[string]any{"workspace_id": item.ID, "command": "cm update"}
	guarded, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "guarded_action", Arguments: args})
	if err != nil || !guarded.IsError {
		t.Fatalf("guarded result=%#v err=%v", guarded, err)
	}
	body, ok := guarded.StructuredContent.(map[string]any)
	if !ok || body["code"] != "approval_required" {
		t.Fatalf("guarded structured content=%#v", guarded.StructuredContent)
	}
	challengeID, _ := body["challenge_id"].(string)
	if challengeID == "" {
		t.Fatalf("challenge id missing: %#v", body)
	}
	resultCh := make(chan *sdkmcp.CallToolResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tools.ApprovalRequestToolName, Arguments: map[string]any{"workspace_id": item.ID, "challenge_id": challengeID, "title": "Update CodeMCP"}})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()
	request := waitForTunnelApproval(t, runtime.Approvals)
	if _, err := runtime.Approvals.Approve(request.ID, "test", "reviewed"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	case approved := <-resultCh:
		if approved.IsError {
			t.Fatalf("approval result=%#v", approved)
		}
	case <-time.After(time.Second):
		t.Fatal("approval tool did not resolve")
	}
	retry, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "guarded_action", Arguments: args})
	if err != nil || retry.IsError {
		t.Fatalf("approved retry=%#v err=%v", retry, err)
	}
	payload, ok := retry.StructuredContent.(map[string]any)
	if !ok || payload["approved_request"] != request.ID {
		t.Fatalf("approved retry structured content=%#v", retry.StructuredContent)
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("bridge server did not stop")
	}
}

func waitForTunnelApproval(t *testing.T, manager *approval.Manager) approval.Request {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests := manager.List(approval.Filter{Status: approval.StatusPending})
		if len(requests) == 1 {
			return requests[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approval request did not become pending")
	return approval.Request{}
}

func TestSDKResultPreservesMRTRWireShape(t *testing.T) {
	result, err := sdkResultFromTools(tools.Result{
		ResultType:   "input_required",
		RequestState: "opaque-state",
		InputRequests: map[string]any{
			"confirm": map[string]any{
				"method": "elicitation/create",
				"params": map[string]any{"message": "Continue?", "requestedSchema": map[string]any{"type": "object"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{`"resultType":"input_required"`, `"requestState":"opaque-state"`, `"inputRequests"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s in %s", expected, text)
		}
	}
	if !strings.Contains(text, `"content":[]`) {
		t.Fatalf("official Go MCP SDK CallToolResult must marshal its required empty content field: %s", text)
	}
}

func TestSDKToolConversionPreservesAnnotationsAndHeaderSchema(t *testing.T) {
	readOnly := true
	schema := tools.Schema{
		Name: "probe", Title: "Probe", Description: "Probe tool.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"tenant":{"type":"string","x-mcp-header":"Tenant"}}}`),
		Annotations: map[string]any{"readOnlyHint": readOnly, "openWorldHint": false},
	}
	tool, err := sdkToolFromSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name != "probe" || tool.Title != "Probe" || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("tool = %#v", tool)
	}
	data, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"x-mcp-header":"Tenant"`) {
		t.Fatalf("input schema lost x-mcp-header: %s", data)
	}
}
