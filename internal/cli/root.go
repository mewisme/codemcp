package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
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
	return cmd
}

func cliUseName() string { return "cm" }

func initCommand() *cobra.Command {
	var force bool
	var formatName string
	var jsonFormat, yamlFormat, tomlFormat bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration and authentication tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "INIT", "init.preparing", "Preparing configuration initialization")
			options := configOutputOptions{format: formatName, json: jsonFormat, yaml: yamlFormat, toml: tomlFormat}
			format, selected, err := resolveConfigOutputFormat(options)
			if err != nil {
				return err
			}
			logCommandDebug(cmd, "INIT", "init.format.resolved", "Configuration format resolved", logger.WithDebug("format", format), logger.WithDebug("selected", selected), logger.WithDebug("force", force))
			result, err := application.Initialize(application.InitOptions{Context: cmd.Context(), Force: force, Format: format, FormatSelected: selected})
			if err != nil {
				return err
			}
			log := commandLogger(cmd)
			log.Success("INIT", "configuration created")
			log.Detail("config", result.ConfigPath)
			log.Detail("format", result.Format)
			logEndpointDetails(log, result.Config)
			log.Detail("mcp token", result.MCPToken)
			log.Detail("admin token", result.AdminToken)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rewrite config and rotate both tokens if already initialized")
	cmd.Flags().StringVar(&formatName, "format", "", "storage format: json, yaml, or toml")
	cmd.Flags().BoolVar(&jsonFormat, "json", false, "use JSON storage")
	cmd.Flags().BoolVar(&yamlFormat, "yaml", false, "use YAML storage")
	cmd.Flags().BoolVar(&tomlFormat, "toml", false, "use TOML storage")
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
			log := commandLogger(cmd)
			log.Success("UNINIT", "local configuration and state removed")
			log.Detail("root", root)
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
			log := commandLogger(cmd)
			log.Success("AUTH", "token rotated", "type", kind)
			log.Detail(strings.ToUpper(kind), token)
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
			commandLogger(cmd).Success("AUTH", state, "type", kind)
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
			log := commandLogger(cmd)
			log.Info("AUTH", "authentication status")
			log.Detail("mcp", fmt.Sprintf("enabled=%t configured=%t legacy_bearer=%t", status.MCPEnabled, status.MCPConfigured, status.MCPLegacyBearer))
			log.Detail("admin", fmt.Sprintf("enabled=%t configured=%t", status.AdminEnabled, status.AdminConfigured))
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
		logCommandFailure(executed, err, started)
		closeCommandLogger(executed)
		return err
	}
	logCommandCompleted(executed, started)
	closeCommandLogger(executed)
	return nil
}
