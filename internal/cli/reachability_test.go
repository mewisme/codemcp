package cli

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/productadapter"
)

func TestCLIProductReachabilityDescriptorsResolveProductionCommands(t *testing.T) {
	descriptors := ProductReachabilityDescriptors()
	if err := productadapter.Validate(descriptors); err != nil {
		t.Fatal(err)
	}
	root := newRootCommand()
	for _, descriptor := range descriptors {
		if descriptor.State != productadapter.StateLive {
			continue
		}
		spec, _ := capability.Lookup(descriptor.Operation)
		canonical := capability.NormalizePath(spec.CLI.CanonicalPath)
		if canonical == "" {
			t.Fatalf("live CLI descriptor %s has no canonical path", descriptor.Operation)
		}
		command, remaining, err := root.Find(strings.Fields(canonical))
		if err != nil || len(remaining) != 0 || command == nil || !command.Runnable() || command.Hidden {
			t.Fatalf("%s canonical command %q is not production reachable: command=%v remaining=%v err=%v", descriptor.Operation, canonical, command, remaining, err)
		}
		if operation, ok := canonicalCommandOperation(command); !ok || operation != descriptor.Operation {
			t.Fatalf("%s canonical annotation=%s,%t", canonical, operation, ok)
		}
		for _, alias := range spec.CLI.Aliases {
			var resolved = root
			var remaining []string
			var err error
			if capability.NormalizePath(alias) != capability.RootPath {
				resolved, remaining, err = root.Find(strings.Fields(alias))
			}
			if err != nil || len(remaining) != 0 || resolved == nil {
				t.Fatalf("%s alias %q does not resolve: remaining=%v err=%v", descriptor.Operation, alias, remaining, err)
			}
			if operation, ok := canonicalCommandOperation(resolved); !ok || operation != descriptor.Operation {
				t.Fatalf("%s alias %q annotation=%s,%t", descriptor.Operation, alias, operation, ok)
			}
		}
	}
}

func TestCLIRequiredOperationsHaveNoSemanticGaps(t *testing.T) {
	descriptors := ProductReachabilityDescriptors()
	var gaps []string
	for _, descriptor := range descriptors {
		if descriptor.State != productadapter.StateGap {
			continue
		}
		gaps = append(gaps, string(descriptor.Operation)+": "+strings.TrimSpace(descriptor.Gap))
	}
	if len(gaps) != 0 {
		t.Fatalf("CLI semantic gaps remain (%d):\n%s", len(gaps), strings.Join(gaps, "\n"))
	}
}
