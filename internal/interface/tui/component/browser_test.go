package component

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func updateBrowser(t *testing.T, model Browser, message tea.Msg) Browser {
	t.Helper()
	updated, _ := model.Update(message)
	value, ok := updated.(Browser)
	if !ok {
		t.Fatalf("browser update returned %T", updated)
	}
	return value
}

func TestBrowserItemUsesRowPresentationAndSearch(t *testing.T) {
	item := browserItem{Row: Row{ID: "id", Title: "Title", Description: "Description", Meta: "meta", Summary: "summary", Search: "needle"}}
	if item.Title() != "Title" || item.Description() != "Description · meta" {
		t.Fatalf("presentation title=%q description=%q", item.Title(), item.Description())
	}
	for _, want := range []string{"id", "Title", "Description", "meta", "summary", "needle"} {
		if !strings.Contains(item.FilterValue(), want) {
			t.Fatalf("filter value missing %q: %q", want, item.FilterValue())
		}
	}
	if got := (browserItem{Row: Row{ID: "fallback"}}).Title(); got != "fallback" {
		t.Fatalf("ID fallback title=%q", got)
	}
	if got := (browserItem{Row: Row{Summary: "summary"}}).Title(); got != "summary" {
		t.Fatalf("summary fallback title=%q", got)
	}
}

func TestBrowserEnterEmitsOpenMessage(t *testing.T) {
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}, nil)
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Browser)
	if cmd == nil {
		t.Fatal("enter returned no open command")
	}
	message, ok := cmd().(BrowserOpenMsg)
	if !ok || message.Row.ID != "one" {
		t.Fatalf("open message=%#v", message)
	}
	if selected, ok := model.Selected(); !ok || selected.ID != "one" {
		t.Fatalf("selected=%#v ok=%t", selected, ok)
	}
}

func TestBrowserMouseSelectThenOpenSelectedRow(t *testing.T) {
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}, nil)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	model = updated.(Browser)
	rowTargets := func() []MouseTarget {
		result := []MouseTarget{}
		for _, target := range model.MouseTargets(0, 0, 1) {
			if target.ID == "browser.row" {
				result = append(result, target)
			}
		}
		return result
	}
	targets := rowTargets()
	if len(targets) < 2 {
		t.Fatalf("row targets=%d", len(targets))
	}
	message := targets[1].Handle(MouseEvent{Button: tea.MouseLeft})
	updated, cmd := model.Update(message)
	model = updated.(Browser)
	if cmd != nil {
		t.Fatal("first click on unselected row opened it")
	}
	if selected, _ := model.Selected(); selected.ID != "two" {
		t.Fatalf("selected after first click=%q", selected.ID)
	}
	targets = rowTargets()
	message = targets[1].Handle(MouseEvent{Button: tea.MouseLeft})
	updated, cmd = model.Update(message)
	model = updated.(Browser)
	if cmd == nil {
		t.Fatal("second click on selected row did not open")
	}
	opened, ok := cmd().(BrowserOpenMsg)
	if !ok || opened.Row.ID != "two" {
		t.Fatalf("mouse open=%#v", opened)
	}
}

func TestBrowserMouseRowTargetsMatchRenderedRows(t *testing.T) {
	for _, test := range []struct {
		name  string
		model Browser
	}{
		{name: "default", model: NewBrowser(t.Context(), "Items", []Row{{ID: "one", Title: "One"}}, nil)},
		{name: "title-hidden", model: NewBrowser(t.Context(), "Items", []Row{{ID: "one", Title: "One"}}, nil).WithTitleVisible(false).WithExternalHelp(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated, _ := test.model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
			model := updated.(Browser)
			rowY := -1
			for index, line := range strings.Split(ansi.Strip(model.Content()), "\n") {
				if strings.Contains(line, "One") {
					rowY = index
					break
				}
			}
			if rowY < 0 {
				t.Fatal("browser row not rendered")
			}
			for _, target := range model.MouseTargets(0, 0, 1) {
				if target.ID == "browser.row" {
					if target.Rect.Y != rowY {
						t.Fatalf("row target y=%d want rendered y=%d", target.Rect.Y, rowY)
					}
					return
				}
			}
			t.Fatal("browser row target not found")
		})
	}
}

func TestBrowserRefreshPreservesSelection(t *testing.T) {
	refresh := func(context.Context) ([]Row, error) {
		return []Row{{ID: "one", Title: "One updated"}, {ID: "two", Title: "Two updated"}, {ID: "three", Title: "Three"}}, nil
	}
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}, refresh)
	if !model.SelectID("two") {
		t.Fatal("could not select row")
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	model = updated.(Browser)
	if cmd == nil || !model.loading {
		t.Fatalf("refresh cmd=%v loading=%t", cmd, model.loading)
	}
	updated, follow := model.Update(cmd())
	model = updated.(Browser)
	if follow != nil {
		updated, _ = model.Update(follow())
		model = updated.(Browser)
	}
	if model.loading || model.err != nil {
		t.Fatalf("refresh loading=%t err=%v", model.loading, model.err)
	}
	selected, ok := model.Selected()
	if !ok || selected.ID != "two" || selected.Title != "Two updated" {
		t.Fatalf("selected after refresh=%#v ok=%t", selected, ok)
	}
}

func TestBrowserRefreshErrorKeepsRows(t *testing.T) {
	wantErr := errors.New("refresh failed")
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one", Title: "One"}}, func(context.Context) ([]Row, error) { return nil, wantErr })
	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	model = updated.(Browser)
	updated, _ = model.Update(cmd())
	model = updated.(Browser)
	if !errors.Is(model.err, wantErr) || model.loading {
		t.Fatalf("refresh err=%v loading=%t", model.err, model.loading)
	}
	if selected, ok := model.Selected(); !ok || selected.ID != "one" {
		t.Fatalf("row lost after refresh error: %#v ok=%t", selected, ok)
	}
}

func TestBrowserActionUsesSelectedRowAndPredicate(t *testing.T) {
	calls := []string{}
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}, nil).WithAction(RowAction{
		Key: "d", Desc: "delete", When: func(row Row) bool { return row.ID == "two" },
		Run: func(row Row) (string, tea.Cmd, error) {
			calls = append(calls, row.ID)
			return "deleted", nil, nil
		},
	})
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	model = updated.(Browser)
	if len(calls) != 0 {
		t.Fatalf("predicate ignored calls=%v", calls)
	}
	model.SelectID("two")
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	model = updated.(Browser)
	if len(calls) != 1 || calls[0] != "two" || model.notice != "deleted" {
		t.Fatalf("calls=%v notice=%q", calls, model.notice)
	}
}

func TestBrowserSelectionHelpersAndReplaceRows(t *testing.T) {
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one"}, {ID: "two"}, {ID: "three"}}, nil)
	if !model.SelectLast() {
		t.Fatal("SelectLast failed")
	}
	if selected, _ := model.Selected(); selected.ID != "three" {
		t.Fatalf("last selected=%q", selected.ID)
	}
	cmd := model.ReplaceRows([]Row{{ID: "two", Title: "Two"}, {ID: "three", Title: "Three updated"}}, "three")
	if cmd != nil {
		updated, _ := model.Update(cmd())
		model = updated.(Browser)
	}
	selected, ok := model.Selected()
	if !ok || selected.ID != "three" || selected.Title != "Three updated" {
		t.Fatalf("replace selection=%#v ok=%t", selected, ok)
	}
	if model.SelectID("missing") {
		t.Fatal("SelectID accepted missing row")
	}
}

func TestBrowserWheelMovesSelection(t *testing.T) {
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one"}, {ID: "two"}}, nil)
	updated, _ := model.Update(browserMouseMsg{Wheel: 1})
	model = updated.(Browser)
	if selected, _ := model.Selected(); selected.ID != "two" {
		t.Fatalf("wheel down selected=%q", selected.ID)
	}
	updated, _ = model.Update(browserMouseMsg{Wheel: -1})
	model = updated.(Browser)
	if selected, _ := model.Selected(); selected.ID != "one" {
		t.Fatalf("wheel up selected=%q", selected.ID)
	}
}

func TestBrowserFilteringAndHelpState(t *testing.T) {
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one", Search: "alpha"}, {ID: "two", Search: "beta"}}, nil)
	updated, _ := model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	model = updated.(Browser)
	if !model.InputActive() {
		t.Fatal("filter input did not activate")
	}
	model.SetHelpExpanded(true)
	if !model.HelpExpanded() {
		t.Fatal("expanded help state not retained")
	}
}

func TestBrowserExternalHelpDoesNotReserveBodySpace(t *testing.T) {
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one"}, {ID: "two"}}, nil).WithTitleVisible(false).WithExternalHelp(true)
	model.SetHelpBindings(Binding([]string{"a"}, "a", "add"))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	model = updated.(Browser)
	if strings.Contains(model.BodyContent(), "enter open") || strings.Contains(model.BodyContent(), "a add") {
		t.Fatalf("external help leaked into body: %q", model.BodyContent())
	}
	help := ansi.Strip(model.HelpView())
	if !strings.Contains(help, "enter open") || !strings.Contains(help, "a add") {
		t.Fatalf("external help missing bindings: %q", help)
	}
	if got := len(strings.Split(model.BodyContent(), "\n")); got != 8 {
		t.Fatalf("body height=%d want=8", got)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	model = updated.(Browser)
	if !model.HelpExpanded() {
		t.Fatal("external help did not expand")
	}
}

func TestBrowserResponsiveBoundsAlsoClampMouseTargets(t *testing.T) {
	rows := []Row{
		{ID: "one", Title: "A very long browser row title that exceeds the narrow viewport", Description: "Long description"},
		{ID: "two", Title: "Second row", Description: "Long description"},
		{ID: "three", Title: "Third row", Description: "Long description"},
	}
	model := NewBrowser(t.Context(), "A very long browser title that exceeds the viewport", rows, nil)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 24, Height: 10})
	model = updated.(Browser)
	view := model.Content()
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if got := lipgloss.Width(line); got > 24 {
			t.Fatalf("line width=%d want <=24: %q", got, line)
		}
	}
	if got := lipgloss.Height(view); got > 10 {
		t.Fatalf("height=%d want <=10", got)
	}
	for _, target := range model.MouseTargets(7, 11, 3) {
		if target.Rect.X < 7 || target.Rect.Y < 11 || target.Rect.X+target.Rect.Width > 31 || target.Rect.Y+target.Rect.Height > 21 {
			t.Fatalf("target escaped browser bounds: %#v", target.Rect)
		}
	}
}

func TestBrowserTitleNoticeRendersInline(t *testing.T) {
	model := NewBrowser(t.Context(), "Items", []Row{{ID: "one"}}, nil)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	model = updated.(Browser)
	model.SetTitleNotice("saved")
	first := strings.Split(model.Content(), "\n")[0]
	if !strings.Contains(first, "Items") || !strings.Contains(first, "· saved") {
		t.Fatalf("title notice=%q", first)
	}
}

func TestBrowserHelpKeyMessages(t *testing.T) {
	for input, want := range map[string]string{"space": "space", "enter": "enter", "esc": "esc", "x": "x"} {
		if got := browserHelpKeyMsg(input).String(); got != want {
			t.Fatalf("browserHelpKeyMsg(%q)=%q want=%q", input, got, want)
		}
	}
}

func TestActionAvailable(t *testing.T) {
	row := Row{ID: "one"}
	if !actionAvailable(RowAction{}, row) {
		t.Fatal("action without predicate should be available")
	}
	if actionAvailable(RowAction{When: func(Row) bool { return false }}, row) {
		t.Fatal("false predicate reported available")
	}
}
