package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
)

var structuredStateNames = map[string]bool{
	"config": true, "tunnel": true, "upstream": true, "workspaces": true, "oauth": true,
	"shell": true, "index": true, "manifest": true,
}

type conversionFile struct {
	source  string
	target  string
	archive string
	pending string
	data    []byte
	mode    os.FileMode
}

type structuredFile struct {
	path   string
	base   string
	format configformat.Format
	ext    string
}

func convertFormatAt(root string, target configformat.Format) (int, error) {
	source, err := configformat.Discover(root)
	if err != nil {
		return 0, err
	}
	if !source.Exists {
		return 0, errors.New("configuration is not initialized")
	}
	targetExt := configformat.Extension(target)
	structured, err := collectStructuredFiles(root)
	if err != nil {
		return 0, err
	}
	files := make([]conversionFile, 0, len(structured))
	targets := map[string]string{}
	for _, item := range structured {
		targetPath := filepath.Join(filepath.Dir(item.path), item.base+targetExt)
		if previous, exists := targets[targetPath]; exists && previous != item.path {
			return 0, fmt.Errorf("multiple structured files map to conversion target %s: %s, %s", targetPath, previous, item.path)
		}
		targets[targetPath] = item.path
		original, err := os.ReadFile(item.path)
		if err != nil {
			return 0, err
		}
		raw, err := configformat.DecodeGeneric(item.format, original)
		if err != nil {
			return 0, fmt.Errorf("decode %s: %w", item.path, err)
		}
		if item.base == "upstream" {
			if values, ok := raw.([]any); ok {
				raw = map[string]any{"servers": values}
			}
		}
		if targetPath == item.path {
			continue
		}
		if _, err := os.Stat(targetPath); err == nil {
			return 0, fmt.Errorf("conversion target already exists: %s", targetPath)
		} else if !os.IsNotExist(err) {
			return 0, err
		}
		encoded, err := configformat.EncodeGeneric(target, raw)
		if err != nil {
			return 0, fmt.Errorf("encode %s as %s: %w", item.path, target, err)
		}
		info, err := os.Stat(item.path)
		if err != nil {
			return 0, err
		}
		stamp := time.Now().UTC().UnixNano()
		archive := fmt.Sprintf("%s.preserved-%d", item.path, stamp)
		pending := fmt.Sprintf("%s.pending-%d", targetPath, stamp)
		files = append(files, conversionFile{source: item.path, target: targetPath, archive: archive, pending: pending, data: encoded, mode: info.Mode().Perm()})
	}
	if len(files) == 0 {
		return 0, nil
	}

	for _, file := range files {
		if err := state.WriteFileAtomic(file.pending, file.data, file.mode); err != nil {
			return 0, fmt.Errorf("write pending converted file %s: %w", file.pending, err)
		}
	}
	for _, file := range files {
		if err := os.Rename(file.source, file.archive); err != nil {
			return 0, fmt.Errorf("preserve old structured file %s: %w", file.source, err)
		}
	}
	activated := make([]conversionFile, 0, len(files))
	for _, file := range files {
		if err := os.Rename(file.pending, file.target); err != nil {
			for _, current := range activated {
				_ = os.Rename(current.target, current.pending+".failed")
			}
			for _, current := range files {
				if _, statErr := os.Stat(current.archive); statErr == nil {
					_ = os.Rename(current.archive, current.source)
				}
			}
			return 0, fmt.Errorf("activate converted file %s: %w", file.target, err)
		}
		activated = append(activated, file)
	}
	return len(files), nil
}

func collectStructuredFiles(root string) ([]structuredFile, error) {
	files := make([]structuredFile, 0)
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
		if !structuredStateNames[base] && !isTunnelMetadataFile(root, path) {
			return nil
		}
		format, err := configformat.Detect(path)
		if err != nil {
			return err
		}
		files = append(files, structuredFile{path: path, base: base, format: format, ext: filepath.Ext(entry.Name())})
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
