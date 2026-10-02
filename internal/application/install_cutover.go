package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/migration/released024"
	"go.mewis.me/codemcp/internal/version"
)

type InstallCutoverEvent struct {
	Stage   string `json:"stage"`
	State   string `json:"state"`
	Message string `json:"message"`
	Child   bool   `json:"child,omitempty"`
}

type MigrationCutoverResult struct {
	SourceRoot   string `json:"source_root"`
	TargetRoot   string `json:"target_root"`
	JournalPath  string `json:"journal_path"`
	SourceSHA256 string `json:"source_sha256"`
	Binary       string `json:"binary"`
	ServiceID    string `json:"service_id"`
	Retired      bool   `json:"retired"`
}

type InstallCurrentResult struct {
	Install          install.Result              `json:"install"`
	Migration        *MigrationCutoverResult     `json:"migration,omitempty"`
	Supplemental     SupplementalBootstrapResult `json:"supplemental"`
	Version          string                      `json:"version"`
	Binary           string                      `json:"binary"`
	Command          string                      `json:"command"`
	AlreadyInstalled bool                        `json:"already_installed,omitempty"`
}

type installCutoverDependencies struct {
	Detect      func(context.Context, released024.Options) (released024.Manifest, error)
	Discard     func(context.Context, released024.DiscardOptions) (released024.DiscardResult, error)
	Stage       func(context.Context, released024.StageOptions) (released024.StageResult, error)
	Activate    func(context.Context, released024.ActivateOptions) (released024.ActivationResult, error)
	Retire      func(context.Context, released024.RetireOptions) (released024.RetirementResult, error)
	Install     func(install.Options) (install.Result, error)
	PostInstall func(context.Context) SupplementalBootstrapResult
	Layout      func() (install.Layout, error)
	Executable  func() (string, error)
	Stat        func(string) (os.FileInfo, error)
}

func defaultInstallCutoverDependencies() installCutoverDependencies {
	return installCutoverDependencies{
		Detect: released024.Detect, Discard: released024.DiscardUnsupportedPredecessor,
		Stage: released024.Stage, Activate: released024.Activate, Retire: released024.Retire,
		Install: install.Install, PostInstall: RunPostInstallBootstrap, Layout: install.DefaultLayout,
		Executable: os.Executable, Stat: os.Stat,
	}
}

func InstallCurrentContext(ctx context.Context, options InstallCurrentOptions) (InstallCurrentResult, error) {
	return installCurrentWithDependencies(ctx, options, defaultInstallCutoverDependencies())
}

func InstallCurrent(options InstallCurrentOptions) (InstallCurrentResult, error) {
	return InstallCurrentContext(context.Background(), options)
}

func MigrateReleasedInstallIfNeeded(ctx context.Context, options InstallCurrentOptions) (InstallCurrentResult, bool, error) {
	return migrateReleasedInstallIfNeeded(ctx, options, defaultInstallCutoverDependencies())
}

func installCurrentWithDependencies(ctx context.Context, options InstallCurrentOptions, deps installCutoverDependencies) (InstallCurrentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if migrated, ok, err := migrateReleasedInstallIfNeeded(ctx, options, deps); err != nil || ok {
		return migrated, err
	}
	emitInstallCutover(options.Observe, "activate", "running", "Installing canonical CodeMCP binary", false)
	installed, err := deps.Install(install.Options{Context: ctx, Version: version.Version, Force: options.Force})
	if err != nil {
		emitInstallCutover(options.Observe, "activate", "failed", err.Error(), false)
		return InstallCurrentResult{}, err
	}
	emitInstallCutover(options.Observe, "activate", "success", "Canonical CodeMCP binary installed", false)
	emitInstallCutover(options.Observe, "cleanup", "running", "Bootstrapping install supplements", false)
	supplemental := deps.PostInstall(ctx)
	emitSupplementalOutcomes(options.Observe, supplemental)
	emitInstallCutover(options.Observe, "cleanup", "success", "Install supplements processed", false)
	return InstallCurrentResult{
		Install: installed, Supplemental: supplemental, Version: installed.Version,
		Binary: installed.Staged.Binary, Command: installed.Canonical.Path, AlreadyInstalled: installed.AlreadyInstalled,
	}, nil
}

func migrateReleasedInstallIfNeeded(ctx context.Context, options InstallCurrentOptions, deps installCutoverDependencies) (InstallCurrentResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	emitInstallCutover(options.Observe, "detect", "running", "Detecting released CodeMCP predecessor state", false)
	manifest, err := deps.Detect(ctx, released024.Options{})
	if err != nil {
		emitInstallCutover(options.Observe, "detect", "failed", err.Error(), false)
		return InstallCurrentResult{}, false, err
	}
	if !manifest.Found {
		emitInstallCutover(options.Observe, "detect", "success", "No released predecessor state detected", false)
		return InstallCurrentResult{}, false, nil
	}
	if manifest.Unsupported > 0 {
		emitInstallCutover(options.Observe, "detect", "warning", "Previous CodeMCP state requires a clean install", false)
		emitInstallCutover(options.Observe, "detect", "warning", fmt.Sprintf("%s cannot be migrated", installCutoverCount(manifest.Unsupported, "unsupported artifact", "unsupported artifacts")), true)
		emitInstallCutover(options.Observe, "cleanup", "running", "Removing previous CodeMCP state", false)
		discarded, err := deps.Discard(ctx, released024.DiscardOptions{Manifest: manifest})
		if err != nil {
			emitInstallCutover(options.Observe, "cleanup", "failed", err.Error(), false)
			return InstallCurrentResult{}, false, err
		}
		if discarded.ServicesRetired > 0 {
			emitInstallCutover(options.Observe, "cleanup", "success", "Retired "+installCutoverCount(discarded.ServicesRetired, "previous service", "previous services"), true)
		}
		if discarded.LaunchersRemoved > 0 {
			emitInstallCutover(options.Observe, "cleanup", "success", "Removed "+installCutoverCount(discarded.LaunchersRemoved, "previous launcher", "previous launchers"), true)
		}
		emitInstallCutover(options.Observe, "cleanup", "success", "Previous CodeMCP state removed", false)
		return InstallCurrentResult{}, false, nil
	}
	targetRoot := config.RootPath()
	if _, statErr := deps.Stat(targetRoot); statErr == nil {
		emitInstallCutover(options.Observe, "detect", "success", "Canonical CodeMCP state already exists; predecessor state left untouched", false)
		return InstallCurrentResult{}, false, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return InstallCurrentResult{}, false, statErr
	}
	emitInstallCutover(options.Observe, "detect", "success", "Released predecessor state detected", false)

	emitInstallCutover(options.Observe, "stage", "running", "Staging released state migration", false)
	staged, err := deps.Stage(ctx, released024.StageOptions{Manifest: manifest, TargetRoot: targetRoot})
	if err != nil {
		emitInstallCutover(options.Observe, "stage", "failed", err.Error(), false)
		return InstallCurrentResult{}, false, err
	}
	for _, domain := range staged.Domains {
		emitInstallCutover(options.Observe, "stage", domain.State, domain.Domain+" · "+strings.TrimSpace(domain.Detail), true)
	}
	for _, workspace := range staged.Workspaces {
		message := workspace.ID + " · " + workspace.State
		if strings.TrimSpace(workspace.Detail) != "" {
			message += " · " + workspace.Detail
		}
		emitInstallCutover(options.Observe, "stage", workspace.State, message, true)
	}
	emitInstallCutover(options.Observe, "stage", "success", "Released state staged", false)
	emitInstallCutover(options.Observe, "validate", "success", "Staged migration validated", false)

	layout, err := deps.Layout()
	if err != nil {
		return InstallCurrentResult{}, false, err
	}
	source, err := deps.Executable()
	if err != nil {
		return InstallCurrentResult{}, false, err
	}
	emitInstallCutover(options.Observe, "activate", "running", "Activating migrated CodeMCP state", false)
	activated, err := deps.Activate(ctx, released024.ActivateOptions{
		JournalPath: staged.JournalPath, ServiceScope: detectServiceScope(), InstallLayout: layout,
		InstallVersion: version.Version, InstallSource: source,
		Observe: func(event released024.ActivationEvent) {
			emitInstallCutover(options.Observe, "activate", event.State, event.Message, event.Child)
		},
	})
	if err != nil {
		emitInstallCutover(options.Observe, "activate", "failed", err.Error(), false)
		return InstallCurrentResult{}, false, err
	}
	emitInstallCutover(options.Observe, "activate", "success", "Migrated CodeMCP state activated", false)

	emitInstallCutover(options.Observe, "cleanup", "running", "Retiring verified predecessor runtime identities", false)
	retired, err := deps.Retire(ctx, released024.RetireOptions{
		JournalPath: staged.JournalPath,
		Observe: func(event released024.RetirementEvent) {
			emitInstallCutover(options.Observe, "cleanup", event.State, event.Message, event.Child)
		},
	})
	if err != nil {
		emitInstallCutover(options.Observe, "cleanup", "failed", err.Error(), false)
		return InstallCurrentResult{}, true, err
	}
	emitInstallCutover(options.Observe, "cleanup", "success", "Released predecessor runtime retired", false)
	supplemental := deps.PostInstall(ctx)
	emitSupplementalOutcomes(options.Observe, supplemental)
	return InstallCurrentResult{
		Migration: &MigrationCutoverResult{
			SourceRoot: manifest.Source.Root, TargetRoot: targetRoot, JournalPath: staged.JournalPath,
			SourceSHA256: staged.SourceSHA256, Binary: activated.Binary, ServiceID: activated.ServiceID, Retired: retired.Retired,
		},
		Supplemental: supplemental, Version: version.Version, Binary: activated.Binary, Command: layout.CanonicalBinary,
	}, true, nil
}

func installCutoverCount(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}

func emitSupplementalOutcomes(observe func(InstallCutoverEvent), result SupplementalBootstrapResult) {
	if observe == nil {
		return
	}
	telemetryState := "skipped"
	telemetryMessage := "Telemetry disabled"
	if result.Telemetry.Enabled {
		if result.Telemetry.EndpointAvailable && result.Telemetry.IdentityPresent {
			telemetryState, telemetryMessage = "success", "Telemetry ready"
		} else {
			telemetryState, telemetryMessage = "warning", "Telemetry unavailable"
		}
	}
	emitInstallCutover(observe, "cleanup", telemetryState, telemetryMessage, true)
	for _, outcome := range result.Integrations {
		state := "success"
		if outcome.State == "failed" || outcome.State == "unavailable" {
			state = "warning"
		}
		message := outcome.Integration + " · " + outcome.State
		if outcome.Source != "" {
			message += " · " + outcome.Source
		}
		if outcome.Detail != "" {
			message += " · " + outcome.Detail
		}
		if (outcome.State == "failed" || outcome.State == "unavailable") && outcome.Retry != "" {
			message += " · retry: " + outcome.Retry
		}
		emitInstallCutover(observe, "cleanup", state, message, true)
	}
	for _, warning := range result.Warnings {
		emitInstallCutover(observe, "cleanup", "warning", warning, true)
	}
}

func emitInstallCutover(observe func(InstallCutoverEvent), stage, state, message string, child bool) {
	if observe != nil {
		observe(InstallCutoverEvent{Stage: stage, State: state, Message: strings.TrimSpace(message), Child: child})
	}
}
