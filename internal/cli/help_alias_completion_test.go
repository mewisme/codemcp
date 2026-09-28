package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

func TestEveryPublicCommandHelpFormIsSymmetricAndSideEffectFree(t *testing.T) {
	for _, path := range publicCommandPaths(newRootCommand()) {
		name := strings.Join(path, " ")
		if name == "" {
			name = "<root>"
		}
		t.Run(name, func(t *testing.T) {
			longArgs := append(append([]string(nil), path...), "--help")
			shortArgs := append(append([]string(nil), path...), "-h")
			commandArgs := append([]string{"help"}, path...)

			longHelp := renderSideEffectFreeHelp(t, longArgs)
			shortHelp := renderSideEffectFreeHelp(t, shortArgs)
			commandHelp := renderSideEffectFreeHelp(t, commandArgs)

			if longHelp != shortHelp || longHelp != commandHelp {
				t.Fatalf("help forms diverged for %q\n--help=%q\n-h=%q\nhelp=%q", name, longHelp, shortHelp, commandHelp)
			}
			if !strings.Contains(longHelp, "Usage:") || !strings.Contains(longHelp, "Flags:") {
				t.Fatalf("help missing Cobra sections for %q:\n%s", name, longHelp)
			}
		})
	}
}

func TestAliasHelpResolvesCanonicalCommandAndShowsAliasMetadata(t *testing.T) {
	tests := []struct {
		canonical []string
		alias     []string
		metadata  string
		usage     string
	}{
		{
			canonical: []string{"config", "list"},
			alias:     []string{"cfg", "ls"},
			metadata:  "Aliases:\n  list, ls",
			usage:     "cm config list",
		},
		{
			canonical: []string{"upstream", "server", "remove"},
			alias:     []string{"ups", "server", "rm"},
			metadata:  "Aliases:\n  remove, rm",
			usage:     "cm upstream server remove",
		},
		{
			canonical: []string{"telegram", "token", "status"},
			alias:     []string{"tg", "token", "st"},
			metadata:  "Aliases:\n  status, st",
			usage:     "cm telegram token status",
		},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.alias, " "), func(t *testing.T) {
			canonical := renderSideEffectFreeHelp(t, append(append([]string(nil), test.canonical...), "--help"))
			for _, args := range [][]string{
				append(append([]string(nil), test.alias...), "--help"),
				append(append([]string(nil), test.alias...), "-h"),
				append([]string{"help"}, test.alias...),
			} {
				got := renderSideEffectFreeHelp(t, args)
				if got != canonical {
					t.Fatalf("alias help %v diverged from canonical\nwant=%q\ngot =%q", args, canonical, got)
				}
			}
			if !strings.Contains(canonical, test.metadata) {
				t.Fatalf("canonical-first alias metadata missing %q:\n%s", test.metadata, canonical)
			}
			if !strings.Contains(canonical, test.usage) {
				t.Fatalf("canonical usage missing %q:\n%s", test.usage, canonical)
			}
		})
	}
}

func TestAliasCompletionHooksExposeAliasesWithoutDuplicateCommands(t *testing.T) {
	root := newRootCommand()
	assertAliasCompletions(t, root, nil, "", "cfg", "config")
	assertAliasCompletions(t, mustFindCommand(t, root, "cfg"), nil, "", "ls", "list")
	assertAliasCompletions(t, mustFindCommand(t, root, "ups", "server"), nil, "", "rm", "remove")
	assertAliasCompletions(t, mustFindCommand(t, root, "tg", "token"), nil, "", "st", "status")

	root.InitDefaultHelpCmd()
	help := mustFindCommand(t, root, "help")
	if help.ValidArgsFunction == nil {
		t.Fatal("default help command has no completion function")
	}
	rootHelp, directive := help.ValidArgsFunction(help, nil, "")
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 || !hasCompletion(rootHelp, "config") || !hasCompletion(rootHelp, "cfg") {
		t.Fatalf("root help completions=%#v directive=%v", rootHelp, directive)
	}
	deepHelp, directive := help.ValidArgsFunction(help, []string{"ups", "server"}, "")
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 || !hasCompletion(deepHelp, "remove") || !hasCompletion(deepHelp, "rm") {
		t.Fatalf("deep help completions=%#v directive=%v", deepHelp, directive)
	}
}

func TestCobraCompletionEndpointIncludesAliasesForCanonicalAndAliasParents(t *testing.T) {
	root := requestCobraCompletions(t, "")
	if !hasCompletionLine(root, "config") || !hasCompletionLine(root, "cfg") {
		t.Fatalf("root completion missing canonical/alias entries:\n%s", root)
	}

	config := requestCobraCompletions(t, "cfg", "")
	if !hasCompletionLine(config, "list") || !hasCompletionLine(config, "ls") {
		t.Fatalf("alias-parent completion missing canonical/alias entries:\n%s", config)
	}

	upstream := requestCobraCompletions(t, "ups", "server", "")
	if !hasCompletionLine(upstream, "remove") || !hasCompletionLine(upstream, "rm") {
		t.Fatalf("deep alias-parent completion missing canonical/alias entries:\n%s", upstream)
	}
}

func TestHelpOnlyExecuteLifecycleSkipsProductUsage(t *testing.T) {
	previous := newCLIProductUsageRecorder
	defer func() { newCLIProductUsageRecorder = previous }()

	factoryCalls := 0
	recorder := &helpUsageRecorder{}
	newCLIProductUsageRecorder = func(config.Config, bool) (cliProductUsageRecorder, error) {
		factoryCalls++
		return recorder, nil
	}

	for _, args := range [][]string{
		{"cfg", "ls", "--help"},
		{"cfg", "ls", "-h"},
		{"help", "cfg", "ls"},
	} {
		root := newRootCommand()
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs(args)
		if err := executeCommand(root); err != nil {
			t.Fatalf("args=%v: %v", args, err)
		}
	}
	if factoryCalls != 0 || recorder.records != 0 {
		t.Fatalf("help-only execution recorded product usage: factory=%d records=%d", factoryCalls, recorder.records)
	}
}

func TestRemovedLegacyPathsStayAbsentFromHelp(t *testing.T) {
	rootHelp := renderSideEffectFreeHelp(t, []string{"--help"})
	for _, legacy := range []string{"chatgpt-mcp", "cgm", "cmcp"} {
		if strings.Contains(rootHelp, legacy) {
			t.Fatalf("root help exposes legacy executable %q:\n%s", legacy, rootHelp)
		}
	}

	mcpHelp := renderSideEffectFreeHelp(t, []string{"mcp", "--help"})
	if strings.Contains(mcpHelp, "\n  server ") || strings.Contains(mcpHelp, "\n  server\t") {
		t.Fatalf("mcp help exposes removed server path:\n%s", mcpHelp)
	}
	if command, remaining, err := newRootCommand().Find([]string{"mcp", "server"}); err == nil && command != nil && len(remaining) == 0 {
		t.Fatalf("removed mcp server path still resolves to %q", command.CommandPath())
	}
}

func renderSideEffectFreeHelp(t *testing.T, args []string) string {
	t.Helper()
	root := newRootCommand()
	var output bytes.Buffer
	preRuns := 0
	postRuns := 0
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		preRuns++
		return nil
	}
	root.PersistentPostRun = func(*cobra.Command, []string) {
		postRuns++
	}
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("args=%v: %v", args, err)
	}
	if preRuns != 0 || postRuns != 0 {
		t.Fatalf("help executed command lifecycle for %v: pre=%d post=%d", args, preRuns, postRuns)
	}
	return output.String()
}

func publicCommandPaths(root *cobra.Command) [][]string {
	paths := [][]string{nil}
	var walk func(*cobra.Command, []string)
	walk = func(parent *cobra.Command, prefix []string) {
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			path := append(append([]string(nil), prefix...), child.Name())
			paths = append(paths, path)
			walk(child, path)
		}
	}
	walk(root, nil)
	return paths
}

func mustFindCommand(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	command, remaining, err := root.Find(path)
	if err != nil || command == nil || len(remaining) != 0 {
		t.Fatalf("find %v: command=%v remaining=%v err=%v", path, command, remaining, err)
	}
	return command
}

func assertAliasCompletions(t *testing.T, command *cobra.Command, args []string, toComplete, alias, canonical string) {
	t.Helper()
	if command.ValidArgsFunction == nil {
		t.Fatalf("%q has no alias completion function", command.CommandPath())
	}
	values, directive := command.ValidArgsFunction(command, args, toComplete)
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Fatalf("%q completion directive=%v", command.CommandPath(), directive)
	}
	if !hasCompletion(values, alias) {
		t.Fatalf("%q completion missing alias %q: %#v", command.CommandPath(), alias, values)
	}
	for _, value := range values {
		token, description, _ := strings.Cut(value, "\t")
		if token == alias && !strings.Contains(description, "Alias for "+canonical) {
			t.Fatalf("alias completion %q lacks canonical metadata: %q", alias, value)
		}
	}
}

func requestCobraCompletions(t *testing.T, words ...string) string {
	t.Helper()
	root := newRootCommand()
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&diagnostics)
	root.SetArgs(append([]string{cobra.ShellCompRequestCmd}, words...))
	if err := root.Execute(); err != nil {
		t.Fatalf("completion %v: %v\nstderr=%s", words, err, diagnostics.String())
	}
	return output.String()
}

func hasCompletionLine(output, token string) bool {
	for _, line := range strings.Split(output, "\n") {
		candidate, _, _ := strings.Cut(line, "\t")
		if candidate == token {
			return true
		}
	}
	return false
}

type helpUsageRecorder struct {
	records int
}

func (recorder *helpUsageRecorder) Record(context.Context, producttelemetry.EventName, producttelemetry.Usage) bool {
	recorder.records++
	return true
}

func (*helpUsageRecorder) Close(context.Context) {}
