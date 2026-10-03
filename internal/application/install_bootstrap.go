package application

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mewis.me/codemcp/internal/config"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

const InstallIntegrationsEnv = "CM_INSTALL_INTEGRATIONS"

type IntegrationEnsureResult struct {
	Integration string `json:"integration"`
	State       string `json:"state"`
	Source      string `json:"source,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Retry       string `json:"retry,omitempty"`
}

type IntegrationEnsureEvent struct {
	Integration string `json:"integration"`
	Phase       string `json:"phase"`
	State       string `json:"state"`
	Message     string `json:"message,omitempty"`
}

type PostInstallBootstrapOptions struct {
	SkipMissingIntegrations bool
	Observe                 func(IntegrationEnsureEvent)
}

func resolvePostInstallBootstrapOptions(options PostInstallBootstrapOptions, lookup func(string) (string, bool)) (PostInstallBootstrapOptions, error) {
	if options.SkipMissingIntegrations {
		return options, nil
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}
	raw, ok := lookup(InstallIntegrationsEnv)
	if !ok {
		return options, nil
	}
	enabled, valid := config.ParseEnvironmentBool(raw)
	if !valid {
		return PostInstallBootstrapOptions{}, fmt.Errorf("%s must be one of 1, true, yes, on, 0, false, no, off", InstallIntegrationsEnv)
	}
	options.SkipMissingIntegrations = !enabled
	return options, nil
}

type SupplementalBootstrapResult struct {
	Telemetry       TelemetryBootstrapResult      `json:"telemetry"`
	TelemetrySource config.TelemetryEnabledSource `json:"telemetry_source,omitempty"`
	UsageNotice     bool                          `json:"usage_notice,omitempty"`
	Integrations    []IntegrationEnsureResult     `json:"integrations"`
	Warnings        []string                      `json:"warnings,omitempty"`
}

type supplementalRecorder interface {
	Record(context.Context, producttelemetry.EventName, producttelemetry.Usage) bool
	Close(context.Context)
}

type postInstallCoordinator struct {
	Telemetry *TelemetryService
	RTK       *RTKService
	CodeGraph *CodeGraphService
	Recorder  func(config.Config, bool, *producttelemetry.IdentityStore) (supplementalRecorder, error)
}

func RunPostInstallBootstrap(ctx context.Context) SupplementalBootstrapResult {
	return runPostInstallBootstrapWithOptions(ctx, PostInstallBootstrapOptions{})
}

func runPostInstallBootstrapWithOptions(ctx context.Context, options PostInstallBootstrapOptions) SupplementalBootstrapResult {
	return newPostInstallCoordinator().Run(ctx, options)
}

func newPostInstallCoordinator() *postInstallCoordinator {
	return &postInstallCoordinator{
		Telemetry: NewTelemetryService(),
		RTK:       NewRTKService(),
		CodeGraph: NewCodeGraphService(),
		Recorder: func(cfg config.Config, configured bool, identity *producttelemetry.IdentityStore) (supplementalRecorder, error) {
			effective := config.ResolveTelemetryEnabled(cfg, configured)
			return producttelemetry.NewRecorder(producttelemetry.RecorderOptions{
				Enabled: effective.Enabled, Endpoint: producttelemetry.Endpoint, Identity: identity,
			})
		},
	}
}

func (coordinator *postInstallCoordinator) Run(ctx context.Context, _ PostInstallBootstrapOptions) SupplementalBootstrapResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if coordinator == nil {
		coordinator = newPostInstallCoordinator()
	}
	result := SupplementalBootstrapResult{Integrations: []IntegrationEnsureResult{}}
	telemetryService := coordinator.Telemetry
	if telemetryService == nil {
		telemetryService = NewTelemetryService()
	}
	status, statusErr := telemetryService.Status(ctx)
	if statusErr != nil {
		result.Warnings = append(result.Warnings, "telemetry bootstrap unavailable: "+statusErr.Error())
	} else {
		result.TelemetrySource = status.Source
		result.UsageNotice = status.EffectiveEnabled
		bootstrap, err := telemetryService.Bootstrap(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, "telemetry bootstrap failed: "+err.Error())
		} else {
			result.Telemetry = bootstrap
		}
	}

	ensure := []struct {
		name string
		run  func(context.Context) (IntegrationEnsureResult, error)
	}{
		{name: "rtk", run: func(ctx context.Context) (IntegrationEnsureResult, error) {
			service := coordinator.RTK
			if service == nil {
				service = NewRTKService()
			}
			return service.EnsureAvailable(ctx)
		}},
		{name: "codegraph", run: func(ctx context.Context) (IntegrationEnsureResult, error) {
			service := coordinator.CodeGraph
			if service == nil {
				service = NewCodeGraphService()
			}
			return service.EnsureAvailable(ctx)
		}},
	}
	for _, item := range ensure {
		outcome, err := item.run(ctx)
		if outcome.Integration == "" {
			outcome.Integration = item.name
		}
		if err != nil {
			outcome.State = "failed"
			if outcome.Detail == "" {
				outcome.Detail = err.Error()
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s bootstrap failed: %v", item.name, err))
		}
		result.Integrations = append(result.Integrations, outcome)
	}

	if statusErr == nil && result.Telemetry.Enabled && result.Telemetry.EndpointAvailable && result.Telemetry.IdentityPresent {
		cfg, cfgErr := config.Load()
		source, sourceErr := config.Source()
		if cfgErr == nil && sourceErr == nil && coordinator.Recorder != nil {
			recorder, err := coordinator.Recorder(cfg, source.Exists, telemetryService.Identity)
			if err != nil {
				result.Warnings = append(result.Warnings, "product telemetry emission unavailable: "+err.Error())
			} else if recorder != nil {
				if !recorder.Record(ctx, producttelemetry.EventInstallCompleted, producttelemetry.Usage{
					Interface: producttelemetry.InterfaceCLI, Command: "install", Feature: "codemcp", Success: true, OmitDuration: true,
				}) {
					result.Warnings = append(result.Warnings, "product telemetry install event was not queued")
				}
				for _, outcome := range result.Integrations {
					success := outcome.State != "failed" && outcome.State != "unavailable"
					if !recorder.Record(ctx, producttelemetry.EventIntegrationBootstrapCompleted, producttelemetry.Usage{
						Interface: producttelemetry.InterfaceCLI, Command: "install", Feature: outcome.Integration, Success: success, OmitDuration: true,
					}) {
						result.Warnings = append(result.Warnings, outcome.Integration+" telemetry event was not queued")
					}
				}
				closeCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
				recorder.Close(closeCtx)
				cancel()
			}
		}
	}
	return result
}
