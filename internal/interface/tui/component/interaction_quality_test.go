package component

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestInteractionQualityAcrossRepresentativeTerminalSizes(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{name: "narrow", width: 24, height: 10},
		{name: "normal", width: 80, height: 24},
		{name: "wide", width: 120, height: 40},
	} {
		t.Run(size.name, func(t *testing.T) {
			rows := []Row{
				{ID: "one", Title: "One", Description: "first"},
				{ID: "two", Title: "Two", Description: "second"},
				{ID: "three", Title: "Three", Description: "third"},
			}
			model := NewBrowser(t.Context(), "Items", rows, nil)
			updated, _ := model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			model = updated.(Browser)
			if got := lipgloss.Width(model.View().Content); got > size.width {
				t.Fatalf("browser width=%d want <=%d", got, size.width)
			}

			updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			model = updated.(Browser)
			selected, ok := model.Selected()
			if !ok || selected.ID != "two" {
				t.Fatalf("keyboard selection=%#v ok=%t", selected, ok)
			}
			updated, open := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = updated.(Browser)
			if open == nil {
				t.Fatal("keyboard Enter did not open selected row")
			}
			if message, ok := open().(BrowserOpenMsg); !ok || message.Row.ID != "two" {
				t.Fatalf("keyboard open=%#v", message)
			}

			var target MouseTarget
			targetID := ""
			for _, candidate := range model.MouseTargets(0, 0, 1) {
				if candidate.ID != "browser.row" {
					continue
				}
				message, ok := candidate.Handle(MouseEvent{Button: tea.MouseLeft}).(browserMouseMsg)
				if ok && message.RowID != "" {
					target, targetID = candidate, message.RowID
					break
				}
			}
			if target.Handle == nil {
				t.Fatal("mouse target for an alternate visible row missing")
			}
			updated, _ = model.Update(target.Handle(MouseEvent{Button: tea.MouseLeft}))
			model = updated.(Browser)
			selected, _ = model.Selected()
			if selected.ID != targetID {
				t.Fatalf("mouse selection=%q want=%q", selected.ID, targetID)
			}

			body := strings.Join([]string{
				fmt.Sprintf("viewport=%dx%d", size.width, size.height),
				strings.Repeat("long-field-", 12),
				strings.Repeat("line\n", 80),
			}, "\n")
			detail := NewDetailPage("Detail", "fixture", body)
			detail.Resize(size.width, size.height)
			if got := lipgloss.Width(detail.View()); got > size.width {
				t.Fatalf("detail width=%d want <=%d", got, size.width)
			}
			before := detail.viewport.YOffset()
			var wheel tea.Msg
			for _, candidate := range detail.MouseTargets(0, 0, 1) {
				if candidate.ID == "detail.scroll" {
					wheel = candidate.Handle(MouseEvent{Button: tea.MouseWheelDown})
					break
				}
			}
			if wheel == nil {
				t.Fatal("detail mouse-wheel target missing")
			}
			detail, _ = detail.Update(wheel)
			if detail.viewport.YOffset() <= before {
				t.Fatalf("detail wheel did not scroll: before=%d after=%d", before, detail.viewport.YOffset())
			}
		})
	}
}
