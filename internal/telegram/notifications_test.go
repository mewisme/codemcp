package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/explain"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/notification"
)

func TestCompletionNotificationUsesRichCompletionsTopicWithoutPlainDuplicate(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	if err := store.put(42, TopicCompletions, 121); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		root: t.TempDir(), api: api, topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{
			Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true,
			LogsMiniApp: LogsMiniAppHealth{Enabled: true, State: MiniAppReady, PublicURL: "https://logs-one.example"},
		},
	}
	if _, err := NewInterface(InterfaceOptions{Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	message := notification.Message{
		ID: "completion:cmp_1", Kind: notification.KindCompletionAccepted,
		Title: "Agent completed", Subject: "Finished implementation", Summary: "Tests and validation passed.",
		Status: "completed", CompletionID: "cmp_1", WorkspaceID: "ws_1",
		Timestamp: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC),
	}
	if err := runtime.SendNotification(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if len(api.richThreadScreens) != 1 || len(api.richThreadIDs) != 1 || api.richThreadIDs[0] != 121 {
		t.Fatalf("rich completion deliveries ids=%v screens=%d", api.richThreadIDs, len(api.richThreadScreens))
	}
	if len(api.threadSends) != 0 || len(api.generalSends) != 0 {
		t.Fatalf("completion notification duplicated as plain text thread=%v general=%v", api.threadSends, api.generalSends)
	}
	screen := api.richThreadScreens[0]
	if len(screen.Breadcrumb) != 0 {
		t.Fatalf("quick completion notification must not have breadcrumb: %#v", screen.Breadcrumb)
	}
	fallback := RichFallback(screen.Rich)
	for _, expected := range []string{"Agent completed", "Finished implementation", "Tests and validation passed.", "completed", "ws_1", "cmp_1"} {
		if !strings.Contains(fallback.Text, expected) {
			t.Fatalf("completion rich fallback missing %q: %q", expected, fallback.Text)
		}
	}
	if strings.Contains(fallback.Text, "Agent completion · completed") {
		t.Fatalf("completion notification repeated heading/status context: %q", fallback.Text)
	}
	if len(screen.Keyboard) != 0 {
		t.Fatalf("completion notification must not expose Activity Mini App action: %#v", screen.Keyboard)
	}
}

func TestAcceptedCompletionProducesAtMostOneRichTelegramNotification(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	if err := store.put(42, TopicCompletions, 123); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		root: t.TempDir(), api: api, topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{Running: true, Enabled: true, PollingHealthy: true, AuthorizationConfigured: true, TopicsEffective: true},
	}
	if _, err := NewInterface(InterfaceOptions{Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	coordinator := notification.NewCoordinator(notification.CoordinatorOptions{Attempts: 1})
	coordinator.Register(notification.NewTelegramProvider(runtime))
	hook := notification.NewCompletionHook(coordinator, notification.CompletionHookOptions{Policy: func() notification.CompletionPolicy {
		return notification.CompletionPolicy{Enabled: true, Providers: map[string]bool{notification.ProviderTelegram: true}}
	}})
	now := time.Date(2026, 9, 28, 8, 2, 0, 0, time.UTC)
	event := agentcompletion.Event{
		ID: "completion-event:cmp_once", Name: agentcompletion.EventAccepted, Timestamp: now,
		Record: agentcompletion.Record{
			ID: "cmp_once", WorkspaceID: "ws_once", Status: agentcompletion.StatusCompleted,
			Title: "Implemented once", Summary: "Canonical completion notification.", CreatedAt: now,
		},
	}
	if err := hook.Handle(context.Background(), agentcompletion.HookInvocation{Event: event, IdempotencyKey: "delivery-a"}); err != nil {
		t.Fatal(err)
	}
	duplicate := event
	duplicate.ID = "completion-event:transport-duplicate"
	if err := hook.Handle(context.Background(), agentcompletion.HookInvocation{Event: duplicate, IdempotencyKey: "delivery-b"}); err != nil {
		t.Fatal(err)
	}
	coordinator.Stop()
	if len(api.richThreadScreens) != 1 || len(api.richThreadIDs) != 1 || api.richThreadIDs[0] != 123 {
		t.Fatalf("completion deliveries ids=%v screens=%d", api.richThreadIDs, len(api.richThreadScreens))
	}
	if len(api.threadSends) != 0 || len(api.generalSends) != 0 {
		t.Fatalf("completion duplicated outside rich delivery thread=%v general=%v", api.threadSends, api.generalSends)
	}
}

func TestBackgroundProcessNotificationUsesStructuredRichRuntimeCard(t *testing.T) {
	api := &topicTestAPI{}
	store := newTopicStore(t.TempDir())
	if err := store.put(42, TopicRuntime, 122); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		root: t.TempDir(), api: api, topics: store,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, TopicsEnabled: true},
		health: Health{
			Running: true, Enabled: true, AuthorizationConfigured: true, TopicsEffective: true,
			LogsMiniApp: LogsMiniAppHealth{Enabled: true, State: MiniAppReady, PublicURL: "https://logs.example/?source=telegram"},
		},
	}
	if _, err := NewInterface(InterfaceOptions{Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	exitCode := 17
	message := notification.Message{
		ID: "background:proc_1", Kind: notification.KindBackgroundJobFinished,
		Title: "Background process failed", Body: "start_process · failed · process_exit · 2.4s · exit 17",
		Status: "failed", Reason: "process_exit", WorkspaceID: "ws_2", ProcessID: "proc_1",
		ExecutionID: "exec_1", TargetTool: "start_process", DurationMS: 2400, ExitCode: &exitCode,
		Timestamp: time.Date(2026, 9, 28, 8, 1, 0, 0, time.UTC),
	}
	if err := runtime.SendNotification(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if len(api.richThreadScreens) != 1 || api.richThreadIDs[0] != 122 {
		t.Fatalf("rich runtime deliveries ids=%v screens=%d", api.richThreadIDs, len(api.richThreadScreens))
	}
	if len(api.threadSends) != 0 || len(api.generalSends) != 0 {
		t.Fatalf("background notification duplicated as plain text thread=%v general=%v", api.threadSends, api.generalSends)
	}
	screen := api.richThreadScreens[0]
	if len(screen.Breadcrumb) != 0 {
		t.Fatalf("quick background notification must not have breadcrumb: %#v", screen.Breadcrumb)
	}
	fallback := RichFallback(screen.Rich)
	for _, expected := range []string{"Background process failed", "start_process", "failed", "process_exit", "2.4s", "17", "ws_2", "proc_1", "exec_1"} {
		if !strings.Contains(fallback.Text, expected) {
			t.Fatalf("background rich fallback missing %q: %q", expected, fallback.Text)
		}
	}
	for _, forbidden := range []string{"Authorization: Bearer", "session_hash", "call_id"} {
		if strings.Contains(fallback.Text, forbidden) {
			t.Fatalf("background rich fallback leaked %q: %q", forbidden, fallback.Text)
		}
	}
	if len(screen.Keyboard) != 1 || len(screen.Keyboard[0]) != 1 {
		t.Fatalf("background notification process-log keyboard=%#v", screen.Keyboard)
	}
	button := screen.Keyboard[0][0]
	if button.Text != "Open process log" || !strings.Contains(button.WebAppURL, "execution=exec_1") || !strings.Contains(button.WebAppURL, "feed=executions") || !strings.Contains(button.WebAppURL, "source=telegram") {
		t.Fatalf("background process-log button=%#v", button)
	}
}

func TestBackgroundProcessExplanationUsesSharedExpandableLayout(t *testing.T) {
	runtime := &Runtime{health: Health{}}
	ui := &Interface{runtime: runtime}
	message := notification.Message{
		Kind:  notification.KindBackgroundJobFinished,
		Title: "Background process finished",
		Explanation: &explain.Explanation{
			Summary:    "SUMMARY",
			Steps:      []string{"STEP"},
			Effects:    []string{"EFFECT"},
			RiskNotes:  []string{"RISK"},
			Unknowns:   []string{"UNKNOWN"},
			ProviderID: "ollama",
			Model:      "qwen3",
		},
	}
	screen := ui.backgroundProcessNotificationScreen(message)
	var expandable *RichBlock
	for index := range screen.Rich.Blocks {
		block := &screen.Rich.Blocks[index]
		if block.Kind == RichExpandable {
			expandable = block
		}
	}
	if expandable == nil || expandable.Title != "AI explanation · informational" {
		t.Fatalf("background explanation expandable=%#v blocks=%#v", expandable, screen.Rich.Blocks)
	}
	grouped := string(RichMessageHTML(BuildRichPresentation(*expandable)))
	for _, want := range []string{"SUMMARY", "STEP", "EFFECT", "RISK", "UNKNOWN"} {
		if !strings.Contains(grouped, want) {
			t.Fatalf("background expandable missing %q: %q", want, grouped)
		}
	}
	if strings.Contains(grouped, "Generated by") {
		t.Fatalf("background provenance leaked inside expandable: %q", grouped)
	}
	fallback := RichFallback(screen.Rich).Text
	if !strings.Contains(fallback, "AI source · Generated by ollama · qwen3") {
		t.Fatalf("background provenance missing outside expandable: %q", fallback)
	}
}

func TestCompletionNotificationOmitsLogsMiniAppButtonWhenMiniAppIsReady(t *testing.T) {
	runtime := &Runtime{health: Health{LogsMiniApp: LogsMiniAppHealth{Enabled: true, State: MiniAppReady, PublicURL: "https://old.example"}}}
	ui := &Interface{runtime: runtime}
	message := notification.Message{Kind: notification.KindCompletionAccepted, Title: "Agent completed", CompletionID: "cmp_1"}

	first, handled, err := ui.RenderNotification(t.Context(), 42, message)
	if err != nil || !handled || len(first.Keyboard) != 0 {
		t.Fatalf("first notification handled=%t err=%v keyboard=%#v", handled, err, first.Keyboard)
	}
	runtime.mu.Lock()
	runtime.health.LogsMiniApp.PublicURL = "https://new.example"
	runtime.mu.Unlock()

	second, handled, err := ui.RenderNotification(t.Context(), 42, message)
	if err != nil || !handled || len(second.Keyboard) != 0 {
		t.Fatalf("second notification handled=%t err=%v keyboard=%#v", handled, err, second.Keyboard)
	}
}
