package telegram

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/productadapter"
)

func TestTelegramProductReachabilityDescriptorsResolveRegisteredNavigation(t *testing.T) {
	descriptors := ProductReachabilityDescriptors()
	if err := productadapter.Validate(descriptors); err != nil {
		t.Fatal(err)
	}
	commands := map[string]Route{}
	for _, command := range Commands() {
		commands["/"+command.Name] = command.Route
	}
	byOperation := map[capability.ID]productadapter.Descriptor{}
	for _, descriptor := range descriptors {
		byOperation[descriptor.Operation] = descriptor
	}
	for _, descriptor := range descriptors {
		if descriptor.State != productadapter.StateLive {
			continue
		}
		if !capability.TelegramAdapterOperationLive(descriptor.Operation) {
			t.Fatalf("%s descriptor is live without production Telegram dispatch allowance", descriptor.Operation)
		}
		discoverable := false
		for _, entry := range descriptor.Discovery {
			switch entry.Kind {
			case productadapter.EntryCommand:
				if _, ok := commands[entry.Value]; !ok {
					t.Fatalf("%s references unregistered Telegram command %q", descriptor.Operation, entry.Value)
				}
				discoverable = true
			case productadapter.EntryRoute, productadapter.EntryInput, productadapter.EntryMiniAppRead:
				discoverable = true
			}
		}
		if !discoverable {
			t.Fatalf("%s has no registered Telegram discovery evidence", descriptor.Operation)
		}
		spec, _ := capability.Lookup(descriptor.Operation)
		if spec.Effects.Destructive && !requiresExplicitConfirmation(spec) {
			t.Fatalf("%s destructive Telegram adapter does not consume canonical confirmation", descriptor.Operation)
		}
		for _, entry := range descriptor.Dispatch {
			if entry.Kind == productadapter.EntryMiniAppRead && spec.Kind == capability.KindMutation {
				t.Fatalf("%s mutation incorrectly uses Mini App mutation path", descriptor.Operation)
			}
		}
		if descriptor.Parent != "" && byOperation[descriptor.Parent].State != productadapter.StateLive {
			t.Fatalf("%s is live while parent %s is not live", descriptor.Operation, descriptor.Parent)
		}
	}
}

func TestTelegramManagedSecretDescriptorUsesProtectedInput(t *testing.T) {
	for _, descriptor := range ProductReachabilityDescriptors() {
		if descriptor.Operation != capability.LLMProviderCredentialSet {
			continue
		}
		if descriptor.State != productadapter.StateLive || descriptor.SecretPolicy != productadapter.SecretPolicyProtectedInput {
			t.Fatalf("LLM credential descriptor=%#v", descriptor)
		}
		for _, entry := range descriptor.Discovery {
			if entry.Kind == productadapter.EntryInput && entry.Value == inputLLMCredentialSet {
				return
			}
		}
		t.Fatalf("LLM credential descriptor lacks ForceReply/input evidence: %#v", descriptor)
	}
	t.Fatal("LLM credential descriptor missing")
}

func TestTelegramRequiredAdaptersAreProductionReachable(t *testing.T) {
	descriptors := ProductReachabilityDescriptors()
	if gaps := productadapter.CountGaps(descriptors); gaps != 0 {
		for _, descriptor := range descriptors {
			if descriptor.State == productadapter.StateGap {
				t.Errorf("Telegram operation %s remains unreachable: %s", descriptor.Operation, strings.TrimSpace(descriptor.Gap))
			}
		}
		t.Fatalf("remaining Telegram semantic gaps=%d", gaps)
	}
}
