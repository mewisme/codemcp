package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlplane"
	"go.mewis.me/codemcp/internal/logger"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var processCommandArgs = func() []string { return append([]string(nil), os.Args[1:]...) }

func addConfigDirFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().String("config-dir", "", fmt.Sprintf("config/state directory (env: %s)", configformat.EnvConfigDir))
}

func configureConfigDir(cmd *cobra.Command) error {
	value, err := cmd.Root().PersistentFlags().GetString("config-dir")
	if err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		value = os.Getenv(configformat.EnvConfigDir)
	}
	return configformat.SetRootPath(value)
}

func prepareCommand(cmd *cobra.Command, args []string) error {
	if err := validateLoggingFlags(cmd, args); err != nil {
		return err
	}
	cmd.SetContext(tracepkg.WithObserver(cmd.Context(), commandTraceObserver(cmd)))
	logCommandStart(cmd, args)
	if controlplane.ToolContextActive() && !controlplane.IsReadOnlyPath(relativeCommandPath(cmd)) {
		if err := verifyControlApproval(cmd.Context(), cmd.CommandPath(), processCommandArgs()); err != nil {
			return err
		}
	}
	if err := configureConfigDir(cmd); err != nil {
		return err
	}
	commandLogger(cmd).Diagnostic(logger.Info, "CLI", "cli.command.configured", "Command environment configured", logger.WithDebug("config", config.RootPath()))
	return nil
}

func verifyControlApproval(ctx context.Context, commandPath string, actualArgs []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	capability := strings.TrimSpace(os.Getenv(controlplane.ControlApprovalEnv))
	if capability == "" || !controlplane.ApprovalEligibleArgs(actualArgs) {
		return fmt.Errorf("control-plane command denied from MCP tool execution context: %s", commandPath)
	}
	if err := requestRuntimeCLIApproval(ctx, capability, actualArgs); err != nil {
		return fmt.Errorf("control-plane command denied from MCP tool execution context: %s: approval verification failed: %w", commandPath, err)
	}
	return nil
}

func relativeCommandPath(cmd *cobra.Command) string {
	return strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()))
}
