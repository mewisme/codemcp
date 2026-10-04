package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/logger"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/runtime/activity"
	"go.mewis.me/codemcp/internal/tools"
)

type telemetryConfigSetProvider struct{}

type telemetryPlanProvider struct {
	err error
}

func (provider telemetryPlanProvider) AuthorPlan(context.Context, map[string]any) (any, error) {
	if provider.err != nil {
		return nil, provider.err
	}
	return map[string]any{"name": "telemetry-plan", "status": "pending"}, nil
}

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

func TestAttachToolsInlineApprovalMetadataStaysOutOfActivityAndLogs(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := tools.NewRuntime()
	defer runtime.CompletionHooks.Stop()
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const toolName = "telemetry_inline_guard"
	runtime.Registry.MustRegister(toolName, tools.Schema{
		Name:        toolName,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`),
		Approval:    &tools.ApprovalMetadata{Inline: true},
	}, func(ctx context.Context, args map[string]any) (tools.Result, error) {
		if requestID := tools.ApprovalRequestID(ctx); requestID != "" {
			return tools.JSONResult(map[string]any{"approved_request": requestID, "command": args["command"]}), nil
		}
		command, _ := args["command"].(string)
		return tools.Result{}, controlguard.New(
			controlguard.CodeControlPlaneMutation,
			"telemetry inline action requires approval",
			true,
			&controlguard.Invocation{Program: "cm", Args: []string{"update"}, Command: command},
		)
	})
	stream := activity.NewStream()
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Mode: logger.ModeVerbose, Writer: &output})
	AttachTools(runtime, stream, log)
	args := map[string]any{"workspace_id": workspace.ID, "command": "cm update"}
	firstCtx := tools.WithCallSource(context.Background(), "tunnel")
	firstCtx = tools.WithApprovalCorrelation(firstCtx, "telemetry-inline-caller", "telemetry-inline-first")
	first, err := runtime.Call(firstCtx, toolName, args)
	if err != nil || !first.IsError {
		t.Fatalf("first inline challenge=%#v err=%v", first, err)
	}
	data, err := json.Marshal(first.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var challenge map[string]any
	if err := json.Unmarshal(data, &challenge); err != nil {
		t.Fatal(err)
	}
	challengeID, _ := challenge["challenge_id"].(string)
	if challengeID == "" {
		t.Fatalf("challenge id missing: %#v", challenge)
	}
	output.Reset()
	const title = "TELEMETRY_INLINE_TITLE_6d9f"
	inlineArgs := map[string]any{
		"workspace_id": workspace.ID,
		"command":      "cm update",
		tools.InlineApprovalArgumentKey: map[string]any{
			tools.InlineApprovalChallengeID: challengeID,
			tools.InlineApprovalTitle:       title,
		},
	}
	params := map[string]any{"name": toolName, "arguments": inlineArgs}
	request := map[string]any{"jsonrpc": "2.0", "id": "inline-telemetry-call", "method": "tools/call", "params": params}
	secondCtx := tools.WithCallSource(context.Background(), "tunnel")
	secondCtx = tools.WithApprovalCorrelation(secondCtx, "telemetry-inline-caller", "telemetry-inline-second")
	secondCtx = tools.WithCallDetails(secondCtx, "tools/call", params)
	secondCtx = tools.WithCallRequest(secondCtx, request)
	type callResult struct {
		result tools.Result
		err    error
	}
	resultCh := make(chan callResult, 1)
	go func() {
		result, err := runtime.Call(secondCtx, toolName, inlineArgs)
		resultCh <- callResult{result: result, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	var pending approval.Request
	for time.Now().Before(deadline) {
		requests := runtime.Approvals.List(approval.Filter{Status: approval.StatusPending})
		if len(requests) == 1 {
			pending = requests[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pending.ID == "" {
		t.Fatal("inline telemetry approval request did not become pending")
	}
	if _, err := runtime.Approvals.Approve(pending.ID, "reviewer", "reviewed"); err != nil {
		t.Fatal(err)
	}
	resolved := <-resultCh
	if resolved.err != nil || resolved.result.IsError {
		t.Fatalf("inline telemetry result=%#v err=%v", resolved.result, resolved.err)
	}
	events := stream.Recent(16)
	if len(events) < 4 {
		t.Fatalf("activity events=%#v", events)
	}
	secondEvents := events[len(events)-2:]
	detail, ok := stream.FindCallDetail(secondEvents[len(secondEvents)-1].CallID)
	if !ok {
		t.Fatal("inline approval activity detail missing")
	}
	encodedEvents, err := json.Marshal(secondEvents)
	if err != nil {
		t.Fatal(err)
	}
	encodedDetail, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(encodedEvents) + "\n" + string(encodedDetail) + "\n" + output.String()
	for _, forbidden := range []string{tools.InlineApprovalArgumentKey, challengeID, title} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("inline approval metadata leaked through telemetry %q: %s", forbidden, combined)
		}
	}
	if !strings.Contains(string(encodedDetail), "cm update") {
		t.Fatalf("business arguments disappeared from activity detail: %s", encodedDetail)
	}
}

func TestAttachToolsPlanAuthoringKeepsBodiesOutOfAmbientObservability(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()

	const planSecret = "PLAN_AMBIENT_SECRET_41d9"
	const orderSecret = "ORDER_AMBIENT_SECRET_a02f"
	const errorSecret = "PLAN_ERROR_SECRET_f0aa"
	runtime := tools.NewRuntime()
	runtime.SetPlanAuthoringProvider(telemetryPlanProvider{err: errors.New(errorSecret)})
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stream := activity.NewStream()
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Mode: logger.ModeVerbose, Writer: &output})
	AttachTools(runtime, stream, log)

	args := map[string]any{
		"workspace_id":         workspace.ID,
		"mode":                 "create",
		"name":                 "telemetry-plan",
		"plan_content":         "# Plan\n\n" + planSecret,
		"implementation_order": "## Ordered phases\n\n" + orderSecret,
	}
	params := map[string]any{"name": tools.CreatePlanToolName, "arguments": args}
	request := map[string]any{"jsonrpc": "2.0", "id": "plan-call", "method": "tools/call", "params": params}
	ctx := tools.WithCallSource(context.Background(), "tunnel")
	ctx = tools.WithCallDetails(ctx, "tools/call", params)
	ctx = tools.WithCallRequest(ctx, request)
	result, err := runtime.Call(ctx, tools.CreatePlanToolName, args)
	if err != nil || !result.IsError {
		t.Fatalf("create_plan result=%#v err=%v", result, err)
	}

	events := stream.Recent(10)
	if len(events) != 2 || events[1].Message != "plan authoring failed" || events[0].Raw != nil || events[1].Raw != nil {
		t.Fatalf("plan ambient events=%#v", events)
	}
	ambientJSON, err := json.Marshal(activity.PublicEvents(events))
	if err != nil {
		t.Fatal(err)
	}
	ambient := string(ambientJSON) + "\n" + output.String()
	for _, secret := range []string{planSecret, orderSecret, errorSecret} {
		if strings.Contains(ambient, secret) {
			t.Fatalf("plan ambient observability leaked %q: %s", secret, ambient)
		}
	}
	if !strings.Contains(output.String(), "Tool call failed") || !strings.Contains(output.String(), "plan authoring failed") {
		t.Fatalf("plan failure lifecycle missing from verbose log: %q", output.String())
	}

	detail, ok := stream.FindCallDetail(events[1].CallID)
	if !ok || detail.Request == nil {
		t.Fatalf("plan call detail=%#v ok=%t", detail, ok)
	}
	requestJSON, err := json.Marshal(detail.Request)
	if err != nil {
		t.Fatal(err)
	}
	requestText := string(requestJSON)
	for _, secret := range []string{planSecret, orderSecret} {
		if strings.Contains(requestText, secret) {
			t.Fatalf("plan body leaked into explicit request detail: %s", requestText)
		}
	}
	for _, marker := range []string{"plan_content_bytes", "implementation_order_bytes", "telemetry-plan"} {
		if !strings.Contains(requestText, marker) {
			t.Fatalf("plan request detail lost safe metadata %q: %s", marker, requestText)
		}
	}
}
