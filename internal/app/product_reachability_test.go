package app_test

import (
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/cli"
	tui "go.mewis.me/codemcp/internal/interface/tui"
	"go.mewis.me/codemcp/internal/productadapter"
	"go.mewis.me/codemcp/internal/telegram"
)

func TestGoProductSurfaceDescriptorsClassifyEveryRequiredHumanOperation(t *testing.T) {
	tests := []struct {
		surface     capability.Surface
		descriptors []productadapter.Descriptor
	}{
		{surface: capability.SurfaceCLI, descriptors: cli.ProductReachabilityDescriptors()},
		{surface: capability.SurfaceTUI, descriptors: tui.ProductReachabilityDescriptors()},
		{surface: capability.SurfaceTelegram, descriptors: telegram.ProductReachabilityDescriptors()},
	}
	for _, test := range tests {
		t.Run(string(test.surface), func(t *testing.T) {
			if err := productadapter.Validate(test.descriptors); err != nil {
				t.Fatal(err)
			}
			required := requiredHumanOperations(test.surface)
			if len(test.descriptors) != len(required) {
				t.Fatalf("descriptors=%d required-human=%d", len(test.descriptors), len(required))
			}
			seen := map[capability.ID]bool{}
			for _, descriptor := range test.descriptors {
				if descriptor.Surface != test.surface {
					t.Fatalf("%s descriptor uses surface %s", descriptor.Operation, descriptor.Surface)
				}
				if seen[descriptor.Operation] {
					t.Fatalf("duplicate descriptor for %s", descriptor.Operation)
				}
				seen[descriptor.Operation] = true
				if descriptor.State != productadapter.StateLive && descriptor.State != productadapter.StateGap {
					t.Fatalf("%s has unclassified adapter state %q", descriptor.Operation, descriptor.State)
				}
			}
			for _, operation := range required {
				if !seen[operation] {
					t.Fatalf("required operation %s is unclassified", operation)
				}
			}
		})
	}
}

func TestProductSurfaceDescriptorsPreserveOneCanonicalMutationOwner(t *testing.T) {
	owners := map[capability.ID]string{}
	for _, descriptors := range [][]productadapter.Descriptor{
		cli.ProductReachabilityDescriptors(),
		tui.ProductReachabilityDescriptors(),
		telegram.ProductReachabilityDescriptors(),
	} {
		for _, descriptor := range descriptors {
			spec, ok := capability.Lookup(descriptor.Operation)
			if !ok || spec.Kind != capability.KindMutation {
				continue
			}
			want, ok := capability.CanonicalOwnerFor(descriptor.Operation)
			if !ok || descriptor.CanonicalOwner != want {
				t.Fatalf("%s owner=%q want=%q", descriptor.Operation, descriptor.CanonicalOwner, want)
			}
			if current, exists := owners[descriptor.Operation]; exists && current != descriptor.CanonicalOwner {
				t.Fatalf("%s surface owners diverged: %q != %q", descriptor.Operation, current, descriptor.CanonicalOwner)
			}
			owners[descriptor.Operation] = descriptor.CanonicalOwner
		}
	}
}

func requiredHumanOperations(surface capability.Surface) []capability.ID {
	var out []capability.ID
	for _, operation := range capability.RequiredOperations(surface) {
		spec, ok := capability.Lookup(operation)
		if ok && (spec.Audience == capability.AudienceOperator || spec.Audience == capability.AudienceReviewer) {
			out = append(out, operation)
		}
	}
	return out
}
