package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func bindAliasHelpCompletion(root *cobra.Command) {
	if root == nil {
		return
	}
	bindAliasSubcommandCompletions(root)
	bindDefaultHelpLifecycle(root)
}

func bindAliasSubcommandCompletions(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		if parent == nil {
			return
		}
		if hasAliasedChild(parent) {
			previous := parent.ValidArgsFunction
			parent.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
				if len(args) != 0 || strings.HasPrefix(toComplete, "-") {
					if previous == nil {
						return nil, cobra.ShellCompDirectiveDefault
					}
					return previous(cmd, args, toComplete)
				}

				var completions []cobra.Completion
				directive := cobra.ShellCompDirectiveNoFileComp
				if previous != nil {
					completions, directive = previous(cmd, args, toComplete)
					if directive&cobra.ShellCompDirectiveError == 0 &&
						directive&cobra.ShellCompDirectiveFilterFileExt == 0 &&
						directive&cobra.ShellCompDirectiveFilterDirs == 0 {
						directive |= cobra.ShellCompDirectiveNoFileComp
					}
				}
				completions = appendUniqueCompletions(completions, aliasChildCompletions(cmd, toComplete)...)
				return completions, directive
			}
		}
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			walk(child)
		}
	}
	walk(root)
}

func bindDefaultHelpLifecycle(root *cobra.Command) {
	root.InitDefaultHelpCmd()
	help := findDirectCanonicalChild(root, "help")
	if help == nil {
		return
	}

	help.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	help.PersistentPostRun = func(*cobra.Command, []string) {}

	previous := help.ValidArgsFunction
	help.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var completions []cobra.Completion
		directive := cobra.ShellCompDirectiveNoFileComp
		if previous != nil {
			completions, directive = previous(cmd, args, toComplete)
		}
		target, _, err := cmd.Root().Find(args)
		if err == nil {
			if target == nil {
				target = cmd.Root()
			}
			completions = appendUniqueCompletions(completions, aliasChildCompletions(target, toComplete)...)
		}
		return completions, directive
	}

	// Keep the default help command lazy so it does not become part of the
	// canonical product command tree. Cobra re-adds the same command at Execute.
	root.RemoveCommand(help)
}

func hasAliasedChild(parent *cobra.Command) bool {
	if parent == nil {
		return false
	}
	for _, child := range parent.Commands() {
		if !child.Hidden && len(child.Aliases) > 0 {
			return true
		}
	}
	return false
}

func aliasChildCompletions(parent *cobra.Command, toComplete string) []cobra.Completion {
	if parent == nil || strings.HasPrefix(toComplete, "-") {
		return nil
	}
	type candidate struct {
		alias       string
		description string
	}
	candidates := make([]candidate, 0)
	for _, child := range parent.Commands() {
		if child.Hidden || !child.IsAvailableCommand() {
			continue
		}
		for _, alias := range child.Aliases {
			alias = strings.TrimSpace(alias)
			if alias == "" || !strings.HasPrefix(alias, toComplete) {
				continue
			}
			candidates = append(candidates, candidate{
				alias:       alias,
				description: "Alias for " + child.Name(),
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].alias < candidates[j].alias
	})
	completions := make([]cobra.Completion, 0, len(candidates))
	for _, candidate := range candidates {
		completions = append(completions, cobra.CompletionWithDesc(candidate.alias, candidate.description))
	}
	return completions
}

func appendUniqueCompletions(existing []cobra.Completion, additions ...cobra.Completion) []cobra.Completion {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, completion := range existing {
		token, _, _ := strings.Cut(completion, "\t")
		seen[token] = struct{}{}
	}
	for _, completion := range additions {
		token, _, _ := strings.Cut(completion, "\t")
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		existing = append(existing, completion)
	}
	return existing
}

func commandHelpOnly(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if cmd.Name() == "help" {
		return true
	}
	flag := cmd.Flags().Lookup("help")
	if flag == nil {
		return false
	}
	value, err := cmd.Flags().GetBool("help")
	return err == nil && value
}
