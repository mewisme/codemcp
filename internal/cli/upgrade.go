package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/version"
)

func upgradeCommand() *cobra.Command {
	var targetVersion string
	var noRestart bool
	cmd := &cobra.Command{Use: "upgrade", Aliases: []string{"update", "upg"}, SuggestFor: []string{"upg"}, Short: "Check for and install cm upgrades", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		beginMutationProgress(cmd, "Upgrade CodeMCP")
		logCommandStep(cmd, "UPDATE", "update.installation.detecting", "Detecting current installation")
		detection, err := install.DetectCurrent(version.Version)
		if err != nil {
			return fmt.Errorf("detect current installation: %w", err)
		}
		policy := updatepkg.PolicyForInstallation(detection)
		log := commandLogger(cmd)
		progress := newCommandProgress(cmd, "UPDATE")
		logCommandDebug(cmd, "UPDATE", "update.policy.resolved", "Update policy resolved", logger.WithDebug("method", policy.Method), logger.WithDebug("action", policy.Action))
		if policy.Action == updatepkg.PolicyDelegate {
			return runPackageManagedUpgrade(cmd, detection, targetVersion, noRestart)
		}
		if err := policy.Error(); err != nil {
			return err
		}
		layout, err := detection.ManagedLayout()
		if err != nil {
			return fmt.Errorf("managed direct installation not found: %w", err)
		}
		updater := updatepkg.Updater{
			Resolver:   updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version},
			Downloader: updatepkg.Downloader{UserAgent: version.ClientName + "/" + version.Version},
		}
		options := updatepkg.ApplyOptions{Layout: layout, CurrentVersion: version.Version, TargetVersion: targetVersion}
		progress.Start("update.checking", "Checking for updates", "Checked for updates")
		plan, err := updater.Resolve(cmd.Context(), options)
		if err != nil {
			progress.Stop()
			return fmt.Errorf("check update: %w", err)
		}
		progress.Complete()
		if targetVersion == "" {
			cacheLatestRelease(cmd, layout, plan.Target)
		}
		if !plan.Changed {
			kind := presentation.StatusInfo
			message := "Already up to date"
			if plan.Current == plan.Target {
			} else {
				message = "Current version is newer than the latest release"
			}
			renderMutationResult(cmd, "Upgrade CodeMCP", kind, message,
				presentation.Field{Label: "current", Value: plan.Current},
				presentation.Field{Label: "latest", Value: plan.Target},
			)
			return nil
		}
		logCommandStep(cmd, "UPDATE", "update.runtime.inspecting", "Inspecting managed runtime state")
		runtimeState, err := captureUpdateRuntimeState(cmd.Context())
		if err != nil {
			return fmt.Errorf("inspect managed runtime before update: %w", err)
		}
		logCommandStep(cmd, "UPDATE", "update.release.applying", "Downloading and activating release", logger.WithVerbose("target", plan.Target))
		options.ResolvedRelease = &plan.Release
		result, err := updater.Apply(cmd.Context(), options)
		if err != nil {
			return fmt.Errorf("apply update: %w", err)
		}
		logCommandStep(cmd, "UPDATE", "update.runtime.coordinating", "Coordinating updated managed runtime", logger.WithVerbose("restart", !noRestart))
		if err := coordinateUpdatedRuntime(cmd, result.Install, runtimeState, noRestart); err != nil {
			return fmt.Errorf("update to %s failed after activation: %w", result.Target, err)
		}
		if err := install.FinalizeResultContext(cmd.Context(), result.Install); err != nil {
			log.Warning("UPDATE", "update.cleanup-failed", "Update succeeded but old version cleanup failed", err)
		}
		message := "Update complete"
		if result.Downgrade {
			message = "Version change complete"
		}
		renderMutationSuccess(cmd, "Upgrade CodeMCP", message,
			presentation.Field{Label: "previous", Value: result.Current},
			presentation.Field{Label: "current", Value: result.Target},
			presentation.Field{Label: "binary", Value: result.Install.Staged.Binary},
		)
		return nil
	}}
	cmd.Flags().StringVar(&targetVersion, "version", "", "install a specific release version (allows explicit downgrade)")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "do not restart a running managed runtime after updating")
	cmd.AddCommand(upgradeCheckCommand())
	return cmd
}

func upgradeCheckCommand() *cobra.Command {
	return &cobra.Command{Use: "check", Short: "Check the latest available release", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "UPDATE", "update.release.checking", "Resolving latest release", logger.WithVerbose("current", version.Version))
		progress := newCommandProgress(cmd, "UPDATE")
		beginMutationProgress(cmd, "Check for updates")
		progress.Start("update.checking", "Checking for updates", "Checked for updates")
		checker := updatepkg.Checker{Source: updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version}}
		result, err := checker.Check(cmd.Context(), version.Version)
		if err != nil {
			progress.Stop()
			return fmt.Errorf("check latest release: %w", err)
		}
		progress.Complete()
		cacheLatestReleaseForCurrentInstall(cmd, result.Latest)
		kind := presentation.StatusInfo
		message := ""
		switch result.Status {
		case updatepkg.StatusAvailable:
			message = "New version available"
		case updatepkg.StatusUpToDate:
			message = "Already up to date"
		case updatepkg.StatusAhead:
			message = "Current version is newer than the latest release"
		case updatepkg.StatusDevelopment:
			message = "Development build; latest release shown for reference"
		default:
			return fmt.Errorf("unknown update status %q", result.Status)
		}
		fields := []presentation.Field{{Label: "current", Value: result.Current}, {Label: "latest", Value: result.Latest}}
		if result.Status == updatepkg.StatusAvailable {
			fields = append(fields, presentation.Field{Label: "run", Value: cliUseName() + " upgrade"})
		}
		renderMutationResult(cmd, "Check for updates", kind, message, fields...)
		return nil
	}}
}
