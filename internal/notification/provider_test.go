package notification

import (
	"context"
	"strings"
	"testing"
)

func TestDesktopCommandUsesArgumentBoundaries(t *testing.T) {
	message := Message{Title: `Title "quoted"`, Body: `body $(touch nope); rm -rf /`}
	name, args, ok := desktopCommand("linux", message)
	if !ok || name != "notify-send" {
		t.Fatalf("linux command=%q args=%#v ok=%t", name, args, ok)
	}
	if len(args) != 4 || args[0] != "--app-name" || args[1] != "CodeMCP" || args[2] != message.Title || args[3] != message.Body {
		t.Fatalf("linux args=%#v", args)
	}
	name, args, ok = desktopCommand("darwin", message)
	if !ok || name != "osascript" || len(args) != 2 || args[0] != "-e" {
		t.Fatalf("darwin command=%q args=%#v ok=%t", name, args, ok)
	}
	if !strings.Contains(args[1], "display notification") || !strings.Contains(args[1], "with title") {
		t.Fatalf("darwin script=%q", args[1])
	}
	if _, _, ok := desktopCommand("plan9", message); ok {
		t.Fatal("unsupported desktop OS reported available")
	}
}

type telegramSenderFunc func(context.Context, Message) error

func (fn telegramSenderFunc) SendNotification(ctx context.Context, message Message) error {
	return fn(ctx, message)
}

func TestTelegramProviderDelegatesProviderNeutralMessage(t *testing.T) {
	var got Message
	provider := NewTelegramProvider(telegramSenderFunc(func(_ context.Context, message Message) error {
		got = message
		return nil
	}))
	message := Message{ID: "approval.pending:req_1", Kind: KindApprovalPending, RequestID: "req_1", Actions: []Action{{ID: "approve", Label: "Approve"}}}
	if !provider.Available() {
		t.Fatal("configured Telegram provider unavailable")
	}
	if err := provider.Notify(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if got.ID != message.ID || got.RequestID != "req_1" || len(got.Actions) != 1 || got.Actions[0].ID != "approve" {
		t.Fatalf("telegram message=%#v", got)
	}
	if NewTelegramProvider(nil).Available() {
		t.Fatal("unconfigured Telegram provider reported available")
	}
}
