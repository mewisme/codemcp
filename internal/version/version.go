package version

import (
	"fmt"
	"runtime/debug"
)

const ClientName = "codemcp"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		applyBuildInfo(info)
	}
}

func applyBuildInfo(info *debug.BuildInfo) {
	if info == nil {
		return
	}
	if Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
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

func String() string {
	return fmt.Sprintf("CodeMCP version %s (%s) %s", Version, Commit, Date)
}

func Short() string {
	return fmt.Sprintf("%s (%s) %s", Version, Commit, Date)
}
