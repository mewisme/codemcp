package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
)

type skillScopeFlags struct {
	workspace bool
	global    bool
}

func skillsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "skills", Short: "Manage native skills and inspect effective skill inventory"}
	cmd.AddCommand(skillsListCommand(), skillsAddCommand(), skillsInfoCommand(), skillsUpdateCommand(), skillsRemoveCommand())
	return cmd
}

func skillServiceForCommand(cmd *cobra.Command) *application.SkillManagementService {
	return application.NewSkillManagementService(workspaceManagerForCommand(cmd))
}

func addSkillScopeFlags(cmd *cobra.Command, flags *skillScopeFlags) {
	cmd.Flags().BoolVarP(&flags.workspace, "workspace", "w", false, "use the current registered workspace native skill store")
	cmd.Flags().BoolVarP(&flags.global, "global", "g", false, "use the global native skill store")
}

func resolveSkillScope(cmd *cobra.Command, service *application.SkillManagementService, flags skillScopeFlags) (application.SkillScopeRequest, error) {
	if flags.workspace && flags.global {
		return application.SkillScopeRequest{}, errors.New("--workspace and --global are mutually exclusive")
	}
	if flags.global {
		return application.SkillScopeRequest{Global: true}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return application.SkillScopeRequest{}, err
	}
	item, err := service.ResolveWorkspaceForDirectory(cwd)
	if err != nil {
		return application.SkillScopeRequest{}, err
	}
	return application.SkillScopeRequest{WorkspaceID: item.ID, Workspace: flags.workspace}, nil
}

func skillsListCommand() *cobra.Command {
	var scopeFlags skillScopeFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List effective or scoped skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := skillServiceForCommand(cmd)
			scope, err := resolveSkillScope(cmd, service, scopeFlags)
			if err != nil {
				return err
			}
			result, err := service.List(application.SkillListRequest{Scope: scope})
			if err != nil {
				return err
			}
			if asJSON || commandResultModeFor(cmd) != resultModeHuman {
				return writeResultJSON(cmd, result)
			}
			renderSkillList(cmd, result)
			return nil
		},
	}
	addSkillScopeFlags(cmd, &scopeFlags)
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func skillsAddCommand() *cobra.Command {
	var scopeFlags skillScopeFlags
	var selectedSkill string
	var all bool
	var fullDepth bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Install validated skills from a GitHub repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := skillServiceForCommand(cmd)
			scope, err := resolveSkillScope(cmd, service, scopeFlags)
			if err != nil {
				return err
			}
			if selectedSkill != "" && all {
				return errors.New("--skill and --all are mutually exclusive")
			}
			logCommandStep(cmd, "SKILLS", "skills.add.acquiring", "Acquiring and validating GitHub skills")
			result, err := service.Add(cmd.Context(), application.SkillAddRequest{
				Scope: scope, Source: args[0], Skill: selectedSkill, All: all, FullDepth: fullDepth,
			})
			if err != nil {
				return err
			}
			if asJSON || commandResultModeFor(cmd) != resultModeHuman {
				return writeResultJSON(cmd, result)
			}
			fields := []presentation.Field{
				{Label: "scope", Value: result.Scope},
				{Label: "source", Value: result.Source.Identity},
				{Label: "revision", Value: result.Revision},
				{Label: "skills", Value: len(result.Skills)},
			}
			renderMutationSuccess(cmd, "Skills installed", fields...)
			return nil
		},
	}
	addSkillScopeFlags(cmd, &scopeFlags)
	cmd.Flags().StringVar(&selectedSkill, "skill", "", "install exactly one discovered skill by validated name")
	cmd.Flags().BoolVar(&all, "all", false, "install all discovered skills")
	cmd.Flags().BoolVar(&fullDepth, "full-depth", false, "search nested repository paths even when canonical skill locations are found")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func skillsUpdateCommand() *cobra.Command {
	var scopeFlags skillScopeFlags
	var all bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "update [name]",
		Short:             "Update managed GitHub skills from recorded sources",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeManagedSkillName,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if all && name != "" {
				return errors.New("skill name and --all are mutually exclusive")
			}
			if !all && name == "" {
				return errors.New("skill name or --all is required")
			}
			service := skillServiceForCommand(cmd)
			scope, err := resolveSkillScope(cmd, service, scopeFlags)
			if err != nil {
				return err
			}
			logCommandStep(cmd, "SKILLS", "skills.update.acquiring", "Checking managed GitHub skills")
			result, err := service.Update(cmd.Context(), application.SkillUpdateRequest{Scope: scope, Name: name, All: all})
			if err != nil {
				return err
			}
			if asJSON || commandResultModeFor(cmd) != resultModeHuman {
				return writeResultJSON(cmd, result)
			}
			changed := 0
			for _, item := range result.Skills {
				if item.Changed {
					changed++
				}
			}
			renderMutationSuccess(cmd, "Skills updated",
				presentation.Field{Label: "scope", Value: result.Scope},
				presentation.Field{Label: "checked", Value: len(result.Skills)},
				presentation.Field{Label: "changed", Value: changed},
			)
			return nil
		},
	}
	addSkillScopeFlags(cmd, &scopeFlags)
	cmd.Flags().BoolVar(&all, "all", false, "update every managed skill in the selected scope")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func skillsRemoveCommand() *cobra.Command {
	var scopeFlags skillScopeFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "remove <name>",
		Short:             "Remove an exact native skill from the selected scope",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNativeSkillName,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := skillServiceForCommand(cmd)
			scope, err := resolveSkillScope(cmd, service, scopeFlags)
			if err != nil {
				return err
			}
			result, err := service.Remove(application.SkillRemoveRequest{Scope: scope, Name: args[0]})
			if err != nil {
				return err
			}
			if asJSON || commandResultModeFor(cmd) != resultModeHuman {
				return writeResultJSON(cmd, result)
			}
			renderMutationSuccess(cmd, "Skill removed",
				presentation.Field{Label: "scope", Value: result.Scope},
				presentation.Field{Label: "name", Value: result.Name},
				presentation.Field{Label: "managed", Value: result.Managed},
			)
			return nil
		},
	}
	addSkillScopeFlags(cmd, &scopeFlags)
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func skillsInfoCommand() *cobra.Command {
	var scopeFlags skillScopeFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "info <name>",
		Short:             "Show effective or scoped skill details",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeSkillName,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := skillServiceForCommand(cmd)
			scope, err := resolveSkillScope(cmd, service, scopeFlags)
			if err != nil {
				return err
			}
			value, err := service.Info(application.SkillInfoRequest{Scope: scope, Name: args[0]})
			if err != nil {
				return err
			}
			if asJSON || commandResultModeFor(cmd) != resultModeHuman {
				return writeResultJSON(cmd, value)
			}
			renderSkillInfo(cmd, value)
			return nil
		},
	}
	addSkillScopeFlags(cmd, &scopeFlags)
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderSkillList(cmd *cobra.Command, result application.SkillListResult) {
	p := commandPresenter(cmd)
	p.Frame("Skills")
	rows := make([]presentation.Row, 0, len(result.Skills))
	for _, value := range result.Skills {
		origin := skillOriginLabel(value)
		state := "writable"
		if value.ReadOnly {
			state = "read-only"
		} else if value.Managed {
			state = "managed"
		}
		rows = append(rows, presentation.Row{value.Name, origin, state})
	}
	p.AlignedRows([]string{"Name", "Origin", "State"}, rows...)
	p.Complete("Skills listed")
}

func renderSkillInfo(cmd *cobra.Command, value application.SkillView) {
	p := commandPresenter(cmd)
	p.Frame("Skill")
	fields := []presentation.Field{
		{Label: "name", Value: value.Name},
		{Label: "description", Value: value.Description},
		{Label: "origin", Value: skillOriginLabel(value)},
		{Label: "managed", Value: value.Managed},
		{Label: "read only", Value: value.ReadOnly},
		{Label: "path", Value: value.Path},
	}
	if value.GitHub != nil {
		fields = append(fields,
			presentation.Field{Label: "source", Value: value.GitHub.Source},
			presentation.Field{Label: "ref", Value: value.GitHub.Ref},
			presentation.Field{Label: "revision", Value: value.GitHub.Revision},
			presentation.Field{Label: "repository path", Value: value.GitHub.Path},
			presentation.Field{Label: "content hash", Value: value.GitHub.ContentHash},
		)
	}
	p.Fields(fields...)
	p.Complete("Skill loaded")
}

func completeManagedSkillName(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	values := completeScopedSkillViews(cmd, toComplete, func(value application.SkillView) bool { return value.Managed })
	return values, cobra.ShellCompDirectiveNoFileComp
}

func completeNativeSkillName(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	values := completeScopedSkillViews(cmd, toComplete, func(application.SkillView) bool { return true })
	return values, cobra.ShellCompDirectiveNoFileComp
}

func completeScopedSkillViews(cmd *cobra.Command, toComplete string, include func(application.SkillView) bool) []string {
	prepareCompletionConfigRoot(cmd)
	workspaceFlag, _ := cmd.Flags().GetBool("workspace")
	globalFlag, _ := cmd.Flags().GetBool("global")
	service := skillServiceForCommand(cmd)
	scope, err := resolveSkillScope(cmd, service, skillScopeFlags{workspace: workspaceFlag, global: globalFlag})
	if err != nil {
		return nil
	}
	if !scope.Global {
		scope.Workspace = true
	}
	result, err := service.List(application.SkillListRequest{Scope: scope})
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(result.Skills))
	for _, value := range result.Skills {
		if include(value) && strings.HasPrefix(value.Name, toComplete) {
			values = append(values, value.Name)
		}
	}
	return values
}

func skillOriginLabel(value application.SkillView) string {
	scope := strings.TrimSpace(string(value.Scope))
	if scope == "" {
		return value.Source
	}
	if value.Source == "provider" || scope == "provider" {
		return "provider:" + value.Source
	}
	return scope + ":" + value.Source
}

func completeSkillName(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prepareCompletionConfigRoot(cmd)
	workspaceFlag, _ := cmd.Flags().GetBool("workspace")
	globalFlag, _ := cmd.Flags().GetBool("global")
	service := skillServiceForCommand(cmd)
	scope, err := resolveSkillScope(cmd, service, skillScopeFlags{workspace: workspaceFlag, global: globalFlag})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	result, err := service.List(application.SkillListRequest{Scope: scope})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	values := make([]string, 0, len(result.Skills))
	for _, value := range result.Skills {
		if strings.HasPrefix(value.Name, toComplete) {
			values = append(values, value.Name)
		}
	}
	return values, cobra.ShellCompDirectiveNoFileComp
}
