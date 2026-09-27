package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

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
		workspaceContextCommand(),
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
	var destination string
	cmd := &cobra.Command{
		Use:               "doctor <workspace_id>",
		Short:             "Diagnose workspace-local state without modifying it",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(destination) != "" {
				diagnostic, err := workspaceManagerForCommand(cmd).DiagnoseRelocation(cmd.Context(), args[0], destination)
				if err != nil {
					return err
				}
				if asJSON {
					return writeResultJSON(cmd, diagnostic)
				}
				log := commandLogger(cmd)
				log.Info("WORKSPACE", "workspace relocation diagnostics")
				log.Detail("id", diagnostic.WorkspaceID)
				log.Detail("kind", diagnostic.Kind)
				log.Detail("registered root", diagnostic.RegisteredRoot)
				log.Detail("registered state", diagnostic.RegisteredState)
				log.Detail("destination root", diagnostic.DestinationRoot)
				log.Detail("destination state", diagnostic.DestinationState)
				if len(diagnostic.Resolutions) > 0 {
					values := make([]string, 0, len(diagnostic.Resolutions))
					for _, resolution := range diagnostic.Resolutions {
						values = append(values, string(resolution))
					}
					log.Detail("resolutions", strings.Join(values, ", "))
				}
				if diagnostic.Error != "" {
					log.Detail("error", diagnostic.Error)
				}
				return nil
			}
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
	cmd.Flags().StringVar(&destination, "destination", "", "diagnose relocation or duplicate identity state at a destination path")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func workspaceRelocateCommand() *cobra.Command {
	var resolve string
	cmd := &cobra.Command{
		Use:               "relocate <workspace_id> <path>",
		Aliases:           []string{"move"},
		Short:             "Rebind a registered workspace after its project directory moved",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeWorkspaceThenDirectory,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := workspaceServiceForCommand(cmd)
			request := application.WorkspaceRelocateRequest{
				ID:         args[0],
				Path:       args[1],
				Resolution: workspace.RelocationResolution(resolve),
			}
			result, err := service.Relocate(cmd.Context(), request)
			if err != nil && strings.TrimSpace(resolve) == "" && workspaceRelocateInteractive(cmd) {
				if conflict, ok := application.WorkspaceRelocationConflictOf(err); ok {
					resolution, cancelled, promptErr := promptWorkspaceRelocationResolution(cmd, conflict)
					if promptErr != nil {
						return promptErr
					}
					if cancelled {
						presenter := commandPresenter(cmd)
						presenter.StateSection(presentation.StatusInactive, "Workspace relocation cancelled")
						presenter.Complete("Cancelled")
						return nil
					}
					request.Resolution = resolution
					result, err = service.Relocate(cmd.Context(), request)
				}
			}
			if err != nil {
				return err
			}
			before, after := result.Value.Before, result.Value.After
			renderEntityMutationSuccess(cmd, "Workspace relocated", after.ID, presentation.Field{Label: "old id", Value: before.ID}, presentation.Field{Label: "old root", Value: before.Path}, presentation.Field{Label: "root", Value: after.Path})
			return nil
		},
	}
	cmd.Flags().StringVar(&resolve, "resolve", "", "resolve duplicate workspace state using destination, registered, or merge")
	return cmd
}

func workspaceRelocateInteractive(cmd *cobra.Command) bool {
	if cmd == nil || commandResultModeFor(cmd) != resultModeHuman || !commandTerminalCapabilities(cmd).Interactive {
		return false
	}
	input, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(input.Fd()))
}

func promptWorkspaceRelocationResolution(cmd *cobra.Command, conflict application.WorkspaceRelocationConflict) (workspace.RelocationResolution, bool, error) {
	if cmd == nil || cmd.InOrStdin() == nil {
		return "", false, errors.New("interactive workspace relocation requires terminal input and output")
	}
	session := commandProgressSession(cmd)
	scanner := bufio.NewScanner(cmd.InOrStdin())
	for attempts := 0; attempts < 3; attempts++ {
		var input string
		err := session.WithInput(func(presenter *presentation.Presenter) {
			if attempts == 0 {
				presenter.StateSection(presentation.StatusWarning, fmt.Sprintf("Duplicate workspace identity %s exists at both roots", conflict.WorkspaceID))
				presenter.Subsection("Resolution")
				presenter.NestedFields(
					presentation.Field{Label: "1 destination", Value: "Keep destination .cm state"},
					presentation.Field{Label: "2 registered", Value: "Keep registered .cm state"},
					presentation.Field{Label: "3 merge", Value: "Merge through typed workspace state rules"},
					presentation.Field{Label: "4 cancel", Value: "Make no changes"},
				)
			}
			presenter.Prompt("Select resolution [1-4], then press Enter")
		}, func() error {
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return err
				}
				return io.EOF
			}
			input = scanner.Text()
			return nil
		})
		if err != nil {
			return "", false, err
		}
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "1", "destination":
			return workspace.RelocationResolutionDestination, false, nil
		case "2", "registered":
			return workspace.RelocationResolutionRegistered, false, nil
		case "3", "merge":
			return workspace.RelocationResolutionMerge, false, nil
		case "4", "cancel", "c":
			return "", true, nil
		default:
			_ = session.WithInput(func(presenter *presentation.Presenter) {
				presenter.ChildStatus(presentation.StatusWarning, "Invalid selection")
			}, nil)
		}
	}
	return "", false, errors.New("invalid workspace relocation resolution selection")
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
		renderEntityMutationSuccess(cmd, "Workspace container created", value.ID, presentation.Field{Label: "name", Value: value.Name})
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
		renderEntityMutationSuccess(cmd, "Workspace container renamed", value.ID, presentation.Field{Label: "name", Value: value.Name})
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
		renderEntityMutationSuccess(cmd, "Workspace container deleted", value.ID, presentation.Field{Label: "name", Value: value.Name}, presentation.Field{Label: "workspaces", Value: "unchanged"})
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
		renderMutationSuccess(cmd, "Workspace container membership updated", presentation.Field{Label: "container", Value: value.ID}, presentation.Field{Label: action, Value: strings.Join(args[1:], ", ")})
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
			renderEntityMutationSuccess(cmd, "Allowed directory added", item.ID, presentation.Field{Label: "allow dir", Value: args[1]})
			return nil
		}},
		&cobra.Command{Use: "remove <workspace_id> <path>", Short: "Revoke an additional directory from a workspace", Args: cobra.ExactArgs(2), ValidArgsFunction: completeWorkspaceThenDirectory, RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).RemoveAllowDir(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			item := result.Value
			renderEntityMutationSuccess(cmd, "Allowed directory removed", item.ID, presentation.Field{Label: "allow dir", Value: args[1]})
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
			renderEntityMutationSuccess(cmd, "Workspace registered", item.ID, presentation.Field{Label: "root", Value: item.Path})
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
		presenter.Complete("Done")
		return
	}
	presenter.Section(fmt.Sprintf("Registered workspaces · %d", len(items)))
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
	presenter.Complete("Done")
}

func renderWorkspace(cmd *cobra.Command, presenter *presentation.Presenter, item application.WorkspaceView) {
	presenter.Frame("Workspace details")
	presenter.Subsection(item.ID)
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
	presenter.NestedFields(fields...)
	presenter.Complete("Done")
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
		presenter.Complete("Done")
		return
	}
	presenter.Section(fmt.Sprintf("Workspace containers · %d", len(values)))
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
	presenter.Complete("Done")
}

func renderWorkspaceContainer(presenter *presentation.Presenter, value application.WorkspaceContainerView) {
	presenter.Frame("Workspace container details")
	presenter.Subsection(value.ID)
	workspaces := "none"
	if len(value.WorkspaceIDs) > 0 {
		workspaces = strings.Join(value.WorkspaceIDs, ", ")
	}
	presenter.NestedFields(
		presentation.Field{Label: "name", Value: value.Name},
		presentation.Field{Label: "workspaces", Value: workspaces},
	)
	presenter.Complete("Done")
}

func renderWorkspaceAccess(presenter *presentation.Presenter, workspaceID string, allowDirs []string) {
	presenter.Frame("Allowed directories")
	presenter.Section(fmt.Sprintf("Allowed directories · %d", len(allowDirs)))
	presenter.Subsection(workspaceID)
	if len(allowDirs) == 0 {
		presenter.NestedFields(presentation.Field{Label: "allow dirs", Value: "none"})
	} else {
		presenter.NestedFields(presentation.Field{Label: "allow dirs", Value: strings.Join(allowDirs, ", ")})
	}
	presenter.Complete("Done")
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
			renderEntityMutationSuccess(cmd, "Workspace unregistered", item.ID, presentation.Field{Label: "root", Value: item.Path}, presentation.Field{Label: "project files", Value: "unchanged"}, presentation.Field{Label: "local .cm", Value: "unchanged"})
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
			fields := []presentation.Field{{Label: "root", Value: item.Path}, {Label: "project files", Value: "unchanged"}}
			renderEntityMutationSuccess(cmd, "Workspace local state deleted", item.ID, fields...)
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm destructive deletion of workspace-local state")
	return cmd
}
