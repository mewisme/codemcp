package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
	deps := testInstallCutoverDependencies()
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
	deps.PostInstall = func(_ context.Context, options PostInstallBootstrapOptions) SupplementalBootstrapResult {
		postCalls++
		if options.SkipMissingIntegrations || options.Observe != nil {
			t.Fatalf("default bootstrap options=%#v", options)
		}
		return SupplementalBootstrapResult{
			Integrations: []IntegrationEnsureResult{{Integration: "rtk", State: "failed", Detail: "offline", Retry: "cm integration rtk install"}},
			Warnings:     []string{"telemetry bootstrap failed: offline"},
		}
	}
	var events []InstallCutoverEvent
	result, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{Observe: func(event InstallCutoverEvent) {
		events = append(events, event)
	}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if installCalls != 1 || postCalls != 1 {
		t.Fatalf("installCalls=%d postCalls=%d", installCalls, postCalls)
	}
	if result.Version != "v1.2.3" || len(result.Supplemental.Warnings) != 1 {
		t.Fatalf("result=%#v", result)
	}
	for _, event := range events {
		if strings.Contains(event.Message, "rtk bootstrap") || event.Message == "Install supplements processed" {
			t.Fatalf("supplemental result leaked into permanent cutover events: %#v", events)
		}
	}
}

func TestInstallCurrentCompletesConfigurationRuntimeAndSupplements(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	layout, err := install.NewLayout(filepath.Join(t.TempDir(), "install"), filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	sequence := []string{}
	deps := testInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		return released024.Manifest{Found: false}, nil
	}
	deps.Install = func(install.Options) (install.Result, error) {
		sequence = append(sequence, "install")
		return install.Result{
			Layout: layout, Version: "v1.2.3",
			Staged:    install.Staged{Binary: filepath.Join(layout.Versions, "v1.2.3", layout.BinaryName)},
			Canonical: install.CanonicalStatus{Path: layout.CanonicalBinary},
		}, nil
	}
	deps.Initialize = func(options InitOptions) (InitResult, error) {
		sequence = append(sequence, "initialize")
		if options.Context == nil {
			t.Fatal("initialize context missing")
		}
		return InitResult{ConfigPath: filepath.Join(t.TempDir(), "config.json"), Format: configformat.JSON, MCPToken: "mcp_once", AdminToken: "admin_once"}, nil
	}
	deps.RuntimeUp = func(_ context.Context, binary string) (RuntimeActionResult, error) {
		sequence = append(sequence, "runtime")
		if binary != layout.CurrentBinary {
			t.Fatalf("runtime binary=%q want=%q", binary, layout.CurrentBinary)
		}
		return RuntimeActionResult{Action: "up", Service: ServiceOverview{Running: true}}, nil
	}
	deps.PostInstall = func(context.Context, PostInstallBootstrapOptions) SupplementalBootstrapResult {
		sequence = append(sequence, "supplemental")
		return SupplementalBootstrapResult{}
	}

	result, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"install", "initialize", "runtime", "supplemental"}; !reflect.DeepEqual(sequence, want) {
		t.Fatalf("sequence=%v want=%v", sequence, want)
	}
	if !result.Setup.Initialized || result.Setup.MCPToken != "mcp_once" || result.Setup.AdminToken != "admin_once" || result.Setup.Runtime == nil || !result.Setup.Runtime.Service.Running {
		t.Fatalf("setup=%#v", result.Setup)
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
	deps := testInstallCutoverDependencies()
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
	var bootstrapOptions PostInstallBootstrapOptions
	integrationEvents := []IntegrationEnsureEvent{}
	deps.PostInstall = func(_ context.Context, options PostInstallBootstrapOptions) SupplementalBootstrapResult {
		sequence = append(sequence, "supplemental")
		bootstrapOptions = options
		if options.Observe != nil {
			options.Observe(IntegrationEnsureEvent{Integration: "rtk", Phase: "check", State: "running", Message: "checking RTK"})
		}
		return SupplementalBootstrapResult{}
	}

	result, migrated, err := migrateReleasedInstallIfNeeded(t.Context(), InstallCurrentOptions{
		SkipMissingIntegrations: true,
		ObserveIntegration: func(event IntegrationEnsureEvent) {
			integrationEvents = append(integrationEvents, event)
		},
	}, deps)
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
	if !bootstrapOptions.SkipMissingIntegrations || bootstrapOptions.Observe == nil {
		t.Fatalf("bootstrap options=%#v", bootstrapOptions)
	}
	if len(integrationEvents) != 1 || integrationEvents[0].Integration != "rtk" || integrationEvents[0].Phase != "check" {
		t.Fatalf("integration events=%#v", integrationEvents)
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

func TestInstallCurrentMigrationErrorsCleanAndFreshInstall(t *testing.T) {
	for _, failAt := range []string{"detect", "discard", "stage", "layout", "executable", "activate", "retire"} {
		t.Run(failAt, func(t *testing.T) {
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
			deps := testInstallCutoverDependencies()
			deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
				sequence = append(sequence, "detect")
				manifest := released024.Manifest{Found: true, SourceSHA256: "source-sha", Source: released024.SourceDescriptor{Root: filepath.Join(t.TempDir(), "legacy")}}
				if failAt == "discard" {
					manifest.Unsupported = 1
				}
				if failAt == "detect" {
					return manifest, errors.New("detect failed")
				}
				return manifest, nil
			}
			deps.Discard = func(context.Context, released024.DiscardOptions) (released024.DiscardResult, error) {
				sequence = append(sequence, "discard")
				return released024.DiscardResult{}, errors.New("discard failed")
			}
			deps.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
			deps.Stage = func(context.Context, released024.StageOptions) (released024.StageResult, error) {
				sequence = append(sequence, "stage")
				if failAt == "stage" {
					return released024.StageResult{}, errors.New("stage failed")
				}
				return released024.StageResult{JournalPath: filepath.Join(t.TempDir(), "journal.json"), SourceSHA256: "source-sha"}, nil
			}
			deps.Layout = func() (install.Layout, error) {
				sequence = append(sequence, "layout")
				if failAt == "layout" {
					return install.Layout{}, errors.New("layout failed")
				}
				return layout, nil
			}
			deps.Executable = func() (string, error) {
				sequence = append(sequence, "executable")
				if failAt == "executable" {
					return "", errors.New("executable failed")
				}
				return filepath.Join(t.TempDir(), "cm"), nil
			}
			deps.Activate = func(context.Context, released024.ActivateOptions) (released024.ActivationResult, error) {
				sequence = append(sequence, "activate")
				if failAt == "activate" {
					return released024.ActivationResult{}, errors.New("activate failed")
				}
				return released024.ActivationResult{Binary: layout.CurrentBinary, ServiceID: "svc"}, nil
			}
			deps.Retire = func(context.Context, released024.RetireOptions) (released024.RetirementResult, error) {
				sequence = append(sequence, "retire")
				if failAt == "retire" {
					return released024.RetirementResult{}, errors.New("retire failed")
				}
				return released024.RetirementResult{Retired: true}, nil
			}
			deps.FreshCleanup = func(context.Context) (released024.FreshInstallCleanupResult, error) {
				sequence = append(sequence, "cleanup")
				return released024.FreshInstallCleanupResult{TransactionsRemoved: 1, SourcesRemoved: 1}, nil
			}
			deps.Install = func(install.Options) (install.Result, error) {
				sequence = append(sequence, "install")
				return install.Result{
					Layout: layout, Version: "v0.3.2",
					Staged:    install.Staged{Binary: filepath.Join(layout.Versions, "v0.3.2", layout.BinaryName)},
					Canonical: install.CanonicalStatus{Path: layout.CanonicalBinary},
				}, nil
			}
			deps.PostInstall = func(context.Context, PostInstallBootstrapOptions) SupplementalBootstrapResult {
				sequence = append(sequence, "supplemental")
				return SupplementalBootstrapResult{}
			}

			result, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{}, deps)
			if err != nil {
				t.Fatal(err)
			}
			if result.Version != "v0.3.2" {
				t.Fatalf("result=%#v", result)
			}
			cleanup := slices.Index(sequence, "cleanup")
			fresh := slices.Index(sequence, "install")
			if cleanup < 0 || fresh < 0 || cleanup > fresh {
				t.Fatalf("migration failure did not clean before fresh install: %v", sequence)
			}
		})
	}
}

func TestInstallCurrentMigrationCleanupErrorStillFreshInstalls(t *testing.T) {
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
	deps := testInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		sequence = append(sequence, "detect")
		return released024.Manifest{}, errors.New("detect failed")
	}
	deps.FreshCleanup = func(context.Context) (released024.FreshInstallCleanupResult, error) {
		sequence = append(sequence, "cleanup")
		return released024.FreshInstallCleanupResult{}, errors.New("cleanup incomplete")
	}
	deps.Install = func(install.Options) (install.Result, error) {
		sequence = append(sequence, "install")
		return install.Result{
			Layout: layout, Version: "v0.3.2",
			Staged:    install.Staged{Binary: filepath.Join(layout.Versions, "v0.3.2", layout.BinaryName)},
			Canonical: install.CanonicalStatus{Path: layout.CanonicalBinary},
		}, nil
	}
	deps.PostInstall = func(context.Context, PostInstallBootstrapOptions) SupplementalBootstrapResult {
		sequence = append(sequence, "supplemental")
		return SupplementalBootstrapResult{}
	}

	result, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v0.3.2" {
		t.Fatalf("result=%#v", result)
	}
	if want := []string{"detect", "cleanup", "install", "supplemental"}; !reflect.DeepEqual(sequence, want) {
		t.Fatalf("sequence=%v want=%v", sequence, want)
	}
}

func TestInstallCurrentDiscardsUnsupportedReleasedStateThenFreshInstalls(t *testing.T) {
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
	events := []InstallCutoverEvent{}
	deps := testInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		sequence = append(sequence, "detect")
		return released024.Manifest{Found: true, Unsupported: 5}, nil
	}
	deps.Discard = func(context.Context, released024.DiscardOptions) (released024.DiscardResult, error) {
		sequence = append(sequence, "discard")
		return released024.DiscardResult{RootRemoved: true}, nil
	}
	deps.Stage = func(context.Context, released024.StageOptions) (released024.StageResult, error) {
		t.Fatal("stage ran for unsupported predecessor state")
		return released024.StageResult{}, nil
	}
	deps.Install = func(install.Options) (install.Result, error) {
		sequence = append(sequence, "install")
		return install.Result{
			Layout: layout, Version: "v0.3.2",
			Staged:    install.Staged{Binary: filepath.Join(layout.Versions, "v0.3.2", layout.BinaryName)},
			Canonical: install.CanonicalStatus{Path: layout.CanonicalBinary},
		}, nil
	}
	var bootstrapOptions PostInstallBootstrapOptions
	deps.PostInstall = func(_ context.Context, options PostInstallBootstrapOptions) SupplementalBootstrapResult {
		sequence = append(sequence, "supplemental")
		bootstrapOptions = options
		return SupplementalBootstrapResult{}
	}
	result, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{
		SkipMissingIntegrations: true,
		Observe: func(event InstallCutoverEvent) {
			events = append(events, event)
		},
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v0.3.2" {
		t.Fatalf("result=%#v", result)
	}
	want := []string{"detect", "discard", "install", "supplemental"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("sequence=%v want=%v", sequence, want)
	}
	if !bootstrapOptions.SkipMissingIntegrations {
		t.Fatalf("bootstrap options=%#v", bootstrapOptions)
	}
	var messages []string
	for _, event := range events {
		messages = append(messages, event.Message)
	}
	wantMessages := []string{
		"Previous CodeMCP state requires a clean install",
		"5 unsupported artifacts cannot be migrated",
		"Removing previous CodeMCP state",
		"Previous CodeMCP state removed",
	}
	for _, wantMessage := range wantMessages {
		if !slices.Contains(messages, wantMessage) {
			t.Fatalf("events missing %q: %#v", wantMessage, events)
		}
	}
	for _, message := range messages {
		if strings.Contains(message, "0 service") || strings.Contains(message, "0 launcher") {
			t.Fatalf("zero-value cleanup detail leaked into presentation: %q", message)
		}
	}
}

func TestInstallCurrentPrimaryFailureDoesNotRunSupplementalBootstrap(t *testing.T) {
	deps := testInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		return released024.Manifest{Found: false}, nil
	}
	deps.Install = func(install.Options) (install.Result, error) {
		return install.Result{}, errors.New("activation failed")
	}
	deps.PostInstall = func(context.Context, PostInstallBootstrapOptions) SupplementalBootstrapResult {
		t.Fatal("supplemental bootstrap ran after primary install failure")
		return SupplementalBootstrapResult{}
	}
	if _, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{}, deps); err == nil || !strings.Contains(err.Error(), "activation failed") {
		t.Fatalf("err=%v", err)
	}
}

func TestInstallCurrentSupplementalFailureDoesNotRollbackActivatedBinary(t *testing.T) {
	deps := testInstallCutoverDependencies()
	deps.Detect = func(context.Context, released024.Options) (released024.Manifest, error) {
		return released024.Manifest{Found: false}, nil
	}
	installCalls := 0
	deps.Install = func(install.Options) (install.Result, error) {
		installCalls++
		return install.Result{
			Version:   "v0.3.2",
			Staged:    install.Staged{Binary: "/tmp/codemcp/v0.3.2/cm"},
			Canonical: install.CanonicalStatus{Path: "/tmp/codemcp/current/cm"},
		}, nil
	}
	deps.PostInstall = func(context.Context, PostInstallBootstrapOptions) SupplementalBootstrapResult {
		return SupplementalBootstrapResult{
			Integrations: []IntegrationEnsureResult{{Integration: "rtk", State: "failed", Detail: "offline"}},
			Warnings:     []string{"rtk bootstrap failed: offline"},
		}
	}

	result, err := installCurrentWithDependencies(t.Context(), InstallCurrentOptions{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if installCalls != 1 || result.Version != "v0.3.2" || result.Command != "/tmp/codemcp/current/cm" {
		t.Fatalf("installCalls=%d result=%#v", installCalls, result)
	}
	if len(result.Supplemental.Warnings) != 1 || result.Supplemental.Integrations[0].State != "failed" {
		t.Fatalf("supplemental=%#v", result.Supplemental)
	}
}

func testInstallCutoverDependencies() installCutoverDependencies {
	deps := defaultInstallCutoverDependencies()
	deps.Initialize = func(InitOptions) (InitResult, error) {
		return InitResult{}, ErrConfigurationExists
	}
	deps.RuntimeUp = func(context.Context, string) (RuntimeActionResult, error) {
		return RuntimeActionResult{Action: "up"}, nil
	}
	deps.FreshCleanup = func(context.Context) (released024.FreshInstallCleanupResult, error) {
		return released024.FreshInstallCleanupResult{}, nil
	}
	return deps
}
