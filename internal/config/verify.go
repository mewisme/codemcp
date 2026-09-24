package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
)

type VerifyResult struct {
	Format   configformat.Format
	Ext      string
	Files    int
	Warnings []string
}

var currentStructuredStateNames = map[string]bool{
	"config": true, "tunnel": true, "upstream": true, "workspaces": true, "oauth": true,
	"shell": true, "index": true, "manifest": true,
}

type currentStructuredFile struct {
	path string
	ext  string
}

func Verify() (VerifyResult, error) {
	return verifyAt(RootPath(), false)
}

func VerifyAt(root string) (VerifyResult, error) {
	return verifyAt(root, false)
}

func VerifyRuntime() (VerifyResult, error) {
	return verifyAt(RootPath(), true)
}

func verifyAt(root string, runtimeMode bool) (VerifyResult, error) {
	source, err := SourceAt(root)
	if err != nil {
		return VerifyResult{}, err
	}
	if !source.Exists {
		return VerifyResult{}, errors.New("configuration is not initialized")
	}
	files, err := collectCurrentStructuredFiles(root)
	if err != nil {
		return VerifyResult{}, err
	}
	for _, file := range files {
		if runtimeMode && isCheckpointStateFile(root, file.path) {
			continue
		}
		if file.ext != ".json" {
			return VerifyResult{}, fmt.Errorf("structured state format mismatch: %s uses %s, expected .json", file.path, file.ext)
		}
		data, err := os.ReadFile(file.path)
		if err != nil {
			return VerifyResult{}, err
		}
		if _, err := configformat.DecodeGeneric(configformat.JSON, data); err != nil {
			return VerifyResult{}, fmt.Errorf("decode %s: %w", file.path, err)
		}
	}
	load := loadAt
	if runtimeMode {
		load = loadRuntimeAt
	}
	cfg, err := load(source.Path, configformat.StructuredPathFrom(source.Path, "tunnel"))
	if err != nil {
		return VerifyResult{}, err
	}
	if err := Validate(cfg); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{Format: source.Format, Ext: source.Ext, Files: len(files), Warnings: SecurityWarnings(cfg)}, nil
}

func collectCurrentStructuredFiles(root string) ([]currentStructuredFile, error) {
	files := make([]currentStructuredFile, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".json" && ext != ".yaml" && ext != ".yml" && ext != ".toml" {
			return nil
		}
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !currentStructuredStateNames[base] && !isTunnelMetadataFile(root, path) {
			return nil
		}
		files = append(files, currentStructuredFile{path: path, ext: ext})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no structured config files found")
	}
	return files, nil
}

func isTunnelMetadataFile(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return filepath.Dir(relative) == "tunnels"
}

func isCheckpointStateFile(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	return len(parts) >= 4 && parts[0] == "workspaces" && parts[2] == "checkpoints"
}
