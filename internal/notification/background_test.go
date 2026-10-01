package notification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/explain"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

type backgroundTelegramSender struct {
	messages chan Message
}

type backgroundTestExplainer struct {
	input explain.CommandInput
}

func (e *backgroundTestExplainer) Generate(_ context.Context, input explain.CommandInput) (explain.Explanation, error) {
	e.input = input
	return explain.Explanation{Summary: "Runs the retained command", Steps: []string{"Execute command"}, ProviderID: "ollama", Model: "qwen3"}, nil
}

func (s *backgroundTelegramSender) SendNotification(_ context.Context, message Message) error {
	select {
	case s.messages <- message:
	default:
	}
	return nil
}

func TestBackgroundJobNotificationUsesGenericTelegramProviderWithoutConsumingModelDelivery(t *testing.T) {
	broker := backgrounddelivery.New(nil)
	t.Cleanup(broker.Close)
	owner := backgrounddelivery.Owner{ID: "owner-notification", Generation: "generation-notification"}
	if !broker.RegisterStart(backgrounddelivery.Registration{WorkspaceID: "ws_notify", ProcessID: "proc_notify", ExecutionID: "exec_notify", Owner: owner}) {
		t.Fatal("background delivery registration failed")
	}
	finished := time.Now().UTC()
	exitCode := 0
	event := shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_notify", ProcessID: "proc_notify", ExecutionID: "exec_notify", Tool: "start_process",
		Status: shellruntime.ExecutionStatusSuccess, Reason: shellruntime.BackgroundTerminalExit,
		ExitCode: &exitCode, StartedAt: finished.Add(-1500 * time.Millisecond).Format(time.RFC3339Nano), FinishedAt: finished.Format(time.RFC3339Nano),
	}
	broker.ApplyTerminal(event)
	values, err := broker.List("ws_notify", owner)
	if err != nil || len(values) != 1 {
		t.Fatalf("deliveries=%#v err=%v", values, err)
	}

	sender := &backgroundTelegramSender{messages: make(chan Message, 1)}
	coordinator := NewCoordinator(CoordinatorOptions{Attempts: 1})
	coordinator.Register(NewTelegramProvider(sender))
	defer coordinator.Stop()
	bridge := NewBackgroundJobBridge(nil, coordinator, BackgroundJobBridgeOptions{Policy: func() BackgroundJobPolicy {
		return BackgroundJobPolicy{Enabled: true, Providers: map[string]bool{ProviderTelegram: true}}
	}})
	bridge.consume(context.Background(), event)

	select {
	case message := <-sender.messages:
		if message.Kind != KindBackgroundJobFinished || message.WorkspaceID != "ws_notify" || message.TargetTool != "start_process" {
			t.Fatalf("telegram background message=%#v", message)
		}
		if message.ProcessID != "proc_notify" || message.ExecutionID != "exec_notify" || message.Status != shellruntime.ExecutionStatusSuccess || message.Reason != string(shellruntime.BackgroundTerminalExit) || message.DurationMS != 1500 || message.ExitCode == nil || *message.ExitCode != 0 {
			t.Fatalf("telegram background structured fields=%#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("Telegram provider did not receive background terminal notification")
	}
	current, err := broker.Peek("ws_notify", owner, values[0].ID)
	if err != nil || current.State != backgrounddelivery.DeliveryPending {
		t.Fatalf("human notification changed model delivery state=%#v err=%v", current, err)
	}
}

func TestBackgroundJobMessageContainsNoCommandOrOutputPayload(t *testing.T) {
	exitCode := 23
	signal := "SIGTERM"
	message, ok := backgroundJobMessage(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_safe", ProcessID: "proc_safe", ExecutionID: "exec_safe", Tool: "start_process",
		Status: shellruntime.ExecutionStatusFailed, Reason: shellruntime.BackgroundTerminalFailure,
		ExitCode: &exitCode, Signal: &signal, SessionHash: "session-secret", CallID: "call-secret",
	})
	if !ok {
		t.Fatal("background message was not created")
	}
	if message.Kind != KindBackgroundJobFinished || message.Body == "" {
		t.Fatalf("background message=%#v", message)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"session-secret", "call-secret", "session_hash", "call_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("background notification leaked %q: %s", forbidden, encoded)
		}
	}
	if message.ProcessID != "proc_safe" || message.ExecutionID != "exec_safe" || message.ExitCode == nil || *message.ExitCode != 23 || message.Signal != "SIGTERM" {
		t.Fatalf("background message lost safe terminal metadata: %#v", message)
	}
}

func TestBackgroundJobExplanationEnrichesExistingNotificationWithoutLeakingCommand(t *testing.T) {
	sender := &backgroundTelegramSender{messages: make(chan Message, 1)}
	coordinator := NewCoordinator(CoordinatorOptions{Attempts: 1})
	coordinator.Register(NewTelegramProvider(sender))
	defer coordinator.Stop()
	explainer := &backgroundTestExplainer{}
	bridge := NewBackgroundJobBridge(nil, coordinator, BackgroundJobBridgeOptions{Explainer: explainer})
	message := Message{ID: "background:proc_explain", Kind: KindBackgroundJobFinished, Title: "Background process completed", WorkspaceID: "ws_explain", ProcessID: "proc_explain", TargetTool: "start_process"}
	bridge.wg.Add(1)
	bridge.enrich(context.Background(), message, "echo token=secret-value", map[string]bool{ProviderTelegram: true})

	select {
	case enriched := <-sender.messages:
		if !enriched.Update || enriched.ID != message.ID || enriched.Explanation == nil || enriched.Explanation.Summary == "" {
			t.Fatalf("enriched message=%#v", enriched)
		}
		encoded, err := json.Marshal(enriched)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "echo token=secret-value") {
			t.Fatalf("notification leaked command: %s", encoded)
		}
	case <-time.After(time.Second):
		t.Fatal("background explanation update was not delivered")
	}
	if explainer.input.Command != "echo token=secret-value" || explainer.input.TargetTool != "start_process" {
		t.Fatalf("explainer input=%#v", explainer.input)
	}
}
