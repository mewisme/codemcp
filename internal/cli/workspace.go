package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/logger"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/workspace"
)

func workspaceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Aliases: []string{"ws"}, Short: "Manage registered workspace roots"}
	cmd.AddCommand(
		workspaceRegisterCommand(),
		workspaceListCommand(),
		workspaceShowCommand(),
		workspaceDoctorCommand(),
		workspaceRelocateCommand(),
		workspaceUnregisterCommand(),
		workspacePurgeCommand(),
		workspaceAccessCommand(),
		workspaceContainerCommand(),
	)
	return cmd
}

func workspaceDoctorCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "doctor <workspace_id>",
		Short:             "Diagnose workspace-local state without modifying it",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			diagnostic, err := workspaceManagerForCommand(cmd).Diagnose(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return writeResultJSON(cmd, diagnostic)
			}
			log := commandLogger(cmd)
			log.Info("WORKSPACE", "workspace local-state diagnostics")
			log.Detail("id", diagnostic.WorkspaceID)
			log.Detail("root", diagnostic.Root)
			log.Detail("local root", diagnostic.LocalRoot)
			log.Detail("health", diagnostic.Health)
			log.Detail("available", diagnostic.Available)
			log.Detail("size bytes", diagnostic.SizeBytes)
			log.Detail("files", diagnostic.FileCount)
			log.Detail("locked", diagnostic.Locked)
			if diagnostic.LockPID != 0 {
				log.Detail("lock pid", diagnostic.LockPID)
			}
			if diagnostic.LockInstanceID != "" {
				log.Detail("lock instance", diagnostic.LockInstanceID)
			}
			if diagnostic.GitHygiene.Tracked {
				log.Detail("git hygiene", "tracked .cm")
			} else if diagnostic.GitHygiene.Degraded || !diagnostic.GitHygiene.Protected {
				log.Detail("git hygiene", "degraded")
			} else {
				log.Detail("git hygiene", "healthy")
			}
			if diagnostic.GitHygiene.Guidance != "" {
				log.Detail("git guidance", diagnostic.GitHygiene.Guidance)
			}
			if diagnostic.Error != "" {
				log.Detail("error", diagnostic.Error)
			}
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func workspaceRelocateCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "relocate <workspace_id> <path>",
		Aliases:           []string{"move"},
		Short:             "Rebind a registered workspace after its project directory moved",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeWorkspaceThenDirectory,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).Relocate(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			before, after := result.Value.Before, result.Value.After
			renderMutationSuccess(cmd, "Workspace", "Workspace relocated", presentation.Field{Label: "old id", Value: before.ID}, presentation.Field{Label: "id", Value: after.ID}, presentation.Field{Label: "old root", Value: before.Path}, presentation.Field{Label: "root", Value: after.Path})
			return nil
		},
	}
}

func workspaceManagerForCommand(cmd *cobra.Command) *workspace.Manager {
	path := workspace.DefaultStorePath()
	logCommandStep(cmd, "WORKSPACE", "workspace.store.opening", "Opening workspace registry")
	logCommandDebug(cmd, "WORKSPACE", "workspace.store.path", "Workspace registry path resolved", logger.WithDebug("path", path))
	return workspace.NewManager(path).SetTraceObserver(tracepkg.ObserverFromContext(cmd.Context()))
}

func workspaceServiceForCommand(cmd *cobra.Command) *application.WorkspaceService {
	return application.NewDefaultWorkspaceService(workspaceManagerForCommand(cmd))
}

func workspaceContainerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "container", Aliases: []string{"ctr"}, Short: "Manage workspace containers"}
	cmd.AddCommand(
		workspaceContainerListCommand(),
		workspaceContainerCreateCommand(),
		workspaceContainerShowCommand(),
		workspaceContainerRenameCommand(),
		workspaceContainerDeleteCommand(),
		workspaceContainerMembershipCommand(true),
		workspaceContainerMembershipCommand(false),
	)
	return cmd
}

func workspaceContainerListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List workspace containers", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := workspaceServiceForCommand(cmd).ListContainers(cmd.Context())
		if err != nil {
			return err
		}
		values := result.Value
		if asJSON {
			return writeResultJSON(cmd, values)
		}
		renderWorkspaceContainers(commandPresenter(cmd), values)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func workspaceContainerCreateCommand() *cobra.Command {
	return &cobra.Command{Use: "create <name>", Short: "Create a workspace container", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := workspaceServiceForCommand(cmd).CreateContainer(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		value := result.Value
		renderMutationSuccess(cmd, "Workspace container", "Workspace container created", presentation.Field{Label: "id", Value: value.ID}, presentation.Field{Label: "name", Value: value.Name})
		return nil
	}}
}

func workspaceContainerShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "show <wsc_id>", Short: "Show one workspace container", Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceContainerID, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := workspaceServiceForCommand(cmd).GetContainer(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		value := result.Value
		if asJSON {
			return writeResultJSON(cmd, value)
		}
		renderWorkspaceContainer(commandPresenter(cmd), value)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func workspaceContainerRenameCommand() *cobra.Command {
	return &cobra.Command{Use: "rename <wsc_id> <name>", Short: "Rename a workspace container", Args: cobra.ExactArgs(2), ValidArgsFunction: completeWorkspaceContainerThenName, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := workspaceServiceForCommand(cmd).RenameContainer(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		value := result.Value
		renderMutationSuccess(cmd, "Workspace container", "Workspace container renamed", presentation.Field{Label: "id", Value: value.ID}, presentation.Field{Label: "name", Value: value.Name})
		return nil
	}}
}

func workspaceContainerDeleteCommand() *cobra.Command {
	return &cobra.Command{Use: "delete <wsc_id>", Aliases: []string{"rm"}, Short: "Delete a workspace container without unregistering workspaces", Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceContainerID, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := workspaceServiceForCommand(cmd).DeleteContainer(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		value := result.Value
		renderMutationSuccess(cmd, "Workspace container", "Workspace container deleted", presentation.Field{Label: "id", Value: value.ID}, presentation.Field{Label: "name", Value: value.Name}, presentation.Field{Label: "workspaces", Value: "unchanged"})
		return nil
	}}
}

func workspaceContainerMembershipCommand(add bool) *cobra.Command {
	use, short, action := "add <wsc_id> <workspace_id...>", "Add workspaces to a workspace container", "added"
	if !add {
		use, short, action = "remove <wsc_id> <workspace_id...>", "Remove workspaces from a workspace container", "removed"
	}
	return &cobra.Command{Use: use, Short: short, Args: cobra.MinimumNArgs(2), ValidArgsFunction: completeWorkspaceContainerThenWorkspaces, RunE: func(cmd *cobra.Command, args []string) error {
		service := workspaceServiceForCommand(cmd)
		var result application.Result[application.WorkspaceContainerView]
		var err error
		if add {
			result, err = service.AddWorkspacesToContainer(cmd.Context(), args[0], args[1:])
		} else {
			result, err = service.RemoveWorkspacesFromContainer(cmd.Context(), args[0], args[1:])
		}
		if err != nil {
			return err
		}
		value := result.Value
		renderMutationSuccess(cmd, "Workspace container", "Workspace container membership updated", presentation.Field{Label: "container", Value: value.ID}, presentation.Field{Label: action, Value: strings.Join(args[1:], ", ")})
		return nil
	}}
}

func workspaceAccessCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "access", Short: "Manage workspace-specific filesystem access"}
	var listJSON bool
	list := &cobra.Command{Use: "list <workspace_id>", Aliases: []string{"ls"}, Short: "List workspace-specific additional directories", Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceID, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := workspaceServiceForCommand(cmd).AccessList(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		allowDirs := result.Value
		if listJSON {
			return writeResultJSON(cmd, allowDirs)
		}
		renderWorkspaceAccess(commandPresenter(cmd), args[0], allowDirs)
		return nil
	}}
	addJSONResultFlag(list, &listJSON)
	cmd.AddCommand(
		&cobra.Command{Use: "add <workspace_id> <path>", Short: "Grant a workspace access to an additional directory", Args: cobra.ExactArgs(2), ValidArgsFunction: completeWorkspaceThenDirectory, RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).AddAllowDir(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			item := result.Value
			renderMutationSuccess(cmd, "Workspace access", "Allowed directory added", presentation.Field{Label: "id", Value: item.ID}, presentation.Field{Label: "allow_dir", Value: args[1]})
			return nil
		}},
		&cobra.Command{Use: "remove <workspace_id> <path>", Short: "Revoke an additional directory from a workspace", Args: cobra.ExactArgs(2), ValidArgsFunction: completeWorkspaceThenDirectory, RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).RemoveAllowDir(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			item := result.Value
			renderMutationSuccess(cmd, "Workspace access", "Allowed directory removed", presentation.Field{Label: "id", Value: item.ID}, presentation.Field{Label: "allow_dir", Value: args[1]})
			return nil
		}},
		list,
	)
	return cmd
}

func workspaceRegisterCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "register [path]",
		Short:             "Register a canonical workspace root and return its stable workspace_id",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeDirectory,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			} else {
				var err error
				path, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve current directory: %w", err)
				}
			}
			result, err := workspaceServiceForCommand(cmd).Register(cmd.Context(), path)
			if err != nil {
				return err
			}
			item := result.Value
			renderMutationSuccess(cmd, "Workspace", "Workspace registered", presentation.Field{Label: "id", Value: item.ID}, presentation.Field{Label: "root", Value: item.Path})
			return nil
		},
	}
}

func workspaceListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered workspace roots",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).List(cmd.Context())
			if err != nil {
				return err
			}
			items := result.Value
			if asJSON {
				return writeResultJSON(cmd, items)
			}
			renderWorkspaceList(cmd, commandPresenter(cmd), items)
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func workspaceShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "show <workspace_id>",
		Short:             "Show one registered workspace",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			item := result.Value
			if asJSON {
				return writeResultJSON(cmd, item)
			}
			renderWorkspace(cmd, commandPresenter(cmd), item)
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderWorkspaceList(cmd *cobra.Command, presenter *presentation.Presenter, items []application.WorkspaceView) {
	presenter.Frame("Registered workspaces")
	if len(items) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No registered workspaces")
		presenter.FrameEnd("Done")
		return
	}
	presenter.Section(fmt.Sprintf("Registered workspaces loaded · %d", len(items)))
	for _, item := range items {
		presenter.Subsection(item.ID)
		fields := []presentation.Field{
			{Label: "root", Value: item.Path},
			{Label: "available", Value: item.Available},
		}
		if !item.Available {
			if item.Error != "" {
				fields = append(fields, presentation.Field{Label: "error", Value: item.Error})
			}
		} else {
			fields = append(fields, workspaceHygieneFields(cmd, item.Path)...)
		}
		presenter.NestedFields(fields...)
	}
	presenter.FrameEnd("Done")
}

func renderWorkspace(cmd *cobra.Command, presenter *presentation.Presenter, item application.WorkspaceView) {
	presenter.Frame("Workspace details")
	presenter.Section(item.ID)
	fields := []presentation.Field{
		{Label: "root", Value: item.Path},
		{Label: "available", Value: item.Available},
	}
	if item.Error != "" {
		fields = append(fields, presentation.Field{Label: "error", Value: item.Error})
	}
	if item.Available {
		fields = append(fields, workspaceHygieneFields(cmd, item.Path)...)
	}
	allowDirs := "none"
	if len(item.AllowDirs) > 0 {
		allowDirs = strings.Join(item.AllowDirs, ", ")
	}
	fields = append(fields, presentation.Field{Label: "allow dirs", Value: allowDirs})
	if len(item.LegacyIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "legacy ids", Value: strings.Join(item.LegacyIDs, ", ")})
	}
	presenter.Fields(fields...)
	presenter.FrameEnd("Done")
}

func workspaceHygieneFields(cmd *cobra.Command, root string) []presentation.Field {
	hygiene := workspace.InspectLocalStateGitHygiene(cmd.Context(), root)
	state := "healthy"
	if hygiene.Tracked {
		state = "tracked .cm"
	} else if hygiene.Degraded || !hygiene.Protected {
		state = "degraded"
	}
	fields := []presentation.Field{{Label: "git hygiene", Value: state}}
	if hygiene.Guidance != "" {
		fields = append(fields, presentation.Field{Label: "git guidance", Value: hygiene.Guidance})
	}
	return fields
}

func renderWorkspaceContainers(presenter *presentation.Presenter, values []application.WorkspaceContainerView) {
	presenter.Frame("Workspace containers")
	if len(values) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No workspace containers")
		presenter.FrameEnd("Done")
		return
	}
	presenter.Section(fmt.Sprintf("Workspace containers loaded · %d", len(values)))
	for _, value := range values {
		presenter.Subsection(value.ID)
		workspaces := "none"
		if len(value.WorkspaceIDs) > 0 {
			workspaces = strings.Join(value.WorkspaceIDs, ", ")
		}
		presenter.NestedFields(
			presentation.Field{Label: "name", Value: value.Name},
			presentation.Field{Label: "workspaces", Value: workspaces},
		)
	}
	presenter.FrameEnd("Done")
}

func renderWorkspaceContainer(presenter *presentation.Presenter, value application.WorkspaceContainerView) {
	presenter.Frame("Workspace container details")
	presenter.Section(value.ID)
	workspaces := "none"
	if len(value.WorkspaceIDs) > 0 {
		workspaces = strings.Join(value.WorkspaceIDs, ", ")
	}
	presenter.Fields(
		presentation.Field{Label: "name", Value: value.Name},
		presentation.Field{Label: "workspaces", Value: workspaces},
	)
	presenter.FrameEnd("Done")
}

func renderWorkspaceAccess(presenter *presentation.Presenter, workspaceID string, allowDirs []string) {
	presenter.Frame(fmt.Sprintf("Allowed directories loaded · %d", len(allowDirs)))
	presenter.Section("Workspace")
	presenter.Fields(presentation.Field{Label: "id", Value: workspaceID})
	presenter.Spacer()
	if len(allowDirs) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No additional allowed directories")
		presenter.Fields(presentation.Field{Label: "allow dirs", Value: "none"})
	} else {
		presenter.Section("Allowed directories")
		presenter.List(allowDirs...)
	}
	presenter.FrameEnd("Done")
}

func workspaceUnregisterCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "unregister <workspace_id>",
		Short:             "Remove a workspace handle without deleting project files or local .cm state",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).Unregister(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			item := result.Value
			renderMutationSuccess(cmd, "Workspace", "Workspace unregistered", presentation.Field{Label: "id", Value: item.ID}, presentation.Field{Label: "root", Value: item.Path}, presentation.Field{Label: "project files", Value: "unchanged"}, presentation.Field{Label: "local .cm", Value: "unchanged"})
			return nil
		},
	}
}

func workspacePurgeCommand() *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:               "purge <workspace_id_or_path>",
		Short:             "Delete workspace-local .cm state after explicit confirmation",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).Purge(cmd.Context(), args[0], confirm)
			if err != nil {
				return err
			}
			item := result.Value
			fields := []presentation.Field{}
			if item.ID != "" {
				fields = append(fields, presentation.Field{Label: "id", Value: item.ID})
			}
			fields = append(fields, presentation.Field{Label: "root", Value: item.Path}, presentation.Field{Label: "project files", Value: "unchanged"})
			renderMutationSuccess(cmd, "Workspace", "Workspace local state deleted", fields...)
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm destructive deletion of workspace-local state")
	return cmd
}
