package codegraph

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

const (
	Version          = "v1.6.0"
	Repository       = "colbymchenry/codegraph"
	License          = "MIT"
	ChecksumAsset    = "SHA256SUMS"
	ProbeOutputLimit = 16 << 10
	MaxOutputBytes   = 256 << 10
	ProbeTimeout     = 2 * time.Second
	InitTimeout      = 2 * time.Minute
	SyncTimeout      = 30 * time.Second
)

type ExecutableState string

const (
	ExecutableDisabled    ExecutableState = "disabled"
	ExecutableConfigured  ExecutableState = "configured"
	ExecutableSystem      ExecutableState = "system"
	ExecutableManaged     ExecutableState = "managed"
	ExecutableUnavailable ExecutableState = "unavailable"
)

type Asset struct {
	Target     string
	URL        string
	SHA256     string
	Archive    string
	Entrypoint string
	PrefixArgs []string
	Required   []string
}

var assets = map[string]Asset{
	"darwin/amd64":  asset("darwin-x64", "cb86a2b62ee676b62a56bf8423600e7d867e752e57f323cdc98c0f6236efd908", "tar.gz", "bin/codegraph"),
	"darwin/arm64":  asset("darwin-arm64", "1c73033512d55f67be04717e81532e8beaf7be6fb8531f51a179fa23064ad480", "tar.gz", "bin/codegraph"),
	"linux/amd64":   asset("linux-x64", "de3391f79ed42622d937e6cd5b7642a7ea8bb7d1473607e80b879ba73ef216b0", "tar.gz", "bin/codegraph"),
	"linux/arm64":   asset("linux-arm64", "6dc935a7b8f1a61e688a578b98ea34680eb2e36d7b91db079d64f4011f1a668f", "tar.gz", "bin/codegraph"),
	"windows/amd64": windowsAsset("win32-x64", "cd76c3c3391f2d40abef12b142151950b6d77abc2d8429e648f89eaa90f5b68a"),
	"windows/arm64": windowsAsset("win32-arm64", "3ca980010bd718a6b5e75be1145806ae6491afb1a59a2cec6cee4bf5c39f1b3a"),
}

func AssetFor(goos, goarch string) (Asset, bool) {
	value, ok := assets[strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch)]
	if !ok {
		return Asset{}, false
	}
	value.PrefixArgs = append([]string(nil), value.PrefixArgs...)
	value.Required = append([]string(nil), value.Required...)
	return value, true
}

func ManagedRoot(configRoot string) (string, error) {
	configRoot = strings.TrimSpace(configRoot)
	if configRoot == "" {
		return "", errors.New("codegraph managed asset requires a selected CodeMCP config root")
	}
	return filepath.Join(configRoot, "managed-assets"), nil
}

func SystemExecutable() string { return "codegraph" }

func VersionProbeArgs() []string { return []string{"--version"} }

func InitArgs(projectRoot string) []string { return []string{"init", "--yes", projectRoot} }

func SyncArgs(projectRoot string) []string { return []string{"sync", projectRoot} }

func asset(target, sha256, archive, launcher string) Asset {
	prefix := "codegraph-" + target
	entrypoint := prefix + "/" + launcher
	return Asset{
		Target:     target,
		URL:        "https://github.com/" + Repository + "/releases/download/" + Version + "/codegraph-" + target + "." + archive,
		SHA256:     sha256,
		Archive:    archive,
		Entrypoint: entrypoint,
		Required:   []string{prefix + "/node", prefix + "/lib/dist/bin/codegraph.js", entrypoint},
	}
}

func windowsAsset(target, sha256 string) Asset {
	prefix := "codegraph-" + target
	script := prefix + "/lib/dist/bin/codegraph.js"
	return Asset{
		Target:     target,
		URL:        "https://github.com/" + Repository + "/releases/download/" + Version + "/codegraph-" + target + ".zip",
		SHA256:     sha256,
		Archive:    "zip",
		Entrypoint: prefix + "/node.exe",
		PrefixArgs: []string{script},
		Required:   []string{prefix + "/node.exe", script, prefix + "/bin/codegraph.cmd"},
	}
}
