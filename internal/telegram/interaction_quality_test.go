package telegram

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestNativeInteractionQualityGrammarStaysStructurallyStable(t *testing.T) {
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 21}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 21}

	back, err := ui.backButton(owner, RouteStatus)
	if err != nil {
		t.Fatal(err)
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := ui.stateButton(owner, "↻ Refresh", CallbackOpen, ActionState{
		Route: RouteStatus, Back: RouteHome, Operation: capability.StatusOverview,
	})
	if err != nil {
		t.Fatal(err)
	}
	refresh.Role = ButtonRoleNavigation
	remove, err := ui.stateButton(owner, "Delete retained item", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteStatus, Operation: capability.WorkspacePurge, ForceConfirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	remove.Role = ButtonRoleDestructive

	rows := BoundedActionGroups(ActionGroups{
		Destructive: []Button{remove},
		Navigation:  []Button{back, home, refresh},
	})
	if len(rows) != 2 || len(rows[0]) != 1 || len(rows[1]) != 3 {
		t.Fatalf("grouped native controls=%#v", rows)
	}
	if rows[0][0].Role != ButtonRoleDestructive || semanticButtonStyle(rows[0][0]) != ButtonStyleDanger {
		t.Fatalf("destructive action lost danger semantics: %#v", rows[0][0])
	}
	if got := strings.Join([]string{rows[1][0].Text, rows[1][1].Text, rows[1][2].Text}, "|"); got != "« Back|⌂ Home|↻ Refresh" {
		t.Fatalf("navigation grammar=%q", got)
	}
	for _, button := range rows[1] {
		if semanticButtonStyle(button) != "" {
			t.Fatalf("navigation button acquired destructive/primary styling: %#v", button)
		}
	}

	pages, err := ui.paginationKeyboard(owner, ActionState{
		Route: RouteStatus, Back: RouteHome, Page: 1,
	}, 33, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0][1].Text != "( 2 )" || !pages[0][1].Disabled {
		t.Fatalf("pagination structure=%#v", pages)
	}
	if pages[1][0].Text != "« Back" {
		t.Fatalf("pagination lost Back navigation: %#v", pages[1])
	}

	working := workingScreen(ActionState{
		Route: RouteOperation, Back: RouteStatus, Operation: capability.RuntimeRestart,
	})
	if len(working.Keyboard) != 1 || len(working.Keyboard[0]) != 1 || !working.Keyboard[0][0].Disabled {
		t.Fatalf("working screen must expose one disabled terminal-replacement marker: %#v", working.Keyboard)
	}
	if !strings.Contains(screenText(working), "Working") {
		t.Fatalf("working screen text=%q", screenText(working))
	}
}
