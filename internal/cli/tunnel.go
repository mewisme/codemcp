package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/telemetry"
	"go.mewis.me/codemcp/internal/tools"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

func tunnelCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "tunnel", Short: "Manage the OpenAI Secure MCP Tunnel"}
	cmd.AddCommand(tunnelConfigReadCommand(), tunnelAdminCommand(), tunnelRuntimeKeyCommand(), tunnelListCommand(), tunnelGetCommand(), tunnelUseCommand(), tunnelCreateCommand(), tunnelUpdateCommand(), tunnelDeleteCommand(), tunnelStatusCommand(), tunnelSyncCommand(), tunnelConfigureCommand(), tunnelToggleCommand(true), tunnelToggleCommand(false), tunnelRunCommand())
	return cmd
}

func tunnelRuntimeKeyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Manage the stored OpenAI tunnel runtime API key"}
	var fromEnv string
	set := &cobra.Command{Use: "set [runtime-api-key]", Short: "Store the OpenAI tunnel runtime API key", Long: "Store the OpenAI tunnel runtime API key.\n\n" + protectedArgumentWarning, Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		explicit := ""
		if len(args) == 1 {
			explicit = args[0]
		}
		secret, err := readProtectedInput(cmd, protectedInputOptions{Label: "Runtime API key", Explicit: explicit, ExplicitSet: len(args) == 1, FromEnv: fromEnv})
		if err != nil {
			return err
		}
		defer zeroProtectedString(&secret)
		if _, err := settingService().Set(cmd.Context(), "tunnel.api_key", secret); err != nil {
			return err
		}
		renderMutationSuccess(cmd, "Runtime API key saved")
		return nil
	}}
	set.Flags().StringVar(&fromEnv, "from-env", "", "Read the runtime API key from this environment variable")
	remove := &cobra.Command{Use: "remove", Short: "Remove the stored OpenAI tunnel runtime API key", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if _, err := settingService().Unset(cmd.Context(), "tunnel.api_key"); err != nil {
			return err
		}
		renderMutationSuccess(cmd, "Runtime API key removed")
		return nil
	}}
	cmd.AddCommand(markScopedSettings(set, "tunnel.api_key"), markScopedSettings(remove, "tunnel.api_key"))
	return cmd
}

func normalizeTunnelIDs(values []string) []string {
	return application.NormalizeTunnelIDs(values)
}

func tunnelStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "status", Short: "Show tunnel configuration, runtime state, and metadata", Args: cobra.NoArgs, RunE: runTunnelStatus}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func runTunnelStatus(cmd *cobra.Command, _ []string) error {
	logCommandVerbose(cmd, "TUNNEL", "tunnel.status.loading", "Loading tunnel configuration")
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load tunnel configuration: %w", err)
	}
	status := fetchTunnelStatus(cmd.Context(), cfg.Tunnel)
	if status.MetadataError != "" {
		logCommandDebug(cmd, "TUNNEL", "tunnel.metadata.cached-unavailable", "Cached tunnel metadata unavailable", logger.WithDebug("error", status.MetadataError))
	}
	logCommandVerbose(cmd, "TUNNEL", "tunnel.runtime.inspecting", "Inspecting running tunnel state")
	runtimeCtx, cancel := context.WithTimeout(cmd.Context(), time.Second)
	runtimeStatus, runtimeRunning, err := managedRuntimeStatus(runtimeCtx)
	cancel()
	if err != nil {
		return err
	}
	if runtimeRunning {
		status.Running = runtimeStatus.TunnelRunning
		status.Ready = runtimeStatus.TunnelReady
		status.Restarting = runtimeStatus.TunnelRestarting
		status.LastError = runtimeStatus.TunnelLastError
		if status.ID == "" {
			status.ID = runtimeStatus.TunnelID
		}
	}
	status = tunnel.PublicStatus(status)
	if commandResultModeFor(cmd) == resultModeJSON {
		return writeResultJSON(cmd, status)
	}
	if runtimeRunning && transientTunnelState(tunnelCLIState(cfg.Tunnel, status, true)) && commandAnimationEligible(cmd) {
		runtimeStatus = animateRuntimeTunnelState(cmd, runtimeStatus, statusTunnelWatchTimeout)
		status.Running = runtimeStatus.TunnelRunning
		status.Ready = runtimeStatus.TunnelReady
		status.Restarting = runtimeStatus.TunnelRestarting
		status.LastError = runtimeStatus.TunnelLastError
	}
	verbose, _ := commandLogMode(cmd)
	renderTunnelStatusText(commandPresenter(cmd), cfg.Tunnel, status, runtimeRunning, verbose)
	return nil
}

func renderTunnelStatusText(presenter *presentation.Presenter, cfg tunnel.Config, status tunnel.Status, runtimeRunning, verbose bool) {
	status = tunnel.PublicStatus(status)
	state := tunnelCLIState(cfg, status, runtimeRunning)
	presenter.StateSection(statusPresentationKind(state), "OpenAI Secure MCP Tunnel is "+state)
	fields := []presentation.Field{
		{Label: "enabled", Value: status.Enabled},
		{Label: "configured", Value: tunnel.Configured(cfg)},
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		fields = append(fields, presentation.Field{Label: "runtime key", Value: tunnel.SecretPreview(cfg.APIKey)})
	}
	if strings.TrimSpace(cfg.Admin.Key) != "" {
		fields = append(fields, presentation.Field{Label: "admin key", Value: tunnel.SecretPreview(cfg.Admin.Key)})
	}
	if status.ID != "" {
		fields = append(fields, presentation.Field{Label: "id", Value: status.ID})
	}
	if status.Metadata != nil {
		metadata := status.Metadata
		if metadata.Name != "" {
			fields = append(fields, presentation.Field{Label: "name", Value: metadata.Name})
		}
		if verbose {
			if metadata.Description != "" {
				fields = append(fields, presentation.Field{Label: "description", Value: metadata.Description})
			}
			if metadata.Creator != "" {
				fields = append(fields, presentation.Field{Label: "creator", Value: metadata.Creator})
			}
			if len(metadata.WorkspaceIDs) > 0 {
				fields = append(fields, presentation.Field{Label: "workspaces", Value: strings.Join(metadata.WorkspaceIDs, ", ")})
			}
			if len(metadata.OrganizationIDs) > 0 {
				fields = append(fields, presentation.Field{Label: "organizations", Value: strings.Join(metadata.OrganizationIDs, ", ")})
			}
			if len(metadata.TenantIDs) > 0 {
				fields = append(fields, presentation.Field{Label: "tenants", Value: strings.Join(metadata.TenantIDs, ", ")})
			}
		}
	}
	if status.Admin.Configured {
		fields = append(fields, presentation.Field{Label: "admin", Value: "configured " + presenter.Separator() + " " + formatTunnelAdminScope(status.Admin.Scope())})
	} else if verbose {
		fields = append(fields, presentation.Field{Label: "admin", Value: "not configured"})
	}
	if verbose {
		controlPlane := status.ControlPlaneBaseURL
		if strings.TrimSpace(controlPlane) == "" {
			controlPlane = "default"
		}
		fields = append(fields, presentation.Field{Label: "control plane", Value: controlPlane})
		if !status.StartedAt.IsZero() {
			fields = append(fields, presentation.Field{Label: "started", Value: status.StartedAt.Local().Format(time.RFC3339)})
		}
	}
	presenter.Fields(fields...)
	if verbose && status.MetadataError != "" {
		presenter.ChildStatus(presentation.StatusWarning, "Metadata unavailable: "+status.MetadataError)
	}
	if verbose && status.LastError != "" {
		presenter.ChildStatus(presentation.StatusError, status.LastError)
	}
}

func tunnelCLIState(cfg tunnel.Config, status tunnel.Status, runtimeRunning bool) string {
	if !status.Enabled {
		return "disabled"
	}
	if !tunnel.Configured(cfg) {
		return "not configured"
	}
	if !runtimeRunning {
		return "offline"
	}
	switch {
	case status.Ready:
		return "connected"
	case status.Restarting:
		return "reconnecting"
	case status.Running:
		return "connecting"
	case status.LastError != "":
		return "failed"
	default:
		return "starting"
	}
}

func fetchTunnelStatus(ctx context.Context, cfg tunnel.Config) tunnel.Status {
	span := tracepkg.Start(ctx, "STATUS", "status.tunnel.fetch", "Fetching tunnel status and cached metadata", tracepkg.String("tunnel_id", strings.TrimSpace(cfg.ID)), tracepkg.Bool("enabled", cfg.Enabled), tracepkg.Bool("configured", tunnel.Configured(cfg)))
	if strings.TrimSpace(cfg.ID) == "" {
		status := tunnel.StatusFromConfig(cfg, nil)
		span.EndMessage("Tunnel status fetched", tracepkg.Bool("metadata_loaded", false), tracepkg.Bool("running", status.Running), tracepkg.Bool("ready", status.Ready))
		return status
	}
	metadata, err := config.LoadTunnelMetadata(cfg.ID)
	if err == nil {
		status := tunnel.StatusFromConfig(cfg, &metadata)
		span.EndMessage("Tunnel status fetched", tracepkg.Bool("metadata_loaded", true), tracepkg.Bool("metadata_missing", false), tracepkg.Bool("running", status.Running), tracepkg.Bool("ready", status.Ready))
		return status
	} else if !errors.Is(err, os.ErrNotExist) {
		status := tunnel.StatusFromConfig(cfg, nil)
		status.MetadataError = err.Error()
		span.FailMessage("Tunnel cached metadata load failed", err, tracepkg.Bool("metadata_loaded", false))
		return status
	}
	status := tunnel.StatusFromConfig(cfg, nil)
	span.EndMessage("Tunnel status fetched", tracepkg.Bool("metadata_loaded", err == nil), tracepkg.Bool("metadata_missing", errors.Is(err, os.ErrNotExist)), tracepkg.Bool("running", status.Running), tracepkg.Bool("ready", status.Ready))
	return status
}

func tunnelSyncCommand() *cobra.Command {
	return &cobra.Command{Use: "sync", Short: "Fetch and persist metadata for the configured tunnel", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.metadata.preparing", "Preparing tunnel metadata synchronization")
		ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
		defer cancel()
		metadata, path, err := application.SyncConfiguredTunnel(ctx)
		if err != nil {
			return err
		}
		fields := []presentation.Field{{Label: "id", Value: metadata.ID}, {Label: "metadata", Value: path}}
		if metadata.Name != "" {
			fields = append(fields, presentation.Field{Label: "name", Value: metadata.Name})
		}
		renderMutationSuccess(cmd, "Tunnel metadata synced", fields...)
		return nil
	}}
}

func tunnelConfigureCommand() *cobra.Command {
	var enabled bool
	var id, apiKey, controlPlaneBaseURL, organizationID string
	cmd := &cobra.Command{Use: "configure", Short: "Configure the OpenAI Secure MCP Tunnel", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.config.preparing", "Preparing tunnel configuration update")
		input := application.TunnelRuntimeInput{}
		if cmd.Flags().Changed("enabled") {
			input.Enabled = &enabled
		}
		if cmd.Flags().Changed("id") {
			input.ID = &id
		}
		if cmd.Flags().Changed("api-key") {
			input.APIKey = &apiKey
		}
		if cmd.Flags().Changed("control-plane-base-url") {
			input.ControlPlaneBaseURL = &controlPlaneBaseURL
		}
		if cmd.Flags().Changed("organization-id") {
			input.OrganizationID = &organizationID
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
		_, err := application.ConfigureTunnelRuntime(ctx, input)
		cancel()
		if err != nil {
			return err
		}
		renderMutationSuccess(cmd, "Configuration saved")
		return nil
	}}
	cmd.Flags().BoolVar(&enabled, "enabled", false, "enable or disable the OpenAI Secure MCP Tunnel")
	cmd.Flags().StringVar(&id, "id", "", "OpenAI tunnel identifier")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "OpenAI tunnel runtime API key")
	cmd.Flags().StringVar(&controlPlaneBaseURL, "control-plane-base-url", "", "OpenAI tunnel control plane base URL")
	cmd.Flags().StringVar(&organizationID, "organization-id", "", "OpenAI organization identifier")
	return cmd
}

func tunnelToggleCommand(enabled bool) *cobra.Command {
	use, short := "disable", "Disable the OpenAI Secure MCP Tunnel"
	if enabled {
		use, short = "enable", "Enable the OpenAI Secure MCP Tunnel"
	}
	return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.state.updating", "Updating tunnel enabled state", logger.WithVerbose("enabled", enabled))
		if _, err := application.SetTunnelEnabled(cmd.Context(), enabled); err != nil {
			return err
		}
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		renderMutationSuccess(cmd, "OpenAI Secure MCP Tunnel "+state)
		return nil
	}}
}

func tunnelRunCommand() *cobra.Command {
	return &cobra.Command{Use: "run", Short: "Run the OpenAI Secure MCP Tunnel in the foreground", RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.runtime.loading", "Loading tunnel runtime configuration")
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load tunnel runtime configuration: %w", err)
		}
		tunnelConfig := cfg.Tunnel
		tunnelConfig.Enabled = true
		if err := tunnel.ValidateActivationConfig(tunnelConfig); err != nil {
			return err
		}

		log := commandLogger(cmd)
		logCommandVerbose(cmd, "TUNNEL", "tunnel.tools.initializing", "Initializing MCP tool runtime")
		runtime, err := tools.NewRuntimeWithAccessChecked(cfg.Integrations, cfg.Permissions.AllowDirs, func() (bool, int) { return cfg.HTTP.Admin.Enabled, cfg.HTTP.Admin.Port })
		if err != nil {
			return fmt.Errorf("initialize MCP tool runtime: %w", err)
		}
		configProvider := application.NewMCPConfigReadService()
		runtime.SetConfigReadProvider(configProvider)
		runtime.SetConfigSetApprovalProvider(configProvider)
		runtime.SetConfigSetApplyProvider(configProvider)
		runtime.SetInstructionAuthoringProvider(application.NewAgentInstructionAuthoringProvider(runtime.Workspaces, runtime.InstructionChanges))
		runtime.SetPlanAuthoringProvider(application.NewAgentPlanAuthoringProvider(runtime.Workspaces, runtime.InstructionChanges))
		runtime.SetPromptProvider(application.NewAgentPromptProvider(runtime.Workspaces))
		runtime.SetShellPath(cfg.Shell.Path)
		telemetry.AttachTools(runtime, nil, log)
		runtimeCtx, runtimeCancel := context.WithCancel(context.WithoutCancel(cmd.Context()))
		defer runtimeCancel()
		interrupt := newForegroundInterrupt(cmd, true)
		defer interrupt.Close()
		shutdownCtx := interrupt.Context

		client := tunnel.NewConfiguredWithLogger(tunnelConfig, runtime, log)
		if metadata, err := config.LoadTunnelMetadata(tunnelConfig.ID); err == nil {
			if seedErr := client.SeedMetadata(metadata); seedErr != nil {
				logCommandDebug(cmd, "TUNNEL", "tunnel.metadata.seed-failed", "Cached tunnel metadata could not be seeded", logger.WithDebug("error", seedErr.Error()))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			logCommandDebug(cmd, "TUNNEL", "tunnel.metadata.load-failed", "Cached tunnel metadata could not be loaded", logger.WithDebug("error", err.Error()))
		}
		client.SetLifecycleObserver(tunnel.TraceLifecycleObserver(tracepkg.ObserverFromContext(cmd.Context()), func(event tunnel.LifecycleEvent) {
			renderTunnelLifecycleDetail(cmd, event)
		}))
		logCommandVerbose(cmd, "TUNNEL", "tunnel.runtime.starting", "Starting tunnel runtime", logger.WithVerbose("tunnel_id", tunnelConfig.ID))
		if err := client.StartContext(runtimeCtx); err != nil {
			return err
		}
		defer func() {
			status := client.Status()
			if status.Running || status.Restarting {
				tracepkg.EmitPhase(cmd.Context(), tracepkg.PhaseStart, "TUNNEL", "tunnel.stop", "Stopping tunnel")
				if err := client.Stop(); err != nil {
					tracepkg.EmitPhase(cmd.Context(), tracepkg.PhaseError, "TUNNEL", "tunnel.stop", "Tunnel stop failed", tracepkg.String("error", tracepkg.SanitizeError(err)))
					log.Diagnostic(logger.Error, "TUNNEL", "tunnel.stop.failed", "Failed to stop tunnel", logger.WithVerbose("error", err.Error()), logger.WithVerbose("tunnel_id", tunnelConfig.ID))
					if runErr == nil {
						runErr = err
					}
				}
			}
			if runtime.Upstream != nil {
				log.Verbose("UPSTREAM", "upstream.stopping", "Stopping upstream servers")
				upstreamCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := runtime.Upstream.Shutdown(upstreamCtx)
				cancel()
				if err != nil {
					log.Failure("UPSTREAM", "upstream.shutdown.failed", "Upstream shutdown failed", err)
					if runErr == nil {
						runErr = err
					}
				} else {
					log.Verbose("UPSTREAM", "upstream.stopped", "Upstream servers stopped")
				}
			}
			if runErr == nil {
				log.Verbose("TUNNEL", "tunnel.shutdown.complete", "Tunnel shutdown complete")
			}
		}()

		if err := client.WaitUntilReady(shutdownCtx); err != nil {
			if shutdownCtx.Err() != nil {
				log.Verbose("TUNNEL", "tunnel.shutdown.requested", "Shutdown requested")
				return nil
			}
			return err
		}
		<-shutdownCtx.Done()
		log.Verbose("TUNNEL", "tunnel.shutdown.requested", "Shutdown requested", logger.With("reason", interrupt.Reason()))
		return nil
	}}
}

func renderTunnelLifecycleDetail(cmd *cobra.Command, event tunnel.LifecycleEvent) {
	if cmd == nil {
		return
	}
	if event.State == tunnel.LifecycleReady && strings.TrimSpace(event.ID) != "" {
		commandProgressSession(cmd).Append(func(presenter *presentation.Presenter) {
			presenter.Fields(presentation.Field{Label: "tunnel id", Value: event.ID})
		})
	}
}
