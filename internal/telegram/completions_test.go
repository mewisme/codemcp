package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

func TestCompletionListAndDetailUseCanonicalResolvers(t *testing.T) {
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	records := []agentcompletion.Record{
		{ID: "cmp_old", Sequence: 2, WorkspaceID: "ws_old", Status: agentcompletion.StatusPartial, Title: "Older completion", Summary: "Older summary", CreatedAt: now.Add(-time.Minute)},
		{ID: "cmp_new", Sequence: 3, WorkspaceID: "ws_new", Status: agentcompletion.StatusCompleted, Title: "Latest completion", Summary: "Latest summary", Source: "tunnel", CreatedAt: now},
	}
	listCalls := 0
	viewCalls := 0
	runtime := &Runtime{
		root: t.TempDir(), generation: 7,
		health: Health{LogsMiniApp: LogsMiniAppHealth{Enabled: true, State: MiniAppReady, PublicURL: "https://logs-current.example"}},
	}
	ui, err := NewInterface(InterfaceOptions{
		Runtime: runtime,
		CompletionList: func(_ context.Context, workspaceID string, limit int) ([]agentcompletion.Record, error) {
			listCalls++
			if workspaceID != "" || limit != completionHistoryLimit {
				t.Fatalf("list workspace=%q limit=%d", workspaceID, limit)
			}
			return append([]agentcompletion.Record(nil), records...), nil
		},
		CompletionView: func(_ context.Context, id string) (agentcompletion.Record, error) {
			viewCalls++
			if id != "cmp_new" {
				t.Fatalf("view id=%q", id)
			}
			return records[1], nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: runtime.Generation()}
	list, err := ui.completionListScreen(t.Context(), owner, ActionState{Route: RouteCompletions, Back: RouteHome})
	if err != nil {
		t.Fatal(err)
	}
	if listCalls != 1 || list.Rich == nil {
		t.Fatalf("list calls=%d rich=%#v", listCalls, list.Rich)
	}
	fallback := RichFallback(list.Rich)
	if !strings.Contains(fallback.Text, "Latest completion") || !strings.Contains(fallback.Text, "Older completion") {
		t.Fatalf("completion list fallback=%q", fallback.Text)
	}
	if strings.Index(fallback.Text, "Latest completion") > strings.Index(fallback.Text, "Older completion") {
		t.Fatalf("completion history is not newest first: %q", fallback.Text)
	}

	detail, err := ui.completionDetailScreen(t.Context(), owner, ActionState{Route: RouteCompletion, Back: RouteCompletions, ResourceID: "cmp_new"})
	if err != nil {
		t.Fatal(err)
	}
	if viewCalls != 1 || detail.Rich == nil {
		t.Fatalf("view calls=%d rich=%#v", viewCalls, detail.Rich)
	}
	detailFallback := RichFallback(detail.Rich)
	for _, expected := range []string{"Latest completion", "Latest summary", "completed", "ws_new", "cmp_new", "tunnel"} {
		if !strings.Contains(detailFallback.Text, expected) {
			t.Fatalf("completion detail missing %q: %q", expected, detailFallback.Text)
		}
	}
	foundLogs := false
	for _, row := range detail.Keyboard {
		for _, button := range row {
			if button.WebAppURL == "https://logs-current.example" {
				foundLogs = true
			}
		}
	}
	if !foundLogs {
		t.Fatalf("completion detail missing current Logs Mini App button: %#v", detail.Keyboard)
	}
}

func TestCompletionDetailKeepsMessageNativeFallbackWithoutMiniApp(t *testing.T) {
	record := agentcompletion.Record{
		ID: "cmp_1", Sequence: 1, WorkspaceID: "ws_1", Status: agentcompletion.StatusBlocked,
		Title: "Blocked by dependency", Summary: "Waiting for an external dependency.",
		CreatedAt: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC),
	}
	runtime := &Runtime{root: t.TempDir(), generation: 3}
	ui, err := NewInterface(InterfaceOptions{
		Runtime: runtime,
		CompletionView: func(context.Context, string) (agentcompletion.Record, error) {
			return record, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: runtime.Generation()}
	screen, err := ui.completionDetailScreen(t.Context(), owner, ActionState{Route: RouteCompletion, Back: RouteCompletions, ResourceID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	fallback := RichFallback(screen.Rich)
	if !strings.Contains(fallback.Text, "Blocked by dependency") || !strings.Contains(fallback.Text, "Waiting for an external dependency.") {
		t.Fatalf("message-native completion fallback=%q", fallback.Text)
	}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			if button.WebAppURL != "" {
				t.Fatalf("unavailable Mini App still emitted WebApp button: %#v", screen.Keyboard)
			}
		}
	}
}
