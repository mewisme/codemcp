package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var relocateStateRename = os.Rename

func (m *Manager) Relocate(id, path string) (Workspace, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.relocate", "Relocating workspace", tracepkg.String("workspace_id", strings.TrimSpace(id)), tracepkg.String("input_path", path))
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}
	root, err := canonicalExistingDirectory(path)
	if err != nil {
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}
	if m.protected(root) {
		err := fmt.Errorf("workspace root is inside protected control-plane state: %s", root)
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	oldID := m.canonicalIDLocked(strings.TrimSpace(id))
	item, ok := m.items[oldID]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, id)
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}
	oldRoot := item.Path
	newID := workspaceID(root)
	if filepath.Clean(oldRoot) == filepath.Clean(root) && oldID == newID {
		span.EndMessage("Workspace already uses requested root", tracepkg.String("workspace_id", oldID), tracepkg.String("root", root), tracepkg.Bool("changed", false))
		return item, nil
	}
	if existing, exists := m.items[newID]; exists && existing.ID != oldID {
		err := fmt.Errorf("workspace already registered for destination: %s", root)
		span.FailMessage("Workspace relocation failed", err, tracepkg.String("destination_workspace_id", newID))
		return Workspace{}, err
	}
	if target := m.aliases[newID]; target != "" && target != oldID {
		err := fmt.Errorf("workspace destination is reserved by legacy workspace id: %s", newID)
		span.FailMessage("Workspace relocation failed", err, tracepkg.String("destination_workspace_id", newID), tracepkg.String("alias_target", target))
		return Workspace{}, err
	}

	previousItems := cloneWorkspaceItems(m.items)
	previousContainers := cloneWorkspaceContainers(m.containers)
	previousAliases := cloneAliases(m.aliases)

	stateMoved, err := m.migrateRelocatedWorkspaceState(oldID, newID, oldRoot, root)
	if err != nil {
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}

	delete(m.items, oldID)
	item.ID = newID
	item.Path = root
	for index, allowDir := range item.AllowDirs {
		if relocated, ok := relocateAbsolutePath(allowDir, oldRoot, root); ok {
			item.AllowDirs[index] = relocated
		}
	}
	item.AllowDirs = normalizeRoots(item.AllowDirs)
	item.LegacyIDs = normalizeIDs(append(item.LegacyIDs, oldID), newID)
	m.items[newID] = item
	for containerID, container := range m.containers {
		for index, workspaceID := range container.WorkspaceIDs {
			if workspaceID == oldID {
				container.WorkspaceIDs[index] = newID
			}
		}
		container.WorkspaceIDs = normalizeContainerWorkspaceIDs(container.WorkspaceIDs, m.items)
		m.containers[containerID] = container
	}
	for alias, target := range m.aliases {
		if target == oldID || alias == newID {
			delete(m.aliases, alias)
		}
	}
	for _, alias := range item.LegacyIDs {
		if err := m.registerAliasLocked(alias, newID); err != nil {
			m.items, m.containers, m.aliases = previousItems, previousContainers, previousAliases
			if stateMoved {
				_, _ = m.migrateRelocatedWorkspaceState(newID, oldID, root, oldRoot)
			}
			span.FailMessage("Workspace relocation failed", err)
			return Workspace{}, err
		}
	}
	if err := m.saveLocked(); err != nil {
		m.items, m.containers, m.aliases = previousItems, previousContainers, previousAliases
		if stateMoved {
			_, _ = m.migrateRelocatedWorkspaceState(newID, oldID, root, oldRoot)
		}
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}
	span.EndMessage("Workspace relocated", tracepkg.String("legacy_workspace_id", oldID), tracepkg.String("workspace_id", newID), tracepkg.String("previous_root", oldRoot), tracepkg.String("root", root), tracepkg.Bool("state_migrated", stateMoved))
	return item, nil
}

func (m *Manager) migrateRelocatedWorkspaceState(oldID, newID, oldRoot, newRoot string) (bool, error) {
	if oldID == newID {
		return false, nil
	}
	root := filepath.Join(filepath.Dir(m.path), "workspaces")
	oldState := filepath.Join(root, oldID)
	newState := filepath.Join(root, newID)
	if _, err := os.Stat(oldState); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect workspace state: %w", err)
	}
	if _, err := os.Stat(newState); err == nil {
		return false, fmt.Errorf("cannot relocate workspace state: destination already exists: %s", newState)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect destination workspace state: %w", err)
	}
	if _, err := rewriteRelocatedWorkspaceState(oldState, oldID, newID, oldRoot, newRoot); err != nil {
		return false, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return false, err
	}
	if err := relocateStateRename(oldState, newState); err != nil {
		_, rollbackErr := rewriteRelocatedWorkspaceState(oldState, newID, oldID, newRoot, oldRoot)
		if rollbackErr != nil {
			return false, fmt.Errorf("relocate workspace state %s -> %s: %w; rollback state rewrite: %v", oldID, newID, err, rollbackErr)
		}
		return false, fmt.Errorf("relocate workspace state %s -> %s: %w", oldID, newID, err)
	}
	return true, nil
}

func rewriteRelocatedWorkspaceState(root, oldID, newID, oldRoot, newRoot string) (int, error) {
	paths := []string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	rewritten := 0
	for _, path := range paths {
		if !strings.EqualFold(filepath.Ext(path), ".json") {
			continue
		}
		data, err := os.ReadFile(path) // #nosec G304 -- path is emitted by filepath.WalkDir under the workspace-owned state root.
		if err != nil {
			return rewritten, err
		}
		decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
		if err != nil {
			if isCheckpointManifestPath(path) {
				continue
			}
			return rewritten, fmt.Errorf("decode workspace state %s: %w", path, err)
		}
		updated, changed := relocateStateValue(decoded, oldID, newID, oldRoot, newRoot)
		if !changed {
			continue
		}
		encoded, err := configformat.EncodeGeneric(configformat.JSON, updated)
		if err != nil {
			return rewritten, fmt.Errorf("encode workspace state %s: %w", path, err)
		}
		if err := state.WriteFileAtomic(path, encoded, 0600); err != nil {
			return rewritten, fmt.Errorf("rewrite workspace state %s: %w", path, err)
		}
		rewritten++
	}
	return rewritten, nil
}

func isCheckpointManifestPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return strings.Contains(clean, "/checkpoints/data/") && filepath.Base(clean) == "manifest.json"
}

func relocateStateValue(value any, oldID, newID, oldRoot, newRoot string) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if key == "workspace_id" {
				if text, ok := item.(string); ok && text == oldID {
					result[key], changed = newID, true
					continue
				}
			}
			updated, itemChanged := relocateStateValue(item, oldID, newID, oldRoot, newRoot)
			result[key] = updated
			changed = changed || itemChanged
		}
		return result, changed
	case []any:
		changed := false
		result := make([]any, len(typed))
		for index, item := range typed {
			updated, itemChanged := relocateStateValue(item, oldID, newID, oldRoot, newRoot)
			result[index] = updated
			changed = changed || itemChanged
		}
		return result, changed
	case string:
		if relocated, ok := relocateAbsolutePath(typed, oldRoot, newRoot); ok {
			return relocated, true
		}
	}
	return value, false
}

func relocateAbsolutePath(value, oldRoot, newRoot string) (string, bool) {
	if !filepath.IsAbs(value) {
		return value, false
	}
	clean := filepath.Clean(value)
	comparisonValue := clean
	if canonical, err := canonicalForContainment(clean, false); err == nil {
		comparisonValue = canonical
	}
	comparisonOldRoot := filepath.Clean(oldRoot)
	if canonical, err := canonicalForContainment(comparisonOldRoot, false); err == nil {
		comparisonOldRoot = canonical
	}
	relative, err := filepath.Rel(comparisonOldRoot, comparisonValue)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return value, false
	}
	if relative == "." {
		return filepath.Clean(newRoot), true
	}
	return filepath.Join(newRoot, relative), true
}

func cloneWorkspaceItems(values map[string]Workspace) map[string]Workspace {
	result := make(map[string]Workspace, len(values))
	for id, item := range values {
		item.AllowDirs = append([]string(nil), item.AllowDirs...)
		item.LegacyIDs = append([]string(nil), item.LegacyIDs...)
		result[id] = item
	}
	return result
}

func cloneWorkspaceContainers(values map[string]WorkspaceContainer) map[string]WorkspaceContainer {
	result := make(map[string]WorkspaceContainer, len(values))
	for id, item := range values {
		item.WorkspaceIDs = append([]string(nil), item.WorkspaceIDs...)
		result[id] = item
	}
	return result
}

func cloneAliases(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for alias, target := range values {
		result[alias] = target
	}
	return result
}
