package component

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDispatchMouseUsesHighestZAndLocalCoordinates(t *testing.T) {
	lowCalled := false
	targets := []MouseTarget{
		{ID: "low", Rect: Rect{X: 10, Y: 4, Width: 10, Height: 5}, Z: 1, Handle: func(MouseEvent) tea.Msg { lowCalled = true; return "low" }},
		{ID: "high", Rect: Rect{X: 12, Y: 5, Width: 4, Height: 2}, Z: 9, Handle: func(event MouseEvent) tea.Msg { return event }},
	}
	cmd := DispatchMouse(targets, tea.MouseClickMsg(tea.Mouse{X: 14, Y: 6, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("dispatch returned no command")
	}
	event, ok := cmd().(MouseEvent)
	if !ok || event.X != 2 || event.Y != 1 || event.Button != tea.MouseLeft || lowCalled {
		t.Fatalf("event=%#v ok=%t lowCalled=%t", event, ok, lowCalled)
	}
}

func TestDispatchMouseForwardsMotionEvents(t *testing.T) {
	target := MouseTarget{Rect: Rect{X: 5, Y: 3, Width: 4, Height: 2}, Z: 1, Handle: func(event MouseEvent) tea.Msg { return event }}
	cmd := DispatchMouse([]MouseTarget{target}, tea.MouseMotionMsg(tea.Mouse{X: 6, Y: 4}))
	if cmd == nil {
		t.Fatal("motion event returned no command")
	}
	event, ok := cmd().(MouseEvent)
	if !ok || !event.Motion || event.X != 1 || event.Y != 1 {
		t.Fatalf("motion event=%#v ok=%t", event, ok)
	}
}

func TestDispatchMouseIgnoresUnsupportedButtonsAndOutsideTargets(t *testing.T) {
	target := MouseTarget{Rect: Rect{X: 1, Y: 1, Width: 2, Height: 2}, Z: 1, Handle: func(MouseEvent) tea.Msg { return "hit" }}
	if cmd := DispatchMouse([]MouseTarget{target}, tea.MouseClickMsg(tea.Mouse{X: 10, Y: 10, Button: tea.MouseLeft})); cmd != nil {
		t.Fatal("outside click unexpectedly dispatched")
	}
	if cmd := DispatchMouse([]MouseTarget{target}, tea.MouseClickMsg(tea.Mouse{X: 1, Y: 1, Button: tea.MouseRight})); cmd != nil {
		t.Fatal("right click unexpectedly dispatched")
	}
	if cmd := DispatchMouse([]MouseTarget{target}, tea.MouseReleaseMsg(tea.Mouse{X: 1, Y: 1, Button: tea.MouseLeft})); cmd != nil {
		t.Fatal("mouse release unexpectedly dispatched")
	}
}

func TestCenteredOverlayTargetsDismissBackdropAndShieldModal(t *testing.T) {
	targets, x, y := CenteredOverlayTargets("modal\nbody", 40, 20, 3, 4, 10, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(targets) != 2 || targets[0].ID != "overlay.backdrop" || targets[1].ID != "overlay.modal" || targets[1].Z <= targets[0].Z {
		t.Fatalf("targets=%#v", targets)
	}
	cmd := DispatchMouse(targets, tea.MouseClickMsg(tea.Mouse{X: 3, Y: 4, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("backdrop click returned no command")
	}
	if msg, ok := cmd().(tea.KeyPressMsg); !ok || msg.String() != "esc" {
		t.Fatalf("dismiss=%#v", cmd())
	}
	if cmd := DispatchMouse(targets, tea.MouseClickMsg(tea.Mouse{X: 3 + x, Y: 4 + y, Button: tea.MouseLeft})); cmd != nil {
		t.Fatal("modal shield allowed backdrop dismissal")
	}
}
