package cli

import (
	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
)

const telemetrySuppressedAnnotation = "cm.product-telemetry-suppressed"

func telemetryCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "telemetry", Short: "Manage anonymous product telemetry"}
	cmd.AddCommand(
		telemetryStatusCommand(false),
		telemetryToggleCommand(true),
		telemetryToggleCommand(false),
		telemetryStatusCommand(true),
	)
	return cmd
}

func telemetryStatusCommand(show bool) *cobra.Command {
	var asJSON bool
	use := "status"
	short := "Show effective product telemetry state"
	if show {
		use = "show"
		short = "Show product telemetry privacy and endpoint metadata"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := application.NewTelemetryService()
			status, err := service.Status(cmd.Context())
			if show {
				status, err = service.Show(cmd.Context())
			}
			if err != nil {
				return err
			}
			if asJSON {
				return writeResultJSON(cmd, status)
			}
			presenter := commandPresenter(cmd)
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter.Frame("Product telemetry")
			} else {
				presenter.Section("Product telemetry")
			}
			fields := []presentation.Field{
				{Label: "persisted", Value: enabledLabel(status.PersistedEnabled)},
				{Label: "effective", Value: enabledLabel(status.EffectiveEnabled)},
				{Label: "source", Value: string(status.Source)},
			}
			if status.EnvironmentOverride {
				fields = append(fields, presentation.Field{Label: "override", Value: "CM_TELEMETRY"})
			}
			if show {
				fields = append(fields,
					presentation.Field{Label: "endpoint", Value: presenceLabel(status.EndpointAvailable)},
					presentation.Field{Label: "host", Value: valueOrUnavailable(status.EndpointHost)},
					presentation.Field{Label: "product", Value: valueOrUnavailable(status.Product)},
					presentation.Field{Label: "anonymous id", Value: presenceLabel(status.IdentityPresent)},
				)
			}
			presenter.Fields(fields...)
			presenter.Complete("Status complete")
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	markTelemetrySuppressed(cmd)
	return cmd
}

func telemetryToggleCommand(enabled bool) *cobra.Command {
	var asJSON bool
	use := "disable"
	short := "Disable anonymous product telemetry"
	success := "Product telemetry disabled"
	if enabled {
		use = "enable"
		short = "Enable anonymous product telemetry"
		success = "Product telemetry enabled"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := application.NewTelemetryService()
			var (
				status application.TelemetryStatus
				err    error
			)
			if enabled {
				status, err = service.Enable(cmd.Context())
			} else {
				status, err = service.Disable(cmd.Context())
			}
			if err != nil {
				return err
			}
			if asJSON {
				return writeResultJSON(cmd, status)
			}
			renderMutationSuccess(cmd, success,
				presentation.Field{Label: "persisted", Value: enabledLabel(status.PersistedEnabled)},
				presentation.Field{Label: "effective", Value: enabledLabel(status.EffectiveEnabled)},
				presentation.Field{Label: "source", Value: string(status.Source)},
			)
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	markScopedSettings(cmd, "telemetry.enabled")
	markTelemetrySuppressed(cmd)
	return cmd
}

func markTelemetrySuppressed(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[telemetrySuppressedAnnotation] = "true"
}

func productTelemetrySuppressed(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[telemetrySuppressedAnnotation] == "true"
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func presenceLabel(present bool) string {
	if present {
		return "present"
	}
	return "not present"
}

func valueOrUnavailable(value string) string {
	if value == "" {
		return "unavailable"
	}
	return value
}
