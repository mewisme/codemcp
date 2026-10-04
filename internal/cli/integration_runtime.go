package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/integrations/rtk"
)

func rtkStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show RTK integration status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := application.NewRTKService().Status(cmd.Context())
			if err != nil {
				return err
			}
			renderRTKStatus(commandPresenter(cmd), status)
			return nil
		},
	}
}

func rtkToggleCommand(enabled bool) *cobra.Command {
	action, message := "disable", "RTK integration disabled"
	if enabled {
		action, message = "enable", "RTK integration enabled"
	}
	return &cobra.Command{
		Use: action, Short: strings.ToUpper(action[:1]) + action[1:] + " RTK integration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := application.NewRTKService()
			var status rtk.Status
			var err error
			if enabled {
				status, err = service.Enable(cmd.Context())
			} else {
				status, err = service.Disable(cmd.Context())
			}
			if err != nil {
				return err
			}
			renderMutationSuccess(cmd, message, presentation.Field{Label: "enabled", Value: status.Enabled})
			return nil
		},
	}
}

func rtkProbeCommand() *cobra.Command {
	return &cobra.Command{
		Use: "probe", Short: "Probe the effective RTK executable", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewRTKService().Probe(cmd.Context())
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			presenter.StateSection(presentation.StatusSuccess, "RTK executable is available")
			presenter.NestedFields(
				presentation.Field{Label: "source", Value: result.Status.Source},
				presentation.Field{Label: "path", Value: result.Path},
				presentation.Field{Label: "version", Value: result.Version},
			)
			return nil
		},
	}
}

func rtkInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use: "install", Short: "Install the verified managed RTK asset", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewRTKService().Install(cmd.Context())
			if err != nil {
				return err
			}
			message := "Managed RTK installed"
			kind := presentation.StatusSuccess
			if result.AlreadyInstalled {
				message = "Managed RTK already installed"
				kind = presentation.StatusInfo
			}
			renderMutationResult(cmd, kind, message,
				presentation.Field{Label: "path", Value: result.Path},
				presentation.Field{Label: "effective source", Value: result.Status.Source},
			)
			return nil
		},
	}
}

func rtkInstallGlobalCommand() *cobra.Command {
	return &cobra.Command{
		Use: "global", Short: "Use an RTK executable already installed globally by the user", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewRTKService().ResolveGlobal(cmd.Context())
			if err != nil {
				return err
			}
			if result.Available {
				renderMutationResult(cmd, presentation.StatusSuccess, "Global RTK detected",
					presentation.Field{Label: "path", Value: result.Path},
					presentation.Field{Label: "ownership", Value: "user-installed global executable"},
				)
				return nil
			}
			fields := []presentation.Field{{Label: "global", Value: "not installed"}}
			if result.ManagedRecommended {
				fields = append(fields, presentation.Field{Label: "recommended", Value: "cm integration rtk install"})
			} else if result.Status.ManagedInstalled {
				fields = append(fields, presentation.Field{Label: "managed asset", Value: "already available"})
			}
			renderMutationResult(cmd, presentation.StatusWarning, "Global RTK not found", fields...)
			return nil
		},
	}
}

func renderRTKStatus(presenter *presentation.Presenter, status rtk.Status) {
	kind := presentation.StatusSuccess
	message := "RTK is ready"
	if !status.Enabled || status.Source == rtk.SourceDisabled {
		kind, message = presentation.StatusInfo, "RTK is disabled"
	} else if status.Source == rtk.SourceUnavailable {
		kind, message = presentation.StatusWarning, "RTK executable is unavailable"
	}
	presenter.StateSection(kind, message)
	fields := []presentation.Field{
		{Label: "enabled", Value: status.Enabled},
		{Label: "source", Value: status.Source},
		{Label: "version", Value: status.Version},
		{Label: "platform", Value: status.Platform},
		{Label: "managed installed", Value: status.ManagedInstalled},
	}
	if strings.TrimSpace(status.Path) != "" {
		fields = append(fields, presentation.Field{Label: "path", Value: status.Path})
	}
	presenter.NestedFields(fields...)
}

func codeGraphStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show CodeGraph integration status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := application.NewCodeGraphService().Status(cmd.Context())
			if err != nil {
				return err
			}
			renderCodeGraphStatus(commandPresenter(cmd), status)
			return nil
		},
	}
}

func codeGraphProbeCommand() *cobra.Command {
	return &cobra.Command{
		Use: "probe", Short: "Probe the effective CodeGraph executable", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewCodeGraphService().Probe(cmd.Context())
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			presenter.StateSection(presentation.StatusSuccess, "CodeGraph executable is available")
			presenter.NestedFields(
				presentation.Field{Label: "source", Value: result.Status.Resolution.Source},
				presentation.Field{Label: "path", Value: result.Path},
				presentation.Field{Label: "version", Value: result.Version},
			)
			return nil
		},
	}
}

func codeGraphInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use: "install", Short: "Install the verified managed CodeGraph asset", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewCodeGraphService().Install(cmd.Context())
			if err != nil {
				return err
			}
			message := "Managed CodeGraph installed"
			kind := presentation.StatusSuccess
			if result.AlreadyInstalled {
				message = "Managed CodeGraph already installed"
				kind = presentation.StatusInfo
			}
			renderMutationResult(cmd, kind, message,
				presentation.Field{Label: "path", Value: result.Path},
				presentation.Field{Label: "effective source", Value: result.Status.Resolution.Source},
			)
			return nil
		},
	}
}

func codeGraphInstallGlobalCommand() *cobra.Command {
	return &cobra.Command{
		Use: "global", Short: "Use a CodeGraph executable already installed globally by the user", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewCodeGraphService().ResolveGlobal(cmd.Context())
			if err != nil {
				return err
			}
			if result.Available {
				renderMutationResult(cmd, presentation.StatusSuccess, "Global CodeGraph detected",
					presentation.Field{Label: "path", Value: result.Path},
					presentation.Field{Label: "ownership", Value: "user-installed global executable"},
				)
				return nil
			}
			fields := []presentation.Field{{Label: "global", Value: "not installed"}}
			if result.ManagedRecommended {
				fields = append(fields, presentation.Field{Label: "recommended", Value: "cm integration codegraph install"})
			} else if result.Status.ManagedInstalled {
				fields = append(fields, presentation.Field{Label: "managed asset", Value: "already available"})
			}
			renderMutationResult(cmd, presentation.StatusWarning, "Global CodeGraph not found", fields...)
			return nil
		},
	}
}

func renderCodeGraphStatus(presenter *presentation.Presenter, status codegraph.Status) {
	kind := presentation.StatusSuccess
	message := "CodeGraph is ready"
	if !status.Enabled || status.Resolution.Source == codegraph.ExecutableDisabled {
		kind, message = presentation.StatusInfo, "CodeGraph is disabled"
	} else if status.Resolution.Source == codegraph.ExecutableUnavailable {
		kind, message = presentation.StatusWarning, "CodeGraph executable is unavailable"
	}
	presenter.StateSection(kind, message)
	fields := []presentation.Field{
		{Label: "enabled", Value: status.Enabled},
		{Label: "source", Value: status.Resolution.Source},
		{Label: "pinned version", Value: status.PinnedVersion},
		{Label: "platform", Value: status.Platform},
		{Label: "managed installed", Value: status.ManagedInstalled},
	}
	if strings.TrimSpace(status.Resolution.Path) != "" {
		fields = append(fields, presentation.Field{Label: "path", Value: status.Resolution.Path})
	}
	presenter.NestedFields(fields...)
}

func codeGraphWorkspaceCommand(action string) *cobra.Command {
	title := "Initialize CodeGraph for a registered workspace"
	if action == "sync" {
		title = "Sync CodeGraph for a registered workspace"
	}
	return &cobra.Command{
		Use: action + " <workspace_id>", Short: title, Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := application.NewCodeGraphService(workspaceManagerForCommand(cmd))
			input := application.CodeGraphWorkspaceInput{WorkspaceID: strings.TrimSpace(args[0])}
			var result application.CodeGraphWorkspaceActionResult
			var err error
			if action == "init" {
				result, err = service.InitWorkspace(cmd.Context(), input)
			} else {
				result, err = service.SyncWorkspace(cmd.Context(), input)
			}
			if err != nil {
				return err
			}
			message := "CodeGraph workspace " + action + " complete"
			kind := presentation.StatusSuccess
			if result.Skipped {
				message = "CodeGraph workspace " + action + " skipped"
				kind = presentation.StatusInfo
			}
			fields := []presentation.Field{
				{Label: "workspace", Value: result.Status.WorkspaceID},
				{Label: "project", Value: result.Status.ProjectPath},
				{Label: "index", Value: result.Status.IndexState},
				{Label: "freshness", Value: result.Status.Freshness},
			}
			if strings.TrimSpace(result.Reason) != "" {
				fields = append(fields, presentation.Field{Label: "reason", Value: result.Reason})
			}
			renderMutationResult(cmd, kind, message, fields...)
			return nil
		},
	}
}

func codeGraphWorkspaceGroupCommand() *cobra.Command {
	group := &cobra.Command{Use: "workspace", Short: "Inspect CodeGraph workspace state"}
	group.AddCommand(&cobra.Command{
		Use: "status <workspace_id>", Short: "Show CodeGraph state for a registered workspace", Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := application.NewCodeGraphService(workspaceManagerForCommand(cmd)).WorkspaceStatus(cmd.Context(), application.CodeGraphWorkspaceInput{WorkspaceID: strings.TrimSpace(args[0])})
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			presenter.Fields(
				presentation.Field{Label: "workspace", Value: status.WorkspaceID},
				presentation.Field{Label: "project", Value: status.ProjectPath},
				presentation.Field{Label: "index", Value: status.IndexState},
				presentation.Field{Label: "freshness", Value: status.Freshness},
			)
			return nil
		},
	})
	return group
}
