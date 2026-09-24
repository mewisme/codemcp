package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/capability"
)

const canonicalOperationAnnotation = "cm.operation_id"

func bindCanonicalCommandOperations(root *cobra.Command) {
	if root == nil {
		return
	}
	var walk func(*cobra.Command, []string)
	walk = func(command *cobra.Command, prefix []string) {
		path := prefix
		if command != root {
			path = append(append([]string(nil), prefix...), command.Name())
		}
		if command.Runnable() {
			key := capability.RootPath
			if command != root {
				key = capability.NormalizePath(strings.Join(path, " "))
			}
			if id, ok := capability.ForPath(key); ok {
				if command.Annotations == nil {
					command.Annotations = map[string]string{}
				}
				command.Annotations[canonicalOperationAnnotation] = string(id)
			}
		}
		for _, child := range command.Commands() {
			walk(child, path)
		}
	}
	walk(root, nil)
}

func canonicalCommandOperation(command *cobra.Command) (capability.ID, bool) {
	if command == nil || command.Annotations == nil {
		return "", false
	}
	id := capability.ID(strings.TrimSpace(command.Annotations[canonicalOperationAnnotation]))
	if id == "" {
		return "", false
	}
	if _, ok := capability.Lookup(id); !ok {
		return "", false
	}
	return id, true
}
