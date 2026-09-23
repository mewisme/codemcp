package component

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCharmThemeSemanticStylesDoNotInjectComponentGlyphs(t *testing.T) {
	SetDarkBackground(true)
	for name, value := range map[string]string{
		"accent":  ToneText("> ", ToneAccent),
		"success": ToneText("[x]", ToneSuccess),
		"danger":  ToneText("error", ToneDanger),
	} {
		want := map[string]string{"accent": "> ", "success": "[x]", "danger": "error"}[name]
		if got := ansi.Strip(value); got != want {
			t.Fatalf("%s rendered %q, want %q", name, got, want)
		}
	}
}

func TestPageTitleMatchesDefaultListTitleBar(t *testing.T) {
	SetDarkBackground(true)
	browser := NewBrowser(context.Background(), "Workspaces", []Row{{ID: "one", Title: "One"}}, nil)
	browser = updateBrowser(t, browser, tea.WindowSizeMsg{Width: 80, Height: 20})
	wantLines := strings.Split(ansi.Strip(browser.Content()), "\n")
	gotLines := strings.Split(ansi.Strip(PageTitle("Workspaces", 80)), "\n")
	if len(wantLines) < 2 || len(gotLines) < 2 || strings.TrimRight(gotLines[0], " ") != strings.TrimRight(wantLines[0], " ") || strings.TrimSpace(gotLines[1]) != "" || strings.TrimSpace(wantLines[1]) != "" {
		t.Fatalf("page title=%q list title=%q", gotLines, wantLines[:min(2, len(wantLines))])
	}
}

func TestPageTitleNoticeStaysInlineAndMatchesBrowserTitle(t *testing.T) {
	SetDarkBackground(true)
	const notice = "Workspace created"
	wantContent := currentTheme.pageTitle.Render("Workspaces") + " " + Muted("· "+notice)
	if got := pageTitleNoticeContent("Workspaces", notice); got != wantContent {
		t.Fatalf("notice inherited title styling: got=%q want=%q", got, wantContent)
	}
	plain := ansi.Strip(PageTitleNotice("Workspaces", notice, 80))
	if !strings.Contains(strings.Split(plain, "\n")[0], "Workspaces  · Workspace created") || lipgloss.Height(PageTitleNotice("Workspaces", notice, 80)) != lipgloss.Height(PageTitle("Workspaces", 80)) {
		t.Fatalf("page title notice=%q", plain)
	}
	browser := NewBrowser(context.Background(), "Workspaces", []Row{{ID: "one", Title: "One"}}, nil)
	browser.SetTitleNotice(notice)
	browser = updateBrowser(t, browser, tea.WindowSizeMsg{Width: 80, Height: 20})
	if !strings.Contains(ansi.Strip(browser.Content()), "Workspaces  · Workspace created") {
		t.Fatalf("browser title notice=%q", ansi.Strip(browser.Content()))
	}
}

func TestOverlayAtPlacesForegroundWithoutChangingCanvasGeometry(t *testing.T) {
	background := strings.Repeat(".", 20) + "\n" + strings.Repeat(".", 20) + "\n" + strings.Repeat(".", 20) + "\n" + strings.Repeat(".", 20)
	view := OverlayAt(background, "OK", 20, 4, 16, 3)
	if lipgloss.Width(view) != 20 || lipgloss.Height(view) != 4 {
		t.Fatalf("overlay geometry=%dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) != 4 || len(lines[3]) < 18 || lines[3][16:18] != "OK" {
		t.Fatalf("overlay placement=%q", lines)
	}
}
