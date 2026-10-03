package agent

import "testing"

func TestControllerOwnershipRequiresTrustedIdentity(t *testing.T) {
	owner, err := NewMCPController("session-a")
	if err != nil {
		t.Fatal(err)
	}
	same, err := NewMCPController("session-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewMCPController("session-b")
	if err != nil {
		t.Fatal(err)
	}
	if !same.CanControl(owner) {
		t.Fatal("matching MCP controller cannot control its agent")
	}
	if other.CanControl(owner) {
		t.Fatal("different MCP controller controlled another session's agent")
	}
	if !OperatorController().CanControl(owner) {
		t.Fatal("operator controller should have all-agent authority")
	}
	if (Controller{Kind: ControllerMCP}).CanControl(owner) {
		t.Fatal("empty caller identity unexpectedly authorized")
	}
}
