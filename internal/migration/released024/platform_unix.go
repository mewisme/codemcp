//go:build !windows

package released024

import "path/filepath"

type legacyDefaults struct {
	ConfigRoot  string
	InstallRoot string
	BinDir      string
	BinaryName  string
	AliasName   string
}

func legacyPlatformDefaults(home, _ string) legacyDefaults {
	return legacyDefaults{
		ConfigRoot:  filepath.Join(home, ".config", "chatgpt-mcp"),
		InstallRoot: filepath.Join(home, ".chatgpt-mcp"),
		BinDir:      filepath.Join(home, ".local", "bin"),
		BinaryName:  "chatgpt-mcp",
		AliasName:   "cgm",
	}
}
