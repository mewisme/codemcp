//go:build linux

package released024

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/service"
)

type platformHistoricalServiceController struct{}

func (platformHistoricalServiceController) Quiesce(ctx context.Context, source SourceDescriptor, state ServiceState) error {
	if err := verifyHistoricalServiceDefinition(source, state); err != nil {
		return err
	}
	manager := service.NewManager()
	spec, err := historicalServiceSpec(ctx, source, state)
	if err != nil {
		return err
	}
	if err := manager.Stop(spec); err != nil {
		return err
	}
	return waitHistoricalStopped(ctx, manager, spec)
}

func verifyHistoricalServiceDefinition(source SourceDescriptor, state ServiceState) error {
	if strings.TrimSpace(state.DefinitionPath) == "" {
		return fmt.Errorf("historical service %s definition path is missing", state.ID)
	}
	info, err := os.Lstat(state.DefinitionPath)
	if err != nil {
		return fmt.Errorf("inspect historical service %s definition: %w", state.ID, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("historical service %s definition is not a regular non-symlink file", state.ID)
	}
	data, err := os.ReadFile(state.DefinitionPath)
	if err != nil {
		return err
	}
	ownership, _ := inspectSystemdOwnership(string(data), source)
	if ownership != OwnershipVerified {
		return fmt.Errorf("historical service %s ownership changed before quiescence", state.ID)
	}
	launcher := inspectSystemdLauncher(string(data))
	if launcher == "" || !sameComparablePath(launcher, state.Binary) {
		return fmt.Errorf("historical service %s executable changed before quiescence", state.ID)
	}
	return nil
}

func (platformHistoricalServiceController) Restore(ctx context.Context, source SourceDescriptor, state ServiceState) error {
	if !state.Running {
		return nil
	}
	manager := service.NewManager()
	spec, err := historicalServiceSpec(ctx, source, state)
	if err != nil {
		return err
	}
	return manager.Start(spec)
}

func historicalServiceSpec(ctx context.Context, source SourceDescriptor, state ServiceState) (service.Spec, error) {
	scope := service.ScopeUser
	if state.Scope == string(service.ScopeSystem) {
		scope = service.ScopeSystem
	}
	account, err := service.InvokingAccountContext(ctx, scope)
	if err != nil {
		return service.Spec{}, err
	}
	if source.OperatorHome != "" && scope == service.ScopeUser {
		account.HomeDir = source.OperatorHome
	}
	return service.Spec{ID: state.ID, Scope: scope, ConfigRoot: state.ConfigRoot, Binary: state.Binary, Account: account}, nil
}

func waitHistoricalStopped(ctx context.Context, manager service.Manager, spec service.Spec) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		status, err := manager.Status(spec)
		if err != nil {
			return err
		}
		if !status.Running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("historical service %s did not stop", spec.ID)
		case <-ticker.C:
		}
	}
}
