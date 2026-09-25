package notification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
)

type fakeProvider struct {
	name     string
	failures int
	block    bool
	mu       sync.Mutex
	calls    int
	messages []Message
	called   chan struct{}
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Notify(ctx context.Context, message Message) error {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.messages = append(p.messages, message)
	p.mu.Unlock()
	if p.called != nil {
		select {
		case p.called <- struct{}{}:
		default:
		}
	}
	if p.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if call <= p.failures {
		return errors.New("provider failure with sensitive payload that diagnostics must not retain")
	}
	return nil
}

func (p *fakeProvider) snapshot() (int, []Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]Message(nil), p.messages...)
}

func TestGenericCoordinatorDispatchesProviderNeutralMessage(t *testing.T) {
	provider := &fakeProvider{name: ProviderDesktop, called: make(chan struct{}, 1)}
	coordinator := NewCoordinator(CoordinatorOptions{Attempts: 1})
	coordinator.Register(provider)
	defer coordinator.Stop()

	message := Message{
		ID: "completion:cmp_1", Kind: Kind("completion.accepted"), Title: "Task completed",
		Body: "bounded summary", WorkspaceID: "ws_a", Timestamp: time.Now().UTC(),
	}
	if err := coordinator.Dispatch(t.Context(), message, map[string]bool{ProviderDesktop: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.called:
	case <-time.After(time.Second):
		t.Fatal("generic notification was not delivered")
	}
	calls, messages := provider.snapshot()
	if calls != 1 || len(messages) != 1 || messages[0].ID != message.ID || messages[0].Kind != message.Kind {
		t.Fatalf("calls=%d messages=%#v", calls, messages)
	}
}

func TestApprovalPolicyDistinguishesRequestAndGrantLifecycle(t *testing.T) {
	policy := ApprovalPolicy{Enabled: true, Pending: true, Resolved: true}
	tests := []struct {
		name  string
		event approval.Event
		want  bool
	}{
		{name: "pending request", event: approval.Event{Name: approval.EventPending, Subject: approval.EventSubjectRequest, RequestID: "req_1"}, want: true},
		{name: "expired request", event: approval.Event{Name: approval.EventExpired, Subject: approval.EventSubjectRequest, RequestID: "req_1"}, want: true},
		{name: "expired challenge", event: approval.Event{Name: approval.EventExpired, Subject: approval.EventSubjectChallenge, RequestID: "req_1"}, want: false},
		{name: "revoked grant", event: approval.Event{Name: approval.EventRevoked, Subject: approval.EventSubjectGrant, RequestID: "req_1"}, want: true},
		{name: "claimed request", event: approval.Event{Name: approval.EventClaimed, Subject: approval.EventSubjectRequest, RequestID: "req_1"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := policy.Allows(test.event); got != test.want {
				t.Fatalf("Allows(%#v)=%t want=%t", test.event, got, test.want)
			}
		})
	}
}

func TestCoordinatorConsumesCanonicalEventsWithoutConsumingReviewSurface(t *testing.T) {
	manager := approval.NewManager("instance-notify")
	reviewer := manager.Events().Subscribe()
	defer manager.Events().Unsubscribe(reviewer)

	provider := &fakeProvider{name: ProviderDesktop, called: make(chan struct{}, 4)}
	coordinator := NewCoordinator(CoordinatorOptions{})
	coordinator.Register(provider)
	bridge := NewApprovalBridge(manager.Events(), coordinator, ApprovalBridgeOptions{
		Policy: func() ApprovalPolicy {
			return ApprovalPolicy{Enabled: true, Pending: true, Resolved: true, Providers: map[string]bool{ProviderDesktop: true}}
		},
	})
	if err := bridge.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()
	defer coordinator.Stop()

	request := seedApprovalRequest(t, manager, "caller-a", "ws_a")
	select {
	case event := <-reviewer.Events:
		if event.Name != approval.EventCreated {
			t.Fatalf("reviewer first event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("review surface did not receive canonical event")
	}
	select {
	case <-provider.called:
	case <-time.After(time.Second):
		t.Fatal("notification provider did not receive pending event")
	}
	calls, messages := provider.snapshot()
	if calls != 1 || len(messages) != 1 {
		t.Fatalf("calls=%d messages=%#v", calls, messages)
	}
	message := messages[0]
	if message.Kind != KindApprovalPending || message.RequestID != request.ID || len(message.Actions) != 3 {
		t.Fatalf("pending message=%#v", message)
	}
	for _, forbidden := range []string{"secret-command", "approval title"} {
		if strings.Contains(message.Body, forbidden) || strings.Contains(message.Title, forbidden) {
			t.Fatalf("message leaked unsafe request detail %q: %#v", forbidden, message)
		}
	}
}

func TestCoordinatorFailureAndTimeoutNeverChangeApprovalTruth(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		manager := approval.NewManager("instance-failure")
		provider := &fakeProvider{name: ProviderTelegram, failures: 10, called: make(chan struct{}, 8)}
		coordinator := NewCoordinator(CoordinatorOptions{Timeout: 30 * time.Millisecond, Attempts: 2, RetryDelay: time.Millisecond})
		coordinator.Register(provider)
		bridge := NewApprovalBridge(manager.Events(), coordinator, ApprovalBridgeOptions{
			Policy: func() ApprovalPolicy {
				return ApprovalPolicy{Enabled: true, Pending: true, Resolved: true, Providers: map[string]bool{ProviderTelegram: true}}
			},
		})
		if err := bridge.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer bridge.Stop()
		defer coordinator.Stop()

		request := seedApprovalRequest(t, manager, "caller-failure", "ws_failure")
		if _, err := approval.NewReviewService(manager).Resolve(approval.ReviewInput{Request: request.ID, Decision: approval.ReviewApprove, ResolvedBy: "test"}); err != nil {
			t.Fatalf("approval resolution failed because notifier failed: %v", err)
		}
		final, err := approval.NewReviewService(manager).View(request.ID)
		if err != nil || final.Status != approval.StatusApproved {
			t.Fatalf("approval truth=%#v err=%v", final, err)
		}
		waitForDiagnostics(t, coordinator, 2)
		for _, diagnostic := range coordinator.Diagnostics() {
			if diagnostic.Status != DiagnosticFailed || diagnostic.Attempts != 2 {
				t.Fatalf("diagnostic=%#v", diagnostic)
			}
			encoded := diagnostic.Provider + diagnostic.Event + diagnostic.RequestID + diagnostic.WorkspaceID
			if strings.Contains(encoded, "sensitive payload") {
				t.Fatalf("diagnostic retained provider error: %#v", diagnostic)
			}
		}
	})

	t.Run("timeout", func(t *testing.T) {
		manager := approval.NewManager("instance-timeout")
		provider := &fakeProvider{name: ProviderDesktop, block: true, called: make(chan struct{}, 4)}
		coordinator := NewCoordinator(CoordinatorOptions{Timeout: 20 * time.Millisecond, Attempts: 2, RetryDelay: 2 * time.Millisecond})
		coordinator.Register(provider)
		bridge := NewApprovalBridge(manager.Events(), coordinator, ApprovalBridgeOptions{
			Policy: func() ApprovalPolicy {
				return ApprovalPolicy{Enabled: true, Pending: true, Providers: map[string]bool{ProviderDesktop: true}}
			},
		})
		if err := bridge.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer bridge.Stop()
		defer coordinator.Stop()

		started := time.Now()
		request := seedApprovalRequest(t, manager, "caller-timeout", "ws_timeout")
		if request.Status != approval.StatusPending {
			t.Fatalf("request=%#v", request)
		}
		waitForDiagnostics(t, coordinator, 1)
		diagnostic := coordinator.Diagnostics()[0]
		if diagnostic.Status != DiagnosticFailed || diagnostic.Attempts != 2 {
			t.Fatalf("timeout diagnostic=%#v", diagnostic)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("notification timeout was not bounded: %v", elapsed)
		}
	})
}

func TestCoordinatorPolicyIsIndependentFromProviderPresence(t *testing.T) {
	manager := approval.NewManager("instance-policy")
	policy := ApprovalPolicy{
		Enabled: true, Pending: true, Resolved: false,
		Providers: map[string]bool{ProviderDesktop: true, ProviderTelegram: true},
	}
	desktop := &fakeProvider{name: ProviderDesktop, called: make(chan struct{}, 2)}
	coordinator := NewCoordinator(CoordinatorOptions{})
	coordinator.Register(desktop)
	bridge := NewApprovalBridge(manager.Events(), coordinator, ApprovalBridgeOptions{Policy: func() ApprovalPolicy { return policy }})
	if err := bridge.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()
	defer coordinator.Stop()

	request := seedApprovalRequest(t, manager, "caller-policy", "ws_policy")
	select {
	case <-desktop.called:
	case <-time.After(time.Second):
		t.Fatal("desktop pending notification missing")
	}
	waitForDiagnostics(t, coordinator, 2)
	diagnostics := coordinator.Diagnostics()
	foundUnavailable := false
	for _, value := range diagnostics {
		if value.Provider == ProviderTelegram && value.Status == DiagnosticUnavailable {
			foundUnavailable = true
		}
	}
	if !foundUnavailable {
		t.Fatalf("missing Telegram unavailable diagnostic: %#v", diagnostics)
	}
	if _, err := approval.NewReviewService(manager).Resolve(approval.ReviewInput{Request: request.ID, Decision: approval.ReviewDeny, ResolvedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	calls, _ := desktop.snapshot()
	if calls != 1 {
		t.Fatalf("resolved event ignored policy: calls=%d", calls)
	}
}

func seedApprovalRequest(t *testing.T, manager *approval.Manager, callerID, workspaceID string) approval.Request {
	t.Helper()
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		CallerID: callerID, RequestCorrelationID: "apr-test", SessionHash: "hash-test",
		WorkspaceID: workspaceID, Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": workspaceID, "command": "secret-command"},
		GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "approval title",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, callerID, workspaceID, "approval title")
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func waitForDiagnostics(t *testing.T, coordinator *Coordinator, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(coordinator.Diagnostics()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("diagnostics=%#v want at least %d", coordinator.Diagnostics(), count)
}
