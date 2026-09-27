package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/workspace"
)

type domainTestDispatcher struct {
	values map[capability.ID]any
	errors map[capability.ID]error
	calls  []application.DispatchRequest
}

func (dispatcher *domainTestDispatcher) Dispatch(_ context.Context, request application.DispatchRequest) (application.DispatchResult, error) {
	dispatcher.calls = append(dispatcher.calls, request)
	if err := dispatcher.errors[request.Operation]; err != nil {
		return application.DispatchResult{}, err
	}
	return application.DispatchResult{Operation: request.Operation, Value: dispatcher.values[request.Operation]}, nil
}

func newDomainTestInterface(t *testing.T, dispatcher application.OperationDispatcher) (*Interface, ViewOwner) {
	t.Helper()
	runtime := &Runtime{generation: 7, health: Health{Running: true, Enabled: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	return ui, ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
}

func TestWorkspaceListUsesCanonicalDispatcherAndSharedActionGroups(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.WorkspaceList: []application.WorkspaceView{
			{ID: "ws_a", Path: "/a", Available: true},
			{ID: "ws_b", Path: "/b", Available: false},
		},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.workspaceListScreen(t.Context(), owner, ActionState{Route: RouteWorkspaces, Back: RouteHome, Operation: capability.WorkspaceList})
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.WorkspaceList {
		t.Fatalf("dispatch calls=%#v", dispatcher.calls)
	}
	if screen.Rich == nil {
		t.Fatal("workspace list did not use rich presentation")
	}
	fallback := RichFallback(screen.Rich).Text
	for _, want := range []string{"Workspaces", "ws_a", "/a", "ws_b", "/b"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("workspace screen missing %q: %q", want, fallback)
		}
	}
	if len(screen.Keyboard) < 2 || screen.Keyboard[0][0].Text != "Register" {
		t.Fatalf("workspace action groups=%#v", screen.Keyboard)
	}
}

func TestApprovalCardUsesSafeCanonicalProjectionAndActionHierarchy(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	request := approval.Request{
		ID: "req_1", Status: approval.StatusPending, WorkspaceID: "ws_1", TargetTool: "run_command",
		Title: "Run guarded command", GuardCode: controlguard.CodeSemanticRisk,
		GuardReason: "semantic risk high (filesystem): require_approval",
		Command:     "rm -rf build-cache", SimilarCommandPattern: "rm -rf *",
		ExpiresAt: time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC),
	}
	screen, err := ui.requestCard(owner, request)
	if err != nil {
		t.Fatal(err)
	}
	if screen.Rich == nil {
		t.Fatal("approval card did not use rich presentation")
	}
	fallback := RichFallback(screen.Rich).Text
	for _, want := range []string{"semantic risk high", "run_command", "rm -rf build-cache"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("approval card missing %q: %q", want, fallback)
		}
	}
	for _, forbidden := range []string{"provider_payload", "confidence", "raw classification"} {
		if strings.Contains(strings.ToLower(fallback), forbidden) {
			t.Fatalf("approval card leaked %q: %q", forbidden, fallback)
		}
	}
	labels := []string{}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	joined := strings.Join(labels, "|")
	for _, want := range []string{"Approve once", "Deny", "Allow similar"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("approval actions missing %q: %v", want, labels)
		}
	}
}

func TestResolvedApprovalDetailHasNoMutationAuthority(t *testing.T) {
	request := approval.Request{ID: "req_1", Status: approval.StatusApproved, WorkspaceID: "ws_1", TargetTool: "run_command", Title: "Resolved"}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.RequestView: request}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.requestDetailScreen(t.Context(), owner, ActionState{Route: RouteRequest, ResourceID: request.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			switch button.Text {
			case "Approve once", "Deny", "Allow similar":
				t.Fatalf("resolved request retained mutation action: %#v", button)
			}
		}
	}
}

func TestDestructiveWorkspaceOperationRequiresTelegramConfirmation(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.WorkspaceContainerDelete: application.WorkspaceContainerView{ID: "grp_1"}}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	state := ActionState{
		Route: RouteOperation, Back: RouteContainers, Operation: capability.WorkspaceContainerDelete,
		ResourceID: "grp_1", Input: application.WorkspaceIDInput{ID: "grp_1"},
	}
	screen, err := ui.operationScreen(t.Context(), owner, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("destructive operation dispatched before confirmation: %#v", dispatcher.calls)
	}
	foundConfirm := false
	for _, row := range screen.Keyboard {
		for _, button := range row {
			if button.Text == "Confirm" {
				foundConfirm = true
			}
		}
	}
	if !foundConfirm {
		t.Fatalf("confirmation screen missing confirm action: %#v", screen.Keyboard)
	}
	state.Confirmed = true
	if _, err := ui.operationScreen(t.Context(), owner, state); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.WorkspaceContainerDelete {
		t.Fatalf("confirmed dispatch=%#v", dispatcher.calls)
	}
}

func TestRelocationConflictNeverChoosesRemoteResolution(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	conflict := &workspace.DuplicateWorkspaceIdentityError{WorkspaceID: "ws_dup", RegisteredRoot: "/registered", DestinationRoot: "/destination"}
	screen, ok := ui.workspaceRelocationConflictScreen(owner, ActionState{Operation: capability.WorkspaceRelocate}, conflict)
	if !ok {
		t.Fatal("duplicate relocation conflict was not recognized")
	}
	fallback := RichFallback(screen.Rich).Text
	for _, want := range []string{"destination", "registered", "merge", "local"} {
		if !strings.Contains(strings.ToLower(fallback), want) {
			t.Fatalf("conflict screen missing %q: %q", want, fallback)
		}
	}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			for _, forbidden := range []string{"destination", "registered", "merge"} {
				if strings.Contains(strings.ToLower(button.Text), forbidden) && button.CallbackData != "" {
					t.Fatalf("conflict exposed remote resolution action: %#v", button)
				}
			}
		}
	}
}

func TestWorkspaceActionInputsRemainTypedCanonicalInputs(t *testing.T) {
	tests := []struct {
		state ActionState
		text  string
		check func(any) bool
	}{
		{ActionState{InputKind: inputWorkspaceRegister}, "/workspace", func(value any) bool {
			input, ok := value.(application.WorkspaceRegisterInput)
			return ok && input.Path == "/workspace"
		}},
		{ActionState{InputKind: inputWorkspaceRelocate, ResourceID: "ws_1"}, "/moved", func(value any) bool {
			input, ok := value.(application.WorkspaceRelocateRequest)
			return ok && input.ID == "ws_1" && input.Path == "/moved" && input.Resolution == ""
		}},
		{ActionState{InputKind: inputWorkspaceAccessAdd, ResourceID: "ws_1"}, "/allowed", func(value any) bool {
			input, ok := value.(application.WorkspaceAccessInput)
			return ok && input.ID == "ws_1" && input.Path == "/allowed"
		}},
	}
	for _, test := range tests {
		value, err := actionInput(test.state, test.text)
		if err != nil {
			t.Fatal(err)
		}
		if !test.check(value) {
			t.Fatalf("typed input mismatch: %#v", value)
		}
	}
}
