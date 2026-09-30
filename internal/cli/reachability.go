package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/productadapter"
)

func ProductReachabilityDescriptors() []productadapter.Descriptor {
	root := newRootCommand()
	live := make([]productadapter.Descriptor, 0)
	var walk func(*cobra.Command, []string, bool)
	walk = func(command *cobra.Command, prefix []string, hiddenAncestor bool) {
		hidden := hiddenAncestor || command.Hidden
		path := prefix
		if command != root {
			path = append(append([]string(nil), prefix...), command.Name())
		}
		if command.Runnable() && !hidden {
			if operation, ok := canonicalCommandOperation(command); ok {
				if descriptor, ok := cliReachabilityDescriptor(command, operation, path, root); ok {
					live = append(live, descriptor)
				}
			}
		}
		for _, child := range command.Commands() {
			walk(child, path, hidden)
		}
	}
	walk(root, nil, false)
	return productadapter.Complete(capability.SurfaceCLI, live)
}

func cliReachabilityDescriptor(command *cobra.Command, operation capability.ID, path []string, root *cobra.Command) (productadapter.Descriptor, bool) {
	spec, ok := capability.Lookup(operation)
	if !ok || (spec.Audience != capability.AudienceOperator && spec.Audience != capability.AudienceReviewer) {
		return productadapter.Descriptor{}, false
	}
	commandPath := capability.RootPath
	if command != root {
		commandPath = capability.NormalizePath(strings.Join(path, " "))
	}
	discovery := []productadapter.EntryPoint{{Kind: productadapter.EntryCommand, Value: commandPath}}
	for _, alias := range spec.CLI.Aliases {
		alias = capability.NormalizePath(alias)
		if alias != "" {
			discovery = append(discovery, productadapter.EntryPoint{Kind: productadapter.EntryCommand, Value: alias})
		}
	}
	descriptor := productadapter.Live(
		capability.SurfaceCLI,
		operation,
		discovery,
		[]productadapter.EntryPoint{
			{Kind: productadapter.EntryDispatch, Value: canonicalOperationAnnotation + "=" + string(operation)},
			{Kind: productadapter.EntryCommand, Value: commandPath},
		},
	)
	descriptor.Parent = productadapter.DefaultParent(operation)
	descriptor.ConfirmationConsumed = cliConsumesConfirmation(command, spec)
	if spec.Effects.Destructive && spec.Confirmation.Mode != capability.ConfirmationNone && !descriptor.ConfirmationConsumed {
		return productadapter.Descriptor{}, false
	}
	return descriptor, true
}

func cliConsumesConfirmation(command *cobra.Command, spec capability.Spec) bool {
	if !spec.Effects.Destructive || spec.Confirmation.Mode == capability.ConfirmationNone {
		return false
	}
	switch spec.Confirmation.Mode {
	case capability.ConfirmationRequired:
		for _, name := range []string{"yes", "confirm"} {
			if command.Flags().Lookup(name) != nil {
				return true
			}
		}
		return false
	case capability.ConfirmationRecommended, capability.ConfirmationReview:
		return true
	default:
		return false
	}
}
