package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtimeevent"
	managed "go.mewis.me/codemcp/internal/service"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

const serviceReadyTimeout = managed.DefaultLifecycleTimeout

func upCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "up", Short: "Install and start the managed MCP service", Args: cobra.NoArgs, RunE: runUp}
	cmd.Flags().Bool("system", false, "use a machine-level service on Linux/macOS; elevates with sudo when needed")
	cmd.Flags().String("service-environment-hash", "", "internal managed environment snapshot hash")
	_ = cmd.Flags().MarkHidden("service-environment-hash")
	return cmd
}

func downCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "down", Short: "Stop and remove the managed MCP service", Args: cobra.NoArgs, RunE: runDown}
	cmd.Flags().Bool("system", false, "use the machine-level service on Linux/macOS; elevates with sudo when needed")
	return cmd
}

func restartCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "restart", Short: "Restart the managed MCP service", Args: cobra.NoArgs, RunE: runRestart}
	cmd.Flags().Bool("system", false, "use the machine-level service on Linux/macOS; elevates with sudo when needed")
	cmd.Flags().String("service-environment-hash", "", "internal managed environment snapshot hash")
	_ = cmd.Flags().MarkHidden("service-environment-hash")
	return cmd
}

func runUp(cmd *cobra.Command, _ []string) error {
	logCommandStep(cmd, "SERVICE", "service.scope.resolving", "Resolving managed service scope")
	scope, err := managedScopeForCommand(cmd)
	if err != nil {
		return err
	}
	spec, manager, err := managedServiceForCommand(cmd, scope)
	if err != nil {
		return err
	}
	environmentHash, _ := cmd.Flags().GetString("service-environment-hash")
	if environmentHash == "" {
		logCommandStep(cmd, "SERVICE", "service.environment.capturing", "Capturing managed service environment")
		environmentHash, err = saveManagedEnvironmentContext(cmd.Context(), spec)
		if err != nil {
			return err
		}
	}
	spec.EnvironmentHash = environmentHash
	logCommandDebug(cmd, "SERVICE", "service.spec.resolved", "Managed service specification resolved", logger.WithDebug("scope", spec.Scope), logger.WithDebug("service_id", spec.ID), logger.WithDebug("config", spec.ConfigRoot), logger.WithDebug("binary", spec.Binary), logger.WithDebug("backend", manager.Backend()))
	if scope == managed.ScopeSystem && managed.DetectScope() == managed.ScopeUser {
		logCommandStep(cmd, "SERVICE", "service.elevating", "Elevating managed service operation")
		return elevateManagedCommandWithBinary(cmd, "up", environmentHash, spec.Binary)
	}
	return runManagedUp(cmd, spec, manager)
}

func runDown(cmd *cobra.Command, _ []string) error {
	logCommandStep(cmd, "SERVICE", "service.scope.resolving", "Resolving managed service scope")
	scope, err := managedScopeForCommand(cmd)
	if err != nil {
		return err
	}
	spec, manager, err := managedServiceForCommand(cmd, scope)
	if err != nil {
		return err
	}
	if scope == managed.ScopeSystem && managed.DetectScope() == managed.ScopeUser {
		return elevateManagedCommandWithBinary(cmd, "down", "", spec.Binary)
	}
	return runManagedDown(cmd, spec, manager)
}

func runRestart(cmd *cobra.Command, _ []string) error {
	logCommandStep(cmd, "SERVICE", "service.scope.resolving", "Resolving managed service scope")
	scope, err := managedScopeForCommand(cmd)
	if err != nil {
		return err
	}
	spec, manager, err := managedServiceForCommand(cmd, scope)
	if err != nil {
		return err
	}
	environmentHash, _ := cmd.Flags().GetString("service-environment-hash")
	if environmentHash == "" {
		logCommandStep(cmd, "SERVICE", "service.environment.capturing", "Capturing managed service environment")
		environmentHash, err = saveManagedEnvironmentContext(cmd.Context(), spec)
		if err != nil {
			return err
		}
	}
	spec.EnvironmentHash = environmentHash
	if scope == managed.ScopeSystem && managed.DetectScope() == managed.ScopeUser {
		return elevateManagedCommandWithBinary(cmd, "restart", environmentHash, spec.Binary)
	}
	return runManagedRestart(cmd, spec, manager)
}

func runManagedRestart(cmd *cobra.Command, spec managed.Spec, manager managed.Manager) error {
	logCommandStep(cmd, "SERVICE", "service.config.verifying", "Verifying runtime configuration")
	source, err := config.Source()
	if err != nil {
		return err
	}
	if !source.Exists {
		return errors.New("chatgpt-mcp is not initialized; run chatgpt-mcp init first")
	}
	if _, err := config.VerifyRuntime(); err != nil {
		return err
	}
	cfg, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	log := commandLogger(cmd)
	progress := managedLifecycleProgress(cmd)
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: managedRuntimeStatus, Shutdown: requestManagedShutdown, Timeout: serviceReadyTimeout, Observe: func(event managed.LifecycleEvent) {
		progress.Start("service."+event.Phase, event.Message, managedLifecycleDoneMessage(event))
	}}
	result, err := lifecycle.Restart(cmd.Context())
	if err != nil {
		progress.Stop()
		logManagedStartupFailure(cmd, spec, manager, err)
		return err
	}
	progress.Complete()
	status := result.Status
	log.Ready("SERVICE", "service.restarted", "Managed service restarted")
	logManagedDetails(log, spec, manager)
	log.Ready("SERVER", "server.started", "Server started")
	logRuntimeDetails(log, status)
	logRuntimeTunnelResult(log, status)
	logRuntimeTunnelMetadata(log, cfg.Tunnel, status, config.LoadTunnelMetadata)
	logManagedHints(log, spec)
	return nil
}

func saveManagedEnvironment(spec managed.Spec) (string, error) {
	return saveManagedEnvironmentContext(context.Background(), spec)
}

func saveManagedEnvironmentContext(ctx context.Context, spec managed.Spec) (string, error) {
	source, err := config.Source()
	if err != nil {
		return "", err
	}
	if !source.Exists {
		return "", errors.New("chatgpt-mcp is not initialized; run chatgpt-mcp init first")
	}
	cfg, err := config.LoadRuntime()
	if err != nil {
		return "", err
	}
	snapshot := managed.CaptureEnvironment(spec.Account, cfg.Shell.Path)
	tracepkg.Emit(ctx, "SERVICE", "service.environment.captured", "Captured managed service environment snapshot", tracepkg.String("path", managed.EnvironmentPath(spec.ConfigRoot)), tracepkg.Int("variable_count", len(snapshot.Values)))
	return managed.SaveEnvironmentContext(ctx, spec.ConfigRoot, snapshot)
}

func managedScopeForCommand(cmd *cobra.Command) (managed.Scope, error) {
	detected := managed.DetectScope()
	system, err := cmd.Flags().GetBool("system")
	if err != nil {
		return "", err
	}
	span := tracepkg.Start(cmd.Context(), "SERVICE", "service.scope.resolve", "Resolving managed service scope", tracepkg.String("requested_scope", map[bool]string{true: string(managed.ScopeSystem), false: "auto"}[system]), tracepkg.String("detected_scope", string(detected)), tracepkg.String("platform", runtime.GOOS))
	if system {
		if runtime.GOOS == "windows" {
			err := errors.New("system service scope is not supported on Windows; managed services use a per-user Scheduled Task")
			span.FailMessage("Managed service scope resolution failed", err)
			return "", err
		}
		span.EndMessage("Managed service scope resolved", tracepkg.String("scope", string(managed.ScopeSystem)), tracepkg.String("resolution", "explicit"))
		return managed.ScopeSystem, nil
	}
	span.EndMessage("Managed service scope resolved", tracepkg.String("scope", string(detected)), tracepkg.String("resolution", "detected"))
	return detected, nil
}

func managedServiceForCommand(cmd *cobra.Command, scope managed.Scope) (managed.Spec, managed.Manager, error) {
	return managedServiceForCommandWithBinary(cmd, scope, os.Args[0])
}

func managedServiceForCommandWithBinary(cmd *cobra.Command, scope managed.Scope, binarySource string) (managed.Spec, managed.Manager, error) {
	ctx := cmd.Context()
	account, err := managed.InvokingAccountContext(ctx, scope)
	if err != nil {
		return managed.Spec{}, nil, err
	}
	if err := resolveManagedConfigRoot(cmd, scope, account); err != nil {
		return managed.Spec{}, nil, err
	}
	binary, err := managed.PrepareManagedBinaryContext(ctx, config.RootPath(), binarySource)
	if err != nil {
		return managed.Spec{}, nil, err
	}
	spec, err := managed.NewSpecContext(ctx, config.RootPath(), binary, scope, account)
	if err != nil {
		return managed.Spec{}, nil, err
	}
	manager := managed.NewManagerWithObserver(tracepkg.ObserverFromContext(ctx))
	tracepkg.Emit(ctx, "SERVICE", "service.manager.resolved", "Resolved managed service backend", tracepkg.String("service", spec.ID), tracepkg.String("scope", string(spec.Scope)), tracepkg.String("backend", manager.Backend()))
	return spec, manager, nil
}

func resolveManagedConfigRoot(cmd *cobra.Command, scope managed.Scope, account managed.Account) error {
	ctx := cmd.Context()
	flagValue, err := cmd.Root().PersistentFlags().GetString("config-dir")
	if err != nil {
		return err
	}
	envValue := strings.TrimSpace(os.Getenv(configformat.EnvConfigDir))
	span := tracepkg.Start(ctx, "SERVICE", "service.config-root.resolve", "Resolving managed service configuration root", tracepkg.String("scope", string(scope)), tracepkg.Bool("flag_configured", strings.TrimSpace(flagValue) != ""), tracepkg.Bool("environment_configured", envValue != ""))
	if strings.TrimSpace(flagValue) != "" {
		span.EndMessage("Managed service configuration root resolved", tracepkg.String("path", config.RootPath()), tracepkg.String("source", "flag"))
		return nil
	}
	if envValue != "" {
		span.EndMessage("Managed service configuration root resolved", tracepkg.String("path", config.RootPath()), tracepkg.String("source", "environment"))
		return nil
	}
	if scope != managed.ScopeSystem {
		span.EndMessage("Managed service configuration root resolved", tracepkg.String("path", config.RootPath()), tracepkg.String("source", "current"))
		return nil
	}
	path := managed.DefaultConfigRoot(account)
	if err := configformat.SetRootPath(path); err != nil {
		span.FailMessage("Managed service configuration root resolution failed", err, tracepkg.String("path", path), tracepkg.String("source", "account_default"))
		return err
	}
	span.EndMessage("Managed service configuration root resolved", tracepkg.String("path", config.RootPath()), tracepkg.String("source", "account_default"))
	return nil
}

func runManagedUp(cmd *cobra.Command, spec managed.Spec, manager managed.Manager) error {
	logCommandStep(cmd, "SERVICE", "service.config.verifying", "Verifying runtime configuration")
	source, err := config.Source()
	if err != nil {
		return err
	}
	if !source.Exists {
		return errors.New("chatgpt-mcp is not initialized; run chatgpt-mcp init first")
	}
	if _, err := config.VerifyRuntime(); err != nil {
		return err
	}
	cfg, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	logCommandStep(cmd, "SERVICE", "service.backend.inspecting", "Inspecting managed service backend", logger.WithVerbose("backend", manager.Backend()))
	backendStatus, err := manager.Status(spec)
	if err != nil {
		return err
	}
	matches, err := manager.DefinitionMatches(spec)
	if err != nil {
		return err
	}
	action := "installed"
	if backendStatus.Installed {
		if matches {
			action = "started"
		} else {
			action = "updated"
		}
	}
	log := commandLogger(cmd)
	progress := managedLifecycleProgress(cmd)
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: managedRuntimeStatus, Shutdown: requestManagedShutdown, Timeout: serviceReadyTimeout, Observe: func(event managed.LifecycleEvent) {
		progress.Start("service."+event.Phase, event.Message, managedLifecycleDoneMessage(event))
	}}
	result, err := lifecycle.Up(cmd.Context())
	if err != nil {
		progress.Stop()
		logManagedStartupFailure(cmd, spec, manager, err)
		return err
	}
	progress.Complete()
	status := result.Status
	if !result.Changed {
		logManagedAlreadyRunning(cmd, spec, manager, status, cfg.Tunnel)
		return nil
	}
	logManagedUp(log, spec, manager, status, action)
	logRuntimeTunnelResult(log, status)
	logRuntimeTunnelMetadata(log, cfg.Tunnel, status, config.LoadTunnelMetadata)
	logManagedHints(log, spec)
	return nil
}

func logManagedStartupFailure(cmd *cobra.Command, spec managed.Spec, manager managed.Manager, cause error) {
	log := commandLogger(cmd)
	status, statusErr := manager.Status(spec)
	fields := []logger.Field{logger.WithVerbose("backend", manager.Backend()), logger.WithVerbose("installed", status.Installed), logger.WithVerbose("running", status.Running)}
	if status.PID != 0 {
		fields = append(fields, logger.WithVerbose("pid", status.PID))
	}
	if statusErr != nil {
		fields = append(fields, logger.WithVerbose("backend_status_error", statusErr.Error()))
	}
	log.Verbose("SERVICE", "service.startup.failed", "Managed runtime failed readiness", fields...)
	events, err := runtimeevent.Read(spec.ConfigRoot, runtimeevent.Query{Tail: 20})
	if err != nil {
		log.Verbose("SERVICE", "service.startup.logs.unavailable", "Managed runtime logs unavailable", logger.WithVerbose("error", err.Error()))
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Level != "error" && event.Error == "" {
			continue
		}
		message := event.Message
		if event.Error != "" {
			message += ": " + event.Error
		}
		log.Verbose("SERVICE", "service.startup.runtime-error", "Managed runtime error", logger.WithVerbose("event", event.Name), logger.WithVerbose("detail", message))
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Status != "error" {
			continue
		}
		log.Verbose("SERVICE", "service.startup.runtime-error", "Managed runtime error", logger.WithVerbose("event", event.Name), logger.WithVerbose("detail", event.Message))
		return
	}
	log.Verbose("SERVICE", "service.startup.no-runtime-error", "Managed runtime exited before reporting a runtime error", logger.WithVerbose("cause", cause.Error()), logger.WithVerbose("logs", runtimeevent.Path(spec.ConfigRoot)))
}

func runManagedDown(cmd *cobra.Command, spec managed.Spec, manager managed.Manager) error {
	log := commandLogger(cmd)
	progress := managedLifecycleProgress(cmd)
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: managedRuntimeStatus, Shutdown: requestManagedShutdown, Timeout: serviceReadyTimeout, Observe: func(event managed.LifecycleEvent) {
		progress.Start("service."+event.Phase, event.Message, managedLifecycleDoneMessage(event))
	}}
	result, err := lifecycle.Down(cmd.Context())
	if err != nil {
		progress.Stop()
		return err
	}
	progress.Complete()
	if !result.Changed {
		log.Notice("SERVICE", "service.not-installed", "Managed service is not installed")
		return nil
	}
	log.Ready("SERVICE", "service.stopped", "Server stopped")
	log.Ready("SERVICE", "service.removed", "Managed service removed")
	log.Detail("config preserved", spec.ConfigRoot)
	log.Detail("logs preserved", filepath.Join(spec.ConfigRoot, "logs"))
	return nil
}

func managedLifecycleProgress(cmd *cobra.Command) *commandProgress {
	return newCommandProgress(cmd, "SERVICE")
}

func managedLifecycleDoneMessage(event managed.LifecycleEvent) string {
	switch event.Phase {
	case "runtime.stopping":
		return "Managed runtime stopped"
	case "backend.stopping":
		return "Managed service backend stopped"
	case "definition.installing":
		if strings.HasPrefix(strings.ToLower(event.Message), "updating") {
			return "Managed service definition updated"
		}
		return "Managed service definition installed"
	case "definition.uninstalling":
		return "Managed service definition removed"
	case "backend.starting":
		return "Managed service backend started"
	case "runtime.waiting":
		return "Managed runtime ready"
	default:
		return event.Message + " complete"
	}
}

func managedRuntimeStatus(ctx context.Context) (runtimeStatusResult, bool, error) {
	status, err := requestRuntimeStatus(ctx)
	if err == nil {
		return status, true, nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no running server found") || strings.Contains(message, "control endpoint unavailable") || strings.Contains(message, "connection refused") || strings.Contains(message, "actively refused") {
		return runtimeStatusResult{}, false, nil
	}
	return runtimeStatusResult{}, false, err
}

func requestManagedShutdown(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	return requestRuntimeShutdown(ctx)
}

func waitManagedRuntimeReady(parent context.Context, spec managed.Spec, timeout time.Duration) (runtimeStatusResult, error) {
	return managed.WaitRuntimeReady(parent, spec, managedRuntimeStatus, "", timeout)
}

func managedScopeConflict(status runtimeStatusResult, spec managed.Spec, action string) error {
	return managed.ValidateRuntimeOwner(status, true, spec, action)
}

func logManagedAlreadyRunning(cmd *cobra.Command, spec managed.Spec, manager managed.Manager, status runtimeStatusResult, cfg tunnel.Config) {
	log := commandLogger(cmd)
	log.Ready("SERVICE", "service.already-running", "Managed service already running")
	logManagedDetails(log, spec, manager)
	logRuntimeDetails(log, status)
	logRuntimeTunnelResult(log, status)
	logRuntimeTunnelMetadata(log, cfg, status, config.LoadTunnelMetadata)
	logManagedHints(log, spec)
}

func logManagedUp(log *logger.Logger, spec managed.Spec, manager managed.Manager, status runtimeStatusResult, action string) {
	message := "Managed service " + action
	if spec.Scope == managed.ScopeSystem {
		message = "System service " + action
	}
	log.Ready("SERVICE", "service."+action, message)
	logManagedDetails(log, spec, manager)
	log.Ready("SERVER", "server.started", "Server started")
	logRuntimeDetails(log, status)
}

func logManagedDetails(log *logger.Logger, spec managed.Spec, manager managed.Manager) {
	log.Detail("scope", spec.Scope)
	log.Detail("backend", managedBackendLabel(manager, spec))
	if spec.Scope == managed.ScopeSystem && spec.Account.Username != "" {
		log.Detail("user", spec.Account.Username)
	}
	log.Detail("config", spec.ConfigRoot)
	log.Detail("service", spec.ID)
}

func logRuntimeDetails(log *logger.Logger, status runtimeStatusResult) {
	if status.RunID != "" {
		log.Detail("session", shortSessionID(status.RunID))
	}
	log.Detail("pid", status.PID)
	if status.ServerEnabled {
		log.Detail("mcp http", fmt.Sprintf("http://127.0.0.1:%d/mcp", status.ServerPort))
	} else {
		log.Detail("mcp http", "disabled")
	}
	if status.AdminEnabled {
		log.Detail("admin", fmt.Sprintf("http://127.0.0.1:%d/", status.AdminPort))
	}
}

func logRuntimeTunnelResult(log *logger.Logger, status runtimeStatusResult) {
	state := statusTunnelState(status, true)
	switch state {
	case "connected":
		log.Ready("TUNNEL", "tunnel.connected", "OpenAI Secure MCP Tunnel connected")
	case "failed":
		var err error
		if status.TunnelLastError != "" {
			err = errors.New(status.TunnelLastError)
		}
		log.Failure("TUNNEL", "tunnel.failed", "OpenAI Secure MCP Tunnel failed", err)
	case "starting", "connecting", "reconnecting":
		log.Warning("TUNNEL", "tunnel.pending", "OpenAI Secure MCP Tunnel is still "+state, nil)
	default:
		log.Notice("TUNNEL", "tunnel."+strings.ReplaceAll(state, " ", "-"), "OpenAI Secure MCP Tunnel is "+state)
	}
	if status.TunnelID != "" {
		log.Detail("tunnel id", status.TunnelID)
	}
}

type tunnelMetadataLoadFunc func(string) (tunnel.Metadata, error)

func logRuntimeTunnelMetadata(log *logger.Logger, cfg tunnel.Config, status runtimeStatusResult, load tunnelMetadataLoadFunc) {
	if log == nil || load == nil || statusTunnelState(status, true) != "connected" {
		return
	}
	id := strings.TrimSpace(status.TunnelID)
	if id == "" {
		id = strings.TrimSpace(cfg.ID)
	}
	metadata, err := load(id)
	if err != nil {
		log.Verbose("TUNNEL", "tunnel.metadata.unavailable", "Tunnel metadata unavailable", logger.WithVerbose("error", err.Error()))
		return
	}
	if metadata.Name != "" {
		log.Detail("tunnel name", metadata.Name)
	}
	if metadata.Description != "" {
		log.Detail("tunnel description", metadata.Description)
	}
	if scope := tunnelMetadataScope(metadata); scope != "" {
		log.Detail("tunnel scope", scope)
	}
}

func tunnelMetadataScope(metadata tunnel.Metadata) string {
	parts := make([]string, 0, 3)
	if len(metadata.OrganizationIDs) > 0 {
		parts = append(parts, "organization:"+strings.Join(metadata.OrganizationIDs, ","))
	}
	if len(metadata.WorkspaceIDs) > 0 {
		parts = append(parts, "workspace:"+strings.Join(metadata.WorkspaceIDs, ","))
	}
	if len(metadata.TenantIDs) > 0 {
		parts = append(parts, "tenant:"+strings.Join(metadata.TenantIDs, ","))
	}
	return strings.Join(parts, " · ")
}

func runtimeTunnelSummary(status runtimeStatusResult) string {
	parts := []string{}
	if status.TunnelEnabled {
		parts = append(parts, "enabled")
	} else {
		parts = append(parts, "disabled")
	}
	if status.TunnelConfigured {
		parts = append(parts, "configured")
	} else {
		parts = append(parts, "not configured")
	}
	if status.TunnelEnabled {
		switch {
		case status.TunnelReady:
			parts = append(parts, "connected")
		case status.TunnelRestarting:
			parts = append(parts, "reconnecting")
		case status.TunnelRunning:
			parts = append(parts, "connecting")
		default:
			parts = append(parts, "starting")
		}
	}
	return strings.Join(parts, " · ")
}

func logManagedHints(log *logger.Logger, spec managed.Spec) {
	if spec.Scope == managed.ScopeSystem {
		log.Notice("SERVICE", "service.machine-start", "Service starts automatically with the machine")
	} else if warning := managed.PersistenceWarning(spec); warning != "" {
		log.Warning("SERVICE", "service.persistence.warning", warning, nil)
		if runtime.GOOS == "linux" && spec.Account.Username != "" {
			log.Detail("machine service", "cgm up --system")
		}
	} else {
		log.Notice("SERVICE", "service.detached", "Runtime will continue independently of this terminal")
	}
	log.Notice("SERVICE", "service.logs-hint", "View logs: cgm logs -f")
	stop := "cgm down"
	if spec.Scope == managed.ScopeSystem && runtime.GOOS != "windows" {
		stop = "cgm down --system"
	}
	log.Notice("SERVICE", "service.stop-hint", "Stop service: "+stop)
}

func managedBackendLabel(manager managed.Manager, spec managed.Spec) string {
	if runtime.GOOS == "linux" && spec.Scope == managed.ScopeUser {
		return "systemd --user"
	}
	return manager.Backend()
}
