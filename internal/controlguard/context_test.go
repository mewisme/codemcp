package controlguard

import (
	"context"
	"testing"
)

func TestApprovalContextRoundTripAndCloning(t *testing.T) {
	invocation := Invocation{Program: "cm", Args: []string{"update"}, Command: "cm update"}
	ctx := WithApproval(context.Background(), Approval{RequestID: "req_test", Capability: "cap_test", Invocation: invocation})
	invocation.Args[0] = "changed"
	value, ok := ApprovalFromContext(ctx)
	if !ok || value.RequestID != "req_test" || value.Capability != "cap_test" || value.Invocation.Args[0] != "update" {
		t.Fatalf("approval = %#v ok=%t", value, ok)
	}
	value.Invocation.Args[0] = "changed-again"
	reloaded, ok := ApprovalFromContext(ctx)
	if !ok || reloaded.Invocation.Args[0] != "update" {
		t.Fatalf("stored approval mutated: %#v ok=%t", reloaded, ok)
	}
}

func TestApprovalContextRequiresRequestAndCapability(t *testing.T) {
	for _, value := range []Approval{{}, {RequestID: "req_test"}, {Capability: "cap_test"}} {
		ctx := WithApproval(context.Background(), value)
		if got, ok := ApprovalFromContext(ctx); ok || got.RequestID != "" {
			t.Fatalf("invalid approval stored: %#v ok=%t", got, ok)
		}
	}
}

func TestGrantContextRoundTrip(t *testing.T) {
	ctx := WithGrant(context.Background(), Grant{RequestID: " req_test ", Code: CodeDestructiveMutation})
	value, ok := GrantFromContext(ctx)
	if !ok || value.RequestID != "req_test" || value.Code != CodeDestructiveMutation {
		t.Fatalf("grant = %#v ok=%t", value, ok)
	}
	if _, ok := GrantFromContext(context.Background()); ok {
		t.Fatal("unexpected grant in empty context")
	}
}

func TestSameInvocationIsExact(t *testing.T) {
	base := Invocation{Program: "cm", Args: []string{"config", "set", "http.mcp.port", "41001"}, Command: "cm config set http.mcp.port 41001"}
	if !SameInvocation(base, base) {
		t.Fatal("identical invocation did not match")
	}
	for _, changed := range []Invocation{
		{Program: "chatgpt-mcp", Args: base.Args, Command: base.Command},
		{Program: base.Program, Args: []string{"config", "set", "http.mcp.port", "41002"}, Command: base.Command},
		{Program: base.Program, Args: base.Args, Command: base.Command + " --force"},
	} {
		if SameInvocation(base, changed) {
			t.Fatalf("changed invocation matched: %#v", changed)
		}
	}
}
