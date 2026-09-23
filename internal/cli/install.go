package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	installpkg "go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/version"
)

func installCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{Use: "install", Aliases: []string{"ins"}, SuggestFor: []string{"ins"}, Short: "Install this binary into the managed versioned layout", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		log := commandLogger(cmd)
		logCommandStep(cmd, "INSTALL", "install.plan", "Preparing installation", logger.WithVerbose("version", version.Version), logger.WithDebug("force", force))
		result, err := installpkg.Install(installpkg.Options{Context: cmd.Context(), Version: version.Version, Force: force})
		if err != nil {
			return fmt.Errorf("install managed binary: %w", err)
		}
		if result.AlreadyInstalled {
			log.Notice("INSTALL", "install.already-installed", "Already installed")
		} else {
			log.Success("INSTALL", "binary installed")
		}
		log.Detail("version", result.Version)
		log.Detail("binary", result.Staged.Binary)
		log.Detail("current", result.Layout.CurrentBinary)
		log.Detail("command", result.Canonical.Path)
		return nil
	}}
	cmd.Flags().BoolVar(&force, "force", false, "allow installing a development build")
	return cmd
}
