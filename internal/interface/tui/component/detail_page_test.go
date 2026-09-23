package component

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDetailPagePreservesWorkspaceDetailStructure(t *testing.T) {
	page := NewDetailPage("Workspace · ws_one", "2 extra roots", "Root  /tmp/project")
	page.SetFeedback("Updated", errors.New("warning"))
	page.SetBindings(DetailPageBinding{Key: "e", Desc: "edit", Message: tea.KeyPressMsg{Code: 'e'}})
	page.Resize(80, 20)
	view := ansi.Strip(page.View())
	for _, value := range []string{"Workspace · ws_one", "2 extra roots", "warning", "Updated", "Root  /tmp/project", "j/k scroll", "e edit"} {
		if !strings.Contains(view, value) {
			t.Fatalf("view missing %q: %q", value, view)
		}
	}
	if strings.Contains(view, "╭") || strings.Contains(view, "╰") {
		t.Fatalf("detail page unexpectedly rendered modal border: %q", view)
	}
}

func TestDetailPageCanHideTitleForBreadcrumbChild(t *testing.T) {
	page := NewDetailPage("Overview", "running", "Body content").WithTitleVisible(false)
	page.Resize(60, 12)
	view := ansi.Strip(page.View())
	if strings.Contains(view, "Overview") || !strings.Contains(view, "running") || !strings.Contains(view, "Body content") {
		t.Fatalf("titleless detail=%q", view)
	}
}

func TestDetailPageScrollsLongContent(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	page := NewDetailPage("Detail", "", strings.Join(lines, "\n"))
	page.Resize(40, 10)
	before := page.viewport.YOffset()
	updated, _ := page.Update(tea.KeyPressMsg{Code: 'j'})
	if updated.viewport.YOffset() <= before {
		t.Fatalf("viewport did not scroll: before=%d after=%d", before, updated.viewport.YOffset())
	}
}

func TestDetailPageContentRefreshCanPreserveScroll(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	page := NewDetailPage("Detail", "", strings.Join(lines, "\n"))
	page.Resize(40, 10)
	page.viewport.SetYOffset(12)
	page.SetContentPreserveScroll(strings.Join(lines, "\n") + "\nupdated")
	if got := page.viewport.YOffset(); got != 12 {
		t.Fatalf("viewport offset=%d want=12", got)
	}
	page.SetContent("replacement")
	if got := page.viewport.YOffset(); got != 0 {
		t.Fatalf("reset content offset=%d want=0", got)
	}
}

func TestDetailPageBindingEmitsBeforeDomainHandling(t *testing.T) {
	message := tea.KeyPressMsg{Code: 'x'}
	page := NewDetailPage("Detail", "", "body")
	page.SetBindings(DetailPageBinding{Key: "x", Desc: "open child", Message: message})
	updated, cmd := page.Update(tea.KeyPressMsg{Code: 'x'})
	_ = updated
	if cmd == nil {
		t.Fatal("binding returned no command")
	}
	got, ok := cmd().(tea.KeyPressMsg)
	if !ok || got.Code != 'x' {
		t.Fatalf("binding message=%#v", got)
	}
}

func TestDetailPageCollapsesLargeActionFooter(t *testing.T) {
	page := NewDetailPage("Detail", "", "body")
	page.SetBindings(
		DetailPageBinding{Key: "a", Desc: "one", Message: tea.KeyPressMsg{Code: 'a'}},
		DetailPageBinding{Key: "b", Desc: "two", Message: tea.KeyPressMsg{Code: 'b'}},
		DetailPageBinding{Key: "c", Desc: "three", Message: tea.KeyPressMsg{Code: 'c'}},
		DetailPageBinding{Key: "d", Desc: "four", Message: tea.KeyPressMsg{Code: 'd'}},
		DetailPageBinding{Key: "e", Desc: "five", Message: tea.KeyPressMsg{Code: 'e'}},
	)
	page.Resize(80, 20)
	collapsed := ansi.Strip(page.View())
	if !strings.Contains(collapsed, "? more") || strings.Contains(collapsed, "five") {
		t.Fatalf("collapsed detail footer=%q", collapsed)
	}
	updated, cmd := page.Update(tea.KeyPressMsg{Code: '?'})
	if cmd != nil || !updated.help.Expanded() {
		t.Fatalf("detail help expansion cmd=%v expanded=%t", cmd, updated.help.Expanded())
	}
	expanded := ansi.Strip(updated.View())
	if !strings.Contains(expanded, "five") || !strings.Contains(expanded, "less") {
		t.Fatalf("expanded detail footer=%q", expanded)
	}
}

func TestDetailPageMouseBindingAndNoBackdrop(t *testing.T) {
	page := NewDetailPage("Detail", "meta", "body")
	page.SetBindings(DetailPageBinding{Key: "e", Desc: "edit", Message: tea.KeyPressMsg{Code: 'e'}})
	page.Resize(50, 12)
	targets := page.MouseTargets(2, 3, 5)
	found := false
	for _, target := range targets {
		if strings.Contains(target.ID, "backdrop") {
			t.Fatalf("detail page exposed backdrop target: %#v", target)
		}
		if target.ID != "detail.binding" {
			continue
		}
		found = true
		msg := target.Handle(MouseEvent{Button: tea.MouseLeft})
		keyMsg, ok := msg.(tea.KeyPressMsg)
		if !ok || keyMsg.Code != 'e' {
			t.Fatalf("mouse message=%#v", msg)
		}
	}
	if !found {
		t.Fatal("detail binding target missing")
	}
}

func TestDetailPageMouseWheelScrollsViewport(t *testing.T) {
	page := NewDetailPage("Detail", "", strings.Repeat("line\n", 40))
	page.Resize(40, 10)
	targets := page.MouseTargets(0, 0, 1)
	var wheel tea.Msg
	for _, target := range targets {
		if target.ID == "detail.scroll" {
			wheel = target.Handle(MouseEvent{Button: tea.MouseWheelDown})
			break
		}
	}
	if wheel == nil {
		t.Fatal("detail scroll target missing")
	}
	updated, _ := page.Update(wheel)
	if updated.viewport.YOffset() <= page.viewport.YOffset() {
		t.Fatalf("mouse wheel did not scroll: before=%d after=%d", page.viewport.YOffset(), updated.viewport.YOffset())
	}
}

func TestDetailPageNarrowLayoutStaysBounded(t *testing.T) {
	page := NewDetailPage("Very long detail title", "very long metadata", "content")
	page.Resize(18, 8)
	view := page.View()
	if got := lipgloss.Width(view); got > 18 {
		t.Fatalf("detail width=%d want <=18\n%s", got, ansi.Strip(view))
	}
}

func TestDetailPageHardWrapsLongContentAndReflowsFromRawSource(t *testing.T) {
	raw := `{"path":"/` + strings.Repeat("nested/", 10) + `file.json","token":"` + strings.Repeat("x", 48) + `"}`
	page := NewDetailPage("Detail", "", raw)
	page.Resize(24, 12)
	for _, line := range strings.Split(page.viewport.GetContent(), "\n") {
		if got := lipgloss.Width(line); got > 24 {
			t.Fatalf("wrapped content width=%d want <=24: %q", got, ansi.Strip(line))
		}
	}
	if got := strings.ReplaceAll(ansi.Strip(page.viewport.GetContent()), "\n", ""); got != raw {
		t.Fatalf("content changed after wrap: %q", got)
	}
	page.Resize(11, 12)
	for _, line := range strings.Split(page.viewport.GetContent(), "\n") {
		if got := lipgloss.Width(line); got > 11 {
			t.Fatalf("reflowed content width=%d want <=11: %q", got, ansi.Strip(line))
		}
	}
	if got := strings.ReplaceAll(ansi.Strip(page.viewport.GetContent()), "\n", ""); got != raw {
		t.Fatalf("content progressively wrapped: %q", got)
	}
}

func TestTwoColumnStacksWhenContentCannotFit(t *testing.T) {
	view := TwoColumn(Title(strings.Repeat("left", 5)), Secondary(strings.Repeat("right", 5)), 18)
	if !strings.Contains(view, "\n") {
		t.Fatalf("two-column content did not stack: %q", ansi.Strip(view))
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 18 {
			t.Fatalf("two-column line width=%d want <=18: %q", got, ansi.Strip(line))
		}
	}
}
