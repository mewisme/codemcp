package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/oslock"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type LocalStateHealth string

const (
	LocalStateHealthy     LocalStateHealth = "healthy"
	LocalStateUnavailable LocalStateHealth = "unavailable"
	LocalStateCorrupt     LocalStateHealth = "corrupt"
	LocalStateConflict    LocalStateHealth = "conflict"
)

type LocalStateDiagnostic struct {
	WorkspaceID    string               `json:"workspace_id"`
	Root           string               `json:"root"`
	LocalRoot      string               `json:"local_root"`
	Health         LocalStateHealth     `json:"health"`
	Available      bool                 `json:"available"`
	SizeBytes      int64                `json:"size_bytes"`
	FileCount      int                  `json:"file_count"`
	Locked         bool                 `json:"locked"`
	LockPath       string               `json:"lock_path,omitempty"`
	LockPID        int                  `json:"lock_pid,omitempty"`
	LockInstanceID string               `json:"lock_instance_id,omitempty"`
	LockStartedAt  time.Time            `json:"lock_started_at,omitempty"`
	GitHygiene     GitHygieneDiagnostic `json:"git_hygiene"`
	Error          string               `json:"error,omitempty"`
}

func (m *Manager) Diagnose(ctx context.Context, id string) (LocalStateDiagnostic, error) {
	if m == nil {
		return LocalStateDiagnostic{}, errors.New("workspace manager is unavailable")
	}
	requested := strings.TrimSpace(id)
	m.mu.RLock()
	loaded := m.loaded
	canonical := m.canonicalIDLocked(requested)
	item, ok := m.items[canonical]
	owned := m.runtime != nil && m.runtime.locks[canonical] != nil
	m.mu.RUnlock()
	if !loaded {
		var err error
		item, err = m.diagnosticRegistryWorkspace(requested)
		if err != nil {
			return LocalStateDiagnostic{}, err
		}
		ok = true
		owned = false
	}
	if !ok {
		return LocalStateDiagnostic{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return diagnoseWorkspaceLocalState(ctx, item, owned), nil
}

func (m *Manager) diagnosticRegistryWorkspace(requested string) (Workspace, error) {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return Workspace{}, fmt.Errorf("%w: %s", ErrNotFound, requested)
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("read workspace registry for diagnostics: %w", err)
	}
	var stored storeFile
	if err := configformat.Unmarshal(configformat.JSON, data, &stored); err != nil {
		return Workspace{}, fmt.Errorf("decode workspace registry for diagnostics: %w", err)
	}
	if stored.Version < 1 || stored.Version > storeVersion {
		return Workspace{}, fmt.Errorf("unsupported workspace registry version: %d", stored.Version)
	}
	for _, item := range stored.Workspaces {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Path) == "" {
			return Workspace{}, errors.New("workspace registry contains invalid entry")
		}
		if stored.Version < 5 {
			previousID := item.ID
			item.ID = workspaceID(item.Path)
			item.LegacyIDs = appendUniqueString(item.LegacyIDs, previousID)
		}
		item.LegacyIDs = normalizeIDs(item.LegacyIDs, item.ID)
		if requested == item.ID || containsString(item.LegacyIDs, requested) {
			return item, nil
		}
	}
	return Workspace{}, fmt.Errorf("%w: %s", ErrNotFound, requested)
}

func diagnoseWorkspaceLocalState(ctx context.Context, item Workspace, owned bool) LocalStateDiagnostic {
	local := workspacestate.New(item.Path)
	result := LocalStateDiagnostic{
		WorkspaceID: item.ID, Root: item.Path, LocalRoot: local.Root(),
		Health: LocalStateHealthy, Available: true, LockPath: local.RuntimeLockPath(),
	}
	root, err := canonicalExistingDirectory(item.Path)
	if err != nil || !sameCanonicalRoot(root, item.Path) {
		result.Health = LocalStateUnavailable
		result.Available = false
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Error = fmt.Sprintf("workspace root identity changed: registered %s, resolves to %s", item.Path, root)
		}
		return result
	}

	identity, err := local.LoadIdentity()
	if err != nil {
		result.Health = LocalStateCorrupt
		result.Error = err.Error()
		result.GitHygiene = InspectLocalStateGitHygiene(ctx, item.Path)
		return result
	}
	if identity.ID != item.ID {
		result.Health = LocalStateConflict
		result.Error = fmt.Sprintf("workspace identity mismatch: local %s, expected %s", identity.ID, item.ID)
		result.GitHygiene = InspectLocalStateGitHygiene(ctx, item.Path)
		return result
	}
	result.SizeBytes, result.FileCount, err = inspectLocalStateSize(local.Root())
	if err != nil {
		result.Health = LocalStateCorrupt
		result.Error = err.Error()
	}
	result.GitHygiene = InspectLocalStateGitHygiene(ctx, item.Path)
	inspectDiagnosticRuntimeLock(&result, owned)
	return result
}

func inspectLocalStateSize(root string) (int64, int, error) {
	var bytes int64
	files := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace local state contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace local state contains non-regular file: %s", path)
		}
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files, err
}

func inspectDiagnosticRuntimeLock(result *LocalStateDiagnostic, owned bool) {
	if result == nil || result.LockPath == "" {
		return
	}
	data, err := os.ReadFile(result.LockPath)
	if err == nil {
		var metadata runtimeLockMetadata
		if decodeErr := json.Unmarshal(data, &metadata); decodeErr != nil {
			result.Health = LocalStateCorrupt
			result.Error = appendDiagnosticError(result.Error, "runtime lock metadata is invalid")
		} else {
			result.LockPID = metadata.PID
			result.LockInstanceID = metadata.InstanceID
			result.LockStartedAt = metadata.StartedAt
			if metadata.WorkspaceID != "" && metadata.WorkspaceID != result.WorkspaceID {
				result.Health = LocalStateConflict
				result.Error = appendDiagnosticError(result.Error, "runtime lock belongs to another workspace")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		result.Health = LocalStateCorrupt
		result.Error = appendDiagnosticError(result.Error, "runtime lock cannot be read")
	}
	if owned {
		result.Locked = true
		return
	}
	if _, err := os.Stat(result.LockPath); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		result.Health = LocalStateCorrupt
		result.Error = appendDiagnosticError(result.Error, "runtime lock cannot be inspected")
		return
	}
	lock, ok, err := oslock.TryAcquireExisting(result.LockPath, oslock.Exclusive)
	if err != nil {
		result.Health = LocalStateCorrupt
		result.Error = appendDiagnosticError(result.Error, "runtime lock probe failed")
		return
	}
	if ok {
		_ = lock.Release()
		return
	}
	result.Locked = true
}

func appendDiagnosticError(current, next string) string {
	current, next = strings.TrimSpace(current), strings.TrimSpace(next)
	if current == "" {
		return next
	}
	if next == "" || strings.Contains(current, next) {
		return current
	}
	return current + "; " + next
}
