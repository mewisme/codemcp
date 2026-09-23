package install

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
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

type LegacyCleanupFailure struct {
	Path string
	Err  error
}

type LegacyCleanupResult struct {
	Removed        []LegacyInstallation
	RemovedAliases []string
	Preserved      []LegacyInstallation
	Failed         []LegacyCleanupFailure
}

type LegacyCleanupOptions struct {
	Context        context.Context
	Layout         Layout
	Source         string
	PreserveSource bool
}

type legacyEnvironment struct {
	Path   string
	Home   string
	GoBin  string
	GoPath string
	Scoop  string
}

type legacyBackup struct {
	Path   string
	Backup string
}

func FindLegacyInstallations(layout Layout, source string) ([]LegacyInstallation, error) {
	env := currentLegacyEnvironment()
	return findLegacyInstallations(layout, source, env)
}

func CleanupLegacyInstallations(options LegacyCleanupOptions) (LegacyCleanupResult, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "INSTALL", "install.legacy.cleanup", "Cleaning legacy installations", tracepkg.String("source", options.Source), tracepkg.Bool("preserve_source", options.PreserveSource))
	items, err := FindLegacyInstallations(options.Layout, options.Source)
	if err != nil {
		span.FailMessage("Legacy installation discovery failed", err)
		return LegacyCleanupResult{}, err
	}
	result := LegacyCleanupResult{}
	for _, item := range items {
		if options.PreserveSource && options.Source != "" && samePath(item.Target, options.Source) {
			item.Removable = false
			item.Reason = "current executable"
			result.Preserved = append(result.Preserved, item)
			continue
		}
		if !item.Removable {
			result.Preserved = append(result.Preserved, item)
			continue
		}
		aliasPath, aliasMatches, aliasErr := legacyAliasForBinary(options.Layout, item.Path)
		if aliasErr != nil {
			result.Failed = append(result.Failed, LegacyCleanupFailure{Path: aliasPath, Err: aliasErr})
		}
		if err := os.Remove(item.Path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				result.Failed = append(result.Failed, LegacyCleanupFailure{Path: item.Path, Err: err})
				continue
			}
		}
		result.Removed = append(result.Removed, item)
		if aliasMatches {
			if err := os.Remove(aliasPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				result.Failed = append(result.Failed, LegacyCleanupFailure{Path: aliasPath, Err: err})
			} else {
				result.RemovedAliases = append(result.RemovedAliases, aliasPath)
			}
		}
	}
	span.EndMessage("Legacy installations cleaned", tracepkg.Int("candidates", len(items)), tracepkg.Int("removed", len(result.Removed)), tracepkg.Int("removed_aliases", len(result.RemovedAliases)), tracepkg.Int("preserved", len(result.Preserved)), tracepkg.Int("failed", len(result.Failed)))
	return result, nil
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
		path := filepath.Join(dir, layout.BinaryName)
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

func inspectCanonicalLegacy(layout Layout, source string) (*LegacyInstallation, error) {
	if _, err := os.Lstat(layout.CanonicalBinary); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	item, err := inspectLegacyInstallation(layout, source, layout.CanonicalBinary, currentLegacyEnvironment())
	if err != nil {
		return nil, err
	}
	return &item, nil
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

func removableLegacyAt(items []LegacyInstallation, path string) *LegacyInstallation {
	for index := range items {
		if items[index].Removable && samePath(items[index].Path, path) {
			return &items[index]
		}
	}
	return nil
}

func backupLegacyPath(path string) (legacyBackup, error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".chatgpt-mcp-legacy-*")
	if err != nil {
		return legacyBackup{}, err
	}
	backup := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(backup)
		return legacyBackup{}, err
	}
	if err := os.Remove(backup); err != nil {
		return legacyBackup{}, err
	}
	if err := os.Rename(path, backup); err != nil {
		return legacyBackup{}, fmt.Errorf("backup legacy installation %s: %w", path, err)
	}
	return legacyBackup{Path: path, Backup: backup}, nil
}

func restoreLegacyBackups(backups []legacyBackup) error {
	var restoreErr error
	for index := len(backups) - 1; index >= 0; index-- {
		backup := backups[index]
		if err := os.Remove(backup.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			restoreErr = errors.Join(restoreErr, err)
			continue
		}
		if err := os.Rename(backup.Backup, backup.Path); err != nil {
			restoreErr = errors.Join(restoreErr, err)
		}
	}
	return restoreErr
}

func discardLegacyBackups(backups []legacyBackup, cleanup *LegacyCleanupResult) {
	for _, backup := range backups {
		if err := os.Remove(backup.Backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanup.Failed = append(cleanup.Failed, LegacyCleanupFailure{Path: backup.Path, Err: err})
		}
	}
}

func legacyAliasForBinary(layout Layout, binaryPath string) (string, bool, error) {
	aliasPath := filepath.Join(filepath.Dir(binaryPath), layout.AliasName)
	target, ok, err := legacyAliasTargetPlatform(aliasPath, layout.BinaryName)
	if err != nil || !ok {
		return aliasPath, false, err
	}
	return aliasPath, samePath(target, binaryPath), nil
}

func legacyAliasMatchesAny(layout Layout, items []LegacyInstallation) (bool, error) {
	target, ok, err := legacyAliasTargetPlatform(layout.AliasPath, layout.BinaryName)
	if err != nil || !ok {
		return false, err
	}
	for _, item := range items {
		if item.Removable && (samePath(target, item.Path) || samePath(target, item.Target)) {
			return true, nil
		}
	}
	return false, nil
}
