package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/version"
)

func upgradeCommand() *cobra.Command {
	var targetVersion string
	var noRestart bool
	cmd := &cobra.Command{Use: "upgrade", Short: "Check for and install cm upgrades", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		migrated, didMigrate, err := application.MigrateReleasedInstallIfNeeded(cmd.Context(), application.InstallCurrentOptions{
			Observe: installCutoverObserver(cmd),
		})
		if err != nil {
			return fmt.Errorf("migrate released installation: %w", err)
		}
		if didMigrate {
			renderSupplementalInstallSummary(cmd, migrated.Supplemental)
		}
		logCommandVerbose(cmd, "UPDATE", "update.installation.detecting", "Detecting current installation")
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
		plan, err := updater.Resolve(cmd.Context(), options)
		if err != nil {
			return fmt.Errorf("check update: %w", err)
		}
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
			renderMutationResult(cmd, kind, message,
				presentation.Field{Label: "current", Value: plan.Current},
				presentation.Field{Label: "latest", Value: plan.Target},
			)
			return nil
		}
		logCommandVerbose(cmd, "UPDATE", "update.runtime.inspecting", "Inspecting managed runtime state")
		runtimeInspectSpan := tracepkg.Start(cmd.Context(), "UPDATE", "update.runtime.inspect", "Inspecting managed runtime")
		runtimeState, err := captureUpdateRuntimeState(cmd.Context())
		if err != nil {
			runtimeInspectSpan.Fail(err)
			return fmt.Errorf("inspect managed runtime before update: %w", err)
		}
		runtimeInspectSpan.End()
		logCommandVerbose(cmd, "UPDATE", "update.release.applying", "Downloading and activating release", logger.WithVerbose("target", plan.Target))
		options.ResolvedRelease = &plan.Release
		result, err := updater.Apply(cmd.Context(), options)
		if err != nil {
			return fmt.Errorf("apply update: %w", err)
		}
		for _, warning := range result.Warnings {
			log.Verbose("UPDATE", "update.signature-warning", "Update signature warning", logger.WithVerbose("warning", warning))
			commandProgressSession(cmd).Append(func(p *presentation.Presenter) { p.ChildStatus(presentation.StatusWarning, warning) })
		}
		logCommandVerbose(cmd, "UPDATE", "update.runtime.coordinating", "Coordinating updated managed runtime", logger.WithVerbose("restart", !noRestart))
		if err := coordinateUpdatedRuntime(cmd, result.Install, runtimeState, noRestart); err != nil {
			return fmt.Errorf("update to %s failed after activation: %w", result.Target, err)
		}
		finalizeSpan := tracepkg.Start(cmd.Context(), "UPDATE", "update.finalize", "Finalizing update")
		if err := install.FinalizeResultContext(cmd.Context(), result.Install); err != nil {
			finalizeSpan.Fail(err)
			log.Verbose("UPDATE", "update.cleanup-failed", "Update succeeded but old version cleanup failed", logger.WithVerbose("error", err.Error()))
			commandProgressSession(cmd).Append(func(p *presentation.Presenter) {
				p.ChildStatus(presentation.StatusWarning, "Update succeeded but old version cleanup failed")
				p.Fields(presentation.Field{Label: "reason", Value: err.Error()})
			})
		} else {
			finalizeSpan.End()
		}
		bootstrapSpan := tracepkg.Start(cmd.Context(), "UPDATE", "update.supplemental.bootstrap", "Bootstrapping install supplements")
		supplemental, err := application.RunPostInstallBootstrap(cmd.Context())
		if err != nil {
			bootstrapSpan.Fail(err)
			return fmt.Errorf("bootstrap install supplements: %w", err)
		}
		bootstrapSpan.End()
		renderSupplementalInstallSummary(cmd, supplemental)
		message := "Update complete"
		if result.Downgrade {
			message = "Version change complete"
		}
		renderMutationSuccess(cmd, message,
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
		logCommandVerbose(cmd, "UPDATE", "update.release.checking", "Resolving latest release", logger.WithVerbose("current", version.Version))
		checker := updatepkg.Checker{Source: updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version}}
		planSpan := tracepkg.Start(cmd.Context(), "UPDATE", "update.plan", "Checking for updates")
		result, err := checker.Check(cmd.Context(), version.Version)
		if err != nil {
			planSpan.Fail(err)
			return fmt.Errorf("check latest release: %w", err)
		}
		planSpan.End()
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
		renderMutationResult(cmd, kind, message, fields...)
		return nil
	}}
}
