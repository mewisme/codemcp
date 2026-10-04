package cli

import (
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/version"
)

func cacheLatestRelease(cmd *cobra.Command, layout install.Layout, latest string) {
	if err := updatepkg.WriteCache(layout.UpdateCache, latest, time.Now()); err != nil {
		commandLogger(cmd).Verbose("UPDATE", "update.cache-write-failed", "Update check succeeded but cache write failed", logger.WithVerbose("error", err.Error()))
		commandProgressSession(cmd).Append(func(p *presentation.Presenter) {
			p.ChildStatus(presentation.StatusWarning, "Update check succeeded but cache write failed")
			p.Fields(presentation.Field{Label: "reason", Value: err.Error()})
		})
	}
}

func cacheLatestReleaseForCurrentInstall(cmd *cobra.Command, latest string) {
	detection, err := install.DetectCurrent(version.Version)
	if err != nil || detection.Method != install.MethodDirect || detection.Metadata == nil {
		return
	}
	layout, err := detection.ManagedLayout()
	if err != nil {
		return
	}
	cacheLatestRelease(cmd, layout, latest)
}

func cachedUpdateStatus(now time.Time) *updatepkg.CachedCheck {
	detection, err := install.DetectCurrent(version.Version)
	if err != nil || detection.Method != install.MethodDirect || detection.Metadata == nil {
		return nil
	}
	layout, err := detection.ManagedLayout()
	if err != nil {
		return nil
	}
	cached, ok, err := updatepkg.ReadFreshCache(layout.UpdateCache, version.Version, now, updatepkg.DefaultCacheTTL)
	if err != nil || !ok {
		return nil
	}
	return &cached
}

func formatCachedUpdate(cached *updatepkg.CachedCheck) string {
	if cached == nil {
		return ""
	}
	switch cached.Status {
	case updatepkg.StatusAvailable:
		return cached.Latest + " available"
	case updatepkg.StatusUpToDate:
		return "up to date"
	case updatepkg.StatusAhead:
		return "current is newer than " + cached.Latest
	case updatepkg.StatusDevelopment:
		return "development build · latest " + cached.Latest
	default:
		return string(cached.Status)
	}
}

func logCachedUpdate(log *logger.Logger, cached *updatepkg.CachedCheck) {
	if log == nil || cached == nil {
		return
	}
	log.Detail("update", formatCachedUpdate(cached))
	log.Detail("update checked", cached.CheckedAt.Local().Format(time.RFC3339))
}
