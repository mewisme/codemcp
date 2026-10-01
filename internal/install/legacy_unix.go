//go:build !windows

package install

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func historicalBinaryName() string { return "chatgpt-mcp" }
func historicalAliasName() string  { return "cgm" }

func platformPackageManagerOwnsPath(path string) bool {
	normalized := normalizedPath(path)
	for _, marker := range []string{"/nix/store/", "/snap/", "/flatpak/", "/opt/local/", "/cellar/", "/caskroom/"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	if runtime.GOOS != "linux" {
		return false
	}
	if ownership := linuxPackageOwnership(path); ownership.Package != "" {
		return true
	}
	checks := []struct {
		command string
		args    []string
	}{
		{command: "pacman", args: []string{"-Qo", path}},
		{command: "apk", args: []string{"info", "--who-owns", path}},
	}
	for _, check := range checks {
		command, err := exec.LookPath(check.command)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), packageOwnerProbeTimeout)
		_, err = runPackageOwnerProbe(ctx, command, check.args...)
		cancel()
		if err == nil {
			return true
		}
	}
	return false
}

func legacyAliasTargetPlatform(aliasPath, binaryName string) (string, bool, error) {
	info, err := os.Lstat(aliasPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", false, nil
	}
	target, err := os.Readlink(aliasPath)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(aliasPath), target)
	}
	if !strings.EqualFold(filepath.Base(target), binaryName) {
		return "", false, nil
	}
	return filepath.Clean(target), true, nil
}
