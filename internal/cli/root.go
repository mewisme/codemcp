package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/version"
)

var root = newRootCommand()

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               cliUseName(),
		Short:             "CodeMCP workspace-bound local MCP server for ChatGPT",
		RunE:              runServer,
		Version:           version.Short(),
		SilenceErrors:     true,
		SilenceUsage:      true,
		PersistentPreRunE: prepareCommand,
	}
	addExposeFlag(cmd)
	addConfigDirFlag(cmd)
	addLoggingFlags(cmd)
	addTerminalPresentationFlags(cmd)
	cmd.AddCommand(
		installCommand(),
		upgradeCommand(),
		initCommand(),
		uninitCommand(),
		upCommand(),
		downCommand(),
		restartCommand(),
		logsCommand(),
		requestCommand(),
		tuiCommand(),
		configCommand(),
		authCommand(),
		workspaceCommand(),
		upstreamCommand(),
		mcpCommand(),
		tunnelCommand(),
		serveCommand(),
		statusCommand(),
		completionCommand(),
		internalServiceCommand(),
		&cobra.Command{Use: "version", Short: "Show the cm version and build information", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, args []string) {
			commandLogger(cmd).Notice("VERSION", "cli.version", version.String())
		}},
	)
	bindCanonicalCommandOperations(cmd)
	return cmd
}

func cliUseName() string { return "cm" }

func initCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration and authentication tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "INIT", "init.preparing", "Preparing configuration initialization")
			logCommandDebug(cmd, "INIT", "init.format.resolved", "Configuration format resolved", logger.WithDebug("format", "json"), logger.WithDebug("force", force))
			beginMutationProgress(cmd, "Initialize CodeMCP")
			result, err := application.Initialize(application.InitOptions{Context: cmd.Context(), Force: force})
			if err != nil {
				return err
			}
			fields := []presentation.Field{{Label: "config", Value: result.ConfigPath}, {Label: "format", Value: result.Format}}
			fields = append(fields, endpointPresentationFields(result.Config)...)
			fields = append(fields, presentation.Field{Label: "mcp token", Value: result.MCPToken}, presentation.Field{Label: "admin token", Value: result.AdminToken})
			renderMutationSuccess(cmd, "Initialize CodeMCP", "CodeMCP initialized", fields...)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rewrite config and rotate both tokens if already initialized")
	return cmd
}

func uninitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninit",
		Short: "Remove all local CodeMCP configuration and state",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := config.RootPath()
			logCommandStep(cmd, "UNINIT", "uninit.removing", "Removing local configuration and state", logger.WithVerbose("root", root))
			if err := application.UninitializeContext(cmd.Context(), root); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Uninitialize CodeMCP", "Local configuration and state removed", presentation.Field{Label: "root", Value: root})
			return nil
		},
	}
}

func purgeStoredSecrets(root string) error { return application.PurgeStoredSecrets(root) }
func removeConfigRoot(root string) error   { return application.RemoveConfigRoot(root) }

func authCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage MCP and admin authentication"}
	cmd.AddCommand(
		authKindCommand("mcp"),
		authKindCommand("admin"),
		authStatusCommand(),
	)
	return cmd
}

func authKindCommand(kind string) *cobra.Command {
	cmd := &cobra.Command{Use: kind, Short: "Manage " + kind + " authentication"}
	cmd.AddCommand(authCreateCommand(kind), authToggleCommand(kind, true), authToggleCommand(kind, false))
	return cmd
}

func authCreateCommand(kind string) *cobra.Command {
	return &cobra.Command{
		Use:   "create",
		Short: "Create or rotate the " + kind + " token",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "AUTH", "auth.token.rotating", "Creating or rotating authentication token", logger.WithVerbose("type", kind))
			token, _, err := application.RotateAuthToken(cmd.Context(), kind)
			if err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Authentication", "Token rotated", presentation.Field{Label: "type", Value: kind}, presentation.Field{Label: strings.ToUpper(kind), Value: token})
			return nil
		},
	}
}

func authToggleCommand(kind string, enabled bool) *cobra.Command {
	action := "disable"
	if enabled {
		action = "enable"
	}
	return &cobra.Command{
		Use:   action,
		Short: action + " " + kind + " authentication",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "AUTH", "auth.state.updating", "Updating authentication state", logger.WithVerbose("type", kind), logger.WithVerbose("enabled", enabled))
			if _, err := application.SetAuthEnabled(cmd.Context(), kind, enabled); err != nil {
				return err
			}
			state := "disabled"
			if enabled {
				state = "enabled"
			}
			renderMutationSuccess(cmd, "Authentication", state, presentation.Field{Label: "type", Value: kind})
			return nil
		},
	}
}

func authStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Aliases: []string{"st"},
		Short:   "Show authentication state without revealing token hashes",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "AUTH", "auth.status.loading", "Loading authentication state")
			status, err := application.GetAuthStatusContext(cmd.Context())
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter.Frame("Authentication")
				presenter.Section("MCP")
				presenter.Fields(
					presentation.Field{Label: "enabled", Value: status.MCPEnabled},
					presentation.Field{Label: "configured", Value: status.MCPConfigured},
					presentation.Field{Label: "legacy bearer", Value: status.MCPLegacyBearer},
				)
				presenter.Spacer()
				presenter.Section("Admin")
				presenter.Fields(
					presentation.Field{Label: "enabled", Value: status.AdminEnabled},
					presentation.Field{Label: "configured", Value: status.AdminConfigured},
				)
				presenter.FrameEnd("Status complete")
				return nil
			}
			presenter.Section("Authentication")
			presenter.Subsection("MCP")
			presenter.NestedFields(
				presentation.Field{Label: "enabled", Value: status.MCPEnabled},
				presentation.Field{Label: "configured", Value: status.MCPConfigured},
				presentation.Field{Label: "legacy bearer", Value: status.MCPLegacyBearer},
			)
			presenter.Spacer()
			presenter.Subsection("Admin")
			presenter.NestedFields(
				presentation.Field{Label: "enabled", Value: status.AdminEnabled},
				presentation.Field{Label: "configured", Value: status.AdminConfigured},
			)
			return nil
		},
	}
}

func Execute() error {
	return executeCommand(root)
}

func executeCommand(command *cobra.Command) error {
	started := time.Now()
	executed, err := command.ExecuteC()
	if executed == nil {
		executed = command
	}
	if err != nil {
		closeCommandProgress(executed, err)
		logCommandFailure(executed, err, started)
		closeCommandLogger(executed)
		return err
	}
	closeCommandProgress(executed, nil)
	logCommandCompleted(executed, started)
	closeCommandLogger(executed)
	return nil
}
