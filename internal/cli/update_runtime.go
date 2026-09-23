package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	managed "go.mewis.me/codemcp/internal/service"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type updateRuntimeState struct {
	Running bool
	Status  runtimeStatusResult
}

type updateRuntimeRestartFunc func(*cobra.Command, install.Layout, runtimeStatusResult) error

func captureUpdateRuntimeState(parent context.Context) (updateRuntimeState, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	status, running, err := managedRuntimeStatus(ctx)
	if err != nil {
		return updateRuntimeState{}, err
	}
	return updateRuntimeState{Running: running, Status: status}, nil
}

func coordinateUpdatedRuntime(cmd *cobra.Command, installed install.Result, state updateRuntimeState, noRestart bool) error {
	return coordinateUpdatedRuntimeWith(cmd, installed, state, noRestart, restartManagedRuntimeAfterUpdate)
}

func coordinateUpdatedRuntimeWith(cmd *cobra.Command, installed install.Result, state updateRuntimeState, noRestart bool, restart updateRuntimeRestartFunc) error {
	logCommandDebug(cmd, "UPDATE", "update.runtime.state", "Resolved runtime coordination state", logger.WithDebug("running", state.Running), logger.WithDebug("managed", state.Status.Managed), logger.WithDebug("pid", state.Status.PID), logger.WithDebug("service", state.Status.ServiceID))
	if !state.Running {
		return nil
	}
	log := commandLogger(cmd)
	if noRestart {
		log.Notice("UPDATE", "update.restart-skipped", "Runtime restart skipped")
		log.Detail("pid", state.Status.PID)
		return nil
	}
	if !state.Status.Managed {
		log.Notice("UPDATE", "update.foreground-running", "Foreground runtime is still using the previous version; restart it manually")
		log.Detail("pid", state.Status.PID)
		return nil
	}
	log.Action("UPDATE", "update.runtime-restarting", "Restarting managed runtime")
	if err := restart(cmd, installed.Layout, state.Status); err != nil {
		log.Warning("UPDATE", "update.runtime-restart-failed", "Managed runtime restart failed; rolling back", err)
		if rollbackErr := install.RollbackResult(installed); rollbackErr != nil {
			return fmt.Errorf("managed runtime restart failed: %w; rollback failed: %v", err, rollbackErr)
		}
		previous := installed.Activation.PreviousVersion
		log.Ready("UPDATE", "update.rollback-complete", "Previous version restored")
		if previous != "" {
			log.Detail("current", previous)
		}
		if rollbackRestartErr := restart(cmd, installed.Layout, state.Status); rollbackRestartErr != nil {
			return fmt.Errorf("managed runtime restart failed: %w; rolled back to %s but previous runtime restart failed: %v", err, previous, rollbackRestartErr)
		}
		log.Ready("UPDATE", "update.rollback-runtime-restarted", "Previous managed runtime restarted")
		return fmt.Errorf("managed runtime restart failed: %w; rolled back to %s", err, previous)
	}
	log.Ready("UPDATE", "update.runtime-restarted", "Managed runtime restarted")
	return nil
}

func restartManagedRuntimeAfterUpdate(cmd *cobra.Command, layout install.Layout, status runtimeStatusResult) error {
	logCommandStep(cmd, "UPDATE", "update.runtime.restart.preparing", "Preparing managed runtime restart after update")
	if filepath.Clean(status.ConfigRoot) != filepath.Clean(config.RootPath()) {
		return fmt.Errorf("managed runtime config root mismatch: runtime %s, selected %s", status.ConfigRoot, config.RootPath())
	}
	scope := managed.Scope(status.ServiceScope)
	if scope != managed.ScopeUser && scope != managed.ScopeSystem {
		return fmt.Errorf("managed runtime has invalid service scope %q", status.ServiceScope)
	}
	account, err := managed.InvokingAccountContext(cmd.Context(), scope)
	if err != nil {
		return err
	}
	spec, err := managed.NewSpecContext(cmd.Context(), status.ConfigRoot, layout.CanonicalBinary, scope, account)
	if err != nil {
		return err
	}
	if status.ServiceID == "" || spec.ID != status.ServiceID {
		return fmt.Errorf("managed runtime service mismatch: runtime %s, expected %s", status.ServiceID, spec.ID)
	}
	if scope == managed.ScopeSystem && managed.DetectScope() == managed.ScopeUser {
		logCommandStep(cmd, "UPDATE", "update.runtime.restart.elevating", "Elevating managed runtime restart")
		environmentHash, err := saveManagedEnvironmentContext(cmd.Context(), spec)
		if err != nil {
			return err
		}
		return elevateManagedCommandWithBinary(cmd, "restart", environmentHash, layout.CanonicalBinary)
	}
	logCommandStep(cmd, "UPDATE", "update.runtime.restart.local", "Restarting managed runtime in place")
	return restartManagedRuntimeInPlace(cmd.Context(), spec, managed.NewManagerWithObserver(tracepkg.ObserverFromContext(cmd.Context())))
}

func restartManagedRuntimeInPlace(parent context.Context, spec managed.Spec, manager managed.Manager) error {
	return restartManagedRuntimeInPlaceWith(parent, spec, manager, managedRuntimeStatus, requestManagedShutdown)
}

func restartManagedRuntimeInPlaceWith(parent context.Context, spec managed.Spec, manager managed.Manager, probe managed.RuntimeProbe, shutdown managed.RuntimeShutdown) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	current, running, statusErr := probe(ctx)
	cancel()
	if statusErr != nil {
		return statusErr
	}
	if running && (!current.Managed || current.ServiceID != spec.ID || current.ServiceScope != string(spec.Scope)) {
		return managedScopeConflict(current, spec, "restart")
	}
	backendStatus, err := manager.Status(spec)
	if err != nil {
		return err
	}
	if !backendStatus.Installed {
		return errors.New("managed service is not installed")
	}
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: probe, Shutdown: shutdown, Timeout: serviceReadyTimeout}
	_, err = lifecycle.Restart(parent)
	return err
}
