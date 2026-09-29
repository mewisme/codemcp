package application

import (
	"context"
	"fmt"
	"runtime"

	"go.mewis.me/codemcp/internal/install"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	managed "go.mewis.me/codemcp/internal/service"
)

type UninstallResult struct {
	Install  install.UninstallResult `json:"install"`
	Services []managed.Scope         `json:"services,omitempty"`
}

type UninstallOptions struct {
	ExternalCleanup bool `json:"external_cleanup,omitempty"`
}

func UninstallCurrent(ctx context.Context, options UninstallOptions) (UninstallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	layout, err := install.DefaultLayout()
	if err != nil {
		return UninstallResult{}, err
	}
	result := UninstallResult{Services: []managed.Scope{}}
	for _, scope := range []managed.Scope{managed.ScopeUser, managed.ScopeSystem} {
		if scope == managed.ScopeSystem && runtime.GOOS == "windows" {
			continue
		}
		spec, manager, specErr := managedService(scope, layout.CanonicalBinary)
		if specErr != nil {
			continue
		}
		status, statusErr := manager.Status(spec)
		if statusErr != nil {
			return result, fmt.Errorf("inspect %s managed service: %w", scope, statusErr)
		}
		if !status.Installed {
			continue
		}
		lifecycle := managed.Lifecycle{
			Manager: manager, Spec: spec,
			Probe: func(ctx context.Context) (runtimeStatus runtimecontrol.RuntimeStatus, running bool, err error) {
				return RuntimeStatus(ctx)
			},
			Shutdown: func(ctx context.Context) error { return runtimecontrol.RequestShutdownAt(ctx, spec.ConfigRoot) },
			WaitStatusChange: func(ctx context.Context, lifecycle string) (runtimecontrol.RuntimeStatus, error) {
				return runtimecontrol.WaitStatusChangeAt(ctx, spec.ConfigRoot, lifecycle)
			},
		}
		if _, downErr := lifecycle.Down(ctx); downErr != nil {
			return result, fmt.Errorf("remove %s managed service: %w", scope, downErr)
		}
		result.Services = append(result.Services, scope)
	}
	removed, err := install.UninstallOwnedWithOptions(install.UninstallOptions{Layout: layout, PreserveBinaryTree: options.ExternalCleanup})
	if err != nil {
		return result, err
	}
	result.Install = removed
	return result, nil
}
