package application

import (
	"context"
	"fmt"
	"runtime"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	managed "go.mewis.me/codemcp/internal/service"
)

type UninstallResult struct {
	Install            install.UninstallResult `json:"install"`
	Services           []managed.Scope         `json:"services,omitempty"`
	GlobalStateRemoved bool                    `json:"global_state_removed,omitempty"`
}

type UninstallOptions struct {
	ExternalCleanup bool `json:"external_cleanup,omitempty"`
}

type uninstallDependencies struct {
	Layout         func() (install.Layout, error)
	RemoveServices func(context.Context, install.Layout) ([]managed.Scope, error)
	ConfigRoot     func() string
	Uninitialize   func(context.Context, string) error
	UninstallOwned func(install.UninstallOptions) (install.UninstallResult, error)
}

func UninstallCurrent(ctx context.Context, options UninstallOptions) (UninstallResult, error) {
	return uninstallCurrentWithDependencies(ctx, options, uninstallDependencies{
		Layout:         install.DefaultLayout,
		RemoveServices: removeManagedServicesForUninstall,
		ConfigRoot:     configformat.RootPath,
		Uninitialize:   UninitializeContext,
		UninstallOwned: install.UninstallOwnedWithOptions,
	})
}

func uninstallCurrentWithDependencies(ctx context.Context, options UninstallOptions, deps uninstallDependencies) (UninstallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	layout, err := deps.Layout()
	if err != nil {
		return UninstallResult{}, err
	}
	services, err := deps.RemoveServices(ctx, layout)
	if err != nil {
		return UninstallResult{}, err
	}
	result := UninstallResult{Services: services}

	root := deps.ConfigRoot()
	if shouldRemoveGlobalState(root) {
		if err := deps.Uninitialize(ctx, root); err != nil {
			return result, fmt.Errorf("remove CodeMCP global state: %w", err)
		}
		result.GlobalStateRemoved = true
	}
	removed, err := deps.UninstallOwned(install.UninstallOptions{Layout: layout, PreserveBinaryTree: options.ExternalCleanup})
	if err != nil {
		return result, err
	}
	result.Install = removed
	return result, nil
}

func shouldRemoveGlobalState(root string) bool {
	return configformat.IsManagedRoot(root) || root == configformat.DefaultRootPath()
}

func removeManagedServicesForUninstall(ctx context.Context, layout install.Layout) ([]managed.Scope, error) {
	removed := []managed.Scope{}
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
			return removed, fmt.Errorf("inspect %s managed service: %w", scope, statusErr)
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
			return removed, fmt.Errorf("remove %s managed service: %w", scope, downErr)
		}
		removed = append(removed, scope)
	}
	return removed, nil
}
