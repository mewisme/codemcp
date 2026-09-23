package install

import (
	"debug/buildinfo"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	currentModulePath      = "go.mewis.me/codemcp"
	legacyModulePath       = "go.mewis.me/chatgpt-mcp"
	legacyGitHubModulePath = "github.com/mewisme/chatgpt-mcp"
)

type LegacyInstallation struct {
	Path           string
	Target         string
	Method         Method
	Verified       bool
	PackageManaged bool
	Removable      bool
	Reason         string
}

type legacyEnvironment struct {
	Path   string
	Home   string
	GoBin  string
	GoPath string
	Scoop  string
}

func FindLegacyInstallations(layout Layout, source string) ([]LegacyInstallation, error) {
	env := currentLegacyEnvironment()
	return findLegacyInstallations(layout, source, env)
}

func currentLegacyEnvironment() legacyEnvironment {
	home, _ := os.UserHomeDir()
	return legacyEnvironment{Path: os.Getenv("PATH"), Home: home, GoBin: os.Getenv("GOBIN"), GoPath: os.Getenv("GOPATH"), Scoop: os.Getenv("SCOOP")}
}

func findLegacyInstallations(layout Layout, source string, env legacyEnvironment) ([]LegacyInstallation, error) {
	seen := map[string]struct{}{}
	items := make([]LegacyInstallation, 0)
	for _, dir := range filepath.SplitList(env.Path) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, historicalBinaryName())
		absolute, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		key := normalizedPath(absolute)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := os.Lstat(absolute); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		item, err := inspectLegacyInstallation(layout, source, absolute, env)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func inspectLegacyInstallation(layout Layout, source, path string, env legacyEnvironment) (LegacyInstallation, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return LegacyInstallation{}, err
	}
	target := absolute
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		target = resolved
	}
	item := LegacyInstallation{Path: filepath.Clean(absolute), Target: filepath.Clean(target), Method: MethodStandalone}
	if withinPath(layout.Root, item.Target) || samePath(item.Target, layout.CurrentBinary) {
		item.Method = MethodDirect
		item.Reason = "managed direct installation"
		return item, nil
	}
	if isHomebrewPath(item.Path) || isHomebrewPath(item.Target) {
		item.Method = MethodHomebrew
		item.PackageManaged = true
		item.Reason = "managed by Homebrew"
		return item, nil
	}
	if isScoopCandidate(item.Path, item.Target, env.Scoop) {
		item.Method = MethodScoop
		item.PackageManaged = true
		item.Reason = "managed by Scoop"
		return item, nil
	}
	if isGoInstallPath(item.Path, env.Home, env.GoBin, env.GoPath) || isGoInstallPath(item.Target, env.Home, env.GoBin, env.GoPath) {
		item.Method = MethodGo
		item.Reason = "managed by go install"
		return item, nil
	}
	if platformPackageManagerOwnsPath(item.Path) || !samePath(item.Path, item.Target) && platformPackageManagerOwnsPath(item.Target) {
		item.Method = MethodUnknown
		item.PackageManaged = true
		item.Reason = "owned by a package manager"
		return item, nil
	}
	item.Verified = verifyChatGPTMCPBinary(item.Target)
	if !item.Verified {
		item.Method = MethodUnknown
		item.Reason = "unable to verify executable ownership"
		return item, nil
	}
	if samePath(item.Path, layout.CanonicalBinary) && samePath(item.Target, layout.CurrentBinary) {
		item.Method = MethodDirect
		item.Reason = "managed canonical command"
		return item, nil
	}
	if source != "" && samePath(item.Target, source) {
		item.Reason = "legacy standalone source executable"
	} else {
		item.Reason = "verified legacy standalone executable"
	}
	item.Removable = true
	return item, nil
}

func verifyChatGPTMCPBinary(path string) bool {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return false
	}
	for _, root := range []string{currentModulePath, legacyModulePath, legacyGitHubModulePath} {
		if info.Main.Path == root || strings.HasPrefix(info.Main.Path, root+"/") {
			return true
		}
	}
	return false
}

func isScoopCandidate(path, target, scoopRoot string) bool {
	if isScoopPath(path, scoopRoot) || isScoopPath(target, scoopRoot) {
		return true
	}
	if strings.TrimSpace(scoopRoot) == "" {
		return strings.Contains(normalizedPath(path), "/scoop/shims/") || strings.Contains(normalizedPath(target), "/scoop/shims/")
	}
	shims := filepath.Join(scoopRoot, "shims")
	return withinPath(shims, path) || withinPath(shims, target)
}
