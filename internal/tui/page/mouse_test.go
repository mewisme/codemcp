package page

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/tui/component"
)

func TestKeyHintMouseTargetsKeepSpaceShortcut(t *testing.T) {
	view := "e Configure · Space Enable/Disable · s Sync"
	targets := keyHintMouseTargets(view, map[string]string{"Enable/Disable": " "}, 0, 0, 1)
	if len(targets) != 1 {
		t.Fatalf("targets=%d want=1", len(targets))
	}
	message, ok := targets[0].Handle(component.MouseEvent{Button: tea.MouseLeft}).(tea.KeyPressMsg)
	if !ok || message.String() != "space" {
		t.Fatalf("space mouse message=%#v", message)
	}
}
