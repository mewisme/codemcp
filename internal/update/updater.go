package update

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/install"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var (
	ErrDevelopmentUpdate      = errors.New("development builds cannot self-update")
	ErrCurrentVersionMismatch = errors.New("running version does not match managed current version")
)

type Resolver interface {
	Latest(context.Context) (Release, error)
	Version(context.Context, string) (Release, error)
}

type ArtifactSource interface {
	Download(context.Context, Release) (Artifact, error)
}

type Updater struct {
	Resolver   Resolver
	Downloader ArtifactSource
	Install    func(install.Options) (install.Result, error)
}

type ApplyOptions struct {
	Layout          install.Layout
	CurrentVersion  string
	TargetVersion   string
	ResolvedRelease *Release
}

type ApplyResult struct {
	Current   string
	Target    string
	Changed   bool
	Downgrade bool
	Release   Release
	Install   install.Result
	Warnings  []string
}

func (u Updater) Resolve(ctx context.Context, options ApplyOptions) (ApplyResult, error) {
	span := tracepkg.Start(ctx, "UPDATE", "update.plan", "Resolving update plan", tracepkg.String("running_version", options.CurrentVersion), tracepkg.String("requested_target", strings.TrimSpace(options.TargetVersion)))
	result, err := u.resolve(ctx, options)
	if err != nil {
		span.FailMessage("Update plan resolution failed", err)
		return ApplyResult{}, err
	}
	span.EndMessage("Update plan resolved", tracepkg.String("current", result.Current), tracepkg.String("target", result.Target), tracepkg.Bool("changed", result.Changed), tracepkg.Bool("downgrade", result.Downgrade))
	return result, nil
}

func (u Updater) Apply(ctx context.Context, options ApplyOptions) (ApplyResult, error) {
	applySpan := tracepkg.Start(ctx, "UPDATE", "update.apply", "Applying update", tracepkg.String("running_version", options.CurrentVersion), tracepkg.String("requested_target", strings.TrimSpace(options.TargetVersion)))
	result, err := u.resolve(ctx, options)
	if err != nil {
		applySpan.FailMessage("Update failed", err)
		return ApplyResult{}, err
	}
	if !result.Changed {
		decision := "current"
		if result.Current != result.Target {
			decision = "ahead"
		}
		applySpan.EndMessage("Update not required", tracepkg.String("current", result.Current), tracepkg.String("target", result.Target), tracepkg.String("decision", decision), tracepkg.Bool("changed", false))
		return result, nil
	}
	downloader := u.Downloader
	if downloader == nil {
		downloader = Downloader{}
	}
	downloadSpan := tracepkg.Start(ctx, "UPDATE", "update.artifact.download", "Downloading verified release artifact", tracepkg.String("version", result.Target), tracepkg.String("archive", result.Release.ArchiveName))
	artifact, err := downloader.Download(ctx, result.Release)
	if err != nil {
		downloadSpan.FailMessage("Verified release artifact download failed", err)
		applySpan.FailMessage("Update failed", err)
		return ApplyResult{}, err
	}
	downloadSpan.EndMessage("Verified release artifact downloaded", tracepkg.String("directory", artifact.Dir), tracepkg.String("binary", artifact.Binary))
	result.Warnings = append(result.Warnings, artifact.Warnings...)
	defer func() {
		cleanupSpan := tracepkg.Start(ctx, "UPDATE", "update.artifact.cleanup", "Cleaning update artifact", tracepkg.String("path", artifact.Dir))
		if cleanupErr := artifact.Cleanup(); cleanupErr != nil {
			cleanupSpan.FailMessage("Update artifact cleanup failed", cleanupErr)
		} else {
			cleanupSpan.EndMessage("Update artifact cleaned")
		}
	}()
	installer := u.Install
	if installer == nil {
		installer = install.Install
	}
	installSpan := tracepkg.Start(ctx, "UPDATE", "update.install", "Installing resolved update", tracepkg.String("version", result.Target), tracepkg.String("source", artifact.Binary), tracepkg.String("layout", options.Layout.Root))
	installed, err := installer(install.Options{Context: ctx, Layout: options.Layout, Version: result.Target, Source: artifact.Binary})
	if err != nil {
		installSpan.FailMessage("Resolved update installation failed", err)
		applySpan.FailMessage("Update failed", err)
		return ApplyResult{}, fmt.Errorf("install update %s: %w", result.Target, err)
	}
	installSpan.EndMessage("Resolved update installed", tracepkg.String("binary", installed.Staged.Binary), tracepkg.Bool("already_installed", installed.AlreadyInstalled))
	result.Install = installed
	applySpan.EndMessage("Update applied", tracepkg.String("previous", result.Current), tracepkg.String("current", result.Target), tracepkg.Bool("changed", true), tracepkg.Bool("downgrade", result.Downgrade))
	return result, nil
}

func (u Updater) resolve(ctx context.Context, options ApplyOptions) (ApplyResult, error) {
	current := strings.TrimSpace(options.CurrentVersion)
	if isDevelopmentVersion(current) {
		return ApplyResult{}, ErrDevelopmentUpdate
	}
	current, err := NormalizeVersion(current)
	if err != nil {
		return ApplyResult{}, err
	}
	currentSpan := tracepkg.Start(ctx, "UPDATE", "update.current.read", "Reading managed current version", tracepkg.String("metadata", options.Layout.Metadata), tracepkg.String("running_version", current))
	managedCurrent, _, err := install.CurrentVersion(options.Layout)
	if err != nil {
		currentSpan.FailMessage("Managed current version read failed", err)
		return ApplyResult{}, fmt.Errorf("read managed current version: %w", err)
	}
	managedCurrent, err = NormalizeVersion(managedCurrent)
	if err != nil {
		currentSpan.FailMessage("Managed current version normalization failed", err)
		return ApplyResult{}, fmt.Errorf("managed current version: %w", err)
	}
	currentSpan.EndMessage("Managed current version read", tracepkg.String("managed_version", managedCurrent), tracepkg.Bool("matches_running", managedCurrent == current))
	if managedCurrent != current {
		err := fmt.Errorf("%w: running %s, current %s", ErrCurrentVersionMismatch, current, managedCurrent)
		return ApplyResult{}, err
	}
	resolver := u.Resolver
	if resolver == nil {
		resolver = Client{}
	}
	targetRequest := strings.TrimSpace(options.TargetVersion)
	var release Release
	resolveSpan := tracepkg.Start(ctx, "UPDATE", "update.target.resolve", "Resolving update target", tracepkg.String("current", current), tracepkg.String("requested_target", targetRequest))
	if options.ResolvedRelease != nil {
		release = *options.ResolvedRelease
	} else if targetRequest == "" {
		release, err = resolver.Latest(ctx)
	} else {
		targetRequest, err = NormalizeVersion(targetRequest)
		if err == nil {
			release, err = resolver.Version(ctx, targetRequest)
		}
	}
	if err != nil {
		resolveSpan.FailMessage("Update target resolution failed", err)
		return ApplyResult{}, err
	}
	if targetRequest != "" {
		targetRequest, err = NormalizeVersion(targetRequest)
		if err != nil {
			resolveSpan.FailMessage("Update target normalization failed", err)
			return ApplyResult{}, err
		}
	}
	target, err := NormalizeVersion(release.Version)
	if err != nil {
		resolveSpan.FailMessage("Update target normalization failed", err)
		return ApplyResult{}, err
	}
	if targetRequest != "" && target != targetRequest {
		err := fmt.Errorf("resolved release version %s does not match requested version %s", target, targetRequest)
		resolveSpan.FailMessage("Update target resolution failed", err)
		return ApplyResult{}, err
	}
	comparison, err := CompareVersions(current, target)
	if err != nil {
		resolveSpan.FailMessage("Update target comparison failed", err)
		return ApplyResult{}, err
	}
	changed := comparison != 0 && (targetRequest != "" || comparison < 0)
	result := ApplyResult{Current: current, Target: target, Changed: changed, Downgrade: comparison > 0, Release: release}
	decision := "update"
	if comparison == 0 {
		decision = "current"
	} else if targetRequest == "" && comparison > 0 {
		decision = "ahead"
	} else if comparison > 0 {
		decision = "downgrade"
	}
	resolveSpan.EndMessage("Update target resolved", tracepkg.String("target", target), tracepkg.Int("comparison", comparison), tracepkg.Bool("changed", result.Changed), tracepkg.Bool("downgrade", result.Downgrade), tracepkg.String("decision", decision), tracepkg.String("archive", release.ArchiveName))
	return result, nil
}
