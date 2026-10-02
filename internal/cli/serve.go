package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/app"
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/logger"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

func serveCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "serve", Short: "Start the MCP server", RunE: runServer}
	addExposeFlag(cmd)
	return cmd
}

func runServer(cmd *cobra.Command, args []string) (runErr error) {
	ctx := cmd.Context()
	var commandSession *presentation.ProgressSession
	if !commandPresentationExempt(cmd) {
		commandSession = commandProgressSession(cmd)
		commandSession.Update(presentation.ProgressPhase{ID: "server.starting", Label: "Starting server", State: presentation.ProgressRunning})
	}
	logCommandStep(cmd, "SERVER", "server.config.loading", "Loading runtime configuration")
	configSpan := tracepkg.Start(ctx, "CONFIG", "server.config.load", "Loading server runtime configuration", tracepkg.String("config_root", config.RootPath()))
	source, err := config.Source()
	if err != nil {
		configSpan.FailMessage("Server runtime config source discovery failed", err)
		return err
	}
	if !source.Exists {
		err := errors.New("cm is not initialized; run cm init")
		configSpan.FailMessage("Server runtime config unavailable", err, tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", false))
		return err
	}
	cfg, err := config.LoadRuntime()
	if err != nil {
		configSpan.FailMessage("Server runtime config load failed", err, tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)))
		return err
	}
	if err := applyExposeOverride(cmd, &cfg); err != nil {
		configSpan.FailMessage("Server runtime exposure override failed", err, tracepkg.String("path", source.Path))
		return err
	}
	if err := config.Validate(cfg); err != nil {
		configSpan.FailMessage("Server runtime config validation failed", err, tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)))
		return err
	}
	configSpan.EndMessage("Server runtime configuration loaded", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", true), tracepkg.Bool("mcp_http_enabled", cfg.HTTP.MCP.Enabled), tracepkg.Bool("admin_enabled", cfg.HTTP.Admin.Enabled), tracepkg.Bool("tunnel_enabled", cfg.Tunnel.Enabled), tracepkg.String("exposure_mode", string(cfg.HTTP.Exposure.Mode)), tracepkg.Any("interfaces", append([]string(nil), cfg.HTTP.Exposure.Interfaces...)))
	logCommandDebug(cmd, "SERVER", "server.config.loaded", "Runtime configuration loaded", logger.WithDebug("mcp_http", cfg.HTTP.MCP.Enabled), logger.WithDebug("admin", cfg.HTTP.Admin.Enabled), logger.WithDebug("tunnel", cfg.Tunnel.Enabled), logger.WithDebug("expose", cfg.HTTP.Exposure.Mode))

	runtimeCtx, runtimeCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer runtimeCancel()

	logCommandStep(cmd, "NETWORK", "server.listeners.resolving", "Resolving listener plan")
	planSpan := tracepkg.Start(runtimeCtx, "NETWORK", "server.listener-plan.resolve", "Resolving server listener plan", tracepkg.String("exposure_mode", string(cfg.HTTP.Exposure.Mode)), tracepkg.Any("interfaces", append([]string(nil), cfg.HTTP.Exposure.Interfaces...)))
	plan, err := resolveListenerPlan(cfg.HTTP.Exposure)
	if err != nil {
		planSpan.FailMessage("Server listener plan resolution failed", err)
		return err
	}
	addresses := make([]string, 0, len(plan.Addresses))
	for _, address := range plan.Addresses {
		addresses = append(addresses, address.Host)
	}
	listenerComponents := 0
	if cfg.HTTP.MCP.Enabled {
		listenerComponents++
	}
	if cfg.HTTP.Admin.Enabled {
		listenerComponents++
	}
	listenerCount := len(plan.Hosts) * listenerComponents
	planSpan.EndMessage("Server listener plan resolved", tracepkg.Any("hosts", append([]string(nil), plan.Hosts...)), tracepkg.Any("addresses", addresses), tracepkg.Int("address_count", len(plan.Addresses)), tracepkg.Int("listener_count", listenerCount))
	logCommandDebug(cmd, "NETWORK", "server.listeners.resolved", "Listener plan resolved", logger.WithDebug("hosts", plan.Hosts), logger.WithDebug("addresses", len(plan.Addresses)))
	startedAt := time.Now().UTC()
	serviceInfo := runtimeServiceInfo(cmd)
	interrupt := newForegroundInterrupt(cmd, false)
	defer interrupt.Close()
	log := commandLogger(cmd)
	metadata := runtimeevent.Metadata{RunID: idgen.Must("run", 8), PID: os.Getpid(), Managed: serviceInfo.Managed, ServiceID: serviceInfo.ID, ServiceScope: serviceInfo.Scope}
	logCommandStep(cmd, "SESSION", "runtime.journal.opening", "Opening runtime journal")
	journal, err := runtimeevent.NewJournal(config.RootPath(), runtimeevent.Options{Metadata: metadata})
	if err != nil {
		return err
	}
	recorder := runtimeevent.NewRecorder(journal, metadata)
	if err := recorder.Record(runtimeevent.Event{Time: startedAt, Level: "info", Kind: "action", Name: "runtime.session.started", Component: "SESSION", Message: "Runtime session started", Fields: []runtimeevent.Field{{Key: "session", Value: metadata.RunID}, {Key: "mode", Value: runtimeSessionMode(metadata)}, {Key: "config", Value: config.RootPath()}}}); err != nil {
		return err
	}
	defer func() {
		status := "ok"
		errorText := ""
		if runErr != nil {
			status = "error"
			errorText = runErr.Error()
		}
		durationMS := time.Since(startedAt).Milliseconds()
		_ = recorder.Record(runtimeevent.Event{Time: time.Now().UTC(), Level: "info", Kind: "info", Name: "runtime.session.ended", Component: "SESSION", Message: "Runtime session ended", Status: status, Error: errorText, DurationMS: durationMS, Fields: []runtimeevent.Field{{Key: "session", Value: metadata.RunID}, {Key: "status", Value: status}, {Key: "duration_ms", Value: durationMS}}})
	}()
	log.AddSink(recorder)
	sessionSpan := tracepkg.Start(runtimeCtx, "SESSION", "runtime.session", "Running server runtime session", tracepkg.String("session", metadata.RunID), tracepkg.String("mode", runtimeSessionMode(metadata)), tracepkg.Bool("managed", metadata.Managed), tracepkg.String("service", metadata.ServiceID), tracepkg.String("service_scope", metadata.ServiceScope))
	defer func() {
		fields := []tracepkg.Field{tracepkg.String("session", metadata.RunID), tracepkg.String("mode", runtimeSessionMode(metadata)), tracepkg.Int("pid", metadata.PID)}
		if runErr != nil {
			sessionSpan.FailMessage("Server runtime session ended with error", runErr, fields...)
		} else {
			sessionSpan.EndMessage("Server runtime session ended", fields...)
		}
	}()
	var control *runtimeControl
	var bindings *httpBindings
	var operationMu sync.Mutex
	var stateMu sync.RWMutex
	lifecycle := "bootstrapping"
	stateChanged := make(chan struct{})
	setLifecycle := func(next string) {
		stateMu.Lock()
		if lifecycle == next {
			stateMu.Unlock()
			return
		}
		previous := lifecycle
		lifecycle = next
		close(stateChanged)
		stateChanged = make(chan struct{})
		stateMu.Unlock()
		_ = recorder.Record(runtimeevent.Event{Time: time.Now().UTC(), Level: "info", Kind: "info", Name: "runtime.lifecycle.changed", Component: "RUNTIME", Message: "Runtime lifecycle changed", Status: next, Fields: []runtimeevent.Field{{Key: "previous", Value: previous}, {Key: "lifecycle", Value: next}}})
	}
	shutdownRequest := make(chan struct{}, 1)
	restartRequest := make(chan struct{}, 1)
	log.Verbose("NETWORK", "server.listeners.opening", "Opening HTTP listeners")
	bindings, err = openHTTPBindingsContext(runtimeCtx, cfg, plan)
	if err != nil {
		return err
	}
	if bindings.cfg.HTTP.MCP.Port != cfg.HTTP.MCP.Port {
		if commandSession != nil {
			commandPresenter(cmd).ChildStatus(presentation.StatusWarning, "Configured MCP HTTP port is unavailable; using next available port")
			commandPresenter(cmd).Fields(presentation.Field{Label: "configured port", Value: cfg.HTTP.MCP.Port}, presentation.Field{Label: "port", Value: bindings.cfg.HTTP.MCP.Port})
		} else {
			log.Warning("NETWORK", "server.mcp.port-fallback", "Configured MCP HTTP port is unavailable; using next available port", nil, logger.With("configured_port", cfg.HTTP.MCP.Port), logger.With("port", bindings.cfg.HTTP.MCP.Port))
		}
	}
	if bindings.cfg.HTTP.Admin.Port != cfg.HTTP.Admin.Port {
		if commandSession != nil {
			commandPresenter(cmd).ChildStatus(presentation.StatusWarning, "Configured admin port is unavailable; using next available port")
			commandPresenter(cmd).Fields(presentation.Field{Label: "configured port", Value: cfg.HTTP.Admin.Port}, presentation.Field{Label: "port", Value: bindings.cfg.HTTP.Admin.Port})
		} else {
			log.Warning("NETWORK", "server.admin.port-fallback", "Configured admin port is unavailable; using next available port", nil, logger.With("configured_port", cfg.HTTP.Admin.Port), logger.With("port", bindings.cfg.HTTP.Admin.Port))
		}
	}
	cfg = bindings.cfg
	runtime, err := app.NewWithLoggerContext(runtimeCtx, cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		runtime.Logger.Verbose("SERVER", "server.runtime.cleanup", "Cleaning up runtime services")
		if err := runtime.Stop(); err != nil {
			if commandSession != nil {
				runtime.Logger.Diagnostic(logger.Error, "SERVER", "server.runtime.cleanup.failed", "Runtime cleanup failed", logger.WithVerbose("error", err.Error()))
			} else {
				runtime.Logger.Failure("SERVER", "server.runtime.cleanup.failed", "Runtime cleanup failed", err)
			}
			if runErr == nil {
				runErr = err
			}
		} else if commandSession == nil {
			runtime.Logger.Ready("SERVER", "server.stopped", "Server stopped")
		}
		if control != nil {
			if err := control.Close(); err != nil {
				runtime.Logger.Warning("CONTROL", "runtime.control.close-failed", "Runtime control cleanup failed", err)
			}
		}
	}()

	currentCfg, currentPlan := cfg, plan
	defer bindings.CloseUnstarted()
	errCh := make(chan error, max(1, len(bindings.mcpListeners)+len(bindings.adminListeners)))
	reload := func(reloadCtx context.Context) (result runtimeReloadResult, reloadErr error) {
		if reloadCtx == nil {
			reloadCtx = context.Background()
		}
		if observer := tracepkg.ObserverFromContext(runtimeCtx); observer != nil && tracepkg.ObserverFromContext(reloadCtx) == nil {
			reloadCtx = tracepkg.WithObserver(reloadCtx, observer)
		}
		operationMu.Lock()
		defer operationMu.Unlock()
		stateMu.RLock()
		ready := lifecycle == "ready"
		previousCfg, previousPlan := currentCfg, currentPlan
		stateMu.RUnlock()
		next := previousCfg
		networkRestarted, restorePerformed := false, false
		if commandSession != nil {
			commandSession.Update(presentation.ProgressPhase{ID: "server.reloading", Label: "Reloading server listeners", State: presentation.ProgressRunning})
		}
		reloadSpan := tracepkg.Start(reloadCtx, "CONFIG", "server.reload", "Reloading server runtime", tracepkg.Int("old_mcp_port", previousCfg.HTTP.MCP.Port), tracepkg.Int("old_admin_port", previousCfg.HTTP.Admin.Port), tracepkg.String("old_exposure_mode", string(previousCfg.HTTP.Exposure.Mode)))
		defer func() {
			if commandSession != nil {
				if reloadErr != nil {
					commandSession.Warn("server.reloading", "Reloading server listeners", "Server reload failed")
				} else {
					commandSession.Success("server.reloading", "Reloading server listeners", "Server listeners reloaded")
				}
			}
			fields := []tracepkg.Field{tracepkg.Bool("network_restarted", networkRestarted), tracepkg.Bool("restore_performed", restorePerformed), tracepkg.Int("old_mcp_port", previousCfg.HTTP.MCP.Port), tracepkg.Int("old_admin_port", previousCfg.HTTP.Admin.Port), tracepkg.Int("new_mcp_port", next.HTTP.MCP.Port), tracepkg.Int("new_admin_port", next.HTTP.Admin.Port), tracepkg.String("new_exposure_mode", string(next.HTTP.Exposure.Mode))}
			if reloadErr != nil {
				reloadSpan.FailMessage("Server runtime reload failed", reloadErr, fields...)
			} else {
				reloadSpan.EndMessage("Server runtime reloaded", fields...)
			}
		}()
		if !ready {
			reloadErr = errors.New("runtime is still starting")
			return result, reloadErr
		}

		configReloadSpan := tracepkg.Start(reloadCtx, "CONFIG", "server.reload.config", "Loading and validating reload configuration", tracepkg.String("config_root", config.RootPath()))
		next, reloadErr = config.LoadRuntime()
		if reloadErr != nil {
			configReloadSpan.FailMessage("Reload configuration load failed", reloadErr)
			return result, reloadErr
		}
		if reloadErr = applyExposeOverride(cmd, &next); reloadErr != nil {
			configReloadSpan.FailMessage("Reload exposure override failed", reloadErr)
			return result, reloadErr
		}
		if reloadErr = config.Validate(next); reloadErr != nil {
			configReloadSpan.FailMessage("Reload configuration validation failed", reloadErr)
			return result, reloadErr
		}
		configReloadSpan.EndMessage("Reload configuration loaded and validated", tracepkg.Int("mcp_port", next.HTTP.MCP.Port), tracepkg.Int("admin_port", next.HTTP.Admin.Port), tracepkg.String("exposure_mode", string(next.HTTP.Exposure.Mode)), tracepkg.Any("interfaces", append([]string(nil), next.HTTP.Exposure.Interfaces...)))

		planReloadSpan := tracepkg.Start(reloadCtx, "NETWORK", "server.reload.listener-plan", "Resolving reload listener plan", tracepkg.Any("previous_hosts", append([]string(nil), previousPlan.Hosts...)))
		nextPlan, err := resolveListenerPlan(next.HTTP.Exposure)
		if err != nil {
			reloadErr = err
			planReloadSpan.FailMessage("Reload listener plan resolution failed", err)
			return result, reloadErr
		}
		networkConfigChanged := !networkConfigEqual(previousCfg, next)
		listenerPlanChanged := !listenerPlanEqual(previousPlan, nextPlan)
		networkRestarted = networkConfigChanged || listenerPlanChanged
		portDisjoint := listenerPortsDisjoint(previousCfg, next)
		planReloadSpan.EndMessage("Reload listener plan resolved", tracepkg.Any("hosts", append([]string(nil), nextPlan.Hosts...)), tracepkg.Bool("listener_plan_changed", listenerPlanChanged), tracepkg.Bool("network_config_changed", networkConfigChanged))
		tracepkg.Emit(reloadCtx, "NETWORK", "server.reload.decision", "Resolved server reload network decision", tracepkg.Bool("network_restarted", networkRestarted), tracepkg.Bool("network_config_changed", networkConfigChanged), tracepkg.Bool("listener_plan_changed", listenerPlanChanged), tracepkg.Bool("ports_disjoint", portDisjoint), tracepkg.Int("old_mcp_port", previousCfg.HTTP.MCP.Port), tracepkg.Int("new_mcp_port", next.HTTP.MCP.Port), tracepkg.Int("old_admin_port", previousCfg.HTTP.Admin.Port), tracepkg.Int("new_admin_port", next.HTTP.Admin.Port))
		setLifecycle("reloading")
		defer setLifecycle("ready")
		if !networkRestarted {
			if reloadErr = runtime.ReloadConfig(next); reloadErr != nil {
				return result, reloadErr
			}
			stateMu.Lock()
			currentCfg, currentPlan = next, nextPlan
			stateMu.Unlock()
			runtime.Logger.Diagnostic(logger.Info, "CONFIG", "config.reloaded", "Configuration reloaded")
			result = reloadResult(next, false)
			return result, nil
		}

		openCandidate := func() (*httpBindings, error) {
			span := tracepkg.Start(reloadCtx, "NETWORK", "server.reload.candidate-open", "Opening candidate server listeners", tracepkg.Int("mcp_port", next.HTTP.MCP.Port), tracepkg.Int("admin_port", next.HTTP.Admin.Port), tracepkg.Any("hosts", append([]string(nil), nextPlan.Hosts...)))
			candidate, err := openHTTPBindingsExactContext(reloadCtx, next, nextPlan)
			if err != nil {
				span.FailMessage("Candidate server listener open failed", err)
				return nil, err
			}
			span.EndMessage("Candidate server listeners opened", tracepkg.Int("selected_mcp_port", candidate.cfg.HTTP.MCP.Port), tracepkg.Int("selected_admin_port", candidate.cfg.HTTP.Admin.Port), tracepkg.Int("listener_count", len(candidate.mcpListeners)+len(candidate.adminListeners)))
			return candidate, nil
		}
		shutdownPrevious := func() error {
			span := tracepkg.Start(reloadCtx, "NETWORK", "server.reload.previous-listeners-shutdown", "Shutting down previous server listeners", tracepkg.Int("listener_count", len(bindings.mcpListeners)+len(bindings.adminListeners)), tracepkg.Int("mcp_port", previousCfg.HTTP.MCP.Port), tracepkg.Int("admin_port", previousCfg.HTTP.Admin.Port))
			err := bindings.Shutdown()
			if err != nil {
				span.FailMessage("Previous server listener shutdown failed", err)
			} else {
				span.EndMessage("Previous server listeners shut down")
			}
			return err
		}
		restorePrevious := func(reason string) error {
			restorePerformed = true
			span := tracepkg.Start(reloadCtx, "NETWORK", "server.reload.restore", "Restoring previous server listeners", tracepkg.String("reason", reason), tracepkg.Int("mcp_port", previousCfg.HTTP.MCP.Port), tracepkg.Int("admin_port", previousCfg.HTTP.Admin.Port))
			restored, err := restoreHTTPBindingsContext(reloadCtx, runtime, previousCfg, previousPlan, errCh)
			if err != nil {
				span.FailMessage("Previous server listener restore failed", err)
				return err
			}
			bindings = restored
			span.EndMessage("Previous server listeners restored", tracepkg.Int("listener_count", len(restored.mcpListeners)+len(restored.adminListeners)))
			return nil
		}

		if portDisjoint {
			candidate, err := openCandidate()
			if err != nil {
				reloadErr = err
				runtime.Logger.Diagnostic(logger.Error, "SERVER", "server.reload.failed", "Server reload failed", logger.WithVerbose("error", err.Error()))
				return result, reloadErr
			}
			next = candidate.cfg
			if reloadErr = runtime.ReloadConfig(next); reloadErr != nil {
				candidate.CloseUnstarted()
				runtime.Logger.Diagnostic(logger.Error, "SERVER", "server.reload.failed", "Server reload failed", logger.WithVerbose("error", reloadErr.Error()))
				return result, reloadErr
			}
			if err := shutdownPrevious(); err != nil {
				runtime.Logger.Warning("NETWORK", "server.reload.shutdown.warning", "Previous listeners did not shut down cleanly", err)
			}
			candidate.Start(runtime, errCh)
			bindings = candidate
			stateMu.Lock()
			currentCfg, currentPlan = next, nextPlan
			stateMu.Unlock()
			if commandSession == nil {
				logReadyEndpoints(runtime.Logger, next, nextPlan)
			} else {
				runtime.Logger.Diagnostic(logger.Info, "SERVER", "server.reload.endpoints", "Reloaded server endpoints", logger.WithDebug("mcp_port", next.HTTP.MCP.Port), logger.WithDebug("admin_port", next.HTTP.Admin.Port))
			}
			result = reloadResult(next, true)
			return result, nil
		}

		if err := shutdownPrevious(); err != nil {
			runtime.Logger.Warning("NETWORK", "server.reload.shutdown.warning", "Previous listeners did not shut down cleanly", err)
		}
		candidate, err := openCandidate()
		if err != nil {
			restoreErr := restorePrevious("candidate_open_failed")
			reloadErr = errors.Join(err, restoreErr)
			runtime.Logger.Diagnostic(logger.Error, "SERVER", "server.reload.failed", "Server reload failed", logger.WithVerbose("error", reloadErr.Error()))
			return result, reloadErr
		}
		next = candidate.cfg
		if reloadErr = runtime.ReloadConfig(next); reloadErr != nil {
			candidate.CloseUnstarted()
			restoreErr := restorePrevious("runtime_reload_failed")
			reloadErr = errors.Join(reloadErr, restoreErr)
			runtime.Logger.Failure("SERVER", "server.reload.failed", "Server reload failed", reloadErr)
			return result, reloadErr
		}
		candidate.Start(runtime, errCh)
		bindings = candidate
		stateMu.Lock()
		currentCfg, currentPlan = next, nextPlan
		stateMu.Unlock()
		if commandSession == nil {
			logReadyEndpoints(runtime.Logger, next, nextPlan)
		} else {
			runtime.Logger.Diagnostic(logger.Info, "SERVER", "server.reload.endpoints", "Reloaded server endpoints", logger.WithDebug("mcp_port", next.HTTP.MCP.Port), logger.WithDebug("admin_port", next.HTTP.Admin.Port))
		}
		result = reloadResult(next, true)
		return result, nil
	}
	status := func() runtimeStatusResult {
		stateMu.RLock()
		cfgSnapshot, lifecycleSnapshot := currentCfg, lifecycle
		stateMu.RUnlock()
		tunnelStatus := runtime.Tunnel.Status()
		overview, _ := runtime.StatusOverview(runtimeCtx)
		fingerprint, _ := config.RuntimeFingerprint(cfgSnapshot)
		return runtimeStatusResult{PID: os.Getpid(), RunID: metadata.RunID, Lifecycle: lifecycleSnapshot, Starting: runtimeLifecycleStarting(lifecycleSnapshot), Managed: metadata.Managed, ServiceID: metadata.ServiceID, ServiceScope: metadata.ServiceScope, StartedAt: startedAt, ConfigRoot: config.RootPath(), ConfigFingerprint: fingerprint, ServerEnabled: cfgSnapshot.HTTP.MCP.Enabled, ServerPort: cfgSnapshot.HTTP.MCP.Port, AdminEnabled: cfgSnapshot.HTTP.Admin.Enabled, AdminPort: cfgSnapshot.HTTP.Admin.Port, Exposure: cfgSnapshot.HTTP.Exposure.Mode, TunnelEnabled: cfgSnapshot.Tunnel.Enabled, TunnelConfigured: tunnel.Configured(cfgSnapshot.Tunnel), TunnelRunning: tunnelStatus.Running, TunnelReady: tunnelStatus.Ready, TunnelRestarting: tunnelStatus.Restarting, TunnelID: strings.TrimSpace(cfgSnapshot.Tunnel.ID), TunnelLastError: tunnelStatus.LastError, ToolProfile: "full", ToolCount: len(runtime.Tools.List()), Readiness: runtimeReadinessComponents(cfgSnapshot, overview, lifecycleSnapshot)}
	}
	statusWait := func(ctx context.Context, previous string) runtimeStatusResult {
		for {
			stateMu.RLock()
			current, changed := lifecycle, stateChanged
			stateMu.RUnlock()
			if previous == "" || current != previous {
				return status()
			}
			select {
			case <-ctx.Done():
				return status()
			case <-changed:
			}
		}
	}
	runtime.Logger.Verbose("CONTROL", "runtime.control.starting", "Starting runtime control endpoint")
	control, err = startRuntimeControlContext(runtimeCtx, runtimeControlOptions{RunID: metadata.RunID, Managed: metadata.Managed, ServiceID: metadata.ServiceID, ServiceScope: metadata.ServiceScope, StartedAt: startedAt, Events: recorder.Stream, Activity: runtime.Activity, Reload: reload, ReloadWorkspaces: func() (workspaceReloadResult, error) {
		if err := runtime.Tools.ReloadWorkspaces(); err != nil {
			return workspaceReloadResult{}, err
		}
		items, err := runtime.Tools.Workspaces.List()
		if err != nil {
			return workspaceReloadResult{}, err
		}
		runtime.Logger.Diagnostic(logger.Info, "WORKSPACE", "workspace.registry.reloaded", "Workspace registry reloaded", logger.WithDebug("count", len(items)))
		return workspaceReloadResult{PID: os.Getpid(), Count: len(items)}, nil
	}, ReloadUpstreams: func(ctx context.Context) (upstreamReloadResult, error) {
		previous := runtime.Tools.Upstream.List()
		if err := runtime.Tools.Upstream.Reload(ctx); err != nil {
			return upstreamReloadResult{}, err
		}
		if err := runtime.Tools.RefreshUpstreams(ctx, true); err != nil {
			restoreErr := runtime.Tools.Upstream.RestoreRuntimeSnapshot(ctx, previous)
			return upstreamReloadResult{}, errors.Join(err, restoreErr)
		}
		items := runtime.Tools.Upstream.List()
		runtime.Logger.Diagnostic(logger.Info, "UPSTREAM", "upstream.registry.reloaded", "Upstream registry reloaded", logger.WithDebug("count", len(items)))
		return upstreamReloadResult{PID: os.Getpid(), Count: len(items)}, nil
	}, Status: status, StatusWait: statusWait, Approvals: runtime.Tools.Approvals, Operations: runtime.Operations, Completions: runtime.Tools.Completions, Executions: runtime.Tools.Executions, Log: runtime.Logger, Shutdown: func() {
		runtimeCancel()
		select {
		case shutdownRequest <- struct{}{}:
		default:
		}
	}, Restart: func() {
		select {
		case restartRequest <- struct{}{}:
		default:
		}
	}, ClearLogs: journal.Clear})
	if err != nil {
		return err
	}
	runtime.Logger.Diagnostic(logger.Info, "CONTROL", "runtime.control.started", "Runtime control endpoint started", logger.WithDebug("address", control.state.Address), logger.WithDebug("path", control.path))
	runtime.Logger.Verbose("RUNTIME", "runtime.services.starting", "Starting runtime services")
	if err := runtime.Start(runtimeCtx); err != nil {
		return err
	}
	bindings.Start(runtime, errCh)
	runtime.Logger.Verbose("NETWORK", "server.listeners.waiting", "Waiting for HTTP listener readiness")
	if err := waitRuntimeHTTPReady(runtimeCtx, cfg, 3*time.Second); err != nil {
		return errors.Join(err, bindings.Shutdown())
	}
	setLifecycle("listeners_ready")
	if cfg.Tunnel.Enabled && tunnel.Configured(cfg.Tunnel) {
		setLifecycle("tunnel_connecting")
		if commandSession != nil {
			commandSession.Update(presentation.ProgressPhase{ID: "tunnel.readiness", Label: "Waiting for OpenAI Secure MCP Tunnel readiness", State: presentation.ProgressRunning})
		}
		if err := runtime.Tunnel.WaitUntilReady(runtimeCtx); err != nil {
			return errors.Join(err, bindings.Shutdown())
		}
		if commandSession != nil {
			commandSession.Success("tunnel.readiness", "Waiting for OpenAI Secure MCP Tunnel readiness", "OpenAI Secure MCP Tunnel ready")
		}
	}
	setLifecycle("ready")
	if commandSession != nil {
		commandSession.Success("server.starting", "Starting server", "Server ready")
		commandPresenter(cmd).Fields(endpointPresentationFields(cfg)...)
	} else {
		logReadyEndpoints(runtime.Logger, cfg, plan)
	}

	shutdown := func(reason string) error {
		operationMu.Lock()
		defer operationMu.Unlock()
		setLifecycle("stopping")
		span := tracepkg.Start(runtimeCtx, "SERVER", "server.shutdown", "Stopping server runtime", tracepkg.String("reason", reason), tracepkg.Int("mcp_port", currentCfg.HTTP.MCP.Port), tracepkg.Int("admin_port", currentCfg.HTTP.Admin.Port))
		if commandSession != nil {
			commandSession.Update(presentation.ProgressPhase{ID: "server.stopping", Label: "Stopping server", State: presentation.ProgressRunning})
		}
		listenerSpan := tracepkg.Start(runtimeCtx, "NETWORK", "server.listeners.shutdown", "Shutting down server listeners", tracepkg.String("reason", reason), tracepkg.Int("listener_count", len(bindings.mcpListeners)+len(bindings.adminListeners)))
		err := bindings.Shutdown()
		if err != nil {
			listenerSpan.FailMessage("Server listener shutdown failed", err)
			span.FailMessage("Server runtime shutdown failed", err)
			if commandSession != nil {
				commandSession.Warn("server.stopping", "Stopping server", "Server shutdown failed")
			}
			runtime.Logger.Diagnostic(logger.Error, "SERVER", "server.shutdown.failed", "Server shutdown failed", logger.WithVerbose("error", err.Error()))
			return err
		}
		listenerSpan.EndMessage("Server listeners shut down")
		span.EndMessage("Server runtime shutdown initiated", tracepkg.String("reason", reason))
		if commandSession != nil {
			commandSession.Success("server.stopping", "Stopping server", "Server stopped")
		}
		return nil
	}

	select {
	case err := <-errCh:
		if err != nil {
			runtime.Logger.Diagnostic(logger.Error, "SERVER", "server.listener.failed", "HTTP listener failed", logger.WithVerbose("error", err.Error()))
			return errors.Join(err, shutdown("listener_error"))
		}
		return nil
	case <-interrupt.Context.Done():
		reason := interrupt.Reason()
		if reason == "" {
			reason = "context canceled"
		}
		runtime.Logger.Verbose("SERVER", "server.shutdown.requested", "Shutdown requested", logger.With("reason", reason))
		return shutdown(reason)
	case <-shutdownRequest:
		runtime.Logger.Verbose("SERVER", "server.shutdown.requested", "Shutdown requested", logger.With("reason", "runtime control"))
		return shutdown("runtime_control")
	case <-restartRequest:
		runtime.Logger.Verbose("SERVER", "server.restart.requested", "Managed runtime self-restart requested")
		if err := shutdown("runtime_control_restart"); err != nil {
			return err
		}
		return errManagedRuntimeSelfRestart
	}
}

var errManagedRuntimeSelfRestart = errors.New("managed runtime self-restart requested")

func runtimeLifecycleStarting(state string) bool {
	switch state {
	case "ready", "reloading", "stopping":
		return false
	default:
		return true
	}
}

func runtimeReadinessComponents(cfg config.Config, overview application.StatusOverview, lifecycle string) []runtimecontrol.ReadinessComponent {
	listenersReady := lifecycle == "listeners_ready" || lifecycle == "tunnel_connecting" || lifecycle == "ready"
	components := make([]runtimecontrol.ReadinessComponent, 0, 9)
	add := func(id, label string, configured, ready bool) {
		if configured {
			components = append(components, runtimecontrol.ReadinessComponent{ID: id, Label: label, Configured: true, Ready: ready})
		}
	}
	if cfg.HTTP.MCP.Enabled {
		add("mcp-http", "MCP HTTP server", true, listenersReady)
	}
	if cfg.HTTP.Admin.Enabled {
		add("admin-http", "Admin HTTP server", true, listenersReady)
	}
	if overview.TypeSafeEnabled {
		add("typesafe", "TypeSafe semantic provider", overview.TypeSafeConfigured, overview.TypeSafeAvailable)
	}
	if overview.SemanticApprovalEnabled {
		add("semantic-approval", "Semantic approval", overview.TypeSafeConfigured, overview.SemanticApprovalEffective)
	}
	if overview.TelegramEnabled {
		add("telegram", "Telegram runtime", overview.TelegramConfigured, overview.TelegramRunning)
	}
	if overview.TelegramTopicsEnabled {
		add("telegram-topics", "Telegram topics", overview.TelegramConfigured, overview.TelegramTopicsEffective && overview.TelegramTopicsStoreHealthy)
	}
	if overview.LogsMiniAppEnabled {
		add("telegram-logs-mini-app", "Telegram Logs Mini App", overview.TelegramConfigured && overview.LogsMiniAppAvailable, overview.LogsMiniAppEffective)
	}
	if cfg.Notifications.Approval.Enabled {
		configured := cfg.Notifications.Approval.DesktopEnabled || (cfg.Notifications.Approval.TelegramEnabled && overview.TelegramConfigured)
		add("approval-notifications", "Approval notifications", configured, overview.RuntimeRunning)
	}
	if cfg.Notifications.Completion.Enabled {
		configured := cfg.Notifications.Completion.DesktopEnabled || (cfg.Notifications.Completion.TelegramEnabled && overview.TelegramConfigured)
		add("completion-notifications", "Completion notifications", configured, overview.RuntimeRunning)
	}
	if overview.TunnelEnabled {
		add("openai-tunnel", "OpenAI Secure MCP Tunnel", overview.TunnelConfigured, overview.TunnelReady)
	}
	return components
}

func runtimeSessionMode(metadata runtimeevent.Metadata) string {
	if !metadata.Managed {
		return "foreground"
	}
	if metadata.ServiceScope != "" {
		return "managed/" + metadata.ServiceScope
	}
	return "managed"
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
}

func waitRuntimeHTTPReady(parent context.Context, cfg config.Config, timeout time.Duration) error {
	if parent == nil {
		parent = context.Background()
	}
	endpoints := []string{}
	if cfg.HTTP.MCP.Enabled {
		endpoints = append(endpoints, endpointURL("127.0.0.1", cfg.HTTP.MCP.Port, "/health"))
	}
	if cfg.HTTP.Admin.Enabled {
		endpoints = append(endpoints, endpointURL("127.0.0.1", cfg.HTTP.Admin.Port, "/"))
	}
	span := tracepkg.Start(parent, "NETWORK", "server.http-readiness", "Waiting for HTTP listener readiness", tracepkg.Any("endpoints", append([]string(nil), endpoints...)), tracepkg.DurationMS("timeout_ms", timeout))
	if len(endpoints) == 0 {
		span.EndMessage("HTTP listener readiness skipped", tracepkg.Int("attempts", 0), tracepkg.Int("endpoint_count", 0))
		return nil
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	var lastErr error
	firstFailure := ""
	attempts := 0
	for time.Now().Before(deadline) {
		attempts++
		ready := true
		for _, endpoint := range endpoints {
			ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err == nil {
				var response *http.Response
				response, err = client.Do(request)
				if err == nil {
					_ = response.Body.Close()
					if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest {
						err = fmt.Errorf("HTTP %d", response.StatusCode)
					}
				}
			}
			cancel()
			if err != nil {
				lastErr = fmt.Errorf("%s: %w", endpoint, err)
				if firstFailure == "" {
					firstFailure = lastErr.Error()
					tracepkg.Emit(parent, "NETWORK", "server.http-readiness.first-failure", "HTTP readiness first probe failure", tracepkg.Int("attempt", attempts), tracepkg.URL("endpoint", endpoint), tracepkg.String("reason", err.Error()))
				}
				ready = false
				break
			}
		}
		if ready {
			span.EndMessage("HTTP listeners ready", tracepkg.Int("attempts", attempts), tracepkg.Int("endpoint_count", len(endpoints)), tracepkg.String("first_failure", firstFailure))
			return nil
		}
		select {
		case <-parent.Done():
			err := parent.Err()
			span.FailMessage("HTTP listener readiness canceled", err, tracepkg.Int("attempts", attempts), tracepkg.Int("endpoint_count", len(endpoints)), tracepkg.String("first_failure", firstFailure))
			return err
		case <-time.After(50 * time.Millisecond):
		}
	}
	if lastErr != nil {
		err := fmt.Errorf("server listeners did not become ready: %w", lastErr)
		span.FailMessage("HTTP listeners did not become ready", err, tracepkg.Int("attempts", attempts), tracepkg.Int("endpoint_count", len(endpoints)), tracepkg.String("first_failure", firstFailure))
		return err
	}
	err := errors.New("server listeners did not become ready")
	span.FailMessage("HTTP listeners did not become ready", err, tracepkg.Int("attempts", attempts), tracepkg.Int("endpoint_count", len(endpoints)), tracepkg.String("first_failure", firstFailure))
	return err
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func shutdownServers(servers []*http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, len(servers))
	for _, server := range servers {
		server := server
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				closeErr := server.Close()
				if closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
					err = errors.Join(err, closeErr)
				}
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	var shutdownErr error
	for err := range errCh {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	return shutdownErr
}
