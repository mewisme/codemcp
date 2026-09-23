package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/version"
)

func upgradeCommand() *cobra.Command {
	var targetVersion string
	var noRestart bool
	cmd := &cobra.Command{Use: "upgrade", Aliases: []string{"update", "upg"}, SuggestFor: []string{"upg"}, Short: "Check for and install cm upgrades", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "UPDATE", "update.installation.detecting", "Detecting current installation")
		detection, err := install.DetectCurrent(version.Version)
		if err != nil {
			return fmt.Errorf("detect current installation: %w", err)
		}
		policy := updatepkg.PolicyForInstallation(detection)
		log := commandLogger(cmd)
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
		startCommandSpinner(cmd, log, "UPDATE", "update.checking", "Checking for updates")
		plan, err := updater.Resolve(cmd.Context(), options)
		if err != nil {
			return fmt.Errorf("check update: %w", err)
		}
		log.StopAnimation()
		if targetVersion == "" {
			cacheLatestRelease(cmd, layout, plan.Target)
		}
		if !plan.Changed {
			if plan.Current == plan.Target {
				log.Ready("UPDATE", "update.current", "Already up to date")
			} else {
				log.Notice("UPDATE", "update.ahead", "Current version is newer than the latest release")
			}
			log.Detail("current", plan.Current)
			log.Detail("latest", plan.Target)
			return nil
		}
		if targetVersion == "" {
			log.Ready("UPDATE", "update.available", "Update available")
			log.Detail("current", plan.Current)
			log.Detail("latest", plan.Target)
		} else {
			log.Ready("UPDATE", "update.target-resolved", "Target version resolved")
			log.Detail("current", plan.Current)
			log.Detail("target", plan.Target)
		}
		logCommandStep(cmd, "UPDATE", "update.runtime.inspecting", "Inspecting managed runtime state")
		runtimeState, err := captureUpdateRuntimeState(cmd.Context())
		if err != nil {
			return fmt.Errorf("inspect managed runtime before update: %w", err)
		}
		startCommandSpinner(cmd, log, "UPDATE", "update.applying", "Applying update "+plan.Target)
		logCommandStep(cmd, "UPDATE", "update.release.applying", "Downloading and activating release", logger.WithVerbose("target", plan.Target))
		options.ResolvedRelease = &plan.Release
		result, err := updater.Apply(cmd.Context(), options)
		if err != nil {
			return fmt.Errorf("apply update: %w", err)
		}
		log.StopAnimation()
		log.Ready("UPDATE", "update.applied", "Update applied")
		log.Detail("previous", result.Current)
		log.Detail("current", result.Target)
		log.Detail("binary", result.Install.Staged.Binary)
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
		log.Success("UPDATE", message)
		return nil
	}}
	cmd.Flags().StringVar(&targetVersion, "version", "", "install a specific release version (allows explicit downgrade)")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "do not restart a running managed runtime after updating")
	cmd.AddCommand(upgradeCheckCommand())
	return cmd
}

func upgradeCheckCommand() *cobra.Command {
	return &cobra.Command{Use: "check", Short: "Check the latest available release", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		log := commandLogger(cmd)
		logCommandStep(cmd, "UPDATE", "update.release.checking", "Resolving latest release", logger.WithVerbose("current", version.Version))
		startCommandSpinner(cmd, log, "UPDATE", "update.checking", "Checking for updates")
		checker := updatepkg.Checker{Source: updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version}}
		result, err := checker.Check(cmd.Context(), version.Version)
		if err != nil {
			return fmt.Errorf("check latest release: %w", err)
		}
		log.StopAnimation()
		cacheLatestReleaseForCurrentInstall(cmd, result.Latest)
		switch result.Status {
		case updatepkg.StatusAvailable:
			log.Notice("UPDATE", "update.available", "New version available")
		case updatepkg.StatusUpToDate:
			log.Ready("UPDATE", "update.current", "Already up to date")
		case updatepkg.StatusAhead:
			log.Notice("UPDATE", "update.ahead", "Current version is newer than the latest release")
		case updatepkg.StatusDevelopment:
			log.Notice("UPDATE", "update.development", "Development build; latest release shown for reference")
		default:
			return fmt.Errorf("unknown update status %q", result.Status)
		}
		log.Detail("current", result.Current)
		log.Detail("latest", result.Latest)
		if result.Status == updatepkg.StatusAvailable {
			log.Detail("run", cliUseName()+" upgrade")
		}
		return nil
	}}
}
