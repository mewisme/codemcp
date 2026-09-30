package tui

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/productadapter"
)

func TestTUIProductReachabilityDescriptorsResolveLiveActions(t *testing.T) {
	descriptors := ProductReachabilityDescriptors()
	if err := productadapter.Validate(descriptors); err != nil {
		t.Fatal(err)
	}
	registry := defaultActionRegistry()
	byOperation := map[capability.ID]productadapter.Descriptor{}
	for _, descriptor := range descriptors {
		byOperation[descriptor.Operation] = descriptor
	}
	for _, descriptor := range descriptors {
		if descriptor.State != productadapter.StateLive {
			continue
		}
		resolvedAction := false
		for _, entry := range descriptor.Discovery {
			if entry.Kind != productadapter.EntryAction {
				continue
			}
			item, ok := registry.Get(entry.Value)
			if !ok || item.Run == nil {
				t.Fatalf("%s references missing TUI action %q", descriptor.Operation, entry.Value)
			}
			owns := item.Operation == descriptor.Operation
			if !owns {
				spec, _ := capability.Lookup(descriptor.Operation)
				if spec.Kind == capability.KindMutation {
					t.Fatalf("mutation %s is only claimed by navigation action %s", descriptor.Operation, item.ID)
				}
				for _, id := range item.Capabilities {
					if id == descriptor.Operation {
						owns = true
						break
					}
				}
			}
			if owns {
				resolvedAction = true
			}
		}
		if !resolvedAction {
			t.Fatalf("%s has no production action evidence: %#v", descriptor.Operation, descriptor)
		}
		if descriptor.Parent != "" && byOperation[descriptor.Parent].State == productadapter.StateGap {
			t.Fatalf("%s is live while parent %s is a gap", descriptor.Operation, descriptor.Parent)
		}
	}
}

func TestTUIRequiredButUnimplementedAdaptersRemainGaps(t *testing.T) {
	descriptors := ProductReachabilityDescriptors()
	if productadapter.CountGaps(descriptors) == 0 {
		t.Fatal("expected remaining TUI semantic gaps before interface closure")
	}
	for _, descriptor := range descriptors {
		if descriptor.State == productadapter.StateGap && strings.TrimSpace(descriptor.Gap) == "" {
			t.Fatalf("unclassified TUI gap: %#v", descriptor)
		}
	}
}
