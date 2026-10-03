package plan

import (
	"errors"
	"strings"
	"testing"
)

func TestExecutionManagerBindingIsIdempotentAndWorkspaceScoped(t *testing.T) {
	manager := NewExecutionManager()
	binding := ExecutionBinding{
		WorkspaceID:       "ws_alpha",
		PlanName:          "alpha-plan",
		BaselineContentID: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Phase:             Phase{ID: "1A", Title: "Define model"},
	}
	first, err := manager.Bind("session-one", binding)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Bind("session-one", binding)
	if err != nil || second != first {
		t.Fatalf("idempotent Bind() = %#v, %v; want %#v", second, err, first)
	}
	if got, ok := manager.Lookup("session-one", binding.WorkspaceID); !ok || got != binding {
		t.Fatalf("Lookup() = %#v, %v; want %#v, true", got, ok, binding)
	}

	otherWorkspace := binding
	otherWorkspace.WorkspaceID = "ws_beta"
	if _, err := manager.Bind("session-one", otherWorkspace); err != nil {
		t.Fatalf("Bind(other workspace) error = %v", err)
	}
}

func TestExecutionManagerRejectsConflictingBinding(t *testing.T) {
	manager := NewExecutionManager()
	first := ExecutionBinding{
		WorkspaceID:       "ws_alpha",
		PlanName:          "alpha-plan",
		BaselineContentID: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Phase:             Phase{ID: "1A", Title: "Define model"},
	}
	if _, err := manager.Bind("session-one", first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.PlanName = "beta-plan"
	second.BaselineContentID = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := manager.Bind("session-one", second); !errors.Is(err, ErrExecutionBindingConflict) {
		t.Fatalf("Bind(conflict) error = %v, want ErrExecutionBindingConflict", err)
	}
}

func TestExecutionManagerValidatesAndCommitsBoundProgress(t *testing.T) {
	manager := NewExecutionManager()
	basePlan, baseOrder := lifecycleFixture(false, false, false)
	base, err := ParseParts(basePlan, baseOrder)
	if err != nil {
		t.Fatal(err)
	}
	binding := ExecutionBinding{
		WorkspaceID:       "ws_alpha",
		PlanName:          "alpha-plan",
		BaselineContentID: base.ContentID(),
		CompletedPhases:   0,
		Phase:             Phase{ID: "1A", Title: "Define model"},
	}
	if _, err := manager.Bind("session-one", binding); err != nil {
		t.Fatal(err)
	}

	editedPlan := strings.Replace(basePlan, "Exercise derived lifecycle state.", "Exercise derived lifecycle state with edited future detail.", 1)
	edited, err := ParseParts(editedPlan, baseOrder)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := manager.PrepareUpdate("session-one", binding.WorkspaceID, binding.PlanName, base.ContentID(), edited)
	if err != nil {
		t.Fatalf("PrepareUpdate(non-progress) error = %v", err)
	}
	if transition.closes {
		t.Fatal("non-progress transition unexpectedly closes binding")
	}
	if err := manager.CommitUpdate("session-one", transition); err != nil {
		t.Fatal(err)
	}
	afterEdit, ok := manager.Lookup("session-one", binding.WorkspaceID)
	if !ok || afterEdit.Closed || afterEdit.BaselineContentID != edited.ContentID() || afterEdit.CompletedPhases != 0 {
		t.Fatalf("binding after non-progress edit = %#v", afterEdit)
	}

	progressPlan, progressOrder := lifecycleFixture(true, false, false)
	progress, err := ParseParts(progressPlan, progressOrder)
	if err != nil {
		t.Fatal(err)
	}
	transition, err = manager.PrepareUpdate("session-one", binding.WorkspaceID, binding.PlanName, edited.ContentID(), progress)
	if err != nil {
		t.Fatalf("PrepareUpdate(progress) error = %v", err)
	}
	if !transition.closes {
		t.Fatal("bound phase completion did not close transition")
	}
	if err := manager.CommitUpdate("session-one", transition); err != nil {
		t.Fatal(err)
	}
	closed, ok := manager.Lookup("session-one", binding.WorkspaceID)
	if !ok || !closed.Closed || closed.CompletedPhases != 1 || closed.BaselineContentID != progress.ContentID() {
		t.Fatalf("closed binding = %#v", closed)
	}
}

func TestExecutionManagerRejectsSkippedMultiPhaseAndBoundIdentityChanges(t *testing.T) {
	manager := NewExecutionManager()
	basePlan, baseOrder := lifecycleFixture(false, false, false)
	base, err := ParseParts(basePlan, baseOrder)
	if err != nil {
		t.Fatal(err)
	}
	binding := ExecutionBinding{
		WorkspaceID:       "ws_alpha",
		PlanName:          "alpha-plan",
		BaselineContentID: base.ContentID(),
		CompletedPhases:   0,
		Phase:             Phase{ID: "1A", Title: "Define model"},
	}
	if _, err := manager.Bind("session-one", binding); err != nil {
		t.Fatal(err)
	}

	multiPlan, multiOrder := lifecycleFixture(true, true, false)
	multi, err := ParseParts(multiPlan, multiOrder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PrepareUpdate("session-one", binding.WorkspaceID, binding.PlanName, base.ContentID(), multi); err == nil || !strings.Contains(err.Error(), "only bound phase") {
		t.Fatalf("PrepareUpdate(multi phase) error = %v", err)
	}

	renamedPlan := strings.Replace(basePlan, "Phase 1A - Define model", "Phase 1A - Rename model", 1)
	renamedOrder := strings.Replace(baseOrder, "Phase 1A - Define model", "Phase 1A - Rename model", 1)
	renamed, err := ParseParts(renamedPlan, renamedOrder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PrepareUpdate("session-one", binding.WorkspaceID, binding.PlanName, base.ContentID(), renamed); err == nil || !strings.Contains(err.Error(), "renamed") {
		t.Fatalf("PrepareUpdate(renamed bound phase) error = %v", err)
	}
}

func TestExecutionManagerRejectsPreparedTransitionAfterBindingChanges(t *testing.T) {
	manager := NewExecutionManager()
	basePlan, baseOrder := lifecycleFixture(false, false, false)
	base, err := ParseParts(basePlan, baseOrder)
	if err != nil {
		t.Fatal(err)
	}
	binding := ExecutionBinding{
		WorkspaceID:       "ws_alpha",
		PlanName:          "alpha-plan",
		BaselineContentID: base.ContentID(),
		CompletedPhases:   0,
		Phase:             Phase{ID: "1A", Title: "Define model"},
	}
	if _, err := manager.Bind("session-one", binding); err != nil {
		t.Fatal(err)
	}
	editedPlan := strings.Replace(basePlan, "Exercise derived lifecycle state.", "Exercise updated lifecycle state.", 1)
	edited, err := ParseParts(editedPlan, baseOrder)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.PrepareUpdate("session-one", binding.WorkspaceID, binding.PlanName, base.ContentID(), edited)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.PrepareUpdate("session-one", binding.WorkspaceID, binding.PlanName, base.ContentID(), edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CommitUpdate("session-one", first); err != nil {
		t.Fatal(err)
	}
	if err := manager.CommitUpdate("session-one", second); !errors.Is(err, ErrExecutionBindingConflict) {
		t.Fatalf("second CommitUpdate() error = %v, want ErrExecutionBindingConflict", err)
	}
}

func TestExecutionManagerReleaseIsWorkspaceScopedAndIdempotent(t *testing.T) {
	manager := NewExecutionManager()
	binding := ExecutionBinding{
		WorkspaceID:       "ws_alpha",
		PlanName:          "alpha-plan",
		BaselineContentID: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Phase:             Phase{ID: "1A", Title: "Define model"},
	}
	if _, err := manager.Bind("session-one", binding); err != nil {
		t.Fatal(err)
	}
	other := binding
	other.WorkspaceID = "ws_beta"
	if _, err := manager.Bind("session-one", other); err != nil {
		t.Fatal(err)
	}
	if !manager.Release("session-one", binding.WorkspaceID) {
		t.Fatal("Release() = false, want true")
	}
	if manager.Release("session-one", binding.WorkspaceID) {
		t.Fatal("second Release() = true, want false")
	}
	if _, ok := manager.Lookup("session-one", binding.WorkspaceID); ok {
		t.Fatal("released binding still present")
	}
	if got, ok := manager.Lookup("session-one", other.WorkspaceID); !ok || got != other {
		t.Fatalf("other workspace binding = %#v, %v; want %#v, true", got, ok, other)
	}
}
