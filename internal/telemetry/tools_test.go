package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"

	"go.mewis.me/codemcp/internal/logger"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/runtime/activity"
	"go.mewis.me/codemcp/internal/tools"
)

type telemetryConfigSetProvider struct{}

func (telemetryConfigSetProvider) BindSetApproval(_ context.Context, arguments map[string]any) (mcpconfigwire.SetApprovalBinding, mcpconfigwire.ErrorCode) {
	changes, _, err := mcpconfigwire.CanonicalSetArguments(arguments)
	if err != nil {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfigwire.ErrorInvalidRequest
	}
	return mcpconfigwire.SetApprovalBinding{Changes: changes, ConfigRoot: "/telemetry-test", ConfigFingerprint: "telemetry-fingerprint"}, ""
}

func (telemetryConfigSetProvider) ApplySet(context.Context, map[string]any, mcpconfigwire.SetApprovalBinding) (mcpconfigwire.MutationResult, *mcpconfigwire.MutationError) {
	return mcpconfigwire.MutationResult{}, &mcpconfigwire.MutationError{Code: mcpconfigwire.ErrorApplyFailed}
}

func TestAttachToolsPublishesActivityAndKeepsDefaultLogQuiet(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()
	registry := tools.NewRegistry()
	registry.MustRegister("echo", tools.Schema{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, map[string]any) (tools.Result, error) { return tools.TextResult("ok"), nil })
	runtime := &tools.Runtime{Registry: registry}
	stream := activity.NewStream()
	var output bytes.Buffer
	AttachTools(runtime, stream, logger.NewWithWriter(logger.Info, &output))
	args := map[string]any{"workspace_id": "ws_test", "message": "hello"}
	params := map[string]any{"name": "echo", "arguments": args, "requestState": "state_test", "inputResponses": map[string]any{"approval": true}, "_meta": map[string]any{"request_id": "req_test"}}
	request := map[string]any{"jsonrpc": "2.0", "id": "call_1", "method": "tools/call", "params": params}
	ctx := tools.WithCallRequest(tools.WithCallSource(context.Background(), "tunnel"), request)
	ctx = tools.WithCallDetails(ctx, "tools/call", params)
	result, err := runtime.Call(ctx, "echo", args)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if output.Len() != 0 {
		t.Fatalf("default tool logging should be quiet: %q", output.String())
	}
	events := stream.Recent(10)
	if len(events) != 2 || events[0].Phase != "start" || events[0].Status != "running" || events[1].Phase != "finish" {
		t.Fatalf("events=%#v", events)
	}
	event := events[1]
	if event.Kind != "tool_call" || event.CallID == "" || event.Source != "tunnel" || event.Tool != "echo" || event.WorkspaceID != "" || event.Status != "ok" {
		t.Fatalf("event=%#v", event)
	}
	if event.Raw != nil || events[0].Raw != nil {
		t.Fatalf("summary stream retained raw payload: %#v", events)
	}
	detail, ok := stream.FindCallDetail(event.CallID)
	if !ok || detail.Request == nil || detail.Response == nil {
		t.Fatalf("tool detail=%#v ok=%t", detail, ok)
	}
	requestDetail, ok := detail.Request.(map[string]any)
	if !ok || requestDetail["method"] != "tools/call" {
		t.Fatalf("request detail=%#v", detail.Request)
	}
	requestParams, ok := requestDetail["params"].(map[string]any)
	if !ok || requestParams["requestState"] != "state_test" {
		t.Fatalf("request params=%#v", requestDetail)
	}
	requestArgs, ok := requestParams["arguments"].(map[string]any)
	if !ok || requestArgs["message"] != "hello" {
		t.Fatalf("request arguments=%#v", requestParams)
	}
	response, ok := detail.Response.(map[string]any)
	if !ok || response["resultType"] != "complete" {
		t.Fatalf("response detail=%#v", detail.Response)
	}
}

func TestAttachToolsVerboseLogsStartAndCompletion(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()
	registry := tools.NewRegistry()
	registry.MustRegister("echo", tools.Schema{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, map[string]any) (tools.Result, error) { return tools.TextResult("ok"), nil })
	runtime := &tools.Runtime{Registry: registry}
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Mode: logger.ModeVerbose, Writer: &output})
	AttachTools(runtime, nil, log)
	_, err := runtime.Call(tools.WithCallSource(context.Background(), "tunnel"), "echo", map[string]any{"workspace_id": "ws_test"})
	if err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "Tool call started") || !strings.Contains(text, "Tool call completed") || !strings.Contains(text, "tool: echo") || !strings.Contains(text, "status: running") {
		t.Fatalf("verbose output = %q", text)
	}
	if strings.Index(text, "Tool call started") > strings.Index(text, "Tool call completed") {
		t.Fatalf("start log must precede completion: %q", text)
	}
}

func TestAttachToolsVerboseLogsStartBeforeToolReturns(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()
	registry := tools.NewRegistry()
	entered := make(chan struct{})
	release := make(chan struct{})
	registry.MustRegister("slow", tools.Schema{Name: "slow", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, map[string]any) (tools.Result, error) {
		close(entered)
		<-release
		return tools.TextResult("ok"), nil
	})
	runtime := &tools.Runtime{Registry: registry}
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Mode: logger.ModeVerbose, Writer: &output})
	AttachTools(runtime, nil, log)
	done := make(chan error, 1)
	go func() {
		_, err := runtime.Call(tools.WithCallSource(context.Background(), "tunnel"), "slow", map[string]any{})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	text := output.String()
	if !strings.Contains(text, "Tool call started") || strings.Contains(text, "Tool call completed") {
		t.Fatalf("start log was not emitted before tool completion: %q", text)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool did not complete")
	}
	if !strings.Contains(output.String(), "Tool call completed") {
		t.Fatalf("completion log missing: %q", output.String())
	}
}

func TestAttachToolsConfigSetActivityAndVerboseLogNeverExposeValues(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()
	runtime := tools.NewRuntime()
	provider := telemetryConfigSetProvider{}
	runtime.SetConfigSetApprovalProvider(provider)
	runtime.SetConfigSetApplyProvider(provider)
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stream := activity.NewStream()
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Mode: logger.ModeVerbose, Writer: &output})
	AttachTools(runtime, stream, log)
	privateValue := "telemetry-private-credential-like-value"
	args := map[string]any{
		"workspace_id": workspace.ID,
		"changes": []any{
			map[string]any{"key": "tunnel.organization_id", "value": privateValue},
		},
	}
	params := map[string]any{"name": mcpconfigwire.SetToolName, "arguments": args}
	request := map[string]any{"jsonrpc": "2.0", "id": "config-set-call", "method": "tools/call", "params": params}
	ctx := tools.WithCallSource(context.Background(), "tunnel")
	ctx = tools.WithApprovalCorrelation(ctx, "telemetry-caller", "telemetry-request")
	ctx = tools.WithCallDetails(ctx, "tools/call", params)
	ctx = tools.WithCallRequest(ctx, request)
	result, err := runtime.Call(ctx, mcpconfigwire.SetToolName, args)
	if err != nil || !result.IsError {
		t.Fatalf("config_set result=%#v err=%v", result, err)
	}
	events := stream.Recent(10)
	eventJSON, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := stream.FindCallDetail(events[len(events)-1].CallID)
	if !ok {
		t.Fatal("config_set diagnostic detail missing")
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(eventJSON) + "\n" + string(detailJSON) + "\n" + output.String()
	if strings.Contains(combined, privateValue) {
		t.Fatalf("config_set telemetry/log leaked user value: %s", combined)
	}
	if !strings.Contains(string(detailJSON), "tunnel.organization_id") || !strings.Contains(string(detailJSON), "change_count") {
		t.Fatalf("config_set diagnostic lost safe summary: %s", detailJSON)
	}
	if !strings.Contains(output.String(), "Tool call started") || !strings.Contains(output.String(), "Tool call failed") {
		t.Fatalf("config_set approval lifecycle missing: %q", output.String())
	}
}
