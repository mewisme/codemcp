package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
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
			log := commandLogger(cmd)
			log.Success("WORKSPACE", "workspace relocated")
			log.Detail("old id", before.ID)
			log.Detail("id", after.ID)
			log.Detail("old root", before.Path)
			log.Detail("root", after.Path)
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
		log := commandLogger(cmd)
		log.Success("WORKSPACE", "workspace containers loaded", "count", len(values))
		for _, value := range values {
			log.Detail(value.ID, value.Name+" · "+strconv.Itoa(len(value.WorkspaceIDs))+" workspaces")
		}
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
		log := commandLogger(cmd)
		log.Success("WORKSPACE", "workspace container created")
		log.Detail("id", value.ID)
		log.Detail("name", value.Name)
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
		log := commandLogger(cmd)
		log.Info("WORKSPACE", "workspace container details")
		log.Detail("id", value.ID)
		log.Detail("name", value.Name)
		if len(value.WorkspaceIDs) == 0 {
			log.Detail("workspaces", "none")
		} else {
			log.Detail("workspaces", value.WorkspaceIDs)
		}
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
		log := commandLogger(cmd)
		log.Success("WORKSPACE", "workspace container renamed")
		log.Detail("id", value.ID)
		log.Detail("name", value.Name)
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
		log := commandLogger(cmd)
		log.Success("WORKSPACE", "workspace container deleted")
		log.Detail("id", value.ID)
		log.Detail("name", value.Name)
		log.Detail("workspaces", "unchanged")
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
		log := commandLogger(cmd)
		log.Success("WORKSPACE", "workspace container membership updated")
		log.Detail("container", value.ID)
		log.Detail(action, args[1:])
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
		log := commandLogger(cmd)
		log.Success("WORKSPACE", "allowed directories loaded", "count", len(allowDirs))
		log.Detail("workspace", args[0])
		if len(allowDirs) == 0 {
			log.Detail("allow dirs", "none")
		} else {
			log.Detail("allow dirs", allowDirs)
		}
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
			log := commandLogger(cmd)
			log.Success("WORKSPACE", "allowed directory added")
			log.Detail("id", item.ID)
			log.Detail("allow_dir", args[1])
			return nil
		}},
		&cobra.Command{Use: "remove <workspace_id> <path>", Short: "Revoke an additional directory from a workspace", Args: cobra.ExactArgs(2), ValidArgsFunction: completeWorkspaceThenDirectory, RunE: func(cmd *cobra.Command, args []string) error {
			result, err := workspaceServiceForCommand(cmd).RemoveAllowDir(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			item := result.Value
			log := commandLogger(cmd)
			log.Success("WORKSPACE", "allowed directory removed")
			log.Detail("id", item.ID)
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
			log := commandLogger(cmd)
			log.Success("WORKSPACE", "workspace registered")
			log.Detail("id", item.ID)
			log.Detail("root", item.Path)
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
			log := commandLogger(cmd)
			log.Success("WORKSPACE", "registered workspaces loaded", "count", len(items))
			for _, item := range items {
				value := item.Path
				if !item.Available {
					value += " · unavailable: " + item.Error
				} else {
					hygiene := workspace.InspectLocalStateGitHygiene(cmd.Context(), item.Path)
					if hygiene.Tracked {
						value += " · git hygiene: tracked .cm; " + hygiene.Guidance
					} else if hygiene.Degraded || !hygiene.Protected {
						value += " · git hygiene: degraded"
					}
				}
				log.Detail(item.ID, value)
			}
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
			log := commandLogger(cmd)
			log.Info("WORKSPACE", "workspace details")
			log.Detail("id", item.ID)
			log.Detail("root", item.Path)
			log.Detail("available", item.Available)
			if item.Error != "" {
				log.Detail("error", item.Error)
			}
			if item.Available {
				hygiene := workspace.InspectLocalStateGitHygiene(cmd.Context(), item.Path)
				state := "healthy"
				if hygiene.Tracked {
					state = "tracked .cm"
				} else if hygiene.Degraded || !hygiene.Protected {
					state = "degraded"
				}
				log.Detail("git hygiene", state)
				if hygiene.Guidance != "" {
					log.Detail("git guidance", hygiene.Guidance)
				}
			}
			if len(item.AllowDirs) == 0 {
				log.Detail("allow dirs", "none")
			} else {
				log.Detail("allow dirs", item.AllowDirs)
			}
			if len(item.LegacyIDs) > 0 {
				log.Detail("legacy ids", item.LegacyIDs)
			}
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
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
			log := commandLogger(cmd)
			log.Success("WORKSPACE", "workspace unregistered")
			log.Detail("id", item.ID)
			log.Detail("root", item.Path)
			log.Detail("project files", "unchanged")
			log.Detail("local .cm", "unchanged")
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
			log := commandLogger(cmd)
			log.Success("WORKSPACE", "workspace local state deleted")
			if item.ID != "" {
				log.Detail("id", item.ID)
			}
			log.Detail("root", item.Path)
			log.Detail("project files", "unchanged")
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm destructive deletion of workspace-local state")
	return cmd
}
