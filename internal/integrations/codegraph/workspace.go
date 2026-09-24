package codegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/oslock"
	statepkg "go.mewis.me/codemcp/internal/state"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	workspaceMetadataVersion = 1
	workspaceMetadataLimit   = int64(2 << 20)
	maxFingerprintEntries    = 100000
)

type IndexState string

const (
	IndexUnindexed IndexState = "unindexed"
	IndexIndexed   IndexState = "indexed"
)

type FreshnessState string

const (
	FreshnessUnknown FreshnessState = "unknown"
	FreshnessFresh   FreshnessState = "fresh"
	FreshnessDirty   FreshnessState = "dirty"
)

type WorkspaceStatus struct {
	Runtime           Status         `json:"runtime"`
	WorkspaceID       string         `json:"workspace_id"`
	ProjectPath       string         `json:"project_path"`
	RelativePath      string         `json:"relative_path"`
	IndexState        IndexState     `json:"index_state"`
	Freshness         FreshnessState `json:"freshness"`
	DirtyReason       string         `json:"dirty_reason,omitempty"`
	LastInitializedAt string         `json:"last_initialized_at,omitempty"`
	LastSyncedAt      string         `json:"last_synced_at,omitempty"`
	Diagnostic        string         `json:"diagnostic,omitempty"`
}

type workspaceMetadata struct {
	Version     int                        `json:"version"`
	WorkspaceID string                     `json:"workspace_id"`
	Projects    map[string]projectMetadata `json:"projects"`
}

type projectMetadata struct {
	RelativePath      string `json:"relative_path"`
	Fingerprint       string `json:"fingerprint"`
	LastInitializedAt string `json:"last_initialized_at,omitempty"`
	LastSyncedAt      string `json:"last_synced_at,omitempty"`
	UpdatedAt         string `json:"updated_at"`
}

func WorkspaceStatePath(store workspacestate.Store) (string, error) {
	return store.StatePath("codegraph.json")
}

func WorkspaceMutationLockPath(store workspacestate.Store) (string, error) {
	return store.Join("runtime", "codegraph.lock")
}

func AcquireWorkspaceMutationLock(store workspacestate.Store) (*oslock.Lock, error) {
	path, err := WorkspaceMutationLockPath(store)
	if err != nil {
		return nil, err
	}
	return oslock.Acquire(path, oslock.Exclusive)
}

func InspectWorkspace(runtimeStatus Status, store workspacestate.Store, workspaceID, projectRoot, relativePath string) WorkspaceStatus {
	relativePath = normalizeRelativeProjectPath(relativePath)
	result := WorkspaceStatus{
		Runtime: runtimeStatus, WorkspaceID: strings.TrimSpace(workspaceID), ProjectPath: filepath.Clean(projectRoot),
		RelativePath: relativePath, IndexState: IndexUnindexed, Freshness: FreshnessUnknown,
	}
	if !InspectIndex(projectRoot) {
		return result
	}
	result.IndexState = IndexIndexed

	metadata, err := loadWorkspaceMetadata(store, workspaceID)
	if errors.Is(err, os.ErrNotExist) {
		result.Freshness = FreshnessDirty
		result.DirtyReason = "index_not_recorded"
		return result
	}
	if err != nil {
		result.Freshness = FreshnessDirty
		result.DirtyReason = "metadata_unavailable"
		result.Diagnostic = boundedDiagnostic(err.Error())
		return result
	}
	record, ok := metadata.Projects[relativePath]
	if !ok {
		result.Freshness = FreshnessDirty
		result.DirtyReason = "index_not_recorded"
		return result
	}
	result.LastInitializedAt = record.LastInitializedAt
	result.LastSyncedAt = record.LastSyncedAt
	if strings.TrimSpace(record.Fingerprint) == "" {
		result.Freshness = FreshnessDirty
		result.DirtyReason = "fingerprint_missing"
		return result
	}
	fingerprint, err := projectFingerprint(projectRoot)
	if err != nil {
		result.Freshness = FreshnessUnknown
		result.Diagnostic = boundedDiagnostic(err.Error())
		return result
	}
	if fingerprint != record.Fingerprint {
		result.Freshness = FreshnessDirty
		result.DirtyReason = "source_changed"
		return result
	}
	result.Freshness = FreshnessFresh
	return result
}

func RecordWorkspaceLifecycle(store workspacestate.Store, workspaceID, projectRoot, relativePath, action string, now time.Time) error {
	relativePath = normalizeRelativeProjectPath(relativePath)
	fingerprint, err := projectFingerprint(projectRoot)
	if err != nil {
		return err
	}
	metadata, err := loadWorkspaceMetadata(store, workspaceID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if metadata.Projects == nil {
		metadata = workspaceMetadata{Version: workspaceMetadataVersion, WorkspaceID: strings.TrimSpace(workspaceID), Projects: map[string]projectMetadata{}}
	}
	record := metadata.Projects[relativePath]
	record.RelativePath = relativePath
	record.Fingerprint = fingerprint
	timestamp := now.UTC().Format(time.RFC3339Nano)
	switch strings.TrimSpace(action) {
	case "init":
		record.LastInitializedAt = timestamp
	case "sync":
		record.LastSyncedAt = timestamp
	default:
		return fmt.Errorf("unsupported CodeGraph workspace lifecycle action %q", action)
	}
	record.UpdatedAt = timestamp
	metadata.Version = workspaceMetadataVersion
	metadata.WorkspaceID = strings.TrimSpace(workspaceID)
	metadata.Projects[relativePath] = record
	return saveWorkspaceMetadata(store, metadata)
}

func InspectIndex(projectRoot string) bool {
	info, err := os.Lstat(filepath.Join(projectRoot, ".codegraph"))
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func normalizeRelativeProjectPath(value string) string {
	value = filepath.Clean(strings.TrimSpace(value))
	if value == "" || value == "." {
		return "."
	}
	return filepath.ToSlash(value)
}

func loadWorkspaceMetadata(store workspacestate.Store, workspaceID string) (workspaceMetadata, error) {
	path, err := WorkspaceStatePath(store)
	if err != nil {
		return workspaceMetadata{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workspaceMetadata{Version: workspaceMetadataVersion, WorkspaceID: strings.TrimSpace(workspaceID), Projects: map[string]projectMetadata{}}, os.ErrNotExist
		}
		return workspaceMetadata{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > workspaceMetadataLimit {
		return workspaceMetadata{}, errors.New("CodeGraph workspace metadata must be a bounded regular non-symlink file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return workspaceMetadata{}, err
	}
	var metadata workspaceMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return workspaceMetadata{}, fmt.Errorf("decode CodeGraph workspace metadata: %w", err)
	}
	if metadata.Version != workspaceMetadataVersion || metadata.WorkspaceID != strings.TrimSpace(workspaceID) {
		return workspaceMetadata{}, errors.New("CodeGraph workspace metadata identity does not match registered workspace")
	}
	if metadata.Projects == nil {
		metadata.Projects = map[string]projectMetadata{}
	}
	return metadata, nil
}

func saveWorkspaceMetadata(store workspacestate.Store, metadata workspaceMetadata) error {
	path, err := WorkspaceStatePath(store)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if int64(len(data)) > workspaceMetadataLimit {
		return errors.New("CodeGraph workspace metadata exceeds size limit")
	}
	return statepkg.WriteFileAtomic(path, append(data, '\n'), 0600)
}

func projectFingerprint(root string) (string, error) {
	root = filepath.Clean(root)
	hash := sha256.New()
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		base := entry.Name()
		if entry.IsDir() && (base == ".git" || base == ".cm" || base == ".codegraph") {
			return filepath.SkipDir
		}
		entries++
		if entries > maxFingerprintEntries {
			return fmt.Errorf("CodeGraph source fingerprint exceeds %d entries", maxFingerprintEntries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := ""
		if info.Mode()&os.ModeSymlink != 0 {
			target, _ = os.Readlink(path)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%d\x00%s\n", relative, info.Mode(), info.Size(), info.ModTime().UnixNano(), target)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func boundedDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}
