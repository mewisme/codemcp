package plan

import (
	"errors"
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
