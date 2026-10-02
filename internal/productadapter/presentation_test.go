package productadapter

import (
	"bytes"
	"os"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/capability"
)

func TestRepresentativeOperationPresentationUsesCanonicalCapabilitySemantics(t *testing.T) {
	tests := []struct {
		operation    capability.ID
		category     ActionCategory
		danger       DangerLevel
		confirmation capability.ConfirmationMode
		input        InputShape
	}{
		{capability.StatusOverview, ActionCategoryRead, DangerNone, capability.ConfirmationNone, InputNone},
		{capability.WorkspacePurge, ActionCategoryDelete, DangerDestructive, capability.ConfirmationRequired, InputForm},
		{capability.RequestApprove, ActionCategoryReview, DangerCaution, capability.ConfirmationReview, InputDecision},
		{capability.LLMProviderCredentialSet, ActionCategoryChange, DangerCaution, capability.ConfirmationNone, InputProtectedSecret},
		{capability.RuntimeRestart, ActionCategoryRun, DangerCaution, capability.ConfirmationNone, InputForm},
	}
	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			presentation, ok := PresentationFor(test.operation, "")
			if !ok {
				t.Fatal("presentation missing")
			}
			if presentation.Category != test.category || presentation.Danger != test.danger || presentation.Confirmation != test.confirmation || presentation.Input != test.input {
				t.Fatalf("presentation=%#v", presentation)
			}
			if presentation.Title == "" || presentation.Subject == "" {
				t.Fatalf("presentation lacks title/subject: %#v", presentation)
			}
			if err := ValidatePresentation(presentation); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEveryDestructivePresentationUsesDestructiveActionGrammar(t *testing.T) {
	for _, presentation := range AllOperationPresentation() {
		if presentation.Danger != DangerDestructive {
			continue
		}
		if presentation.Category != ActionCategoryDelete && presentation.Category != ActionCategoryReview {
			t.Fatalf("%s category=%q", presentation.Operation, presentation.Category)
		}
		if presentation.Confirmation == capability.ConfirmationNone {
			t.Fatalf("%s destructive operation has no canonical confirmation", presentation.Operation)
		}
	}
}

func TestLifecycleNavigationAndTimestampVocabularyIsCanonical(t *testing.T) {
	wantStates := []LifecycleState{
		LifecycleIdle,
		LifecycleConfirming,
		LifecycleWorking,
		LifecycleSuccess,
		LifecyclePartial,
		LifecycleRetryableFailure,
		LifecycleTerminalFailure,
		LifecycleUnavailable,
	}
	states := LifecycleStates()
	if len(states) != len(wantStates) {
		t.Fatalf("states=%#v", states)
	}
	for index, state := range states {
		if state.State != wantStates[index] || state.Label == "" {
			t.Fatalf("state[%d]=%#v", index, state)
		}
	}
	for _, intent := range NavigationIntents() {
		if NavigationLabel(intent) == "" {
			t.Fatalf("navigation label missing for %q", intent)
		}
	}
	value := time.Date(2026, time.October, 1, 4, 5, 6, 7, time.FixedZone("ICT", 7*60*60))
	if got := CanonicalTimestamp(value); got != "2026-09-30T21:05:06.000000007Z" {
		t.Fatalf("timestamp=%q", got)
	}
}

func TestGapPresentationCarriesUnavailableReason(t *testing.T) {
	descriptor := Gap(capability.SurfaceCLI, capability.StatusOverview, "runtime inventory unavailable")
	if descriptor.Presentation.UnavailableReason != descriptor.Gap {
		t.Fatalf("presentation=%#v gap=%q", descriptor.Presentation, descriptor.Gap)
	}
	if err := ValidateDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedBrowserPresentationContractIsCurrent(t *testing.T) {
	want, err := GeneratedTypeScript()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../frontend/src/lib/operation-presentation.generated.ts")
	if err != nil {
		t.Fatal(err)
	}
	got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
	if string(got) != string(want) {
		t.Fatal("generated browser presentation contract is stale; run go generate ./internal/productadapter")
	}
}
