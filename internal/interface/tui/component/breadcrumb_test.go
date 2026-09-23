package component

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestBreadcrumbLayoutRendersAllItemsAndSpans(t *testing.T) {
	view, spans := BreadcrumbLayout([]string{"Workspaces", "ws_demo", "Project Context"}, 80)
	plain := ansi.Strip(view)
	for _, want := range []string{"Workspaces", "ws_demo", "Project Context", " / "} {
		if !strings.Contains(plain, want) {
			t.Fatalf("breadcrumb missing %q: %q", want, plain)
		}
	}
	if len(spans) != 3 || spans[0].Index != 0 || spans[1].Index != 1 || spans[2].Index != 2 {
		t.Fatalf("spans=%#v", spans)
	}
	if lipgloss.Width(view) > 80 {
		t.Fatalf("breadcrumb width=%d", lipgloss.Width(view))
	}
}

func TestBreadcrumbLayoutCollapsesMiddleOnNarrowWidth(t *testing.T) {
	view, spans := BreadcrumbLayout([]string{"Workspaces", "ws_0123456789", "Access", "Add"}, 24)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "…") || !strings.Contains(plain, "Add") || lipgloss.Width(view) > 24 {
		t.Fatalf("collapsed breadcrumb=%q width=%d", plain, lipgloss.Width(view))
	}
	if len(spans) != 2 || spans[0].Index != 0 || spans[1].Index != 3 {
		t.Fatalf("collapsed spans=%#v", spans)
	}
}
