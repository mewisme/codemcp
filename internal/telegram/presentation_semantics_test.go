package telegram

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

func TestOperationScreenUsesCanonicalPresentationWithoutOwningConfirmationPolicy(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "purged"}
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 1}, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 1}
	state := ActionState{
		Route:     RouteOperation,
		Back:      RouteWorkspaces,
		Operation: capability.WorkspacePurge,
		Input:     application.WorkspacePurgeInput{Target: "ws_demo", Confirm: true},
	}
	screen, err := ui.operationScreen(t.Context(), owner, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("presentation bypassed canonical confirmation policy: %#v", dispatcher.calls)
	}
	for _, want := range []string{"Confirmation required", "Purge Workspace", "Workspace", "destructive"} {
		if !strings.Contains(screen.Text, want) {
			t.Fatalf("confirmation screen missing %q: %q", want, screen.Text)
		}
	}
}

func TestOperationLifecycleAndNavigationUseCanonicalVocabulary(t *testing.T) {
	working := workingScreen(ActionState{Route: RouteOperation, Operation: capability.RuntimeRestart})
	if !strings.Contains(working.Text, "Working") || !strings.Contains(working.Text, "Restart") {
		t.Fatalf("working screen=%q", working.Text)
	}

	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 1}
	for intent, button := range map[string]func() (Button, error){
		"Back":    func() (Button, error) { return ui.backButton(owner, RouteHome) },
		"Home":    func() (Button, error) { return ui.homeButton(owner) },
		"Refresh": func() (Button, error) { return ui.refreshButton(owner, ActionState{Route: RouteHome}) },
		"Retry":   func() (Button, error) { return ui.retryButton(owner, ActionState{Route: RouteHome}) },
		"Cancel":  func() (Button, error) { return ui.cancelButton(owner, RouteHome) },
		"Close":   func() (Button, error) { return ui.closeButton(owner, ActionState{Route: RouteHome}) },
	} {
		value, err := button()
		if err != nil {
			t.Fatal(err)
		}
		if value.Text != intent {
			t.Fatalf("navigation button=%q want=%q", value.Text, intent)
		}
	}
}
