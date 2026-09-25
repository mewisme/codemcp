package version

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
)

const ClientName = "codemcp"

const DevelopmentVersion = "0.0.1-dev"

var releaseVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

var (
	// ReleaseTag is populated by tagged release builds. Version is derived from
	// it rather than linked independently so every surface observes one semantic
	// version authority.
	ReleaseTag = ""
	Version    = DevelopmentVersion
	Commit     = "unknown"
	Date       = "unknown"
)

func init() {
	info, _ := debug.ReadBuildInfo()
	applyBuildInfo(info)
}

func applyBuildInfo(info *debug.BuildInfo) {
	if Version == DevelopmentVersion {
		if release := normalizeReleaseVersion(ReleaseTag); release != "" {
			Version = release
		}
	}
	if info == nil {
		return
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if Commit == "unknown" && setting.Value != "" {
				Commit = setting.Value
			}
		case "vcs.time":
			if Date == "unknown" && setting.Value != "" {
				Date = setting.Value
			}
		}
	}
}

func normalizeReleaseVersion(value string) string {
	value = strings.TrimSpace(value)
	if !releaseVersionPattern.MatchString(value) {
		return ""
	}
	return strings.TrimPrefix(value, "v")
}

func UserAgent() string { return ClientName + "/" + Version }

func IsDevelopment(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || value == DevelopmentVersion || value == "dev" || value == "(devel)" || strings.HasPrefix(value, "dev-")
}

func String() string {
	return fmt.Sprintf("CodeMCP version %s (%s) %s", Version, Commit, Date)
}

func Short() string {
	return fmt.Sprintf("%s (%s) %s", Version, Commit, Date)
}
