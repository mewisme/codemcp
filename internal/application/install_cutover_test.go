package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/migration/released024"
)

func TestInstallCurrentKeepsPrimaryInstallSuccessfulWhenSupplementalBootstrapWarns(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	layout, err := install.NewLayout(filepath.Join(t.TempDir(), "install"), filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	installCalls, postCalls := 0, 0
	deps := defaultInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		return released024.Manifest{Found: false}, nil
	}
	deps.Install = func(install.Options) (install.Result, error) {
		installCalls++
		return install.Result{
			Layout: layout, Version: "v1.2.3", Staged: install.Staged{Binary: filepath.Join(layout.Versions, "v1.2.3", layout.BinaryName)},
			Canonical: install.CanonicalStatus{Path: layout.CanonicalBinary},
		}, nil
	}
	deps.PostInstall = func(context.Context) SupplementalBootstrapResult {
		postCalls++
		return SupplementalBootstrapResult{
			Integrations: []IntegrationEnsureResult{{Integration: "rtk", State: "failed", Detail: "offline", Retry: "cm integration rtk install"}},
			Warnings:     []string{"telemetry bootstrap failed: offline"},
		}
	}
	result, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if installCalls != 1 || postCalls != 1 {
		t.Fatalf("installCalls=%d postCalls=%d", installCalls, postCalls)
	}
	if result.Version != "v1.2.3" || len(result.Supplemental.Warnings) != 1 {
		t.Fatalf("result=%#v", result)
	}
}

func TestReleasedInstallMigrationRunsStageActivateRetireBeforeSupplementalBootstrap(t *testing.T) {
	root := filepath.Join(t.TempDir(), "canonical")
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	layout, err := install.NewLayout(filepath.Join(t.TempDir(), "install"), filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	sequence := []string{}
	deps := defaultInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		sequence = append(sequence, "detect")
		return released024.Manifest{
			Found: true, SourceSHA256: "source-sha",
			Source: released024.SourceDescriptor{Root: filepath.Join(t.TempDir(), "legacy")},
		}, nil
	}
	deps.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	deps.Stage = func(_ context.Context, options released024.StageOptions) (released024.StageResult, error) {
		sequence = append(sequence, "stage")
		if options.TargetRoot != root {
			t.Fatalf("target=%q want=%q", options.TargetRoot, root)
		}
		return released024.StageResult{JournalPath: filepath.Join(t.TempDir(), "journal.json"), SourceSHA256: "source-sha", TargetRoot: root}, nil
	}
	deps.Layout = func() (install.Layout, error) { return layout, nil }
	deps.Executable = func() (string, error) { return filepath.Join(t.TempDir(), "cm"), nil }
	deps.Activate = func(_ context.Context, options released024.ActivateOptions) (released024.ActivationResult, error) {
		sequence = append(sequence, "activate")
		if options.InstallLayout.Root != layout.Root || options.InstallVersion == "" || options.InstallSource == "" {
			t.Fatalf("activation options=%#v", options)
		}
		return released024.ActivationResult{TargetRoot: root, Binary: layout.CurrentBinary, ServiceID: "svc_test", Committed: true}, nil
	}
	deps.Retire = func(_ context.Context, options released024.RetireOptions) (released024.RetirementResult, error) {
		sequence = append(sequence, "retire")
		if options.JournalPath == "" {
			t.Fatal("retirement journal missing")
		}
		return released024.RetirementResult{TargetRoot: root, Retired: true}, nil
	}
	deps.Install = func(install.Options) (install.Result, error) {
		t.Fatal("low-level install must not run outside activation during migration")
		return install.Result{}, nil
	}
	deps.PostInstall = func(context.Context) SupplementalBootstrapResult {
		sequence = append(sequence, "supplemental")
		return SupplementalBootstrapResult{}
	}

	result, migrated, err := migrateReleasedInstallIfNeeded(t.Context(), InstallCurrentOptions{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || result.Migration == nil || !result.Migration.Retired {
		t.Fatalf("migrated=%t result=%#v", migrated, result)
	}
	want := []string{"detect", "stage", "activate", "retire", "supplemental"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("sequence=%v want=%v", sequence, want)
	}

	sequence = nil
	deps.Stat = func(string) (os.FileInfo, error) { return nil, nil }
	second, migrated, err := migrateReleasedInstallIfNeeded(t.Context(), InstallCurrentOptions{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if migrated || second.Migration != nil {
		t.Fatalf("successful migration rerun was not a no-op: migrated=%t result=%#v", migrated, second)
	}
	if want := []string{"detect"}; !reflect.DeepEqual(sequence, want) {
		t.Fatalf("rerun sequence=%v want=%v", sequence, want)
	}
}

func TestReleasedInstallMigrationFailureDoesNotInstallOrBootstrap(t *testing.T) {
	root := filepath.Join(t.TempDir(), "canonical")
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	deps := defaultInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		return released024.Manifest{Found: true, SourceSHA256: "source-sha", Source: released024.SourceDescriptor{Root: filepath.Join(t.TempDir(), "legacy")}}, nil
	}
	deps.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	deps.Stage = func(context.Context, released024.StageOptions) (released024.StageResult, error) {
		return released024.StageResult{}, errors.New("stage failed")
	}
	deps.Install = func(install.Options) (install.Result, error) {
		t.Fatal("install ran after staging failure")
		return install.Result{}, nil
	}
	deps.PostInstall = func(context.Context) SupplementalBootstrapResult {
		t.Fatal("supplemental bootstrap ran after staging failure")
		return SupplementalBootstrapResult{}
	}
	_, migrated, err := migrateReleasedInstallIfNeeded(t.Context(), InstallCurrentOptions{}, deps)
	if err == nil || migrated {
		t.Fatalf("migrated=%t err=%v", migrated, err)
	}
}
