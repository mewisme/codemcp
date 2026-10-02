package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/version"
)

func installCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{Use: "install", Short: "Install this binary into the managed versioned layout", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "INSTALL", "install.plan", "Preparing installation", logger.WithVerbose("version", version.Version), logger.WithDebug("force", force))
		result, err := application.InstallCurrentContext(cmd.Context(), application.InstallCurrentOptions{Force: force, Observe: installCutoverObserver(cmd)})
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
			presentation.Field{Label: "binary", Value: result.Binary},
			presentation.Field{Label: "command", Value: result.Command},
		)
		renderSupplementalInstallSummary(cmd, result.Supplemental)
		return nil
	}}
	cmd.Flags().BoolVar(&force, "force", false, "allow installing a development build")
	return cmd
}

func renderSupplementalInstallSummary(cmd *cobra.Command, result application.SupplementalBootstrapResult) {
	session := commandProgressSession(cmd)
	for _, outcome := range result.Integrations {
		if outcome.State != "failed" && outcome.State != "unavailable" {
			continue
		}
		message := outcome.Integration + " bootstrap " + outcome.State
		if outcome.Detail != "" {
			message += " · " + outcome.Detail
		}
		if outcome.Retry != "" {
			message += " · retry: " + outcome.Retry
		}
		session.Append(func(p *presentation.Presenter) { p.ChildStatus(presentation.StatusWarning, message) })
	}
	for _, warning := range result.Warnings {
		session.Append(func(p *presentation.Presenter) { p.ChildStatus(presentation.StatusWarning, warning) })
	}
}
