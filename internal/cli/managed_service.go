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

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
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
	logCommandVerbose(cmd, "SERVICE", "service.scope.resolving", "Resolving managed service scope")
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
		logCommandVerbose(cmd, "SERVICE", "service.environment.capturing", "Capturing managed service environment")
		environmentHash, err = saveManagedEnvironmentContext(cmd.Context(), spec)
		if err != nil {
			return err
		}
	}
	spec.EnvironmentHash = environmentHash
	logCommandDebug(cmd, "SERVICE", "service.spec.resolved", "Managed service specification resolved", logger.WithDebug("scope", spec.Scope), logger.WithDebug("service_id", spec.ID), logger.WithDebug("config", spec.ConfigRoot), logger.WithDebug("binary", spec.Binary), logger.WithDebug("backend", manager.Backend()))
	if scope == managed.ScopeSystem && managed.DetectScope() == managed.ScopeUser {
		logCommandVerbose(cmd, "SERVICE", "service.elevating", "Elevating managed service operation")
		return elevateManagedCommandWithBinary(cmd, "up", environmentHash, spec.Binary)
	}
	return runManagedUp(cmd, spec, manager)
}

func runDown(cmd *cobra.Command, _ []string) error {
	logCommandVerbose(cmd, "SERVICE", "service.scope.resolving", "Resolving managed service scope")
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
	logCommandVerbose(cmd, "SERVICE", "service.scope.resolving", "Resolving managed service scope")
	scope, err := managedScopeForCommand(cmd)
	if err != nil {
		return err
	}
	spec, manager, err := managedServiceForCommand(cmd, scope)
	if err != nil {
		return err
	}
	binary, err := managed.PrepareManagedRestartBinaryContext(cmd.Context(), spec.ConfigRoot, spec.Binary)
	if err != nil {
		return err
	}
	spec.Binary = binary
	environmentHash, _ := cmd.Flags().GetString("service-environment-hash")
	if environmentHash == "" {
		logCommandVerbose(cmd, "SERVICE", "service.environment.capturing", "Capturing managed service environment")
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
	logCommandVerbose(cmd, "SERVICE", "service.config.verifying", "Verifying runtime configuration")
	source, err := config.Source()
	if err != nil {
		return err
	}
	if !source.Exists {
		return application.ErrNotInitialized
	}
	if _, err := config.VerifyRuntime(); err != nil {
		return err
	}
	progress := managedLifecycleProgress(cmd)
	observeReadiness, _ := managedRestartReadinessObserver(progress)
	lastGroup := ""
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: managedRestartRuntimeStatus, Shutdown: requestManagedShutdown, WaitStatusUpdate: runtimecontrol.WaitStatusUpdate, Timeout: serviceReadyTimeout, ObserveStatus: observeReadiness, Observe: func(event managed.LifecycleEvent) {
		group := managedRestartLifecycleGroup(event.Phase)
		if lastGroup != "" && group != "" && group != lastGroup {
			progress.Break()
		}
		if group != "" {
			lastGroup = group
		}
		if event.Phase == "runtime.waiting" {
			progress.Start("service."+event.Phase, "Waiting for runtime services", "")
			return
		}
		progress.Start("service."+event.Phase, event.Message, managedLifecycleDoneMessage(event))
	}}
	result, err := lifecycle.Restart(cmd.Context())
	if err != nil {
		progress.Stop()
		logManagedStartupFailure(cmd, spec, manager, err)
		return err
	}
	progress.Stop()
	status := result.Status
	renderManagedRestartResult(cmd, spec, manager, status)
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
		return "", application.ErrNotInitialized
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
	logCommandVerbose(cmd, "SERVICE", "service.config.verifying", "Verifying runtime configuration")
	source, err := config.Source()
	if err != nil {
		return err
	}
	if !source.Exists {
		return application.ErrNotInitialized
	}
	if _, err := config.VerifyRuntime(); err != nil {
		return err
	}
	cfg, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	logCommandVerbose(cmd, "SERVICE", "service.backend.inspecting", "Inspecting managed service backend", logger.WithVerbose("backend", manager.Backend()))
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
		renderManagedLifecycleResult(cmd, "Managed service already running", spec, manager, status, cfg.Tunnel)
		return nil
	}
	message := "Managed service " + action
	if spec.Scope == managed.ScopeSystem {
		message = "System service " + action
	}
	renderManagedLifecycleResult(cmd, message, spec, manager, status, cfg.Tunnel)
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
		renderMutationResult(cmd, presentation.StatusInfo, "Managed service is not installed")
		return nil
	}
	renderMutationBlock(cmd, func(presenter *presentation.Presenter) {
		presenter.ChildStatus(presentation.StatusSuccess, "Server stopped")
		presenter.Status(presentation.StatusSuccess, "Managed service removed")
		presenter.Fields(
			presentation.Field{Label: "config preserved", Value: spec.ConfigRoot},
			presentation.Field{Label: "logs preserved", Value: filepath.Join(spec.ConfigRoot, "logs")},
		)
	})
	return nil
}

func renderManagedLifecycleResult(cmd *cobra.Command, message string, spec managed.Spec, manager managed.Manager, status runtimeStatusResult, cfg tunnel.Config) {
	renderManagedLifecycleResultWithReadiness(cmd, message, spec, manager, status, cfg, nil)
}

func renderManagedLifecycleResultWithReadiness(cmd *cobra.Command, message string, spec managed.Spec, manager managed.Manager, status runtimeStatusResult, cfg tunnel.Config, skipReadiness map[string]struct{}) {
	renderMutationBlock(cmd, func(presenter *presentation.Presenter) {
		presenter.Status(presentation.StatusSuccess, message)
		presenter.ChildStatus(presentation.StatusSuccess, "Server started")
		fields := []presentation.Field{
			{Label: "scope", Value: spec.Scope},
			{Label: "backend", Value: managedBackendLabel(manager, spec)},
		}
		if spec.Scope == managed.ScopeSystem && spec.Account.Username != "" {
			fields = append(fields, presentation.Field{Label: "user", Value: spec.Account.Username})
		}
		fields = append(fields,
			presentation.Field{Label: "config", Value: spec.ConfigRoot},
			presentation.Field{Label: "service", Value: spec.ID},
		)
		if status.RunID != "" {
			fields = append(fields, presentation.Field{Label: "session", Value: shortSessionID(status.RunID)})
		}
		fields = append(fields, presentation.Field{Label: "pid", Value: status.PID})
		if status.ServerEnabled {
			fields = append(fields, presentation.Field{Label: "mcp http", Value: fmt.Sprintf("http://127.0.0.1:%d/mcp", status.ServerPort)})
		} else {
			fields = append(fields, presentation.Field{Label: "mcp http", Value: "disabled"})
		}
		if status.AdminEnabled {
			fields = append(fields, presentation.Field{Label: "admin", Value: fmt.Sprintf("http://127.0.0.1:%d/", status.AdminPort)})
		}
		presenter.NestedFields(fields...)
		renderManagedReadinessScopesExcept(presenter, status, skipReadiness)
		if _, streamed := skipReadiness["tunnel"]; !streamed {
			presenter.Spacer()
			state := statusTunnelState(status, true)
			presenter.ChildState(statusPresentationKind(state), "OpenAI Secure MCP Tunnel", state)
			tunnelFields := []presentation.Field{}
			tunnelScopeFields := []presentation.Field{}
			if status.TunnelID != "" {
				tunnelFields = append(tunnelFields, presentation.Field{Label: "id", Value: status.TunnelID})
			}
			if state == "connected" {
				id := strings.TrimSpace(status.TunnelID)
				if id == "" {
					id = strings.TrimSpace(cfg.ID)
				}
				if metadata, err := config.LoadTunnelMetadata(id); err == nil {
					if metadata.Name != "" {
						tunnelFields = append(tunnelFields, presentation.Field{Label: "name", Value: metadata.Name})
					}
					if metadata.Description != "" {
						tunnelFields = append(tunnelFields, presentation.Field{Label: "description", Value: metadata.Description})
					}
					tunnelScopeFields = tunnelMetadataScopeFields(metadata)
				}
			}
			presenter.NestedFields(tunnelFields...)
			presenter.NestedFieldGroup("scope", tunnelScopeFields...)
		}
		if warning := managed.PersistenceWarning(spec); warning != "" {
			presenter.ChildStatus(presentation.StatusWarning, warning)
		}
		presenter.Spacer()
		presenter.Subsection("Actions")
		presenter.NestedFields(
			presentation.Field{Label: "View logs", Value: "cm logs -f"},
			presentation.Field{Label: "Stop service", Value: managedStopCommand(spec)},
		)
	})
}

func renderManagedRestartResult(cmd *cobra.Command, spec managed.Spec, manager managed.Manager, status runtimeStatusResult) {
	renderMutationBlock(cmd, func(presenter *presentation.Presenter) {
		presenter.ChildState(presentation.StatusSuccess, "Server", "running")
		runtimeValue := fmt.Sprintf("pid %d", status.PID)
		if status.RunID != "" {
			runtimeValue += " · " + shortSessionID(status.RunID)
		}
		backendValue := managedBackendLabel(manager, spec) + " · " + string(spec.Scope)
		if spec.Scope == managed.ScopeSystem && spec.Account.Username != "" {
			backendValue += " · " + spec.Account.Username
		}
		fields := []presentation.Field{
			{Label: "service", Value: spec.ID},
			{Label: "runtime", Value: runtimeValue},
			{Label: "backend", Value: backendValue},
			{Label: "config", Value: spec.ConfigRoot},
		}
		if status.ServerEnabled {
			fields = append(fields, presentation.Field{Label: "mcp http", Value: fmt.Sprintf("http://127.0.0.1:%d/mcp", status.ServerPort)})
		} else {
			fields = append(fields, presentation.Field{Label: "mcp http", Value: "disabled"})
		}
		if status.AdminEnabled {
			fields = append(fields, presentation.Field{Label: "admin", Value: fmt.Sprintf("http://127.0.0.1:%d/", status.AdminPort)})
		}
		presenter.NestedFields(fields...)
		if warning := managed.PersistenceWarning(spec); warning != "" {
			presenter.ChildStatus(presentation.StatusWarning, warning)
		}
		presenter.Spacer()
		presenter.Subsection("Actions")
		presenter.NestedFields(
			presentation.Field{Label: "Logs", Value: "cm logs -f"},
			presentation.Field{Label: "Stop", Value: managedStopCommand(spec)},
		)
	})
}

func managedStopCommand(spec managed.Spec) string {
	stop := "cm down"
	if spec.Scope == managed.ScopeSystem && runtime.GOOS != "windows" {
		stop = "cm down --system"
	}
	return stop
}

func managedLifecycleProgress(cmd *cobra.Command) *commandProgress {
	return newCommandProgress(cmd, "SERVICE")
}

type managedReadinessScope struct {
	ID           string
	Label        string
	ComponentIDs []string
}

type managedReadinessScopeState struct {
	Scope  managedReadinessScope
	Fields []presentation.Field
	Ready  bool
}

var managedReadinessScopes = []managedReadinessScope{
	{ID: "semantic", Label: "Semantic", ComponentIDs: []string{"typesafe", "semantic-approval"}},
	{ID: "telegram", Label: "Telegram", ComponentIDs: []string{"telegram", "telegram-topics", "telegram-logs-mini-app"}},
	{ID: "notifications", Label: "Notifications", ComponentIDs: []string{"approval-notifications", "completion-notifications"}},
}

var managedRestartReadinessScopes = []managedReadinessScope{
	{ID: "telegram", Label: "Telegram", ComponentIDs: []string{"telegram"}},
}

func managedReadinessScopeStates(status runtimeStatusResult) []managedReadinessScopeState {
	return readinessScopeStates(status, managedReadinessScopes)
}

func managedRestartReadinessScopeStates(status runtimeStatusResult) []managedReadinessScopeState {
	return readinessScopeStates(status, managedRestartReadinessScopes)
}

func readinessScopeStates(status runtimeStatusResult, scopes []managedReadinessScope) []managedReadinessScopeState {
	components := make(map[string]runtimecontrol.ReadinessComponent, len(status.Readiness))
	for _, component := range status.Readiness {
		if component.Configured {
			components[component.ID] = component
		}
	}
	states := make([]managedReadinessScopeState, 0, len(scopes))
	for _, scope := range scopes {
		fields := make([]presentation.Field, 0, len(scope.ComponentIDs))
		allReady := true
		for _, id := range scope.ComponentIDs {
			component, ok := components[id]
			if !ok {
				continue
			}
			state := "ready"
			if !component.Ready {
				state = "not ready"
				allReady = false
			}
			fields = append(fields, presentation.Field{Label: component.Label, Value: state})
		}
		if len(fields) == 0 {
			continue
		}
		states = append(states, managedReadinessScopeState{Scope: scope, Fields: fields, Ready: allReady})
	}
	return states
}

func renderManagedReadinessScope(presenter *presentation.Presenter, state managedReadinessScopeState) {
	if presenter == nil {
		return
	}
	kind := presentation.StatusSuccess
	value := "ready"
	if !state.Ready {
		kind = presentation.StatusWarning
		value = "partial"
	}
	presenter.Spacer()
	presenter.ChildState(kind, state.Scope.Label, value)
	presenter.NestedFields(state.Fields...)
}

func renderManagedReadinessScopesExcept(presenter *presentation.Presenter, status runtimeStatusResult, skip map[string]struct{}) {
	if presenter == nil {
		return
	}
	for _, state := range managedReadinessScopeStates(status) {
		if _, ok := skip[state.Scope.ID]; ok {
			continue
		}
		renderManagedReadinessScope(presenter, state)
	}
}

func managedRestartReadinessObserver(progress *commandProgress) (func(runtimeStatusResult), map[string]struct{}) {
	rendered := map[string]struct{}{}
	return func(status runtimeStatusResult) {
		if progress == nil || progress.session == nil {
			return
		}
		ready := make([]managedReadinessScopeState, 0, len(managedRestartReadinessScopes))
		for _, state := range managedRestartReadinessScopeStates(status) {
			if !state.Ready {
				continue
			}
			if _, ok := rendered[state.Scope.ID]; ok {
				continue
			}
			rendered[state.Scope.ID] = struct{}{}
			ready = append(ready, state)
		}
		if status.TunnelEnabled && status.TunnelConfigured && status.TunnelReady {
			if _, ok := rendered["tunnel"]; !ok {
				rendered["tunnel"] = struct{}{}
				ready = append(ready, managedReadinessScopeState{
					Scope: managedReadinessScope{ID: "tunnel", Label: "OpenAI Secure MCP Tunnel"},
					Ready: true,
				})
			}
		}
		if len(ready) == 0 {
			return
		}
		progress.session.Suspend()
		for _, state := range ready {
			renderManagedReadinessScope(progress.session.Presenter(), state)
		}
		progress.session.Resume()
	}, rendered
}

func managedRestartRuntimeStatus(ctx context.Context) (runtimeStatusResult, bool, error) {
	status, running, err := managedRuntimeStatus(ctx)
	if err == nil && running && !managedRestartStatusReady(status) {
		status.Starting = true
	}
	return status, running, err
}

func managedRestartStatusReady(status runtimeStatusResult) bool {
	if status.Starting {
		return false
	}
	if status.TunnelEnabled && status.TunnelConfigured && !status.TunnelReady {
		return false
	}
	for _, component := range status.Readiness {
		if component.ID == "telegram" && component.Configured && !component.Ready {
			return false
		}
	}
	return true
}

func managedLifecycleDoneMessage(event managed.LifecycleEvent) string {
	switch event.Phase {
	case "runtime.stopping":
		return "Stopped managed runtime"
	case "backend.stopping":
		return "Stopped managed service backend"
	case "definition.installing":
		if strings.HasPrefix(strings.ToLower(event.Message), "updating") {
			return "Updated managed service definition"
		}
		return "Installed managed service definition"
	case "definition.uninstalling":
		return "Removed managed service definition"
	case "backend.starting":
		return "Started managed service backend"
	case "runtime.waiting":
		return "Managed runtime ready"
	default:
		return event.Message + " complete"
	}
}

func managedRestartLifecycleGroup(phase string) string {
	switch phase {
	case "runtime.stopping", "backend.stopping":
		return "stop"
	case "definition.installing":
		return "definition"
	case "backend.starting", "runtime.waiting":
		return "start"
	default:
		return ""
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

func tunnelMetadataScopeFields(metadata tunnel.Metadata) []presentation.Field {
	fields := make([]presentation.Field, 0, 3)
	if len(metadata.OrganizationIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "organization", Value: strings.Join(metadata.OrganizationIDs, ",")})
	}
	if len(metadata.WorkspaceIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "workspace", Value: strings.Join(metadata.WorkspaceIDs, ",")})
	}
	if len(metadata.TenantIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "tenant", Value: strings.Join(metadata.TenantIDs, ",")})
	}
	return fields
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

func managedBackendLabel(manager managed.Manager, spec managed.Spec) string {
	if runtime.GOOS == "linux" && spec.Scope == managed.ScopeUser {
		return "systemd --user"
	}
	return manager.Backend()
}
