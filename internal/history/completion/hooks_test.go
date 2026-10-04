package completion

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type testCompletionHook struct {
	name   string
	handle func(context.Context, HookInvocation) error
}

func (h testCompletionHook) Name() string { return h.name }

func (h testCompletionHook) Handle(ctx context.Context, invocation HookInvocation) error {
	if h.handle == nil {
		return nil
	}
	return h.handle(ctx, invocation)
}

func TestCompletionHooksRunOnlyAfterDurableAcceptanceAndCannotRollbackTruth(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := NewCompletionHookBus(HookBusOptions{Timeout: 25 * time.Millisecond, MaxRecent: 8})
	defer bus.Stop()

	const secretError = "provider failed with secret-token-value"
	durableSeen := make(chan bool, 1)
	if err := bus.Register(testCompletionHook{name: "failing", handle: func(_ context.Context, invocation HookInvocation) error {
		local := workspacestate.New(item.Path)
		path, pathErr := workspaceHistoryPath(local)
		if pathErr != nil {
			durableSeen <- false
			return errors.New(secretError)
		}
		data, readErr := os.ReadFile(path)
		durableSeen <- readErr == nil && strings.Contains(string(data), invocation.Event.Record.ID)
		return errors.New(secretError)
	}}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Register(testCompletionHook{name: "slow", handle: func(ctx context.Context, _ HookInvocation) error {
		<-ctx.Done()
		return ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}

	service, err := NewWorkspaceService(manager, Options{
		Hooks: bus,
		NewID: func() (string, error) { return "completion_hook_truth", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	record, created, err := service.Accept(
		Identity{AgentID: DeriveAgentID("caller-hooks", "generation-hooks"), Source: "mcp"},
		Input{WorkspaceID: item.ID, Status: StatusCompleted, Title: "Finished", Summary: "Verified"},
	)
	if err != nil || !created {
		t.Fatalf("record=%#v created=%t err=%v", record, created, err)
	}
	if persisted, ok, err := service.Get(record.ID); err != nil || !ok || persisted != record {
		t.Fatalf("accepted completion changed by hooks: persisted=%#v ok=%t err=%v", persisted, ok, err)
	}
	select {
	case ok := <-durableSeen:
		if !ok {
			t.Fatal("hook observed completion before durable workspace persistence")
		}
	case <-time.After(time.Second):
		t.Fatal("failing hook did not run")
	}

	diagnostics := waitHookDiagnostics(t, bus, 2)
	statuses := map[string]HookStatus{}
	for _, diagnostic := range diagnostics {
		statuses[diagnostic.Hook] = diagnostic.Status
	}
	if statuses["failing"] != HookFailed || statuses["slow"] != HookTimedOut {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	health := service.Diagnose(item.ID)
	if health.Status != HealthDegraded || health.Hooks.Registered != 2 || health.Hooks.Failures != 1 || health.Hooks.Timeouts != 1 {
		t.Fatalf("completion health=%#v", health)
	}
	data, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secretError) || strings.Contains(string(data), "secret-token-value") {
		t.Fatalf("hook diagnostics leaked raw error: %s", data)
	}
}

func TestCompletionHookBusDeduplicatesPerHookAndCarriesStableCorrelation(t *testing.T) {
	bus := NewCompletionHookBus(HookBusOptions{Timeout: time.Second, MaxSeen: 8})
	defer bus.Stop()
	var calls atomic.Int32
	invocations := make(chan HookInvocation, 2)
	if err := bus.Register(testCompletionHook{name: "codegraph", handle: func(_ context.Context, invocation HookInvocation) error {
		calls.Add(1)
		invocations <- invocation
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	record := Record{
		ID: "completion_abc", Sequence: 7, AgentID: "0123456789abcdef", WorkspaceID: "ws_demo",
		Status: StatusCompleted, Title: "Done", CreatedAt: time.Now().UTC(),
	}
	event := eventFor(record)
	for index := 0; index < 4; index++ {
		if err := bus.Dispatch(event); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case invocation := <-invocations:
		if invocation.Event != event {
			t.Fatalf("event=%#v want=%#v", invocation.Event, event)
		}
		if invocation.IdempotencyKey != "codegraph:"+event.ID || invocation.CorrelationID != record.ID {
			t.Fatalf("invocation=%#v", invocation)
		}
	case <-time.After(time.Second):
		t.Fatal("hook did not run")
	}
	diagnostics := waitHookDiagnostics(t, bus, 4)
	if calls.Load() != 1 {
		t.Fatalf("hook calls=%d diagnostics=%#v", calls.Load(), diagnostics)
	}
	succeeded, duplicates := 0, 0
	for _, diagnostic := range diagnostics {
		switch diagnostic.Status {
		case HookSucceeded:
			succeeded++
		case HookDuplicate:
			duplicates++
		}
	}
	if succeeded != 1 || duplicates != 3 {
		t.Fatalf("succeeded=%d duplicates=%d diagnostics=%#v", succeeded, duplicates, diagnostics)
	}
}

func TestCompletionHookBusCanSkipNotificationWithoutSkippingOtherHooks(t *testing.T) {
	bus := NewCompletionHookBus(HookBusOptions{Timeout: time.Second})
	defer bus.Stop()
	notificationCalls := make(chan struct{}, 1)
	otherCalls := make(chan struct{}, 1)
	if err := bus.Register(testCompletionHook{name: "notification", handle: func(context.Context, HookInvocation) error {
		notificationCalls <- struct{}{}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Register(testCompletionHook{name: "codegraph", handle: func(context.Context, HookInvocation) error {
		otherCalls <- struct{}{}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	record := Record{ID: "completion_skip_notification", Sequence: 10, AgentID: "0123456789abcdef", WorkspaceID: "ws_demo", Status: StatusCompleted, Title: "Done", CreatedAt: time.Now().UTC()}
	if err := bus.DispatchExcept(eventFor(record), map[string]bool{"notification": true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-otherCalls:
	case <-time.After(time.Second):
		t.Fatal("non-notification completion hook did not run")
	}
	select {
	case <-notificationCalls:
		t.Fatal("skipped notification hook ran")
	default:
	}
}

func TestCompletionHookInvocationIsIndependentValuePerHook(t *testing.T) {
	bus := NewCompletionHookBus(HookBusOptions{Timeout: time.Second})
	defer bus.Stop()
	observed := make(chan string, 1)
	if err := bus.Register(testCompletionHook{name: "mutator", handle: func(_ context.Context, invocation HookInvocation) error {
		invocation.Event.Record.Title = "mutated"
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Register(testCompletionHook{name: "observer", handle: func(_ context.Context, invocation HookInvocation) error {
		observed <- invocation.Event.Record.Title
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	record := Record{
		ID: "completion_value", Sequence: 9, AgentID: "0123456789abcdef", WorkspaceID: "ws_value",
		Status: StatusPartial, Title: "original", CreatedAt: time.Now().UTC(),
	}
	if err := bus.Dispatch(eventFor(record)); err != nil {
		t.Fatal(err)
	}
	select {
	case title := <-observed:
		if title != "original" {
			t.Fatalf("hook mutation leaked across invocation copies: %q", title)
		}
	case <-time.After(time.Second):
		t.Fatal("observer hook did not run")
	}
}

func TestCompletionHookStopCancelsOutstandingDispatchAndRejectsNewEvents(t *testing.T) {
	bus := NewCompletionHookBus(HookBusOptions{Timeout: time.Second})
	started := make(chan struct{}, 1)
	if err := bus.Register(testCompletionHook{name: "blocking", handle: func(ctx context.Context, _ HookInvocation) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	record := Record{
		ID: "completion_stop", Sequence: 3, AgentID: "0123456789abcdef", WorkspaceID: "ws_stop",
		Status: StatusCancelled, Title: "Stopped", CreatedAt: time.Now().UTC(),
	}
	event := eventFor(record)
	if err := bus.Dispatch(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking hook did not start")
	}
	bus.Stop()
	diagnostics := bus.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Status != HookCancelled {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if err := bus.Dispatch(event); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("dispatch after stop err=%v", err)
	}
	if err := bus.Register(testCompletionHook{name: "late"}); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("register after stop err=%v", err)
	}
}

func TestCompletionHookStopWaitsForHandlerExit(t *testing.T) {
	bus := NewCompletionHookBus(HookBusOptions{Timeout: 10 * time.Millisecond})
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	if err := bus.Register(testCompletionHook{name: "slow-exit", handle: func(context.Context, HookInvocation) error {
		started <- struct{}{}
		<-release
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	record := Record{
		ID: "completion_stop_wait", Sequence: 4, AgentID: "0123456789abcdef", WorkspaceID: "ws_stop_wait",
		Status: StatusCompleted, Title: "Stopped", CreatedAt: time.Now().UTC(),
	}
	if err := bus.Dispatch(eventFor(record)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow hook did not start")
	}

	stopped := make(chan struct{})
	go func() {
		bus.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned before hook handler exited")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not return after hook handler exited")
	}
}

func TestCompletionHookBusRejectsNonAcceptedEvents(t *testing.T) {
	bus := NewCompletionHookBus(HookBusOptions{})
	defer bus.Stop()
	for _, event := range []Event{
		{},
		{Name: "completion.other", ID: "event", Record: Record{ID: "completion"}},
		{Name: EventAccepted, ID: "event"},
	} {
		if err := bus.Dispatch(event); err == nil {
			t.Fatalf("invalid event accepted: %#v", event)
		}
	}
}

func waitHookDiagnostics(t *testing.T, bus *CompletionHookBus, count int) []HookDiagnostic {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		values := bus.Diagnostics()
		if len(values) >= count {
			return values
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d hook diagnostics: %#v", count, bus.Diagnostics())
	return nil
}
