package backgroundcontinuation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/backgrounddelivery"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

type testAdapter struct {
	id         string
	owner      backgrounddelivery.Owner
	capability Capability
	deliver    DeliverResult
	commit     DeliverResult
	delivered  chan Request
	mu         sync.Mutex
	requests   []Request
	closed     bool
}

func (a *testAdapter) ID() string { return a.id }
func (a *testAdapter) Owner(context.Context) (backgrounddelivery.Owner, error) {
	return a.owner, nil
}
func (a *testAdapter) Capability(context.Context, backgrounddelivery.Owner) (Capability, error) {
	return a.capability, nil
}
func (a *testAdapter) Deliver(_ context.Context, req Request) (DeliverResult, error) {
	a.mu.Lock()
	a.requests = append(a.requests, req)
	a.mu.Unlock()
	if a.delivered != nil {
		select {
		case a.delivered <- req:
		default:
		}
	}
	return a.deliver, nil
}
func (a *testAdapter) Commit(context.Context, Request) (DeliverResult, error) {
	return a.commit, nil
}
func (a *testAdapter) Close() error {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return nil
}

type fixedTail string

func (f fixedTail) Tail(context.Context, backgrounddelivery.Delivery, int) string {
	return string(f)
}

func materialize(t *testing.T, broker *backgrounddelivery.Broker, owner backgrounddelivery.Owner, process string) backgrounddelivery.Delivery {
	t.Helper()
	if !broker.RegisterStart(backgrounddelivery.Registration{WorkspaceID: "ws_test", ProcessID: process, ExecutionID: "exec_" + process, Owner: owner}) {
		t.Fatal("registration rejected")
	}
	broker.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_test", ProcessID: process, ExecutionID: "exec_" + process,
		Status: "success", Reason: shellruntime.BackgroundTerminalExit,
	})
	values, err := broker.List("ws_test", owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.ProcessID == process {
			return value
		}
	}
	t.Fatalf("delivery %q missing", process)
	return backgrounddelivery.Delivery{}
}

func TestCoordinatorResumesFromTerminalEventWithoutPolling(t *testing.T) {
	broker := backgrounddelivery.New(nil)
	t.Cleanup(broker.Close)
	owner := backgrounddelivery.Owner{ID: "owner", Generation: "generation"}
	adapter := &testAdapter{
		id: "reference", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
	}
	coordinator := &Coordinator{Broker: broker, Adapter: adapter, CoalesceWindow: -1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, "ws_test") }()

	delivery := materialize(t, broker, owner, "proc_async")
	select {
	case req := <-adapter.delivered:
		if req.Mode != ModeIdleContinuation || len(req.Completions) != 1 || req.Completions[0].DeliveryID != delivery.ID {
			t.Fatalf("continuation request=%#v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal event did not resume continuation")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, err := broker.Peek("ws_test", owner, delivery.ID)
		if err == nil && current.State == backgrounddelivery.DeliveryCommitted {
			cancel()
			<-done
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("delivered continuation was not committed")
}

func TestCoordinatorUnsupportedRetainsRecoverablePendingDelivery(t *testing.T) {
	broker := backgrounddelivery.New(nil)
	t.Cleanup(broker.Close)
	owner := backgrounddelivery.Owner{ID: "owner", Generation: "generation"}
	adapter := &testAdapter{
		id: "unsupported", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeUnsupported},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
	}
	delivery := materialize(t, broker, owner, "proc_pending")
	coordinator := &Coordinator{Broker: broker, Adapter: adapter, CoalesceWindow: -1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, "ws_test") }()
	select {
	case <-adapter.delivered:
	case <-time.After(time.Second):
		t.Fatal("unsupported adapter was not invoked")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, err := broker.Peek("ws_test", owner, delivery.ID)
		if err == nil && current.State == backgrounddelivery.DeliveryPending {
			cancel()
			<-done
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("unsupported delivery was not released back to recoverable pending state")
}

func TestCoordinatorTerminalNonDeliveryOutcomesRemainRecoverable(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeUnsupported, OutcomeStale, OutcomeClosed} {
		t.Run(string(outcome), func(t *testing.T) {
			broker := backgrounddelivery.New(nil)
			t.Cleanup(broker.Close)
			owner := backgrounddelivery.Owner{ID: "owner", Generation: "generation"}
			adapter := &testAdapter{
				id: "terminal-" + string(outcome), owner: owner,
				capability: Capability{IdleContinuation: true},
				deliver:    DeliverResult{Outcome: outcome},
				commit:     DeliverResult{Outcome: OutcomeDelivered},
				delivered:  make(chan Request, 1),
			}
			delivery := materialize(t, broker, owner, "proc_"+string(outcome))
			coordinator := &Coordinator{Broker: broker, Adapter: adapter, CoalesceWindow: -1}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- coordinator.Run(ctx, "ws_test") }()
			select {
			case <-adapter.delivered:
			case <-time.After(time.Second):
				t.Fatalf("%s adapter was not invoked", outcome)
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				current, err := broker.Peek("ws_test", owner, delivery.ID)
				if err == nil && current.State == backgrounddelivery.DeliveryPending {
					cancel()
					<-done
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatalf("%s outcome left delivery claimed", outcome)
		})
	}
}

func TestCoordinatorCloseClosesAdapterOnce(t *testing.T) {
	adapter := &testAdapter{id: "close"}
	coordinator := &Coordinator{Adapter: adapter}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	closed := adapter.closed
	adapter.mu.Unlock()
	if !closed {
		t.Fatal("adapter was not closed")
	}
}

func TestAuthorizedReconnectReclaimsPendingDeliveryByOwnerGeneration(t *testing.T) {
	broker := backgrounddelivery.New(nil)
	t.Cleanup(broker.Close)
	owner := backgrounddelivery.Owner{ID: "owner", Generation: "generation-a"}
	delivery := materialize(t, broker, owner, "proc_reconnect")

	wrong := &testAdapter{
		id:         "wrong-generation",
		owner:      backgrounddelivery.Owner{ID: owner.ID, Generation: "generation-b"},
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
	}
	wrongCoordinator := &Coordinator{Broker: broker, Adapter: wrong, CoalesceWindow: -1}
	wrongCtx, wrongCancel := context.WithCancel(context.Background())
	wrongDone := make(chan error, 1)
	go func() { wrongDone <- wrongCoordinator.Run(wrongCtx, "ws_test") }()
	select {
	case req := <-wrong.delivered:
		t.Fatalf("replacement generation stole completion: %#v", req)
	case <-time.After(25 * time.Millisecond):
	}
	wrongCancel()
	<-wrongDone

	reconnected := &testAdapter{
		id:         "reconnected",
		owner:      owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
	}
	coordinator := &Coordinator{Broker: broker, Adapter: reconnected, CoalesceWindow: -1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, "ws_test") }()
	select {
	case req := <-reconnected.delivered:
		if len(req.Completions) != 1 || req.Completions[0].DeliveryID != delivery.ID {
			t.Fatalf("reconnected request=%#v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("authorized reconnect did not reclaim pending completion")
	}
	cancel()
	<-done
}

func TestCoordinatorCoalescesMultipleCompletionsAtOneBoundary(t *testing.T) {
	broker := backgrounddelivery.New(nil)
	t.Cleanup(broker.Close)
	owner := backgrounddelivery.Owner{ID: "owner", Generation: "generation"}
	adapter := &testAdapter{
		id: "batch", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 2),
	}
	coordinator := &Coordinator{Broker: broker, Adapter: adapter, CoalesceWindow: 25 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, "ws_test") }()
	materialize(t, broker, owner, "proc_one")
	materialize(t, broker, owner, "proc_two")
	select {
	case req := <-adapter.delivered:
		if len(req.Completions) != 2 {
			t.Fatalf("expected coalesced batch of 2, got %#v", req.Completions)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced continuation was not delivered")
	}
	cancel()
	<-done
}

func TestCoordinatorDistinguishesSteeringFromIdleContinuation(t *testing.T) {
	mode, ok := selectMode(Capability{IdleContinuation: true, InFlightSteering: true, InFlight: true})
	if !ok || mode != ModeInFlightSteering {
		t.Fatalf("in-flight mode=%q ok=%t", mode, ok)
	}
	mode, ok = selectMode(Capability{IdleContinuation: true, InFlightSteering: true})
	if !ok || mode != ModeIdleContinuation {
		t.Fatalf("idle mode=%q ok=%t", mode, ok)
	}
	if _, ok := selectMode(Capability{}); ok {
		t.Fatal("unsupported capability unexpectedly selected a mode")
	}
}

func TestCompletionContextIsBoundedAndOptionalOutputTail(t *testing.T) {
	broker := backgrounddelivery.New(nil)
	t.Cleanup(broker.Close)
	owner := backgrounddelivery.Owner{ID: "owner", Generation: "generation"}
	delivery := materialize(t, broker, owner, "proc_tail")
	claim, err := broker.Claim("ws_test", owner, delivery.ID, "adapter")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &Coordinator{Broker: broker, Output: fixedTail(strings.Repeat("你", 2000)), OutputTail: 127}
	req := coordinator.buildRequest(context.Background(), "ws_test", owner, ModeIdleContinuation, []backgrounddelivery.ClaimResult{claim})
	if len(req.Completions) != 1 || len(req.Completions[0].OutputTail) > 127 {
		t.Fatalf("bounded completion=%#v", req.Completions)
	}
	if !strings.Contains(req.Completions[0].Summary, "Background process finished") {
		t.Fatalf("summary=%q", req.Completions[0].Summary)
	}
	if !utf8.ValidString(req.Completions[0].OutputTail) {
		t.Fatal("output tail is not valid UTF-8")
	}
}

func TestRetryableOutcomeRetriesWithStableIdempotencyKey(t *testing.T) {
	broker := backgrounddelivery.New(nil)
	t.Cleanup(broker.Close)
	owner := backgrounddelivery.Owner{ID: "owner", Generation: "generation"}
	adapter := &testAdapter{
		id: "retry", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeRetryable, RetryAfter: time.Millisecond},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 4),
	}
	delivery := materialize(t, broker, owner, "proc_retry")
	coordinator := &Coordinator{Broker: broker, Adapter: adapter, CoalesceWindow: -1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, "ws_test") }()
	first := <-adapter.delivered
	second := <-adapter.delivered
	if first.IdempotencyKey == "" || first.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("retry keys first=%q second=%q", first.IdempotencyKey, second.IdempotencyKey)
	}
	current, err := broker.Peek("ws_test", owner, delivery.ID)
	if err != nil || current.State != backgrounddelivery.DeliveryClaimed {
		t.Fatalf("retryable delivery state=%#v err=%v", current, err)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
