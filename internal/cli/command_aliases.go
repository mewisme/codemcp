package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type commandAliasDefinition struct {
	Command string
	Aliases []string
}

var commandAliasRegistry = map[string][]commandAliasDefinition{
	"": {
		{Command: "config", Aliases: []string{"cfg"}},
		{Command: "install", Aliases: []string{"ins"}},
		{Command: "logs", Aliases: []string{"log"}},
		{Command: "request", Aliases: []string{"req"}},
		{Command: "status", Aliases: []string{"st"}},
		{Command: "telegram", Aliases: []string{"tg"}},
		{Command: "telemetry", Aliases: []string{"tel"}},
		{Command: "upgrade", Aliases: []string{"update", "upg"}},
		{Command: "upstream", Aliases: []string{"ups"}},
		{Command: "workspace", Aliases: []string{"ws"}},
	},
	"agent completion": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"show", "info"}},
	},
	"auth": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"config": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "verify", Aliases: []string{"validate"}},
	},
	"execution": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"info"}},
	},
	"integration": {
		{Command: "cf", Aliases: []string{"cf-tunnel"}},
	},
	"integration cf": {
		{Command: "remove", Aliases: []string{"rm"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"integration codegraph": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"integration rtk": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"integration typesafe": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"process": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"info"}},
	},
	"prompt": {
		{Command: "delete", Aliases: []string{"rm"}},
		{Command: "list", Aliases: []string{"ls"}},
	},
	"request": {
		{Command: "approve", Aliases: []string{"accept", "allow"}},
		{Command: "deny", Aliases: []string{"reject"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"show", "info"}},
	},
	"request grant": {
		{Command: "list", Aliases: []string{"ls"}},
	},
	"telegram token": {
		{Command: "remove", Aliases: []string{"clear", "rm"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"telemetry": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"tools": {
		{Command: "list", Aliases: []string{"ls"}},
	},
	"tunnel": {
		{Command: "get", Aliases: []string{"info"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "status", Aliases: []string{"st"}},
		{Command: "use", Aliases: []string{"select", "switch"}},
	},
	"tunnel admin key": {
		{Command: "remove", Aliases: []string{"rm"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"tunnel key": {
		{Command: "remove", Aliases: []string{"rm"}},
	},
	"upstream server": {
		{Command: "configure", Aliases: []string{"set"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "remove", Aliases: []string{"rm"}},
		{Command: "show", Aliases: []string{"info"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"upstream server auth": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"workspace": {
		{Command: "container", Aliases: []string{"ctr"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "relocate", Aliases: []string{"move"}},
		{Command: "show", Aliases: []string{"info"}},
	},
	"workspace access": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "remove", Aliases: []string{"rm"}},
	},
	"workspace container": {
		{Command: "delete", Aliases: []string{"rm"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "show", Aliases: []string{"info"}},
	},
}

func bindCommandAliases(root *cobra.Command) {
	if err := applyCommandAliases(root, commandAliasRegistry); err != nil {
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

func canonicalizeCommandArgs(root *cobra.Command, args []string) []string {
	canonical := append([]string(nil), args...)
	if root == nil {
		return canonical
	}
	parent := ""
	current := root
	for index, token := range canonical {
		if strings.HasPrefix(token, "-") {
			break
		}
		command := strings.TrimSpace(token)
		child := findDirectCanonicalChild(current, command)
		if child == nil {
			var ok bool
			command, ok = canonicalAliasToken(parent, token)
			if !ok {
				break
			}
			child = findDirectCanonicalChild(current, command)
			if child == nil {
				break
			}
		}
		canonical[index] = command
		parent = canonicalAliasPath(strings.TrimSpace(parent + " " + command))
		current = child
	}
	return canonical
}

func canonicalAliasToken(parentPath, token string) (string, bool) {
	parentPath = canonicalAliasPath(parentPath)
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	for _, definition := range commandAliasRegistry[parentPath] {
		if definition.Command == token {
			return definition.Command, true
		}
		for _, alias := range definition.Aliases {
			if alias == token {
				return definition.Command, true
			}
		}
	}
	return "", false
}

func commandAliases(parentPath, command string) []string {
	parentPath = canonicalAliasPath(parentPath)
	command = strings.TrimSpace(command)
	for _, definition := range commandAliasRegistry[parentPath] {
		if definition.Command == command {
			return append([]string(nil), definition.Aliases...)
		}
	}
	return nil
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
