package component

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestConfirmButtonsToggleAndRender(t *testing.T) {
	buttons := NewConfirmButtons("Allow", "Deny", true)
	buttons.SetWidth(40)
	if !buttons.AffirmativeSelected() {
		t.Fatal("affirmative should be selected initially")
	}
	view := buttons.View()
	if !strings.Contains(view, "Allow") || !strings.Contains(view, "Deny") {
		t.Fatalf("view=%q", view)
	}
	buttons.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	if buttons.AffirmativeSelected() {
		t.Fatal("right did not select negative button")
	}
	buttons.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !buttons.AffirmativeSelected() {
		t.Fatal("tab did not toggle button selection")
	}
}

func TestConfirmButtonsMouseTargetsReturnSemanticChoice(t *testing.T) {
	buttons := NewConfirmButtons("Allow", "Deny", true)
	targets := buttons.MouseTargets(4, 3, 7)
	if len(targets) != 2 {
		t.Fatalf("targets=%d want=2", len(targets))
	}
	for _, target := range targets {
		message, ok := target.Handle(MouseEvent{Button: tea.MouseLeft}).(ConfirmChoiceMsg)
		if !ok {
			t.Fatalf("message=%T", target.Handle(MouseEvent{Button: tea.MouseLeft}))
		}
		buttons.Select(message.Affirmative)
		if buttons.AffirmativeSelected() != message.Affirmative {
			t.Fatalf("selected=%t message=%t", buttons.AffirmativeSelected(), message.Affirmative)
		}
	}
}
