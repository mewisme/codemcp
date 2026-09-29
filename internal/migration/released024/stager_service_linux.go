//go:build linux

package released024

import (
	"context"
	"fmt"
	"os"
	"strings"

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

func (platformHistoricalServiceController) Retire(ctx context.Context, source SourceDescriptor, state ServiceState) error {
	spec, err := historicalServiceSpec(ctx, source, state)
	if err != nil {
		return err
	}
	manager := service.NewManager()
	if _, err := os.Lstat(state.DefinitionPath); err != nil {
		if os.IsNotExist(err) {
			status, statusErr := manager.Status(spec)
			if statusErr != nil {
				return statusErr
			}
			if !status.Installed && !status.Running {
				return nil
			}
		}
		return err
	}
	if err := verifyHistoricalServiceDefinition(source, state); err != nil {
		return err
	}
	status, err := manager.Status(spec)
	if err != nil {
		return err
	}
	if status.Running {
		if err := manager.Stop(spec); err != nil {
			return err
		}
		if err := waitHistoricalStopped(ctx, manager, spec); err != nil {
			return err
		}
	}
	if err := manager.Uninstall(spec); err != nil {
		return err
	}
	status, err = manager.Status(spec)
	if err != nil {
		return err
	}
	if status.Installed || status.Running {
		return fmt.Errorf("historical service %s remained installed after retirement", state.ID)
	}
	return nil
}
