package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/commandalias"
)

type commandAliasDefinition = commandalias.Definition

func bindCommandAliases(root *cobra.Command) {
	if err := applyCommandAliases(root, commandalias.Registry()); err != nil {
		panic(err)
	}
}

func applyCommandAliases(root *cobra.Command, registry map[string][]commandAliasDefinition) error {
	if root == nil {
		return fmt.Errorf("command alias registry requires a root command")
	}
	parents := make([]string, 0, len(registry))
	for parent := range registry {
		parents = append(parents, canonicalAliasPath(parent))
	}
	sort.Strings(parents)

	for _, parentPath := range parents {
		parent := findCanonicalCommand(root, parentPath)
		if parent == nil {
			return fmt.Errorf("command alias parent %q does not exist", displayAliasPath(parentPath))
		}
		definitions := registry[parentPath]
		owners := map[string]string{}
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			owners[child.Name()] = child.Name()
		}
		for _, definition := range definitions {
			commandName := strings.TrimSpace(definition.Command)
			child := findDirectCanonicalChild(parent, commandName)
			if child == nil {
				return fmt.Errorf("command alias target %q under %q does not exist", commandName, displayAliasPath(parentPath))
			}
			if len(definition.Aliases) == 0 {
				return fmt.Errorf("command alias target %q under %q has no aliases", commandName, displayAliasPath(parentPath))
			}
			for _, alias := range definition.Aliases {
				alias = strings.TrimSpace(alias)
				if alias == "" {
					return fmt.Errorf("command alias target %q under %q has an empty alias", commandName, displayAliasPath(parentPath))
				}
				if previous, exists := owners[alias]; exists && previous != commandName {
					return fmt.Errorf("command alias %q under %q is ambiguous between %q and %q", alias, displayAliasPath(parentPath), previous, commandName)
				}
				owners[alias] = commandName
			}
		}
		for _, definition := range definitions {
			child := findDirectCanonicalChild(parent, definition.Command)
			child.Aliases = append([]string(nil), definition.Aliases...)
		}
	}
	return nil
}

func canonicalizeCommandArgs(_ *cobra.Command, args []string) []string {
	return commandalias.Canonicalize(args)
}

func commandAliases(parentPath, command string) []string {
	return commandalias.Aliases(parentPath, command)
}

func findCanonicalCommand(root *cobra.Command, path string) *cobra.Command {
	if root == nil {
		return nil
	}
	path = canonicalAliasPath(path)
	if path == "" {
		return root
	}
	current := root
	for _, token := range strings.Fields(path) {
		current = findDirectCanonicalChild(current, token)
		if current == nil {
			return nil
		}
	}
	return current
}

func findDirectCanonicalChild(parent *cobra.Command, name string) *cobra.Command {
	if parent == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	for _, child := range parent.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func canonicalAliasPath(path string) string {
	return strings.Join(strings.Fields(path), " ")
}

func displayAliasPath(path string) string {
	if path = canonicalAliasPath(path); path != "" {
		return path
	}
	return "<root>"
}
