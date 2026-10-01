package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/notification"
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
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	screen, err := ui.requestCard(context.Background(), owner, request)
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
	if strings.Contains(joined, "Copy ID") {
		t.Fatalf("approval card retained redundant copy keyboard action: %v", labels)
	}
	foundCopy := false
	for _, block := range screen.Rich.Blocks {
		if block.Kind == RichCopy && block.CopyText == request.ID {
			foundCopy = true
		}
	}
	if !foundCopy {
		t.Fatalf("approval card missing inline copy ID: %#v", screen.Rich.Blocks)
	}
	if len(screen.Keyboard) < 1 || len(screen.Keyboard[0]) != 2 || screen.Keyboard[0][0].Text != "Approve once" || screen.Keyboard[0][1].Text != "Deny" {
		t.Fatalf("approval decision row=%#v", screen.Keyboard)
	}
}

func TestApprovalCardSeparatesAgentSummaryFromAIExplanationAndKeepsExplainStateMinimal(t *testing.T) {
	request := approval.Request{
		ID: "req_explain", Status: approval.StatusPending, WorkspaceID: "ws_1", TargetTool: "run_command",
		Title: "AGENT_SUMMARY", Command: "echo canonical-command", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.RequestExplainStatus: application.ApprovalExplainStatus{Available: true},
		capability.RequestExplanationView: application.ApprovalExplanationResult{
			RequestID: request.ID, State: application.ApprovalExplanationFailed, Failure: "provider unavailable",
		},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.requestCard(t.Context(), owner, request)
	if err != nil {
		t.Fatal(err)
	}
	fallback := RichFallback(screen.Rich).Text
	for _, want := range []string{"Agent summary", "AGENT_SUMMARY", "CodeMCP command", "echo canonical-command", "AI explanation", "provider unavailable"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("approval explanation card missing %q: %q", want, fallback)
		}
	}
	var retry Button
	for _, row := range screen.Keyboard {
		for _, button := range row {
			if button.Text == "Retry explain" {
				retry = button
			}
		}
	}
	if retry.CallbackData == "" {
		t.Fatal("Retry explain action missing")
	}
	ref, err := ui.callbacks.Decode(retry.CallbackData)
	if err != nil {
		t.Fatal(err)
	}
	value, err := ui.states.Get(ref.Token, owner)
	if err != nil {
		t.Fatal(err)
	}
	state := value.(ActionState)
	input, ok := state.Input.(application.ApprovalExplainInput)
	if !ok || input.ID != request.ID || !input.Retry || state.Operation != capability.RequestExplain {
		t.Fatalf("Explain callback state=%#v", state)
	}
	encoded := fmt.Sprintf("%#v", state)
	for _, forbidden := range []string{"AGENT_SUMMARY", "echo canonical-command", "provider unavailable"} {
		if strings.Contains(encoded, forbidden) || strings.Contains(retry.CallbackData, forbidden) {
			t.Fatalf("Explain callback leaked presentation/input context %q: %s", forbidden, encoded)
		}
	}

	dispatcher.values[capability.RequestExplanationView] = application.ApprovalExplanationResult{
		RequestID: request.ID, State: application.ApprovalExplanationReady,
		Explanation: &application.ApprovalExplanation{
			Summary: "AI_SUMMARY", Steps: []string{"AI_STEP"}, RiskNotes: []string{"AI_RISK"}, ProviderID: "ollama", Model: "model-demo",
		},
	}
	screen, err = ui.requestCard(t.Context(), owner, request)
	if err != nil {
		t.Fatal(err)
	}
	fallback = RichFallback(screen.Rich).Text
	for _, want := range []string{"AI_SUMMARY", "AI_STEP", "AI_RISK", "Generated by ollama · model-demo"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("ready explanation missing %q: %q", want, fallback)
		}
	}
}

func TestApprovalPendingNotificationRendersFreshInteractiveCard(t *testing.T) {
	request := approval.Request{
		ID: "req_notify", Status: approval.StatusPending, WorkspaceID: "ws_1", TargetTool: "run_command",
		Title: "Run guarded command", GuardCode: controlguard.CodeSemanticRisk,
		GuardReason: "semantic risk high", Command: "git push origin main", SimilarCommandPattern: "git push origin *",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.RequestView: request}}
	ui, owner := newDomainTestInterface(t, dispatcher)

	screen, handled, err := ui.RenderNotification(t.Context(), owner.ChatID, notification.Message{
		Kind: notification.KindApprovalPending, RequestID: request.ID, Title: "Approval requested",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("approval pending notification was not rendered interactively")
	}
	if len(screen.Breadcrumb) != 0 {
		t.Fatalf("quick approval notification must not have breadcrumb: %#v", screen.Breadcrumb)
	}
	if len(dispatcher.calls) < 1 || dispatcher.calls[0].Operation != capability.RequestView {
		t.Fatalf("notification canonical dispatch=%#v", dispatcher.calls)
	}
	input, ok := dispatcher.calls[0].Input.(application.RequestIDInput)
	if !ok || input.ID != request.ID {
		t.Fatalf("notification request input=%#v", dispatcher.calls[0].Input)
	}

	labels := []string{}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			labels = append(labels, button.Text)
			if button.CallbackData != "" {
				if strings.Contains(button.CallbackData, request.ID) {
					t.Fatalf("callback leaked request id: %q", button.CallbackData)
				}
				ref, decodeErr := ui.callbacks.Decode(button.CallbackData)
				if decodeErr != nil {
					t.Fatalf("decode callback %q: %v", button.Text, decodeErr)
				}
				if _, stateErr := ui.states.Get(ref.Token, owner); stateErr != nil {
					t.Fatalf("callback state not owner-bound for %q: %v", button.Text, stateErr)
				}
			}
		}
	}
	joined := strings.Join(labels, "|")
	for _, want := range []string{"Review", "Approve once", "Deny", "Allow similar"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("interactive notification missing %q: %v", want, labels)
		}
	}
	if len(screen.Keyboard) < 2 || len(screen.Keyboard[0]) != 2 || screen.Keyboard[0][0].Text != "Approve once" || screen.Keyboard[0][1].Text != "Deny" {
		t.Fatalf("notification decision row=%#v", screen.Keyboard)
	}
	if len(screen.Keyboard[1]) != 2 || screen.Keyboard[1][0].Text != "Review" || screen.Keyboard[1][1].Text != "Allow similar" {
		t.Fatalf("notification secondary row=%#v", screen.Keyboard[1])
	}
}

func TestExpiredPendingApprovalNotificationHasNoMutationActions(t *testing.T) {
	request := approval.Request{
		ID: "req_expired", Status: approval.StatusPending, WorkspaceID: "ws_1", TargetTool: "run_command",
		Title: "Expired request", ExpiresAt: time.Now().Add(-time.Minute),
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.RequestView: request}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, handled, err := ui.RenderNotification(t.Context(), owner.ChatID, notification.Message{Kind: notification.KindApprovalPending, RequestID: request.ID})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			switch button.Text {
			case "Approve once", "Deny", "Allow similar":
				t.Fatalf("expired notification retained mutation action: %#v", button)
			}
		}
	}
}

func TestResolvedApprovalNotificationRendersFreshResolvedCard(t *testing.T) {
	request := approval.Request{
		ID: "req_resolved", Status: approval.StatusApproved, WorkspaceID: "ws_1", TargetTool: "run_command",
		Title: "Resolved request", ExpiresAt: time.Now().Add(time.Minute),
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.RequestView: request}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, handled, err := ui.RenderNotification(t.Context(), owner.ChatID, notification.Message{
		Kind: notification.KindApprovalResolved, RequestID: request.ID,
	})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(dispatcher.calls) < 1 || dispatcher.calls[0].Operation != capability.RequestView {
		t.Fatalf("resolved notification canonical dispatch=%#v", dispatcher.calls)
	}
	fallback := RichFallback(screen.Rich).Text
	if !strings.Contains(fallback, displayState(string(approval.StatusApproved))) {
		t.Fatalf("resolved notification screen=%q", fallback)
	}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			switch button.Text {
			case "Approve once", "Deny", "Allow similar":
				t.Fatalf("resolved notification retained mutation action: %#v", button)
			}
		}
	}
}

func TestStaleApprovalActionReloadsCanonicalRequestState(t *testing.T) {
	resolved := approval.Request{
		ID: "req_stale", Status: approval.StatusDenied, WorkspaceID: "ws_1", TargetTool: "run_command",
		Title: "Resolved elsewhere",
	}
	dispatcher := &domainTestDispatcher{
		values: map[capability.ID]any{capability.RequestView: resolved},
		errors: map[capability.ID]error{capability.RequestApprove: errors.New("approval request is no longer pending")},
	}
	ui, owner := newDomainTestInterface(t, dispatcher)
	state := ActionState{
		Route: RouteOperation, Back: RouteRequests, Operation: capability.RequestApprove,
		ResourceID: resolved.ID, Input: application.RequestResolutionInput{ID: resolved.ID},
	}
	screen, err := ui.operationErrorScreen(owner, state, errors.New("approval request is no longer pending"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) < 1 || dispatcher.calls[0].Operation != capability.RequestView {
		t.Fatalf("stale action did not reload canonical request: %#v", dispatcher.calls)
	}
	fallback := RichFallback(screen.Rich).Text
	if !strings.Contains(fallback, displayState(string(approval.StatusDenied))) {
		t.Fatalf("stale action did not render current status: %q", fallback)
	}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			switch button.Text {
			case "Approve once", "Deny", "Allow similar":
				t.Fatalf("stale resolved card retained mutation action: %#v", button)
			}
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
		descriptor, ok := workspaceInputFlow(test.state)
		if !ok {
			t.Fatalf("workspace flow missing for %#v", test.state)
		}
		value, err := descriptor.Build(inputFlowData{values: map[string]string{"value": test.text}, set: map[string]bool{"value": true}})
		if err != nil {
			t.Fatal(err)
		}
		if !test.check(value) {
			t.Fatalf("typed input mismatch: %#v", value)
		}
	}
}
