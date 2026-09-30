package productadapter

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestValidateRejectsGenericHiddenDispatchAsReachability(t *testing.T) {
	descriptor := Live(
		capability.SurfaceTelegram,
		capability.LLMStatus,
		[]EntryPoint{{Kind: EntryDispatch, Value: "telegram.Interface.dispatch"}},
		[]EntryPoint{{Kind: EntryDispatch, Value: "telegram.Interface.dispatch"}},
	)
	if err := ValidateDescriptor(descriptor); err == nil || !strings.Contains(err.Error(), "hidden/generic") {
		t.Fatalf("validation error=%v", err)
	}
}

func TestValidateRequiresCanonicalOwnerAndConfirmationConsumption(t *testing.T) {
	descriptor := Live(
		capability.SurfaceTUI,
		capability.LLMProviderRemove,
		[]EntryPoint{{Kind: EntryAction, Value: "llm.provider.remove"}},
		[]EntryPoint{{Kind: EntryAction, Value: "llm.provider.remove"}},
	)
	if err := ValidateDescriptor(descriptor); err == nil || !strings.Contains(err.Error(), "confirmation consumption") {
		t.Fatalf("confirmation validation error=%v", err)
	}

	descriptor.ConfirmationConsumed = true
	descriptor.CanonicalOwner = "interface.local-owner"
	if err := ValidateDescriptor(descriptor); err == nil || !strings.Contains(err.Error(), "canonical owner") {
		t.Fatalf("owner validation error=%v", err)
	}
}

func TestValidateRejectsDuplicateSurfaceOperationDescriptors(t *testing.T) {
	descriptor := Live(
		capability.SurfaceCLI,
		capability.StatusOverview,
		[]EntryPoint{{Kind: EntryCommand, Value: "status"}},
		[]EntryPoint{{Kind: EntryDispatch, Value: "cm.operation_id=status.overview"}},
	)
	err := Validate([]Descriptor{descriptor, descriptor})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate validation error=%v", err)
	}
}

func TestCompleteKeepsMissingRequiredAdaptersAsExplicitGaps(t *testing.T) {
	live := Live(
		capability.SurfaceCLI,
		capability.StatusOverview,
		[]EntryPoint{{Kind: EntryCommand, Value: "status"}},
		[]EntryPoint{{Kind: EntryDispatch, Value: "cm.operation_id=status.overview"}},
	)
	descriptors := Complete(capability.SurfaceCLI, []Descriptor{live})
	if len(descriptors) == 0 || CountGaps(descriptors) == 0 {
		t.Fatalf("descriptors=%d gaps=%d", len(descriptors), CountGaps(descriptors))
	}
	for _, descriptor := range descriptors {
		if descriptor.Operation == capability.StatusOverview {
			if descriptor.State != StateLive {
				t.Fatalf("status descriptor=%#v", descriptor)
			}
			return
		}
	}
	t.Fatal("status descriptor missing")
}

func TestDefaultParentUsesCanonicalListDetailRelationship(t *testing.T) {
	if got := DefaultParent(capability.WorkspaceShow); got != capability.WorkspaceList {
		t.Fatalf("workspace parent=%s", got)
	}
	if got := DefaultParent(capability.LLMProviderGet); got != capability.LLMProviderList {
		t.Fatalf("LLM provider parent=%s", got)
	}
}

func TestCompleteRejectsLiveDetailWithoutReachableParent(t *testing.T) {
	detail := Live(
		capability.SurfaceCLI,
		capability.WorkspaceShow,
		[]EntryPoint{{Kind: EntryCommand, Value: "workspace show"}},
		[]EntryPoint{{Kind: EntryDispatch, Value: "operation=workspace.show"}},
	)
	detail.Parent = capability.WorkspaceList
	descriptors := Complete(capability.SurfaceCLI, []Descriptor{detail})
	for _, descriptor := range descriptors {
		if descriptor.Operation != capability.WorkspaceShow {
			continue
		}
		if descriptor.State != StateGap || !strings.Contains(descriptor.Gap, "parent") {
			t.Fatalf("workspace detail descriptor=%#v", descriptor)
		}
		return
	}
	t.Fatal("workspace detail descriptor missing")
}

func TestSecretContractsExposeProtectedInputAndOneTimeRecovery(t *testing.T) {
	tests := []struct {
		operation capability.ID
		policy    SecretPolicy
	}{
		{operation: capability.LLMProviderCredentialSet, policy: SecretPolicyProtectedInput},
		{operation: capability.TunnelAdminKeySet, policy: SecretPolicyProtectedInput},
		{operation: capability.AuthMCPRotate, policy: SecretPolicyOneTimeOutput},
		{operation: capability.AuthAdminRotate, policy: SecretPolicyOneTimeOutput},
	}
	for _, test := range tests {
		policy, recovery := SecretContractFor(test.operation)
		if policy != test.policy || strings.TrimSpace(recovery) == "" {
			t.Fatalf("%s secret contract=%q/%q want policy=%q with recovery", test.operation, policy, recovery, test.policy)
		}
	}
}
