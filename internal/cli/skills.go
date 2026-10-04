package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/skills"
)

var (
	errSkillRiskConfirmationRequired = errors.New("skill security risk confirmation is required; rerun with --yes")
	errSkillRiskCancelled            = errors.New("skill security risk review cancelled")
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
			if asJSON {
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
	var yes bool
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
			var progress *commandProgress
			if !asJSON && commandResultModeFor(cmd) == resultModeHuman {
				progress = newCommandProgress(cmd, "SKILLS")
				progress.Start("skills.add.cloning", "Cloning GitHub repository", "Repository cloned")
			}
			result, err := service.Add(cmd.Context(), application.SkillAddRequest{
				Scope: scope, Source: args[0], Skill: selectedSkill, All: all, FullDepth: fullDepth,
				ReviewRisk: skillRiskReviewer(cmd, yes, "installation"),
				Progress:   skillMutationProgressObserver(progress, "add"),
			})
			if err != nil {
				if progress != nil {
					progress.Stop()
				}
				if errors.Is(err, errSkillRiskCancelled) {
					renderSkillRiskCancelled(cmd, "Skill installation cancelled")
					return nil
				}
				return err
			}
			if progress != nil {
				progress.Complete()
			}
			if asJSON {
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
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip security risk confirmation")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func skillsUpdateCommand() *cobra.Command {
	var scopeFlags skillScopeFlags
	var all bool
	var yes bool
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
			var progress *commandProgress
			if !asJSON && commandResultModeFor(cmd) == resultModeHuman {
				progress = newCommandProgress(cmd, "SKILLS")
				progress.Start("skills.update.acquiring", "Acquiring managed GitHub skill sources", "Managed GitHub skill sources acquired")
			}
			result, err := service.Update(cmd.Context(), application.SkillUpdateRequest{
				Scope: scope, Name: name, All: all, ReviewRisk: skillRiskReviewer(cmd, yes, "update"),
				Progress: skillMutationProgressObserver(progress, "update"),
			})
			if err != nil {
				if progress != nil {
					progress.Stop()
				}
				if errors.Is(err, errSkillRiskCancelled) {
					renderSkillRiskCancelled(cmd, "Skill update cancelled")
					return nil
				}
				return err
			}
			if progress != nil {
				progress.Complete()
			}
			if asJSON {
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
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip security risk confirmation")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func skillMutationProgressObserver(progress *commandProgress, operation string) func(application.SkillMutationEvent) {
	if progress == nil {
		return nil
	}
	return func(event application.SkillMutationEvent) {
		switch event.Phase {
		case application.SkillMutationPhaseRepositoryAcquired:
			progress.Start("skills.add.discovering", "Discovering skills", "Skills discovered")
		case application.SkillMutationPhaseDiscovered:
			label := "skills"
			if event.Count == 1 {
				label = "skill"
			}
			progress.CompleteWith(fmt.Sprintf("Found %d %s", event.Count, label))
		case application.SkillMutationPhaseSelected:
			progress.session.Append(func(presenter *presentation.Presenter) {
				if len(event.Skills) == 1 {
					selected := event.Skills[0]
					presenter.ChildStatus(presentation.StatusInfo, "Skill: "+selected.Name)
					if strings.TrimSpace(selected.Description) != "" {
						presenter.NestedFields(presentation.Field{Label: "description", Value: selected.Description})
					}
					return
				}
				names := make([]string, 0, len(event.Skills))
				for _, selected := range event.Skills {
					names = append(names, selected.Name)
				}
				presenter.ChildStatus(presentation.StatusInfo, fmt.Sprintf("Selected %d skills: %s", len(names), strings.Join(names, ", ")))
			})
		case application.SkillMutationPhaseAcquired:
			progress.Complete()
		case application.SkillMutationPhaseInstalling:
			if operation == "update" {
				progress.Start("skills.update.installing", "Installing managed GitHub skill updates", "Managed GitHub skill updates installed")
			} else {
				progress.Start("skills.add.installing", "Installing GitHub skills", "GitHub skills installed")
			}
		}
	}
}

func skillRiskReviewer(cmd *cobra.Command, yes bool, action string) func(skills.SecurityAssessment) error {
	return func(assessment skills.SecurityAssessment) error {
		if !assessment.HasData() {
			return nil
		}
		risky := assessment.RequiresConfirmation()
		if commandResultModeFor(cmd) != resultModeHuman {
			if risky && !yes {
				return errSkillRiskConfirmationRequired
			}
			return nil
		}

		session := commandProgressSession(cmd)
		if !risky || yes {
			return session.WithInput(func(presenter *presentation.Presenter) {
				renderSkillSecurityAssessment(cmd, presenter, assessment)
			}, nil)
		}

		if cmd == nil || cmd.InOrStdin() == nil {
			return errSkillRiskConfirmationRequired
		}
		scanner := bufio.NewScanner(cmd.InOrStdin())
		answer := ""
		prompt := "Security risks detected. Proceed with " + strings.TrimSpace(action) + "? [Y/n]"
		if err := session.WithInput(func(presenter *presentation.Presenter) {
			renderSkillSecurityAssessment(cmd, presenter, assessment)
			presenter.Prompt(prompt)
		}, func() error {
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return err
				}
				return errSkillRiskConfirmationRequired
			}
			answer = strings.ToLower(strings.TrimSpace(scanner.Text()))
			return nil
		}); err != nil {
			return err
		}
		switch answer {
		case "", "y", "yes":
			return nil
		default:
			return errSkillRiskCancelled
		}
	}
}

func renderSkillRiskCancelled(cmd *cobra.Command, message string) {
	session := commandProgressSession(cmd)
	session.Append(func(presenter *presentation.Presenter) {
		presenter.StateSection(presentation.StatusInactive, message)
	})
}

func renderSkillSecurityAssessment(cmd *cobra.Command, presenter *presentation.Presenter, assessment skills.SecurityAssessment) {
	if presenter == nil || !assessment.HasData() {
		return
	}
	theme := presentation.NewTheme(commandTerminalCapabilities(cmd))
	nameWidth := 0
	for _, skill := range assessment.Skills {
		if width := len(skill.Name); width > nameWidth {
			nameWidth = width
		}
	}
	if nameWidth > 36 {
		nameWidth = 36
	}
	if nameWidth < 1 {
		nameWidth = 1
	}
	const providerWidth = 18
	lines := []string{
		strings.Repeat(" ", nameWidth+2) +
			padSkillRiskCell(theme.Render(presentation.RoleMuted, "Gen"), len("Gen"), providerWidth) +
			padSkillRiskCell(theme.Render(presentation.RoleMuted, "Socket"), len("Socket"), providerWidth) +
			theme.Render(presentation.RoleMuted, "Snyk"),
	}
	for _, skill := range assessment.Skills {
		name := skill.Name
		if len(name) > nameWidth {
			if nameWidth > 1 {
				name = name[:nameWidth-1] + "…"
			} else {
				name = "…"
			}
		}
		lines = append(lines,
			padSkillRiskCell(theme.Render(presentation.RoleActive, name), len(name), nameWidth+2)+
				padSkillRiskAudit(theme, skill.Gen, false, providerWidth)+
				padSkillRiskAudit(theme, skill.Socket, true, providerWidth)+
				renderSkillRiskAudit(theme, skill.Snyk, false),
		)
	}
	lines = append(lines, "", theme.Render(presentation.RoleMuted, "Details:")+" "+theme.Render(presentation.RoleMuted, assessment.DetailsURL))
	presenter.Note("Security Risk Assessments", strings.Join(lines, "\n"))
}

func padSkillRiskCell(value string, visibleWidth, width int) string {
	padding := width - visibleWidth
	if padding < 0 {
		padding = 0
	}
	return value + strings.Repeat(" ", padding)
}

func padSkillRiskAudit(theme presentation.Theme, audit *skills.PartnerAudit, socket bool, width int) string {
	value, visibleWidth := skillRiskAuditLabel(theme, audit, socket)
	return padSkillRiskCell(value, visibleWidth, width)
}

func renderSkillRiskAudit(theme presentation.Theme, audit *skills.PartnerAudit, socket bool) string {
	value, _ := skillRiskAuditLabel(theme, audit, socket)
	return value
}

func skillRiskAuditLabel(theme presentation.Theme, audit *skills.PartnerAudit, socket bool) (string, int) {
	if audit == nil {
		return theme.Render(presentation.RoleMuted, "--"), 2
	}
	if socket {
		alerts := 0
		if audit.Alerts != nil {
			alerts = *audit.Alerts
		}
		label := fmt.Sprintf("%d alerts", alerts)
		if alerts == 1 {
			label = "1 alert"
		}
		role := presentation.RoleSuccess
		if alerts > 0 {
			role = presentation.RoleDanger
		}
		return theme.Render(role, label), len(label)
	}
	var label string
	var role presentation.Role
	switch audit.Risk {
	case skills.SecurityRiskCritical:
		label, role = "Critical Risk", presentation.RoleDanger
	case skills.SecurityRiskHigh:
		label, role = "High Risk", presentation.RoleDanger
	case skills.SecurityRiskMedium:
		label, role = "Med Risk", presentation.RoleWarning
	case skills.SecurityRiskLow:
		label, role = "Low Risk", presentation.RoleSuccess
	case skills.SecurityRiskSafe:
		label, role = "Safe", presentation.RoleSuccess
	default:
		label, role = "--", presentation.RoleMuted
	}
	return theme.Render(role, label), len(label)
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
			var progress *commandProgress
			if !asJSON && commandResultModeFor(cmd) == resultModeHuman {
				progress = newCommandProgress(cmd, "SKILLS")
				progress.Start("skills.remove.removing", "Removing native skill", "Native skill removed")
			}
			result, err := service.Remove(application.SkillRemoveRequest{Scope: scope, Name: args[0]})
			if err != nil {
				if progress != nil {
					progress.Stop()
				}
				return err
			}
			if progress != nil {
				progress.Complete()
			}
			if asJSON {
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
			if asJSON {
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
	p.Table([]string{"Name", "Origin", "State"}, rows, presentation.TableOptions{
		Border: presentation.TableBare,
		Layout: presentation.TableAdaptive,
		Depth:  1,
	})
}

func renderSkillInfo(cmd *cobra.Command, value application.SkillView) {
	p := commandPresenter(cmd)
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
