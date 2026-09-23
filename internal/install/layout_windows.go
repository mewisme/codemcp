//go:build windows

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
		root = filepath.Join(home, ".cm")
	}
	return NewLayout(root, filepath.Join(root, "current"))
}

func defaultLayout(home, _ string) (Layout, error) {
	root := filepath.Join(home, ".cm")
	return NewLayout(root, filepath.Join(root, "current"))
}

func platformBinaryName() string { return "cm.exe" }
