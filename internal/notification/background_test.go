package notification

import (
	"context"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/backgrounddelivery"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

type backgroundTelegramSender struct {
	messages chan Message
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
	event := shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_notify", ProcessID: "proc_notify", ExecutionID: "exec_notify", Tool: "start_process",
		Status: shellruntime.ExecutionStatusSuccess, Reason: shellruntime.BackgroundTerminalExit,
		FinishedAt: time.Now().UTC().Format(time.RFC3339Nano),
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
	case <-time.After(time.Second):
		t.Fatal("Telegram provider did not receive background terminal notification")
	}
	current, err := broker.Peek("ws_notify", owner, values[0].ID)
	if err != nil || current.State != backgrounddelivery.DeliveryPending {
		t.Fatalf("human notification changed model delivery state=%#v err=%v", current, err)
	}
}

func TestBackgroundJobMessageContainsNoCommandOrOutputPayload(t *testing.T) {
	message, ok := backgroundJobMessage(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_safe", ProcessID: "proc_safe", Tool: "start_process",
		Status: shellruntime.ExecutionStatusFailed, Reason: shellruntime.BackgroundTerminalFailure,
	})
	if !ok {
		t.Fatal("background message was not created")
	}
	if message.Kind != KindBackgroundJobFinished || message.Body == "" {
		t.Fatalf("background message=%#v", message)
	}
}
