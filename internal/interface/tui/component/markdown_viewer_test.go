package component

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownViewerRerendersOnlyForSourceWidthOrTheme(t *testing.T) {
	original := markdownRender
	defer func() { markdownRender = original }()
	calls := []string{}
	markdownRender = func(source, style string, width int) (string, error) {
		calls = append(calls, fmt.Sprintf("%s:%d:%s", style, width, source))
		return fmt.Sprintf("%s %d\n%s", style, width, source), nil
	}
	viewer := NewMarkdownViewer("# Heading")
	if viewer.renderCount != 1 || len(calls) != 1 {
		t.Fatalf("initial render count=%d calls=%v", viewer.renderCount, calls)
	}
	viewer.Resize(80, 10)
	if viewer.renderCount != 1 {
		t.Fatalf("height-only resize rerendered: %d", viewer.renderCount)
	}
	viewer.Resize(42, 10)
	if viewer.renderCount != 2 || !strings.Contains(calls[len(calls)-1], ":42:") {
		t.Fatalf("width resize count=%d calls=%v", viewer.renderCount, calls)
	}
	updated, _ := viewer.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	viewer = updated
	if viewer.renderCount != 2 {
		t.Fatalf("scroll rerendered markdown: %d", viewer.renderCount)
	}
	updated, _ = viewer.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	viewer = updated
	if viewer.renderCount != 3 || !strings.HasPrefix(calls[len(calls)-1], "light:") {
		t.Fatalf("light theme render count=%d calls=%v", viewer.renderCount, calls)
	}
	updated, _ = viewer.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
	viewer = updated
	if viewer.renderCount != 4 || !strings.HasPrefix(calls[len(calls)-1], "dark:") {
		t.Fatalf("dark theme render count=%d calls=%v", viewer.renderCount, calls)
	}
	viewer.SetSource("## Updated")
	if viewer.renderCount != 5 || viewer.Source() != "## Updated" {
		t.Fatalf("source render count=%d source=%q", viewer.renderCount, viewer.Source())
	}
}

func TestMarkdownViewerFallsBackToPlainMarkdown(t *testing.T) {
	original := markdownRender
	defer func() { markdownRender = original }()
	markdownRender = func(string, string, int) (string, error) { return "", errors.New("render failed") }
	viewer := NewMarkdownViewer("# Heading\n\nFallback body")
	viewer.Resize(30, 8)
	if viewer.RenderError() == nil {
		t.Fatal("render error was not preserved")
	}
	plain := ansi.Strip(viewer.View())
	if !strings.Contains(plain, "# Heading") || !strings.Contains(plain, "Fallback body") {
		t.Fatalf("fallback view=%q", plain)
	}
}

func TestMarkdownViewerMouseWheelUsesSemanticMessage(t *testing.T) {
	viewer := NewMarkdownViewer(strings.Repeat("line\n", 40))
	viewer.Resize(40, 6)
	targets := viewer.MouseTargets(3, 4, 20)
	if len(targets) != 1 || targets[0].ID != "markdown.scroll" {
		t.Fatalf("targets=%#v", targets)
	}
	message, ok := targets[0].Handle(MouseEvent{Button: tea.MouseWheelDown}).(markdownViewerWheelMsg)
	if !ok || message != 1 {
		t.Fatalf("wheel message=%#v", message)
	}
}

func TestRenderCodeBlockUsesLanguageFenceAndExpandsFenceForContent(t *testing.T) {
	original := markdownRender
	defer func() { markdownRender = original }()
	var source, style string
	var width int
	markdownRender = func(value, selectedStyle string, selectedWidth int) (string, error) {
		source, style, width = value, selectedStyle, selectedWidth
		return "rendered", nil
	}
	SetDarkBackground(true)
	got := RenderCodeBlock("{\n  \"fence\": \"```\"\n}", "json", 42)
	if got != "rendered" || style != "dark" || width != 42 {
		t.Fatalf("render result=%q style=%q width=%d", got, style, width)
	}
	if !strings.HasPrefix(source, "````json\n") || !strings.HasSuffix(source, "\n````") {
		t.Fatalf("unexpected fenced source: %q", source)
	}
}

func TestRenderCodeBlockFallsBackToStructuredContent(t *testing.T) {
	original := markdownRender
	defer func() { markdownRender = original }()
	markdownRender = func(string, string, int) (string, error) { return "", errors.New("render failed") }
	got := RenderCodeBlock("alpha beta gamma", "text", 8)
	if strings.TrimSpace(got) == "" || !strings.Contains(got, "alpha") {
		t.Fatalf("fallback output=%q", got)
	}
}

func TestRenderMarkdownCompactAlignsCodeBlockWithLabel(t *testing.T) {
	got := ansi.Strip(RenderMarkdownCompact("REQUEST\n\n```json\n{\n  \"head\": 80\n}\n```", 42))
	lines := strings.Split(got, "\n")
	labelColumn, jsonColumn := -1, -1
	for _, line := range lines {
		if strings.Contains(line, "REQUEST") {
			labelColumn = strings.Index(line, "REQUEST")
		}
		if strings.Contains(line, "{") {
			jsonColumn = strings.Index(line, "{")
			break
		}
	}
	if labelColumn < 0 || jsonColumn < 0 || labelColumn != jsonColumn {
		t.Fatalf("compact markdown columns label=%d json=%d: %q", labelColumn, jsonColumn, got)
	}
}
