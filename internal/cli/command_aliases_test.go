package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/capability"
)

func TestCommandAliasesComeOnlyFromRegistry(t *testing.T) {
	root := newRootCommand()
	var walk func(*cobra.Command, string)
	walk = func(parent *cobra.Command, parentPath string) {
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			want := commandAliases(parentPath, child.Name())
			if !reflect.DeepEqual(child.Aliases, want) {
				t.Errorf("%q aliases=%v want registry aliases=%v", child.CommandPath(), child.Aliases, want)
			}
			childPath := canonicalAliasPath(strings.TrimSpace(parentPath + " " + child.Name()))
			walk(child, childPath)
		}
	}
	walk(root, "")
}

func TestCompactRootAliasesResolveCanonicalCommands(t *testing.T) {
	root := newRootCommand()
	for _, test := range []struct {
		alias     []string
		canonical []string
	}{
		{alias: []string{"cfg", "ls"}, canonical: []string{"config", "list"}},
		{alias: []string{"ws", "ls"}, canonical: []string{"workspace", "list"}},
		{alias: []string{"tg", "token", "st"}, canonical: []string{"telegram", "token", "status"}},
		{alias: []string{"ups", "server", "ls"}, canonical: []string{"upstream", "server", "list"}},
		{alias: []string{"tel", "st"}, canonical: []string{"telemetry", "status"}},
		{alias: []string{"tel", "show"}, canonical: []string{"telemetry", "show"}},
		{alias: []string{"tel", "enable"}, canonical: []string{"telemetry", "enable"}},
	} {
		aliasCommand, aliasRemaining, aliasErr := root.Find(test.alias)
		canonicalCommand, canonicalRemaining, canonicalErr := root.Find(test.canonical)
		if aliasErr != nil || canonicalErr != nil || len(aliasRemaining) != 0 || len(canonicalRemaining) != 0 {
			t.Fatalf("alias=%v canonical=%v aliasRemaining=%v canonicalRemaining=%v aliasErr=%v canonicalErr=%v", test.alias, test.canonical, aliasRemaining, canonicalRemaining, aliasErr, canonicalErr)
		}
		if aliasCommand != canonicalCommand {
			t.Fatalf("alias %q resolved to %q; canonical %q resolved to %q", strings.Join(test.alias, " "), aliasCommand.CommandPath(), strings.Join(test.canonical, " "), canonicalCommand.CommandPath())
		}
		aliasOperation, aliasMapped := canonicalCommandOperation(aliasCommand)
		canonicalOperation, canonicalMapped := canonicalCommandOperation(canonicalCommand)
		if aliasMapped != canonicalMapped || aliasOperation != canonicalOperation {
			t.Fatalf("alias %q operation=%q,%t canonical=%q,%t", strings.Join(test.alias, " "), aliasOperation, aliasMapped, canonicalOperation, canonicalMapped)
		}
		if operation, ok := capability.ForPath(strings.Join(test.alias, " ")); ok {
			t.Fatalf("alias path %q unexpectedly owns separate capability %q", strings.Join(test.alias, " "), operation)
		}
	}
}

func TestCanonicalizeCommandArgsLeftToRightUntilBoundary(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "nested aliases", args: []string{"tg", "token", "st"}, want: []string{"telegram", "token", "status"}},
		{name: "reused status alias at root", args: []string{"st"}, want: []string{"status"}},
		{name: "reused status alias in scope", args: []string{"tunnel", "st"}, want: []string{"tunnel", "status"}},
		{name: "reused list alias in deep scope", args: []string{"ups", "server", "ls"}, want: []string{"upstream", "server", "list"}},
		{name: "stop at positional argument", args: []string{"ups", "server", "info", "github", "st"}, want: []string{"upstream", "server", "show", "github", "st"}},
		{name: "stop at flag", args: []string{"ws", "--json", "ls"}, want: []string{"workspace", "--json", "ls"}},
		{name: "stop at double dash", args: []string{"cfg", "--", "ls"}, want: []string{"config", "--", "ls"}},
		{name: "unknown token is argument boundary", args: []string{"workspace", "show", "ws_demo", "ls"}, want: []string{"workspace", "show", "ws_demo", "ls"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := append([]string(nil), test.args...)
			if got := canonicalizeCommandArgs(newRootCommand(), test.args); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("canonicalizeCommandArgs(%v)=%v want=%v", test.args, got, test.want)
			}
			if !reflect.DeepEqual(test.args, original) {
				t.Fatalf("canonicalizeCommandArgs mutated input: got=%v want=%v", test.args, original)
			}
		})
	}
}

func TestApplyCommandAliasesRejectsAmbiguityDeterministically(t *testing.T) {
	root := &cobra.Command{Use: "cm"}
	root.AddCommand(&cobra.Command{Use: "one"}, &cobra.Command{Use: "two"})
	registry := map[string][]commandAliasDefinition{
		"": {
			{Command: "one", Aliases: []string{"x"}},
			{Command: "two", Aliases: []string{"x"}},
		},
	}
	err := applyCommandAliases(root, registry)
	if err == nil {
		t.Fatal("expected ambiguous alias registry to fail")
	}
	want := "command alias \"x\" under \"<root>\" is ambiguous between \"one\" and \"two\""
	if err.Error() != want {
		t.Fatalf("error=%q want=%q", err, want)
	}
}

func TestAliasRegistryParentOrderIsDeterministic(t *testing.T) {
	root := &cobra.Command{Use: "cm"}
	err := applyCommandAliases(root, map[string][]commandAliasDefinition{
		"z": {{Command: "one", Aliases: []string{"x"}}},
		"a": {{Command: "one", Aliases: []string{"x"}}},
	})
	if err == nil || err.Error() != "command alias parent \"a\" does not exist" {
		t.Fatalf("error=%v", err)
	}
}
