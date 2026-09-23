package install

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	EnvInstallDir = "CHATGPT_MCP_INSTALL_DIR"
	EnvBinDir     = "CHATGPT_MCP_BIN_DIR"
)

type Layout struct {
	Root            string
	Versions        string
	Current         string
	State           string
	BinDir          string
	Metadata        string
	UpdateCache     string
	BinaryName      string
	CurrentBinary   string
	CanonicalBinary string
}

func NewLayout(root, binDir string) (Layout, error) {
	root = strings.TrimSpace(root)
	binDir = strings.TrimSpace(binDir)
	if root == "" {
		return Layout{}, errors.New("install root is required")
	}
	if binDir == "" {
		return Layout{}, errors.New("binary directory is required")
	}
	root = filepath.Clean(root)
	binDir = filepath.Clean(binDir)
	current := filepath.Join(root, "current")
	state := filepath.Join(root, "state")
	binaryName := platformBinaryName()
	return Layout{
		Root:            root,
		Versions:        filepath.Join(root, "versions"),
		Current:         current,
		State:           state,
		BinDir:          binDir,
		Metadata:        filepath.Join(root, "install.json"),
		UpdateCache:     filepath.Join(state, "update.json"),
		BinaryName:      binaryName,
		CurrentBinary:   filepath.Join(current, binaryName),
		CanonicalBinary: filepath.Join(binDir, binaryName),
	}, nil
}

func (l Layout) VersionDir(version string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "" || version == "." || version == ".." || filepath.Base(version) != version {
		return "", errors.New("invalid install version")
	}
	return filepath.Join(l.Versions, version), nil
}

func (l Layout) VersionBinary(version string) (string, error) {
	dir, err := l.VersionDir(version)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, l.BinaryName), nil
}
