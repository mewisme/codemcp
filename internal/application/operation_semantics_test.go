package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestDispatcherPreservesCanonicalSemanticsAcrossInterfaceContexts(t *testing.T) {
	dispatcher := NewDispatcher()
	if err := dispatcher.Register(capability.WorkspaceList, func(_ context.Context, input any) (any, error) {
		return input, nil
	}); err != nil {
		t.Fatal(err)
	}

	want, ok := OperationSemanticsFor(capability.WorkspaceList)
	if !ok {
		t.Fatal("workspace.list semantics missing")
	}
	input := []string{"same", "input"}
	interfaces := []OperationInterface{
		OperationInterfaceCLI,
		OperationInterfaceTUI,
		OperationInterfaceAdmin,
		OperationInterfaceTelegram,
		OperationInterfaceMCP,
	}
	for _, surface := range interfaces {
		t.Run(string(surface), func(t *testing.T) {
			result, err := dispatcher.Dispatch(
				WithOperationInterface(t.Context(), surface),
				DispatchRequest{Operation: capability.WorkspaceList, Input: input},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Operation != capability.WorkspaceList || result.Semantics != want {
				t.Fatalf("dispatch semantics=%#v want=%#v", result.Semantics, want)
			}
			if !reflect.DeepEqual(result.Value, input) {
				t.Fatalf("dispatch value=%#v want=%#v", result.Value, input)
			}
		})
	}
}

func TestDispatcherPreservesCanonicalErrorSemanticsAcrossInterfaceContexts(t *testing.T) {
	dispatcher := NewDispatcher()
	if err := dispatcher.Register(capability.WorkspaceRelocate, func(context.Context, any) (any, error) {
		return nil, staleOperationError(capability.WorkspaceRelocate, errors.New("registry changed"))
	}); err != nil {
		t.Fatal(err)
	}

	want := ErrorSemantics{Code: ErrorConflict, Retryable: true, Stale: true}
	for _, surface := range []OperationInterface{
		OperationInterfaceCLI,
		OperationInterfaceTUI,
		OperationInterfaceAdmin,
		OperationInterfaceTelegram,
	} {
		t.Run(string(surface), func(t *testing.T) {
			_, err := dispatcher.Dispatch(
				WithOperationInterface(t.Context(), surface),
				DispatchRequest{Operation: capability.WorkspaceRelocate, Input: "same-input"},
			)
			if got := ErrorSemanticsOf(err); got != want {
				t.Fatalf("error semantics=%#v want=%#v err=%v", got, want, err)
			}
		})
	}
}

func TestDestructiveConfirmationAndRiskComeFromCanonicalOperationSemantics(t *testing.T) {
	semantics, ok := OperationSemanticsFor(capability.WorkspacePurge)
	if !ok {
		t.Fatal("workspace.purge semantics missing")
	}
	if semantics.Kind != capability.KindMutation ||
		semantics.Risk != capability.RiskDestructive ||
		semantics.Confirmation.Mode != capability.ConfirmationRequired ||
		!semantics.Effects.Destructive {
		t.Fatalf("workspace.purge semantics=%#v", semantics)
	}
}

func TestOperationSemanticsShareTheCanonicalApprovalPolicy(t *testing.T) {
	for _, id := range []capability.ID{capability.WorkspacePurge, capability.AgentConfigSet} {
		semantics, ok := OperationSemanticsFor(id)
		if !ok {
			t.Fatalf("%s semantics missing", id)
		}
		policy, ok := capability.SecurityPolicyFor(id)
		if !ok {
			t.Fatalf("%s security policy missing", id)
		}
		if semantics.Authorization != policy.Authorization ||
			semantics.Risk != policy.Risk ||
			semantics.Effects.Destructive != policy.Destructive ||
			semantics.Confirmation != policy.Confirmation {
			t.Fatalf("%s semantic/security policy drift: semantics=%#v policy=%#v", id, semantics, policy)
		}
	}
	agentConfig, _ := OperationSemanticsFor(capability.AgentConfigSet)
	if agentConfig.Risk != capability.RiskSensitive ||
		agentConfig.Confirmation.Mode != capability.ConfirmationRequired ||
		!agentConfig.Confirmation.ControlApproval {
		t.Fatalf("agent config semantic escalation=%#v", agentConfig)
	}
}

func TestMCPProfileProjectionCannotChangeCanonicalOperationSemantics(t *testing.T) {
	want, ok := OperationSemanticsFor(capability.ShellRun)
	if !ok {
		t.Fatal("shell.run semantics missing")
	}
	base, ok := capability.ProjectProtocol(capability.ShellRun, "base", map[string]any{"presentation": "base"})
	if !ok {
		t.Fatal("base projection missing")
	}
	openai, ok := capability.ProjectProtocol(capability.ShellRun, "openai", map[string]any{"presentation": "openai"})
	if !ok {
		t.Fatal("openai projection missing")
	}

	for name, projection := range map[string]capability.ProtocolProjection{"base": base, "openai": openai} {
		got := OperationSemantics{
			Kind:          want.Kind,
			Authorization: projection.Authorization,
			Risk:          want.Risk,
			Confirmation:  projection.Confirmation,
			Effects:       projection.Effects,
		}
		if projection.Operation != capability.ShellRun || got != want {
			t.Fatalf("%s projection changed canonical semantics: projection=%#v semantics=%#v want=%#v", name, projection, got, want)
		}
	}
	if reflect.DeepEqual(base.Metadata, openai.Metadata) {
		t.Fatal("profile presentation metadata unexpectedly identical")
	}
}
