package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	installpkg "go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/version"
)

func installCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{Use: "install", Short: "Install this binary into the managed versioned layout", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "INSTALL", "install.plan", "Preparing installation", logger.WithVerbose("version", version.Version), logger.WithDebug("force", force))
		result, err := installpkg.Install(installpkg.Options{Context: cmd.Context(), Version: version.Version, Force: force})
		if err != nil {
			return fmt.Errorf("install managed binary: %w", err)
		}
		message := "Binary installed"
		kind := presentation.StatusSuccess
		if result.AlreadyInstalled {
			message = "Already installed"
			kind = presentation.StatusInfo
		}
		renderMutationResult(cmd, kind, message,
			presentation.Field{Label: "version", Value: result.Version},
			presentation.Field{Label: "binary", Value: result.Staged.Binary},
			presentation.Field{Label: "current", Value: result.Layout.CurrentBinary},
			presentation.Field{Label: "command", Value: result.Canonical.Path},
		)
		return nil
	}}
	cmd.Flags().BoolVar(&force, "force", false, "allow installing a development build")
	return cmd
}
