//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
)

func historicalBinaryName() string { return "chatgpt-mcp.exe" }
func historicalAliasName() string  { return "cgm.cmd" }

func platformPackageManagerOwnsPath(path string) bool {
	normalized := normalizedPath(path)
	for _, marker := range []string{"/scoop/apps/", "/scoop/shims/", "/chocolatey/", "/microsoft/winget/", "/windowsapps/"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	for _, root := range []string{os.Getenv("SCOOP"), os.Getenv("ChocolateyInstall")} {
		if strings.TrimSpace(root) != "" && withinPath(root, path) {
			return true
		}
	}
	return false
}

func legacyAliasTargetPlatform(aliasPath, binaryName string) (string, bool, error) {
	data, err := os.ReadFile(aliasPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	content := strings.ToLower(strings.ReplaceAll(string(data), "\\", "/"))
	if !strings.Contains(content, "%~dp0"+strings.ToLower(binaryName)) {
		return "", false, nil
	}
	return filepath.Join(filepath.Dir(aliasPath), binaryName), true, nil
}
