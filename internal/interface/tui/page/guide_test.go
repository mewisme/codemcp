package page

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/docs/tuiguide"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/interface/tui/testutil"
)

func TestGuideIndexBrowsesTopicMetadata(t *testing.T) {
	page, err := NewGuide(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	plain := ansi.Strip(page.View(100, 30))
	for _, want := range []string{"TUI Guide", "Getting Started", "MCP Servers", "10 topics"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("guide index missing %q: %q", want, plain)
		}
	}
	_, cmd := page.Update(component.BrowserOpenMsg{Row: component.Row{ID: "mcp"}})
	if cmd == nil {
		t.Fatal("opening guide topic returned no navigation command")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "guide/mcp" {
		t.Fatalf("guide navigation=%#v", message)
	}
}

func TestGuideTopicLoadsOnlySelectedMarkdownWithGlamourViewer(t *testing.T) {
	page, err := NewGuide(t.Context(), "mcp")
	if err != nil {
		t.Fatal(err)
	}
	want, err := tuiguide.Markdown("mcp")
	if err != nil {
		t.Fatal(err)
	}
	if page.viewer.Source() != want {
		t.Fatal("guide viewer source differs from selected embedded topic")
	}
	plain := ansi.Strip(page.View(100, 30))
	if !strings.Contains(plain, "MCP Servers") || !strings.Contains(plain, "Use Topics for detailed documentation") || strings.Contains(plain, "Shell & Execution") {
		t.Fatalf("selected guide render=%q", plain)
	}
	if page.viewer.RenderError() != nil {
		t.Fatalf("Glamour render failed: %v", page.viewer.RenderError())
	}
}

func TestGuideFolderOverviewAndNestedTopics(t *testing.T) {
	page, err := NewGuide(t.Context(), "config")
	if err != nil {
		t.Fatal(err)
	}
	plain := ansi.Strip(page.View(100, 30))
	if !strings.Contains(plain, "Overview") || !strings.Contains(plain, "Topics") || !strings.Contains(plain, "Configuration") {
		t.Fatalf("config overview=%q", plain)
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	page = updated.(*GuidePage)
	plain = ansi.Strip(page.View(100, 30))
	for _, want := range []string{"Shell & Execution", "Storage & Maintenance"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("config topics missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Configuration Topics") {
		t.Fatalf("guide child repeated breadcrumb title: %q", plain)
	}
	_, cmd := page.Update(component.BrowserOpenMsg{Row: component.Row{ID: "config/storage"}})
	if cmd == nil {
		t.Fatal("opening nested guide returned no navigation command")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "guide/config/storage" {
		t.Fatalf("nested guide navigation=%#v", message)
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	page = updated.(*GuidePage)
	if page.tab != 0 || !strings.Contains(ansi.Strip(page.View(100, 30)), "Configuration") {
		t.Fatalf("left arrow did not return to overview: tab=%d", page.tab)
	}
	nested, err := NewGuide(t.Context(), "config/storage/bundles")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi.Strip(nested.View(100, 30)), "Configuration Bundles") {
		t.Fatal("deep nested guide did not render")
	}
}

func TestGuideResponsiveLayoutsAndScrolling(t *testing.T) {
	for _, topic := range []string{"", "getting-started", "requests", "config"} {
		page, err := NewGuide(t.Context(), topic)
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range [][2]int{{24, 10}, {40, 16}, {80, 24}, {120, 40}} {
			view := page.View(size[0], size[1])
			testutil.AssertLinesFit(t, view, size[0])
			if lines := strings.Count(view, "\n") + 1; lines > size[1] {
				t.Fatalf("topic=%q size=%v height=%d", topic, size, lines)
			}
		}
		if topic != "" {
			before := ansi.Strip(page.View(80, 12))
			updated, _ := page.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
			page = updated.(*GuidePage)
			after := ansi.Strip(page.View(80, 12))
			if before == after {
				t.Fatalf("topic %q did not scroll", topic)
			}
		}
	}
}

func TestGuideRejectsUnknownTopic(t *testing.T) {
	if _, err := NewGuide(t.Context(), "missing"); err == nil {
		t.Fatal("unknown guide topic unexpectedly succeeded")
	}
}
