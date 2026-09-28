package cli

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/commandalias"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlplane"
)

type aliasContractCase struct {
	Canonical []string
	Alias     []string
	AliasName string
}

func TestEveryRegisteredAliasIsSemanticallyTransparent(t *testing.T) {
	defer configformat.SetRootPath("")
	configRoot := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, configRoot)
	if err := configformat.SetRootPath(configRoot); err != nil {
		t.Fatal(err)
	}

	root := newRootCommand()
	for _, test := range registeredAliasContractCases() {
		name := strings.Join(test.Alias, " ")
		t.Run(name, func(t *testing.T) {
			canonicalCommand := mustFindCommand(t, root, test.Canonical...)
			aliasCommand := mustFindCommand(t, root, test.Alias...)
			if aliasCommand != canonicalCommand {
				t.Fatalf("alias owner=%q canonical owner=%q", aliasCommand.CommandPath(), canonicalCommand.CommandPath())
			}

			canonicalOperation, canonicalMapped := canonicalCommandOperation(canonicalCommand)
			aliasOperation, aliasMapped := canonicalCommandOperation(aliasCommand)
			if aliasMapped != canonicalMapped || aliasOperation != canonicalOperation {
				t.Fatalf("operation mismatch: alias=%q,%t canonical=%q,%t", aliasOperation, aliasMapped, canonicalOperation, canonicalMapped)
			}
			if operation, ok := capability.ForPath(strings.Join(test.Alias, " ")); ok {
				if !canonicalMapped || operation != canonicalOperation {
					t.Fatalf("alias path %q maps to operation %q instead of canonical %q", strings.Join(test.Alias, " "), operation, canonicalOperation)
				}
			}

			assertAliasControlPolicyEqual(t, test.Canonical, test.Alias)

			canonicalHelp := renderSideEffectFreeHelp(t, append(append([]string(nil), test.Canonical...), "--help"))
			aliasHelp := renderSideEffectFreeHelp(t, append(append([]string(nil), test.Alias...), "--help"))
			if aliasHelp != canonicalHelp {
				t.Fatalf("help mismatch\ncanonical=%q\nalias=%q", canonicalHelp, aliasHelp)
			}

			canonicalCompletion := requestCobraCompletions(t, append(append([]string(nil), test.Canonical...), "")...)
			aliasCompletion := requestCobraCompletions(t, append(append([]string(nil), test.Alias...), "")...)
			if aliasCompletion != canonicalCompletion {
				t.Fatalf("completion mismatch\ncanonical=%q\nalias=%q", canonicalCompletion, aliasCompletion)
			}

			parent := append([]string(nil), test.Canonical[:len(test.Canonical)-1]...)
			parentCompletion := requestCobraCompletions(t, append(parent, "")...)
			if !hasCompletionLine(parentCompletion, test.Canonical[len(test.Canonical)-1]) {
				t.Fatalf("parent completion missing canonical token %q: %q", test.Canonical[len(test.Canonical)-1], parentCompletion)
			}
			if !hasCompletionLine(parentCompletion, test.AliasName) {
				t.Fatalf("parent completion missing alias token %q: %q", test.AliasName, parentCompletion)
			}
		})
	}
}

func TestRegisteredAliasPairsCoverCobraAliasesExactly(t *testing.T) {
	root := newRootCommand()
	expected := map[string][]string{}
	for _, test := range registeredAliasContractCases() {
		path := strings.Join(test.Canonical, " ")
		expected[path] = append(expected[path], test.AliasName)
	}
	for path := range expected {
		sort.Strings(expected[path])
	}

	actual := map[string][]string{}
	var walk func(*cobra.Command, []string)
	walk = func(parent *cobra.Command, prefix []string) {
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			path := append(append([]string(nil), prefix...), child.Name())
			if len(child.Aliases) > 0 {
				values := append([]string(nil), child.Aliases...)
				sort.Strings(values)
				actual[strings.Join(path, " ")] = values
			}
			walk(child, path)
		}
	}
	walk(root, nil)

	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Cobra aliases drifted from central registry\nexpected=%#v\nactual=%#v", expected, actual)
	}
}

func TestRepresentativeAliasErrorsHaveSameMainExitContract(t *testing.T) {
	defer configformat.SetRootPath("")
	configRoot := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, configRoot)
	if err := configformat.SetRootPath(configRoot); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		canonical []string
		alias     []string
	}{
		{name: "config arity", canonical: []string{"config", "list", "one", "two"}, alias: []string{"cfg", "ls", "one", "two"}},
		{name: "upstream missing id", canonical: []string{"upstream", "server", "remove"}, alias: []string{"ups", "server", "rm"}},
		{name: "workspace container missing id", canonical: []string{"workspace", "container", "show"}, alias: []string{"ws", "ctr", "info"}},
		{name: "request missing id", canonical: []string{"request", "view"}, alias: []string{"req", "info"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, canonicalErr := executeRequestCommandError(configRoot, test.canonical)
			_, aliasErr := executeRequestCommandError(configRoot, test.alias)
			if canonicalErr == nil || aliasErr == nil {
				t.Fatalf("expected both spellings to fail: canonical=%v alias=%v", canonicalErr, aliasErr)
			}
			if canonicalErr.Error() != aliasErr.Error() {
				t.Fatalf("error mismatch: canonical=%q alias=%q", canonicalErr, aliasErr)
			}
			if commandMainExitCode(canonicalErr) != commandMainExitCode(aliasErr) {
				t.Fatalf("exit-code mismatch: canonical=%d alias=%d", commandMainExitCode(canonicalErr), commandMainExitCode(aliasErr))
			}
		})
	}
}

func TestCMHasNoExecutableAliasRegression(t *testing.T) {
	root := newRootCommand()
	if root.Name() != "cm" || root.Use != "cm" {
		t.Fatalf("root identity name=%q use=%q", root.Name(), root.Use)
	}
	if len(root.Aliases) != 0 {
		t.Fatalf("cm executable unexpectedly has Cobra aliases: %#v", root.Aliases)
	}
	if command := findDirectCanonicalChild(root, "alias"); command != nil {
		t.Fatalf("executable alias management command is public: %q", command.CommandPath())
	}
	install := mustFindCommand(t, root, "install")
	if install.Flags().Lookup("no-alias") != nil {
		t.Fatal("install exposes legacy executable-alias flag --no-alias")
	}
	for _, shell := range completionShells {
		script, err := generateCompletion(root, shell, true)
		if err != nil {
			t.Fatalf("%s completion: %v", shell, err)
		}
		for _, legacy := range []string{"chatgpt-mcp", "cgm", "cmcp"} {
			if strings.Contains(script, legacy) {
				t.Fatalf("%s completion registers legacy executable %q", shell, legacy)
			}
		}
	}
}

func registeredAliasContractCases() []aliasContractCase {
	registry := commandalias.Registry()
	parents := make([]string, 0, len(registry))
	for parent := range registry {
		parents = append(parents, parent)
	}
	sort.Strings(parents)

	cases := make([]aliasContractCase, 0)
	for _, parent := range parents {
		parentPath := strings.Fields(parent)
		definitions := append([]commandalias.Definition(nil), registry[parent]...)
		sort.Slice(definitions, func(i, j int) bool {
			return definitions[i].Command < definitions[j].Command
		})
		for _, definition := range definitions {
			aliases := append([]string(nil), definition.Aliases...)
			sort.Strings(aliases)
			for _, alias := range aliases {
				canonical := append(append([]string(nil), parentPath...), definition.Command)
				aliasPath := append(append([]string(nil), parentPath...), alias)
				cases = append(cases, aliasContractCase{
					Canonical: canonical,
					Alias:     aliasPath,
					AliasName: alias,
				})
			}
		}
	}
	return cases
}

func assertAliasControlPolicyEqual(t *testing.T, canonical, alias []string) {
	t.Helper()
	canonicalPath := controlplane.PathFromArgs(canonical)
	aliasPath := controlplane.PathFromArgs(alias)
	if aliasPath != canonicalPath {
		t.Fatalf("control path mismatch: alias=%q canonical=%q", aliasPath, canonicalPath)
	}
	if controlplane.IsReadOnlyArgs(alias) != controlplane.IsReadOnlyArgs(canonical) {
		t.Fatalf("read-only policy mismatch: alias=%v canonical=%v", alias, canonical)
	}
	if controlplane.ApprovalEligibleArgs(alias) != controlplane.ApprovalEligibleArgs(canonical) {
		t.Fatalf("approval policy mismatch: alias=%v canonical=%v", alias, canonical)
	}
}

func commandMainExitCode(err error) int {
	if err != nil {
		return 1
	}
	return 0
}
