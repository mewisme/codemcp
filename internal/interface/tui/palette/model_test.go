package palette

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/interface/tui/action"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

func TestPaletteQueryAndSelection(t *testing.T) {
	model := New([]action.Action{
		{ID: "logs", Title: "Go to Logs", Category: "App"},
		{ID: "config", Title: "Verify configuration", Category: "Config"},
	}, action.Context{})
	model.SetQuery("verify")
	if got := model.SelectedID(); got != "config" {
		t.Fatalf("selected = %q results=%v", got, model.DebugResults())
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("selection command is nil")
	}
	if message, ok := cmd().(SelectedMsg); !ok || message.ID != "config" {
		t.Fatalf("message = %#v", cmd())
	}
	if updated.Query() != "verify" {
		t.Fatalf("query = %q", updated.Query())
	}
}

func TestPaletteClose(t *testing.T) {
	model := New(nil, action.Context{})
	_, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("close command is nil")
	}
	if _, ok := cmd().(ClosedMsg); !ok {
		t.Fatalf("message = %#v", cmd())
	}
}

func TestPaletteMouseClickAndWheelUseSemanticMessages(t *testing.T) {
	model := New([]action.Action{
		{ID: "one", Title: "One", Category: "App"},
		{ID: "two", Title: "Two", Category: "App"},
		{ID: "three", Title: "Three", Category: "App"},
	}, action.Context{})
	targets := model.MouseTargets(5, 3, 20, 72)
	var result component.MouseTarget
	found := false
	for _, target := range targets {
		if target.ID == "palette.result" {
			result, found = target, true
			break
		}
	}
	if !found {
		t.Fatal("palette result target not found")
	}
	message, ok := result.Handle(component.MouseEvent{Button: tea.MouseLeft}).(SelectedMsg)
	if !ok || message.ID == "" {
		t.Fatalf("palette click message=%#v", message)
	}

	scroll := targets[0]
	before := model.SelectedID()
	wheel, ok := scroll.Handle(component.MouseEvent{Button: tea.MouseWheelDown}).(MouseScrollMsg)
	if !ok || wheel.Delta != 1 {
		t.Fatalf("wheel message=%#v", wheel)
	}
	updated, _ := model.Update(wheel)
	if updated.SelectedID() == before {
		t.Fatalf("wheel did not move selection from %q", before)
	}
}

func TestPaletteListOwnsPaginationAndSelection(t *testing.T) {
	actions := make([]action.Action, 12)
	for index := range actions {
		actions[index] = action.Action{ID: fmt.Sprintf("action-%02d", index), Title: fmt.Sprintf("Action %02d", index), Category: "App"}
	}
	model := New(actions, action.Context{})
	for range 10 {
		updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		model = updated
	}
	if got := model.SelectedID(); got != "action-10" {
		t.Fatalf("selected=%q", got)
	}
	plain := ansi.Strip(model.View(72))
	if !strings.Contains(plain, "Action 10") || strings.Contains(plain, "Action 00") {
		t.Fatalf("paged view=%q", plain)
	}
}
