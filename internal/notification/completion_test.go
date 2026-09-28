package notification

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

func TestCompletionHookUsesSharedCoordinatorAndDeduplicatesRecord(t *testing.T) {
	provider := &fakeProvider{name: ProviderDesktop, called: make(chan struct{}, 4)}
	coordinator := NewCoordinator(CoordinatorOptions{Attempts: 1})
	coordinator.Register(provider)
	defer coordinator.Stop()

	hook := NewCompletionHook(coordinator, CompletionHookOptions{Policy: func() CompletionPolicy {
		return CompletionPolicy{Enabled: true, Providers: map[string]bool{ProviderDesktop: true}}
	}})
	event := completionNotificationEvent("cmp_one", agentcompletion.StatusCompleted, "Finished work", "All requested changes passed verification")
	if err := hook.Handle(t.Context(), agentcompletion.HookInvocation{Event: event, IdempotencyKey: "notification:event-a"}); err != nil {
		t.Fatal(err)
	}
	duplicate := event
	duplicate.ID = "completion-event:duplicate-transport-event"
	if err := hook.Handle(t.Context(), agentcompletion.HookInvocation{Event: duplicate, IdempotencyKey: "notification:event-b"}); err != nil {
		t.Fatal(err)
	}

	waitForDiagnostics(t, coordinator, 1)
	calls, messages := provider.snapshot()
	if calls != 1 || len(messages) != 1 {
		t.Fatalf("completion record delivered %d times: %#v", calls, messages)
	}
	message := messages[0]
	if message.Kind != KindCompletionAccepted || message.CompletionID != "cmp_one" || message.WorkspaceID != "ws_test" {
		t.Fatalf("completion message=%#v", message)
	}
	if message.Status != "completed" || message.Subject != "Finished work" || message.Summary != "All requested changes passed verification" {
		t.Fatalf("completion structured presentation fields=%#v", message)
	}
	if message.RequestID != "" || message.TargetTool != "" || len(message.Actions) != 0 {
		t.Fatalf("completion notification leaked approval/tool surface fields: %#v", message)
	}
	diagnostics := coordinator.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].CompletionID != "cmp_one" || diagnostics[0].Event != string(KindCompletionAccepted) {
		t.Fatalf("completion diagnostics=%#v", diagnostics)
	}
}

func TestCompletionMessageMapsStatusesAndBoundsSafeContent(t *testing.T) {
	for status, wantTitle := range map[agentcompletion.Status]string{
		agentcompletion.StatusCompleted: "Agent completed",
		agentcompletion.StatusPartial:   "Agent partially completed",
		agentcompletion.StatusBlocked:   "Agent blocked",
		agentcompletion.StatusCancelled: "Agent cancelled",
	} {
		t.Run(string(status), func(t *testing.T) {
			event := completionNotificationEvent("cmp_"+string(status), status, "Status title", "Status summary")
			message, ok := completionMessage(event)
			if !ok || message.Title != wantTitle {
				t.Fatalf("message=%#v ok=%t", message, ok)
			}
		})
	}

	secret := "secret-marker"
	querySecret := "query-secret"
	longSummary := strings.Repeat("bounded ", 200) + " Authorization: Bearer " + secret + " access_token=" + querySecret
	event := completionNotificationEvent("cmp_safe", agentcompletion.StatusCompleted, "Done token=title-secret", longSummary)
	message, ok := completionMessage(event)
	if !ok {
		t.Fatal("accepted completion did not produce notification")
	}
	for _, forbidden := range []string{secret, querySecret, "title-secret"} {
		if strings.Contains(message.Body, forbidden) || strings.Contains(message.Title, forbidden) || strings.Contains(message.Subject, forbidden) || strings.Contains(message.Summary, forbidden) {
			t.Fatalf("completion notification leaked %q: %#v", forbidden, message)
		}
	}
	if utf8.RuneCountInString(message.Body) > agentcompletion.MaxTitleRunes+3+maxCompletionNotificationSummaryRunes {
		t.Fatalf("completion notification body is unbounded: runes=%d", utf8.RuneCountInString(message.Body))
	}
	if !strings.Contains(message.Body, "<redacted>") {
		t.Fatalf("completion notification did not retain safe redaction marker: %q", message.Body)
	}
}

type delayedCompletionProvider struct {
	called chan Message
}

func (p *delayedCompletionProvider) Name() string { return ProviderDesktop }

func (p *delayedCompletionProvider) Notify(ctx context.Context, message Message) error {
	select {
	case <-time.After(20 * time.Millisecond):
		p.called <- message
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestCompletionHookDeliveryOutlivesHookInvocationContext(t *testing.T) {
	provider := &delayedCompletionProvider{called: make(chan Message, 1)}
	coordinator := NewCoordinator(CoordinatorOptions{Timeout: 100 * time.Millisecond, Attempts: 1})
	coordinator.Register(provider)
	defer coordinator.Stop()

	hook := NewCompletionHook(coordinator, CompletionHookOptions{Policy: func() CompletionPolicy {
		return CompletionPolicy{Enabled: true, Providers: map[string]bool{ProviderDesktop: true}}
	}})
	bus := agentcompletion.NewCompletionHookBus(agentcompletion.HookBusOptions{Timeout: 50 * time.Millisecond})
	defer bus.Stop()
	if err := bus.Register(hook); err != nil {
		t.Fatal(err)
	}
	event := completionNotificationEvent("cmp_detached", agentcompletion.StatusCompleted, "Finished", "Delayed delivery")
	if err := bus.Dispatch(event); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-provider.called:
		if message.CompletionID != event.Record.ID {
			t.Fatalf("message=%#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("completion delivery was cancelled when the hook invocation returned")
	}
	waitForDiagnostics(t, coordinator, 1)
	if got := coordinator.Diagnostics()[0].Status; got != DiagnosticDelivered {
		t.Fatalf("delivery status=%s", got)
	}
}

func TestCompletionNotificationProviderFailureIsBestEffort(t *testing.T) {
	provider := &fakeProvider{name: ProviderDesktop, failures: 10, called: make(chan struct{}, 4)}
	coordinator := NewCoordinator(CoordinatorOptions{Attempts: 2, RetryDelay: time.Millisecond})
	coordinator.Register(provider)
	defer coordinator.Stop()

	hook := NewCompletionHook(coordinator, CompletionHookOptions{Policy: func() CompletionPolicy {
		return CompletionPolicy{Enabled: true, Providers: map[string]bool{ProviderDesktop: true}}
	}})
	event := completionNotificationEvent("cmp_failure", agentcompletion.StatusBlocked, "Blocked", "Needs external input")
	if err := hook.Handle(t.Context(), agentcompletion.HookInvocation{Event: event}); err != nil {
		t.Fatalf("provider delivery failure escaped completion hook: %v", err)
	}
	waitForDiagnostics(t, coordinator, 1)
	diagnostic := coordinator.Diagnostics()[0]
	if diagnostic.Status != DiagnosticFailed || diagnostic.Attempts != 2 || diagnostic.CompletionID != "cmp_failure" {
		t.Fatalf("completion delivery diagnostic=%#v", diagnostic)
	}
}

func completionNotificationEvent(id string, status agentcompletion.Status, title, summary string) agentcompletion.Event {
	now := time.Now().UTC()
	return agentcompletion.Event{
		ID:        "completion-event:" + id,
		Name:      agentcompletion.EventAccepted,
		Timestamp: now,
		Record: agentcompletion.Record{
			ID:          id,
			WorkspaceID: "ws_test",
			Status:      status,
			Title:       title,
			Summary:     summary,
			CreatedAt:   now,
		},
	}
}
