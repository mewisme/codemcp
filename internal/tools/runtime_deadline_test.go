package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToolCallContextReservesTunnelResponseTime(t *testing.T) {
	now := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	parent, cancel := context.WithDeadline(context.Background(), now.Add(20*time.Second))
	defer cancel()
	ctx, cancelCall := toolCallContext(parent, "tunnel", now)
	defer cancelCall()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(now.Add(15*time.Second)) {
		t.Fatalf("deadline=%v ok=%t want=%v", deadline, ok, now.Add(15*time.Second))
	}
}

func TestToolCallContextCapsTunnelResponseReserve(t *testing.T) {
	now := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	parent, cancel := context.WithDeadline(context.Background(), now.Add(time.Minute))
	defer cancel()
	ctx, cancelCall := toolCallContext(parent, "tunnel", now)
	defer cancelCall()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(now.Add(55*time.Second)) {
		t.Fatalf("deadline=%v ok=%t want=%v", deadline, ok, now.Add(55*time.Second))
	}
}

func TestToolCallContextCapsTunnelWithoutParentDeadline(t *testing.T) {
	now := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	ctx, cancelCall := toolCallContext(context.Background(), "tunnel", now)
	defer cancelCall()
	deadline, ok := ctx.Deadline()
	want := now.Add(tunnelToolBudget)
	if !ok || !deadline.Equal(want) {
		t.Fatalf("deadline=%v ok=%t want=%v", deadline, ok, want)
	}
}

func TestToolCallContextUsesLocalBudgetWhenParentDeadlineIsLonger(t *testing.T) {
	now := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	parent, cancel := context.WithDeadline(context.Background(), now.Add(10*time.Minute))
	defer cancel()
	ctx, cancelCall := toolCallContext(parent, "tunnel", now)
	defer cancelCall()
	deadline, ok := ctx.Deadline()
	want := now.Add(tunnelToolBudget)
	if !ok || !deadline.Equal(want) {
		t.Fatalf("deadline=%v ok=%t want=%v", deadline, ok, want)
	}
}

func TestToolCallContextLeavesNonTunnelDeadlineUntouched(t *testing.T) {
	now := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	want := now.Add(20 * time.Second)
	parent, cancel := context.WithDeadline(context.Background(), want)
	defer cancel()
	ctx, cancelCall := toolCallContext(parent, "http", now)
	defer cancelCall()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(want) {
		t.Fatalf("deadline=%v ok=%t want=%v", deadline, ok, want)
	}
}

func TestTunnelResponseBudgetErrorGuidesLongRunCommand(t *testing.T) {
	err := tunnelResponseBudgetError("run_command")
	for _, want := range []string{"start_process", "process_status", "process_output"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "then poll") || !strings.Contains(err.Error(), "not polling") {
		t.Fatalf("deadline guidance encourages polling: %v", err)
	}
}

func TestToolCallContextUsesTunnelBudgetCause(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	ctx, cancelCall := toolCallContext(parent, "tunnel", time.Now())
	defer cancelCall()
	<-ctx.Done()
	if cause := context.Cause(ctx); cause != errTunnelResponseBudgetExceeded {
		t.Fatalf("cause=%v want=%v", cause, errTunnelResponseBudgetExceeded)
	}
}

func TestRuntimeTunnelBudgetReturnsToolErrorBeforeParentCancellation(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister("run_command", Schema{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ map[string]any) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	runtime := &Runtime{Registry: registry}
	parent, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	parent = WithCallSource(parent, "tunnel")
	started := time.Now()
	result, err := runtime.Call(parent, "run_command", map[string]any{})
	if err != nil {
		t.Fatalf("Runtime.Call error = %v", err)
	}
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "start_process") {
		t.Fatalf("result = %#v", result)
	}
	if parent.Err() != nil {
		t.Fatalf("parent canceled before structured tool error was returned: %v", parent.Err())
	}
	if elapsed := time.Since(started); elapsed >= 115*time.Millisecond {
		t.Fatalf("runtime returned too close to parent deadline: %s", elapsed)
	}
}
