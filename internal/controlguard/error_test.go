package controlguard

import (
	"fmt"
	"testing"
)

func TestErrorSupportsWrappedTypeAndCodeChecks(t *testing.T) {
	invocation := &Invocation{Program: "cm", Args: []string{"update"}, Command: "cm update"}
	guard := New(CodeControlPlaneMutation, "control-plane mutation denied", true, invocation)
	wrapped := fmt.Errorf("shell rejected command: %w", guard)
	value, ok := As(wrapped)
	if !ok || value.Code != CodeControlPlaneMutation || !value.Approvable || value.Error() != "control-plane mutation denied" {
		t.Fatalf("guard = %#v ok=%t", value, ok)
	}
	if !IsCode(wrapped, CodeControlPlaneMutation) || IsCode(wrapped, CodeProtectedState) {
		t.Fatalf("code checks failed for %v", wrapped)
	}
	invocation.Args[0] = "changed"
	if value.Invocation.Args[0] != "update" {
		t.Fatalf("invocation was not cloned: %#v", value.Invocation)
	}
}

func TestErrorFallbackMessage(t *testing.T) {
	if got := New(CodeProtectedState, "", false, nil).Error(); got != "control guard denied: protected_state_access" {
		t.Fatalf("message = %q", got)
	}
	var value *Error
	if got := value.Error(); got != "control guard denied" {
		t.Fatalf("nil message = %q", got)
	}
}
