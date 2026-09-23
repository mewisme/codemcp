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
	"go.mewis.me/codemcp/internal/runtime/activity"
	"go.mewis.me/codemcp/internal/tools"
)

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
	if event.Raw["call_id"] != event.CallID {
		t.Fatalf("raw call id=%#v event call id=%q", event.Raw["call_id"], event.CallID)
	}
	if event.Raw["status"] != "ok" || event.Raw["result_type"] != "complete" {
		t.Fatalf("raw outcome=%#v", event.Raw)
	}
	rawParams, ok := event.Raw["params"].(map[string]any)
	if !ok || rawParams["requestState"] != "state_test" || rawParams["_meta"].(map[string]any)["request_id"] != "req_test" {
		t.Fatalf("raw params=%#v", event.Raw)
	}
	rawArgs, ok := event.Raw["arguments"].(map[string]any)
	if !ok || rawArgs["message"] != "hello" {
		t.Fatalf("raw arguments=%#v", event.Raw)
	}
	if _, ok := event.Raw["result"].(tools.Result); !ok {
		t.Fatalf("raw result=%#v", event.Raw["result"])
	}
	rawRequest, ok := event.Raw["request"].(map[string]any)
	if !ok || rawRequest["id"] != "call_1" || rawRequest["method"] != "tools/call" {
		t.Fatalf("raw request=%#v", event.Raw)
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
