package cli

import (
	"context"
	"strconv"
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

type executeCommandLifecycleKey struct{}

func commandUsesExecuteLifecycle(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Context() == nil {
		return false
	}
	value, _ := cmd.Context().Value(executeCommandLifecycleKey{}).(bool)
	return value
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               cliUseName(),
		Short:             "A secure, workspace-bound MCP bridge connecting ChatGPT, Claude, and other AI agents to your machine.",
		Version:           version.Short(),
		SilenceErrors:     true,
		SilenceUsage:      true,
		PersistentPreRunE: prepareCommand,
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			if !commandUsesExecuteLifecycle(cmd) {
				closeCommandProgress(cmd, nil)
			}
		},
	}
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
		llmCommand(),
		tuiCommand(),
		configCommand(),
		authCommand(),
		toolsCommand(),
		executionCommand(),
		processCommand(),
		workspaceCommand(),
		skillsCommand(),
		networkCommand(),
		activityCommand(),
		promptCommand(),
		upstreamCommand(),
		mcpCommand(),
		tunnelCommand(),
		httpSettingsCommand(),
		permissionsSettingsCommand(),
		shellSettingsCommand(),
		notificationSettingsCommand(),
		telemetryCommand(),
		telegramSettingsCommand(),
		integrationSettingsCommand(),
		serveCommand(),
		statusCommand(),
		healthCommand(),
		doctorCommand(),
		agentCommand(),
		completionCommand(),
		internalServiceCommand(),
		&cobra.Command{Use: "version", Short: "Show the cm version and build information", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, args []string) {
			if format, _ := commandLogFormat(cmd); format == logger.FormatJSON {
				commandLogger(cmd).Notice("VERSION", "cli.version", version.String())
				return
			}
			logCommandDebug(cmd, "VERSION", "cli.version.resolved", "Version resolved", logger.WithDebug("version", version.String()))
			presenter := commandPresenter(cmd)
			presenter.StateSection(presentation.StatusInfo, version.String())
		}},
	)
	bindCanonicalScopedSettings(cmd)
	bindCommandAliases(cmd)
	bindAliasHelpCompletion(cmd)
	bindCanonicalCommandOperations(cmd)
	bindCommandPresentation(cmd)
	if commandPresentationTitle(cmd) == "" {
		setCommandPresentationTitle(cmd, defaultCommandPresentationTitle(cmd))
	}
	return cmd
}

func cliUseName() string { return "cm" }

func initCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration and authentication tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandVerbose(cmd, "INIT", "init.preparing", "Preparing configuration initialization")
			logCommandDebug(cmd, "INIT", "init.format.resolved", "Configuration format resolved", logger.WithDebug("format", "json"), logger.WithDebug("force", force))
			result, err := application.Initialize(application.InitOptions{Context: cmd.Context(), Force: force})
			if err != nil {
				return err
			}
			fields := []presentation.Field{{Label: "config", Value: result.ConfigPath}, {Label: "format", Value: result.Format}}
			fields = append(fields, endpointPresentationFields(result.Config)...)
			fields = append(fields, presentation.Field{Label: "mcp token", Value: result.MCPToken}, presentation.Field{Label: "admin token", Value: result.AdminToken})
			renderMutationSuccess(cmd, "CodeMCP initialized", fields...)
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
			logCommandVerbose(cmd, "UNINIT", "uninit.removing", "Removing local configuration and state", logger.WithVerbose("root", root))
			if err := application.UninitializeContext(cmd.Context(), root); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Local configuration and state removed", presentation.Field{Label: "root", Value: root})
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
	if kind == "mcp" {
		legacy := &cobra.Command{Use: "legacy", Short: "Manage legacy MCP compatibility"}
		bearer := &cobra.Command{Use: "bearer", Short: "Manage legacy MCP bearer compatibility"}
		bearer.AddCommand(
			scopedToggleCommand("enable", "Enable legacy MCP bearer compatibility", "Legacy MCP bearer compatibility enabled", "http.mcp.auth.legacy_bearer", true),
			scopedToggleCommand("disable", "Disable legacy MCP bearer compatibility", "Legacy MCP bearer compatibility disabled", "http.mcp.auth.legacy_bearer", false),
		)
		legacy.AddCommand(bearer)
		cmd.AddCommand(legacy)
	}
	return cmd
}

func authCreateCommand(kind string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create or rotate the " + kind + " token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandVerbose(cmd, "AUTH", "auth.token.rotating", "Creating or rotating authentication token", logger.WithVerbose("type", kind))
			key := "http.mcp.auth.token"
			if kind == "admin" {
				key = "http.admin.auth.token"
			}
			result, err := settingService().Rotate(cmd.Context(), key)
			if err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Token rotated", presentation.Field{Label: "type", Value: kind}, presentation.Field{Label: strings.ToUpper(kind), Value: result.Value})
			return nil
		},
	}
	key := "http.mcp.auth.token"
	if kind == "admin" {
		key = "http.admin.auth.token"
	}
	return markScopedSettings(cmd, key)
}

func authToggleCommand(kind string, enabled bool) *cobra.Command {
	action := "disable"
	if enabled {
		action = "enable"
	}
	cmd := &cobra.Command{
		Use:   action,
		Short: action + " " + kind + " authentication",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandVerbose(cmd, "AUTH", "auth.state.updating", "Updating authentication state", logger.WithVerbose("type", kind), logger.WithVerbose("enabled", enabled))
			key := "http.mcp.auth.enabled"
			if kind == "admin" {
				key = "http.admin.auth.enabled"
			}
			if err := scopedSettingSet(cmd, key, strconv.FormatBool(enabled)); err != nil {
				return err
			}
			state := "disabled"
			if enabled {
				state = "enabled"
			}
			renderMutationSuccess(cmd, state, presentation.Field{Label: "type", Value: kind})
			return nil
		},
	}
	key := "http.mcp.auth.enabled"
	if kind == "admin" {
		key = "http.admin.auth.enabled"
	}
	return markScopedSettings(cmd, key)
}

func authStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication state without revealing token hashes",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandVerbose(cmd, "AUTH", "auth.status.loading", "Loading authentication state")
			status, err := application.GetAuthStatusContext(cmd.Context())
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			if commandResultModeFor(cmd) == resultModeHuman {
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
				return nil
			}
			presenter.Section("Authentication")
			presenter.SubsectionItem("MCP", false)
			presenter.NestedFields(
				presentation.Field{Label: "enabled", Value: status.MCPEnabled},
				presentation.Field{Label: "configured", Value: status.MCPConfigured},
				presentation.Field{Label: "legacy bearer", Value: status.MCPLegacyBearer},
			)
			presenter.Spacer()
			presenter.SubsectionItem("Admin", true)
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
	originalContext := command.Context()
	if originalContext == nil {
		originalContext = context.Background()
	}
	executionContext := application.WithOperationInterface(context.WithValue(originalContext, executeCommandLifecycleKey{}, true), application.OperationInterfaceCLI)
	command.SetContext(executionContext)
	executed, err := command.ExecuteC()
	if executed == nil {
		executed = command
	}
	if commandHelpOnly(executed) {
		command.SetContext(originalContext)
		return err
	}
	recordCLIProductUsage(executionContext, executed, err, started)
	command.SetContext(originalContext)
	if err != nil {
		if !renderPresentationExemptFailure(executed, err) {
			ensureCommandPresentationFallback(executed)
		}
		logCommandFailure(executed, err, started)
		closeCommandProgress(executed, err)
		closeCommandLogger(executed)
		return err
	}
	logCommandCompleted(executed, started)
	closeCommandProgress(executed, nil)
	closeCommandLogger(executed)
	return nil
}
