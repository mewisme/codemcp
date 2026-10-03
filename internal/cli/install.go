package cli

import (
	"fmt"
	"strings"

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
		result, err := application.InstallCurrentContext(cmd.Context(), application.InstallCurrentOptions{
			Force: force, Observe: installCutoverObserver(cmd), ObserveIntegration: installIntegrationObserver(cmd),
		})
		if err != nil {
			return fmt.Errorf("install managed binary: %w", err)
		}
		message := "Binary installed"
		kind := presentation.StatusSuccess
		if result.AlreadyInstalled {
			message = "Already installed"
			kind = presentation.StatusInfo
		}
		renderSupplementalInstallSummary(cmd, result.Supplemental)
		renderMutationResult(cmd, kind, message,
			presentation.Field{Label: "version", Value: result.Version},
			presentation.Field{Label: "binary", Value: result.Binary},
			presentation.Field{Label: "command", Value: result.Command},
		)
		return nil
	}}
	cmd.Flags().BoolVar(&force, "force", false, "allow installing a development build")
	return cmd
}

func renderSupplementalInstallSummary(cmd *cobra.Command, result application.SupplementalBootstrapResult) {
	renderSupplementalInstallSummaryToSession(commandProgressSession(cmd), result)
}

func renderSupplementalInstallSummaryToSession(session *presentation.ProgressSession, result application.SupplementalBootstrapResult) {
	if session == nil {
		return
	}
	session.Append(func(p *presentation.Presenter) {
		p.Section("Install supplements")
		telemetryKind, telemetryState := presentation.StatusInfo, "disabled"
		if result.Telemetry.Enabled {
			if result.Telemetry.EndpointAvailable && result.Telemetry.IdentityPresent {
				telemetryKind, telemetryState = presentation.StatusSuccess, "ready"
			} else {
				telemetryKind, telemetryState = presentation.StatusWarning, "unavailable"
			}
		}
		p.ChildStatus(telemetryKind, "Telemetry · "+telemetryState)
		for _, warning := range result.Warnings {
			if supplementalIntegrationWarningDuplicate(warning, result.Integrations) {
				continue
			}
			if supplementalTelemetryWarning(warning) {
				p.NestedFields(presentation.Field{Label: "detail", Value: warning})
				continue
			}
			p.ChildStatus(presentation.StatusWarning, "Supplement warning")
			p.NestedFields(presentation.Field{Label: "detail", Value: warning})
		}

		for _, outcome := range result.Integrations {
			kind := supplementalOutcomeKind(outcome.State)
			message := strings.TrimSpace(outcome.Integration) + " · " + strings.TrimSpace(outcome.State)
			if source := supplementalOutcomeSource(outcome.Source); source != "" {
				message += " · " + source
			}
			p.ChildStatus(kind, message)
			if supplementalOutcomeHasDetail(outcome.State) && strings.TrimSpace(outcome.Detail) != "" {
				p.NestedFields(presentation.Field{Label: "detail", Value: outcome.Detail})
			}
			if (outcome.State == "failed" || outcome.State == "unavailable") && strings.TrimSpace(outcome.Retry) != "" {
				p.NestedFields(presentation.Field{Label: "retry", Value: outcome.Retry})
			}
		}
	})
}

func supplementalOutcomeKind(state string) presentation.StatusKind {
	switch strings.TrimSpace(state) {
	case "available", "installed":
		return presentation.StatusSuccess
	case "failed", "unavailable":
		return presentation.StatusWarning
	default:
		return presentation.StatusInfo
	}
}

func supplementalOutcomeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" || source == "disabled" || source == "unavailable" {
		return ""
	}
	return source
}

func supplementalOutcomeHasDetail(state string) bool {
	switch strings.TrimSpace(state) {
	case "skipped", "failed", "unavailable":
		return true
	default:
		return false
	}
}

func supplementalTelemetryWarning(warning string) bool {
	warning = strings.ToLower(strings.TrimSpace(warning))
	return strings.HasPrefix(warning, "telemetry ") || strings.HasPrefix(warning, "product telemetry ") || strings.Contains(warning, " telemetry event was not queued")
}

func supplementalIntegrationWarningDuplicate(warning string, outcomes []application.IntegrationEnsureResult) bool {
	warning = strings.TrimSpace(warning)
	for _, outcome := range outcomes {
		prefix := strings.TrimSpace(outcome.Integration) + " bootstrap failed:"
		if prefix != " bootstrap failed:" && strings.HasPrefix(warning, prefix) {
			return true
		}
	}
	return false
}
