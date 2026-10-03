package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/install"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	managed "go.mewis.me/codemcp/internal/service"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/version"
)

type InstallationOverview struct {
	Detection      install.Detection
	Policy         updatepkg.InstallPolicy
	Managed        bool
	Layout         install.Layout
	ManagedVersion string
	CachedUpdate   *updatepkg.CachedCheck
}

type InstallCurrentOptions struct {
	Force                   bool                         `json:"force,omitempty"`
	SkipMissingIntegrations bool                         `json:"skip_missing_integrations,omitempty"`
	Observe                 func(InstallCutoverEvent)    `json:"-"`
	ObserveIntegration      func(IntegrationEnsureEvent) `json:"-"`
}

func resolveInstallCurrentOptions(options InstallCurrentOptions) (InstallCurrentOptions, error) {
	return resolveInstallCurrentOptionsWithLookup(options, nil)
}

func resolveInstallCurrentOptionsWithLookup(options InstallCurrentOptions, lookup func(string) (string, bool)) (InstallCurrentOptions, error) {
	resolved, err := resolvePostInstallBootstrapOptions(PostInstallBootstrapOptions{
		SkipMissingIntegrations: options.SkipMissingIntegrations,
		Observe:                 options.ObserveIntegration,
	}, lookup)
	if err != nil {
		return InstallCurrentOptions{}, err
	}
	options.SkipMissingIntegrations = resolved.SkipMissingIntegrations
	return options, nil
}

type UpdateApplyOptions struct {
	TargetVersion string
	NoRestart     bool
}

type UpdateApplyResult struct {
	Result       updatepkg.ApplyResult
	External     *ExternalCommand
	Notice       string
	Supplemental SupplementalBootstrapResult
}

func LoadInstallationOverview() (InstallationOverview, error) {
	detection, err := install.DetectCurrent(version.Version)
	if err != nil {
		return InstallationOverview{}, err
	}
	overview := InstallationOverview{Detection: detection, Policy: updatepkg.PolicyForInstallation(detection)}
	if detection.Method != install.MethodDirect || detection.Metadata == nil {
		return overview, nil
	}
	layout, err := detection.ManagedLayout()
	if err != nil {
		return InstallationOverview{}, err
	}
	overview.Managed, overview.Layout = true, layout
	if managedVersion, _, currentErr := install.CurrentVersion(layout); currentErr == nil {
		overview.ManagedVersion = managedVersion
	}
	if cached, ok, cacheErr := updatepkg.ReadFreshCache(layout.UpdateCache, version.Version, time.Now(), updatepkg.DefaultCacheTTL); cacheErr == nil && ok {
		overview.CachedUpdate = &cached
	}
	return overview, nil
}

func CheckForUpdate(ctx context.Context) (updatepkg.CheckResult, error) {
	checker := updatepkg.Checker{Source: updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version}}
	result, err := checker.Check(ctx, version.Version)
	if err != nil {
		return updatepkg.CheckResult{}, err
	}
	if overview, overviewErr := LoadInstallationOverview(); overviewErr == nil && overview.Managed {
		_ = updatepkg.WriteCache(overview.Layout.UpdateCache, result.Latest, time.Now())
	}
	return result, nil
}

func ApplyUpdate(ctx context.Context, options UpdateApplyOptions) (UpdateApplyResult, error) {
	overview, err := LoadInstallationOverview()
	if err != nil {
		return UpdateApplyResult{}, err
	}
	policy := overview.Policy
	if policy.Action == updatepkg.PolicyDelegate {
		return UpdateApplyResult{External: &ExternalCommand{Command: policy.Command, Reason: policy.Message}}, nil
	}
	if err := policy.Error(); err != nil {
		return UpdateApplyResult{}, err
	}
	if !overview.Managed {
		return UpdateApplyResult{}, errors.New("managed direct installation not found")
	}
	runtimeState, running, err := runtimeStatusFast(ctx)
	if err != nil {
		return UpdateApplyResult{}, err
	}
	if running && runtimeState.Managed && runtimeState.ServiceScope == string(managed.ScopeSystem) && detectServiceScope() == managed.ScopeUser && !options.NoRestart {
		return UpdateApplyResult{External: &ExternalCommand{Command: "cm upgrade", Reason: "Upgrading a running system service requires elevation and must be launched outside the TUI."}}, nil
	}
	updater := updatepkg.Updater{Resolver: updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version}, Downloader: updatepkg.Downloader{UserAgent: version.ClientName + "/" + version.Version}}
	result, err := updater.Apply(ctx, updatepkg.ApplyOptions{Layout: overview.Layout, CurrentVersion: version.Version, TargetVersion: strings.TrimSpace(options.TargetVersion)})
	if err != nil {
		return UpdateApplyResult{}, err
	}
	if strings.TrimSpace(options.TargetVersion) == "" && result.Target != "" {
		_ = updatepkg.WriteCache(overview.Layout.UpdateCache, result.Target, time.Now())
	}
	output := UpdateApplyResult{Result: result}
	if len(result.Warnings) > 0 {
		output.Notice = strings.Join(result.Warnings, "; ")
	}
	if !result.Changed {
		if result.Current == result.Target {
			appendUpdateNotice(&output.Notice, "Already up to date")
		} else {
			appendUpdateNotice(&output.Notice, "Current version is newer than the latest release")
		}
		return output, nil
	}
	if running && !options.NoRestart {
		if runtimeState.Managed {
			if err := restartUpdatedManagedRuntime(ctx, result.Install.Layout, runtimeState); err != nil {
				if rollbackErr := install.RollbackResultContext(ctx, result.Install); rollbackErr != nil {
					return UpdateApplyResult{}, fmt.Errorf("managed runtime restart failed: %w; rollback failed: %v", err, rollbackErr)
				}
				if rollbackRestartErr := restartUpdatedManagedRuntime(ctx, result.Install.Layout, runtimeState); rollbackRestartErr != nil {
					return UpdateApplyResult{}, fmt.Errorf("managed runtime restart failed: %w; rollback succeeded but previous runtime restart failed: %v", err, rollbackRestartErr)
				}
				return UpdateApplyResult{}, fmt.Errorf("managed runtime restart failed: %w; previous version restored", err)
			}
		} else {
			appendUpdateNotice(&output.Notice, fmt.Sprintf("Foreground runtime pid %d still uses the previous version; restart it manually", runtimeState.PID))
		}
	} else if running && options.NoRestart {
		appendUpdateNotice(&output.Notice, fmt.Sprintf("Runtime restart skipped; pid %d still uses the previous version", runtimeState.PID))
	}
	if err := install.FinalizeResultContext(ctx, result.Install); err != nil {
		appendUpdateNotice(&output.Notice, "update succeeded but old version cleanup failed: "+err.Error())
	}
	output.Supplemental = RunPostInstallBootstrap(ctx)
	return output, nil
}

func appendUpdateNotice(target *string, message string) {
	if target == nil || strings.TrimSpace(message) == "" {
		return
	}
	if strings.TrimSpace(*target) != "" {
		*target += "; "
	}
	*target += strings.TrimSpace(message)
}

func restartUpdatedManagedRuntime(ctx context.Context, layout install.Layout, status runtimecontrol.RuntimeStatus) error {
	if filepath.Clean(status.ConfigRoot) != filepath.Clean(config.RootPath()) {
		return fmt.Errorf("managed runtime config root mismatch: runtime %s, selected %s", status.ConfigRoot, config.RootPath())
	}
	scope := managed.Scope(status.ServiceScope)
	if scope != managed.ScopeUser && scope != managed.ScopeSystem {
		return fmt.Errorf("managed runtime has invalid service scope %q", status.ServiceScope)
	}
	spec, manager, err := managedService(scope, layout.CanonicalBinary)
	if err != nil {
		return err
	}
	if status.ServiceID == "" || spec.ID != status.ServiceID {
		return fmt.Errorf("managed runtime service mismatch: runtime %s, expected %s", status.ServiceID, spec.ID)
	}
	backend, err := manager.Status(spec)
	if err != nil {
		return err
	}
	if !backend.Installed {
		return errors.New("managed service is not installed")
	}
	if err := stopManagedBackend(spec, manager); err != nil {
		return err
	}
	if err := waitRuntimeStopped(ctx, managedReadyTimeout); err != nil {
		return err
	}
	if err := manager.Start(spec); err != nil {
		return err
	}
	_, err = waitManagedReady(ctx, spec, status.RunID, managedReadyTimeout)
	return err
}
