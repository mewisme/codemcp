//go:build windows

package released024

import (
	"path/filepath"
	"strings"
)

type legacyDefaults struct {
	ConfigRoot  string
	InstallRoot string
	BinDir      string
	BinaryName  string
	AliasName   string
}

func legacyPlatformDefaults(home, localAppData string) legacyDefaults {
	localAppData = strings.TrimSpace(localAppData)
	if localAppData == "" {
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	installRoot := filepath.Join(localAppData, "chatgpt-mcp")
	return legacyDefaults{
		ConfigRoot:  filepath.Join(home, ".config", "chatgpt-mcp"),
		InstallRoot: installRoot,
		BinDir:      filepath.Join(installRoot, "current"),
		BinaryName:  "chatgpt-mcp.exe",
		AliasName:   "cgm.cmd",
	}
}
