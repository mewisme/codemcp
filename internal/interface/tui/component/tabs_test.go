package component

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTabNavigationWraps(t *testing.T) {
	if got := MoveTab(0, 2, -1); got != 1 {
		t.Fatalf("left wrap=%d", got)
	}
	if got := MoveTab(1, 2, 1); got != 0 {
		t.Fatalf("right wrap=%d", got)
	}
	if delta, ok := TabDelta(tea.KeyPressMsg{Code: tea.KeyRight}); !ok || delta != 1 {
		t.Fatalf("right delta=%d ok=%t", delta, ok)
	}
}

func TestPageTabsLayoutReturnsHitboxes(t *testing.T) {
	view, spans := PageTabsLayout([]string{"Workspaces", "Containers"}, 0, "saved", 80)
	plain := ansi.Strip(view)
	if len(spans) != 2 || spans[0].X != 0 || spans[1].X != spans[0].Width+1 || strings.Count(plain, "│") != 1 || !strings.Contains(plain, "Workspaces") || !strings.Contains(plain, "Containers") || !strings.Contains(plain, "saved") {
		t.Fatalf("view=%q spans=%#v", plain, spans)
	}
}

func TestPageTabsNoticeIncludesContentGap(t *testing.T) {
	view := ansi.Strip(PageTabsNotice([]string{"Context", "Rules", "Sources"}, 0, "", 80))
	lines := strings.Split(view, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "Context") || lines[1] != "" {
		t.Fatalf("view=%q lines=%#v", view, lines)
	}
}
