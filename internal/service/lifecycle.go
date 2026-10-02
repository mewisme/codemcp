package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	DefaultLifecycleTimeout = 45 * time.Second
	defaultPollInterval     = 150 * time.Millisecond
)

type RuntimeProbe func(context.Context) (runtimecontrol.RuntimeStatus, bool, error)
type RuntimeShutdown func(context.Context) error
type RuntimeStatusWait func(context.Context, string) (runtimecontrol.RuntimeStatus, error)

type LifecycleEvent struct {
	Phase   string
	Message string
}

type LifecycleObserver func(LifecycleEvent)

type Lifecycle struct {
	Manager          Manager
	Spec             Spec
	Probe            RuntimeProbe
	Shutdown         RuntimeShutdown
	WaitStatusChange RuntimeStatusWait
	Timeout          time.Duration
	Observe          LifecycleObserver
	ObserveStatus    func(runtimecontrol.RuntimeStatus)
}

type RuntimeOwnerConflictKind string

const (
	RuntimeOwnerForeground RuntimeOwnerConflictKind = "foreground"
	RuntimeOwnerSystem     RuntimeOwnerConflictKind = "system"
	RuntimeOwnerUser       RuntimeOwnerConflictKind = "user"
	RuntimeOwnerOther      RuntimeOwnerConflictKind = "other"
)

type RuntimeOwnerConflictError struct {
	Kind      RuntimeOwnerConflictKind
	Action    string
	ServiceID string
	PID       int
}

func (err *RuntimeOwnerConflictError) Error() string {
	if err == nil {
		return "runtime ownership conflict"
	}
	switch err.Kind {
	case RuntimeOwnerForeground:
		return fmt.Sprintf("runtime is already running outside the managed service (pid %d)", err.PID)
	case RuntimeOwnerSystem:
		return fmt.Sprintf("runtime is managed by a system service; use cm %s --system", err.Action)
	case RuntimeOwnerUser:
		return fmt.Sprintf("runtime is managed by a user service; use cm %s", err.Action)
	default:
		return fmt.Sprintf("another managed service is already running for this config (service %s, pid %d)", err.ServiceID, err.PID)
	}
}

type LifecycleResult struct {
	Status  runtimecontrol.RuntimeStatus
	Changed bool
}

func (l Lifecycle) Up(ctx context.Context) (LifecycleResult, error) {
	if err := l.validate(); err != nil {
		return LifecycleResult{}, err
	}
	current, running, err := l.probeRuntime(ctx, "up")
	if err != nil {
		return LifecycleResult{}, err
	}
	if err := ValidateRuntimeOwner(current, running, l.Spec, "up"); err != nil {
		return LifecycleResult{}, err
	}
	backend, err := l.inspectBackend(ctx)
	if err != nil {
		return LifecycleResult{}, err
	}
	matches, err := l.inspectDefinition(ctx)
	if err != nil {
		return LifecycleResult{}, err
	}
	if running && backend.Installed && matches {
		if current.Starting {
			l.emit("runtime.waiting", "Waiting for managed runtime readiness")
			current, err = l.waitReady(ctx, "")
			if err != nil {
				return LifecycleResult{}, err
			}
		}
		return LifecycleResult{Status: current}, nil
	}
	if running {
		l.emit("runtime.stopping", "Stopping existing managed runtime")
		if err := l.shutdownAndWait(ctx); err != nil {
			return LifecycleResult{}, err
		}
	}
	if backend.Running || (running && backend.Installed) {
		l.emit("backend.stopping", "Stopping managed service backend")
		if err := l.backendOperation(ctx, "stop", "Stopping managed service backend", func() error { return StopBackend(l.Manager, l.Spec) }); err != nil {
			return LifecycleResult{}, err
		}
	}
	if !backend.Installed || !matches {
		l.emit("definition.installing", "Installing managed service definition")
		if err := l.backendOperation(ctx, "install", "Installing managed service definition", func() error { return l.Manager.Install(l.Spec) }); err != nil {
			return LifecycleResult{}, err
		}
	}
	l.emit("backend.starting", "Starting managed service backend")
	if err := l.backendOperation(ctx, "start", "Starting managed service backend", func() error { return l.Manager.Start(l.Spec) }); err != nil {
		return LifecycleResult{}, err
	}
	l.emit("runtime.waiting", "Waiting for managed runtime readiness")
	status, err := l.waitReady(ctx, "")
	if err != nil {
		return LifecycleResult{}, err
	}
	return LifecycleResult{Status: status, Changed: true}, nil
}

func (l Lifecycle) Down(ctx context.Context) (LifecycleResult, error) {
	if err := l.validate(); err != nil {
		return LifecycleResult{}, err
	}
	current, running, err := l.probeRuntime(ctx, "down")
	if err != nil {
		return LifecycleResult{}, err
	}
	if err := ValidateRuntimeOwner(current, running, l.Spec, "down"); err != nil {
		return LifecycleResult{}, err
	}
	backend, err := l.inspectBackend(ctx)
	if err != nil {
		return LifecycleResult{}, err
	}
	if !running && !backend.Installed {
		return LifecycleResult{}, nil
	}
	if running {
		l.emit("runtime.stopping", "Stopping managed runtime")
		if err := l.shutdownAndWait(ctx); err != nil {
			return LifecycleResult{}, err
		}
	}
	if backend.Running || backend.Installed {
		l.emit("backend.stopping", "Stopping managed service backend")
		if err := l.backendOperation(ctx, "stop", "Stopping managed service backend", func() error { return StopBackend(l.Manager, l.Spec) }); err != nil {
			return LifecycleResult{}, err
		}
	}
	if backend.Installed {
		l.emit("definition.uninstalling", "Removing managed service definition")
		if err := l.backendOperation(ctx, "uninstall", "Removing managed service definition", func() error { return l.Manager.Uninstall(l.Spec) }); err != nil {
			return LifecycleResult{}, err
		}
	}
	return LifecycleResult{Changed: true}, nil
}

func (l Lifecycle) Restart(ctx context.Context) (LifecycleResult, error) {
	if err := l.validate(); err != nil {
		return LifecycleResult{}, err
	}
	current, running, err := l.probeRuntime(ctx, "restart")
	if err != nil {
		return LifecycleResult{}, err
	}
	if err := ValidateRuntimeOwner(current, running, l.Spec, "restart"); err != nil {
		return LifecycleResult{}, err
	}
	backend, err := l.inspectBackend(ctx)
	if err != nil {
		return LifecycleResult{}, err
	}
	if !backend.Installed {
		return l.Up(ctx)
	}
	matches, err := l.inspectDefinition(ctx)
	if err != nil {
		return LifecycleResult{}, err
	}
	previousRunID := current.RunID
	if running {
		l.emit("runtime.stopping", "Stopping current managed runtime")
		if err := l.shutdownAndWait(ctx); err != nil {
			return LifecycleResult{}, err
		}
	}
	l.emit("backend.stopping", "Stopping managed service backend")
	if err := l.backendOperation(ctx, "stop", "Stopping managed service backend", func() error { return StopBackend(l.Manager, l.Spec) }); err != nil {
		return LifecycleResult{}, err
	}
	if !matches {
		l.emit("definition.installing", "Updating managed service definition")
		if err := l.backendOperation(ctx, "install", "Updating managed service definition", func() error { return l.Manager.Install(l.Spec) }); err != nil {
			return LifecycleResult{}, err
		}
	}
	l.emit("backend.starting", "Starting managed service backend")
	if err := l.backendOperation(ctx, "start", "Starting managed service backend", func() error { return l.Manager.Start(l.Spec) }); err != nil {
		return LifecycleResult{}, err
	}
	l.emit("runtime.waiting", "Waiting for managed runtime readiness")
	status, err := l.waitReady(ctx, previousRunID)
	if err != nil {
		return LifecycleResult{}, err
	}
	return LifecycleResult{Status: status, Changed: true}, nil
}

func (l Lifecycle) validate() error {
	if l.Manager == nil || l.Probe == nil || l.Shutdown == nil {
		return errors.New("managed lifecycle dependencies are unavailable")
	}
	if l.Spec.ID == "" {
		return errors.New("managed service specification is unavailable")
	}
	return nil
}

func (l Lifecycle) timeout() time.Duration {
	if l.Timeout > 0 {
		return l.Timeout
	}
	return DefaultLifecycleTimeout
}

func (l Lifecycle) emit(phase, message string) {
	if l.Observe != nil {
		l.Observe(LifecycleEvent{Phase: phase, Message: message})
	}
}

func (l Lifecycle) traceFields() []tracepkg.Field {
	backend := ""
	if l.Manager != nil {
		backend = l.Manager.Backend()
	}
	return []tracepkg.Field{tracepkg.String("backend", backend), tracepkg.String("service", l.Spec.ID), tracepkg.String("scope", string(l.Spec.Scope))}
}

func (l Lifecycle) probeRuntime(ctx context.Context, action string) (runtimecontrol.RuntimeStatus, bool, error) {
	span := tracepkg.Start(ctx, "SERVICE", "service.runtime.inspect", "Inspecting managed runtime", append(l.traceFields(), tracepkg.String("action", action))...)
	status, running, err := l.Probe(ctx)
	if err != nil {
		span.FailMessage("Managed runtime inspection failed", err)
		return runtimecontrol.RuntimeStatus{}, false, err
	}
	span.EndMessage("Managed runtime inspected", tracepkg.Bool("running", running), tracepkg.Bool("managed", status.Managed), tracepkg.Bool("starting", status.Starting), tracepkg.Int("pid", status.PID), tracepkg.String("lifecycle", status.Lifecycle), tracepkg.String("runtime_service", status.ServiceID), tracepkg.String("runtime_scope", status.ServiceScope))
	return status, running, nil
}

func (l Lifecycle) inspectBackend(ctx context.Context) (Status, error) {
	span := tracepkg.Start(ctx, "SERVICE", "service.backend.inspect", "Inspecting managed service backend", l.traceFields()...)
	status, err := l.Manager.Status(l.Spec)
	if err != nil {
		span.FailMessage("Managed service backend inspection failed", err)
		return Status{}, err
	}
	span.EndMessage("Managed service backend inspected", tracepkg.String("backend", status.Backend), tracepkg.String("service", l.Spec.ID), tracepkg.Bool("installed", status.Installed), tracepkg.Bool("running", status.Running), tracepkg.Int("pid", status.PID))
	return status, nil
}

func (l Lifecycle) inspectDefinition(ctx context.Context) (bool, error) {
	span := tracepkg.Start(ctx, "SERVICE", "service.definition.inspect", "Inspecting managed service definition", l.traceFields()...)
	matches, err := l.Manager.DefinitionMatches(l.Spec)
	if err != nil {
		span.FailMessage("Managed service definition inspection failed", err)
		return false, err
	}
	span.EndMessage("Managed service definition inspected", tracepkg.Bool("definition_matches", matches))
	return matches, nil
}

func (l Lifecycle) backendOperation(ctx context.Context, operation, message string, run func() error) error {
	span := tracepkg.Start(ctx, "SERVICE", "service.backend."+operation, message, append(l.traceFields(), tracepkg.String("operation", operation))...)
	if err := run(); err != nil {
		span.FailMessage(message+" failed", err)
		return err
	}
	status, statusErr := l.Manager.Status(l.Spec)
	if statusErr != nil {
		span.EndMessage(message+" completed", tracepkg.String("final_state", "unavailable"))
		return nil
	}
	span.EndMessage(message+" completed", tracepkg.String("final_state", "available"), tracepkg.Bool("installed", status.Installed), tracepkg.Bool("running", status.Running), tracepkg.Int("pid", status.PID))
	return nil
}

func (l Lifecycle) shutdownAndWait(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, min(5*time.Second, l.timeout()))
	span := tracepkg.Start(ctx, "SERVICE", "service.runtime.shutdown", "Requesting managed runtime shutdown", l.traceFields()...)
	err := l.Shutdown(shutdownCtx)
	cancel()
	if err != nil {
		span.FailMessage("Managed runtime shutdown request failed", err)
		return err
	}
	span.EndMessage("Managed runtime shutdown requested")
	return l.waitStopped(ctx)
}

func (l Lifecycle) waitStopped(ctx context.Context) error {
	return WaitRuntimeStopped(ctx, l.Probe, l.timeout())
}

func WaitRuntimeStopped(ctx context.Context, probe RuntimeProbe, timeout time.Duration) error {
	if probe == nil {
		return errors.New("managed runtime probe is unavailable")
	}
	if timeout <= 0 {
		timeout = DefaultLifecycleTimeout
	}
	started := time.Now()
	span := tracepkg.Start(ctx, "SERVICE", "service.runtime.stopped.wait", "Waiting for managed runtime to stop", tracepkg.Int64("timeout_ms", timeout.Milliseconds()))
	deadline := time.Now().Add(timeout)
	attempts := 0
	firstProbe := true
	lastLifecycle := ""
	for time.Now().Before(deadline) {
		attempts++
		status, running, err := probe(ctx)
		if firstProbe {
			tracepkg.Emit(ctx, "SERVICE", "service.runtime.probe", "Probed managed runtime", tracepkg.String("mode", "stop"), tracepkg.Int("attempt", attempts), tracepkg.Bool("running", running), tracepkg.Bool("error_present", err != nil), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			firstProbe = false
		}
		if err != nil {
			tracepkg.Emit(ctx, "SERVICE", "service.runtime.probe-error.changed", "Managed runtime probe error changed", tracepkg.String("mode", "stop"), tracepkg.Int("attempt", attempts), tracepkg.String("probe_error", err.Error()), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			span.FailMessage("Managed runtime stop probe failed", err, tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			return err
		}
		if !running {
			span.EndMessage("Managed runtime stopped", tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			return nil
		}
		if status.Lifecycle != lastLifecycle {
			tracepkg.Emit(ctx, "SERVICE", "service.runtime.lifecycle.changed", "Managed runtime lifecycle changed", tracepkg.String("mode", "stop"), tracepkg.Int("attempt", attempts), tracepkg.String("previous", lastLifecycle), tracepkg.String("current", status.Lifecycle), tracepkg.Int("pid", status.PID), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			lastLifecycle = status.Lifecycle
		}
		if status.Lifecycle != "" {
			waitCtx, cancel := context.WithTimeout(ctx, min(10*time.Second, time.Until(deadline)))
			_, waitErr := runtimecontrol.WaitStatusChange(waitCtx, status.Lifecycle)
			cancel()
			if waitErr == nil {
				continue
			}
			if ctx.Err() != nil {
				span.FailMessage("Managed runtime stop wait canceled", ctx.Err(), tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
				return ctx.Err()
			}
		}
		if err := waitLifecyclePoll(ctx); err != nil {
			span.FailMessage("Managed runtime stop wait canceled", err, tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			return err
		}
	}
	err := errors.New("managed runtime did not stop")
	span.FailMessage("Managed runtime did not stop before timeout", err, tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
	return err
}

func (l Lifecycle) waitReady(ctx context.Context, previousRunID string) (runtimecontrol.RuntimeStatus, error) {
	wait := l.WaitStatusChange
	if wait == nil {
		wait = runtimecontrol.WaitStatusChange
	}
	return waitRuntimeReady(ctx, l.Spec, l.Probe, wait, previousRunID, l.timeout(), l.ObserveStatus)
}

func WaitRuntimeReady(ctx context.Context, spec Spec, probe RuntimeProbe, previousRunID string, timeout time.Duration) (runtimecontrol.RuntimeStatus, error) {
	return waitRuntimeReady(ctx, spec, probe, runtimecontrol.WaitStatusChange, previousRunID, timeout, nil)
}

func waitRuntimeReady(ctx context.Context, spec Spec, probe RuntimeProbe, waitStatusChange RuntimeStatusWait, previousRunID string, timeout time.Duration, observeStatus func(runtimecontrol.RuntimeStatus)) (runtimecontrol.RuntimeStatus, error) {
	if probe == nil {
		return runtimecontrol.RuntimeStatus{}, errors.New("managed runtime probe is unavailable")
	}
	if timeout <= 0 {
		timeout = DefaultLifecycleTimeout
	}
	started := time.Now()
	span := tracepkg.Start(ctx, "SERVICE", "service.runtime.ready.wait", "Waiting for managed runtime readiness", tracepkg.String("service", spec.ID), tracepkg.String("scope", string(spec.Scope)), tracepkg.Int64("timeout_ms", timeout.Milliseconds()), tracepkg.Bool("previous_run_present", previousRunID != ""))
	deadline := time.Now().Add(timeout)
	var lastErr error
	attempts := 0
	firstProbe := true
	controlDiscovered := false
	lastLifecycle := ""
	lastProbeError := ""
	for time.Now().Before(deadline) {
		attempts++
		status, running, err := probe(ctx)
		if err == nil && running && observeStatus != nil {
			observeStatus(status)
		}
		if firstProbe {
			tracepkg.Emit(ctx, "SERVICE", "service.runtime.probe", "Probed managed runtime", tracepkg.String("mode", "ready"), tracepkg.Int("attempt", attempts), tracepkg.Bool("running", running), tracepkg.Bool("error_present", err != nil), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			firstProbe = false
		}
		if err != nil {
			lastErr = err
			if err.Error() != lastProbeError {
				tracepkg.Emit(ctx, "SERVICE", "service.runtime.probe-error.changed", "Managed runtime probe error changed", tracepkg.String("mode", "ready"), tracepkg.Int("attempt", attempts), tracepkg.String("probe_error", err.Error()), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
				lastProbeError = err.Error()
			}
		} else if running {
			if !controlDiscovered {
				tracepkg.Emit(ctx, "SERVICE", "service.runtime.control.discovered", "Discovered managed runtime control endpoint", tracepkg.Int("attempt", attempts), tracepkg.Int("pid", status.PID), tracepkg.String("lifecycle", status.Lifecycle), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
				controlDiscovered = true
			}
			if status.Lifecycle != lastLifecycle {
				tracepkg.Emit(ctx, "SERVICE", "service.runtime.lifecycle.changed", "Managed runtime lifecycle changed", tracepkg.String("mode", "ready"), tracepkg.Int("attempt", attempts), tracepkg.String("previous", lastLifecycle), tracepkg.String("current", status.Lifecycle), tracepkg.Int("pid", status.PID), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
				lastLifecycle = status.Lifecycle
			}
			if err := ValidateRuntimeOwner(status, true, spec, "up"); err != nil {
				span.FailMessage("Managed runtime owner validation failed", err, tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
				return runtimecontrol.RuntimeStatus{}, err
			}
			if previousRunID != "" && status.RunID == previousRunID {
				lastErr = errors.New("previous managed runtime is still shutting down")
			} else if status.Starting {
				lastErr = errors.New("managed runtime is still starting")
				if observeStatus == nil && status.Lifecycle != "" && waitStatusChange != nil {
					waitCtx, cancel := context.WithTimeout(ctx, min(10*time.Second, time.Until(deadline)))
					_, waitErr := waitStatusChange(waitCtx, status.Lifecycle)
					cancel()
					if waitErr == nil {
						continue
					}
					if ctx.Err() != nil {
						span.FailMessage("Managed runtime readiness wait canceled", ctx.Err(), tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
						return runtimecontrol.RuntimeStatus{}, ctx.Err()
					}
				}
			} else {
				span.EndMessage("Managed runtime became ready", tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()), tracepkg.Int("pid", status.PID), tracepkg.String("lifecycle", status.Lifecycle))
				return status, nil
			}
		}
		if err := waitLifecyclePoll(ctx); err != nil {
			span.FailMessage("Managed runtime readiness wait canceled", err, tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
			return runtimecontrol.RuntimeStatus{}, err
		}
	}
	if lastErr != nil {
		err := fmt.Errorf("managed service did not become ready: %w", lastErr)
		span.FailMessage("Managed runtime did not become ready before timeout", err, tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
		return runtimecontrol.RuntimeStatus{}, err
	}
	err := errors.New("managed service did not become ready")
	span.FailMessage("Managed runtime did not become ready before timeout", err, tracepkg.Int("attempts", attempts), tracepkg.Int64("elapsed_ms", time.Since(started).Milliseconds()))
	return runtimecontrol.RuntimeStatus{}, err
}

func waitLifecyclePoll(ctx context.Context) error {
	timer := time.NewTimer(defaultPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func StopBackend(manager Manager, spec Spec) error {
	if err := manager.Stop(spec); err != nil {
		status, statusErr := manager.Status(spec)
		if statusErr == nil && status.Installed && !status.Running && status.PID == 0 {
			return nil
		}
		return err
	}
	return nil
}

func ValidateRuntimeOwner(status runtimecontrol.RuntimeStatus, running bool, spec Spec, action string) error {
	if !running {
		return nil
	}
	if !status.Managed {
		return &RuntimeOwnerConflictError{Kind: RuntimeOwnerForeground, Action: action, PID: status.PID}
	}
	if status.ServiceID == spec.ID && status.ServiceScope == string(spec.Scope) {
		return nil
	}
	if status.ServiceScope == string(ScopeSystem) && spec.Scope == ScopeUser {
		return &RuntimeOwnerConflictError{Kind: RuntimeOwnerSystem, Action: action, ServiceID: status.ServiceID, PID: status.PID}
	}
	if status.ServiceScope == string(ScopeUser) && spec.Scope == ScopeSystem {
		return &RuntimeOwnerConflictError{Kind: RuntimeOwnerUser, Action: action, ServiceID: status.ServiceID, PID: status.PID}
	}
	return &RuntimeOwnerConflictError{Kind: RuntimeOwnerOther, Action: action, ServiceID: status.ServiceID, PID: status.PID}
}
