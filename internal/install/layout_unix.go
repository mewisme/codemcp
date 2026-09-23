//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"strings"
)

func DefaultLayout() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, err
	}
	root := strings.TrimSpace(os.Getenv(EnvInstallDir))
	if root == "" {
		root = filepath.Join(home, ".chatgpt-mcp")
	}
	binDir := strings.TrimSpace(os.Getenv(EnvBinDir))
	if binDir == "" {
		binDir = filepath.Join(home, ".local", "bin")
	}
	return NewLayout(root, binDir)
}

func defaultLayout(home string) (Layout, error) {
	return NewLayout(filepath.Join(home, ".chatgpt-mcp"), filepath.Join(home, ".local", "bin"))
}

func platformBinaryName() string { return "cm" }
