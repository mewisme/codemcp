package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
	managed "go.mewis.me/codemcp/internal/service"
)

func TestUninstallCurrentOrdersServiceStateAndInstallerCleanup(t *testing.T) {
	layout, err := install.NewLayout(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sequence := []string{}
	deps := uninstallDependencies{
		Layout: func() (install.Layout, error) { return layout, nil },
		RemoveServices: func(context.Context, install.Layout) ([]managed.Scope, error) {
			sequence = append(sequence, "services")
			return []managed.Scope{managed.ScopeUser}, nil
		},
		ConfigRoot: func() string { return layout.Root },
		Uninitialize: func(context.Context, string) error {
			sequence = append(sequence, "global-state")
			return nil
		},
		UninstallOwned: func(options install.UninstallOptions) (install.UninstallResult, error) {
			sequence = append(sequence, "installer")
			if options.Layout.Root != layout.Root || !options.PreserveBinaryTree {
				t.Fatalf("uninstall options=%#v", options)
			}
			return install.UninstallResult{ConfigRootPreserved: true}, nil
		},
	}
	if err := configRootOwnershipMarker(layout.Root); err != nil {
		t.Fatal(err)
	}
	result, err := uninstallCurrentWithDependencies(t.Context(), UninstallOptions{ExternalCleanup: true}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"services", "global-state", "installer"}; !reflect.DeepEqual(sequence, want) {
		t.Fatalf("sequence=%v want=%v", sequence, want)
	}
	if !result.GlobalStateRemoved || !reflect.DeepEqual(result.Services, []managed.Scope{managed.ScopeUser}) {
		t.Fatalf("result=%#v", result)
	}
}

func TestUninstallCurrentStopsBeforeInstallerWhenGlobalStateCleanupFails(t *testing.T) {
	layout, err := install.NewLayout(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := configRootOwnershipMarker(layout.Root); err != nil {
		t.Fatal(err)
	}
	installerCalled := false
	deps := uninstallDependencies{
		Layout: func() (install.Layout, error) { return layout, nil },
		RemoveServices: func(context.Context, install.Layout) ([]managed.Scope, error) {
			return []managed.Scope{managed.ScopeUser}, nil
		},
		ConfigRoot:   func() string { return layout.Root },
		Uninitialize: func(context.Context, string) error { return errors.New("secret purge failed") },
		UninstallOwned: func(install.UninstallOptions) (install.UninstallResult, error) {
			installerCalled = true
			return install.UninstallResult{}, nil
		},
	}
	if _, err := uninstallCurrentWithDependencies(t.Context(), UninstallOptions{}, deps); err == nil {
		t.Fatal("uninstall unexpectedly continued after global state cleanup failure")
	}
	if installerCalled {
		t.Fatal("installer assets were removed after global state cleanup failure")
	}
}

func TestUninstallCurrentPreservesUnmanagedCustomGlobalRoot(t *testing.T) {
	layout, err := install.NewLayout(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unmanagedRoot := t.TempDir()
	uninitializeCalled := false
	deps := uninstallDependencies{
		Layout:         func() (install.Layout, error) { return layout, nil },
		RemoveServices: func(context.Context, install.Layout) ([]managed.Scope, error) { return nil, nil },
		ConfigRoot:     func() string { return unmanagedRoot },
		Uninitialize: func(context.Context, string) error {
			uninitializeCalled = true
			return nil
		},
		UninstallOwned: func(install.UninstallOptions) (install.UninstallResult, error) {
			return install.UninstallResult{}, nil
		},
	}
	result, err := uninstallCurrentWithDependencies(t.Context(), UninstallOptions{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if uninitializeCalled || result.GlobalStateRemoved {
		t.Fatalf("unmanaged custom root was treated as owned: called=%t result=%#v", uninitializeCalled, result)
	}
}

func configRootOwnershipMarker(root string) error {
	return configformat.MarkRoot(root)
}
