package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/oslock"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

var relocateRegistrySave = func(manager *Manager) error { return manager.saveLocked() }

func (m *Manager) Relocate(id, path string) (Workspace, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.relocate", "Relocating workspace", tracepkg.String("workspace_id", strings.TrimSpace(id)), tracepkg.String("input_path", path))
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	mutation, err := m.beginRegistryMutation()
	if err != nil {
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}
	defer mutation.Release()

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
	if m.workspaceLocalRootAliasesProtected(root) {
		err := fmt.Errorf("workspace local .cm root aliases protected global state: %s", root)
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}

	m.mu.RLock()
	canonical := m.canonicalIDLocked(strings.TrimSpace(id))
	current, found := m.items[canonical]
	owned := found && m.runtime != nil && m.runtime.active && m.runtime.locks[canonical] != nil
	m.mu.RUnlock()
	if found && !owned && current.Available() {
		if err := probeWorkspaceRuntimeLock(current); err != nil {
			span.FailMessage("Workspace relocation failed", err)
			return Workspace{}, err
		}
	}

	local := workspacestate.New(root)
	identity, err := local.LoadIdentity()
	if err != nil {
		err = fmt.Errorf("workspace relocation destination must contain an existing CodeMCP identity: %w", err)
		span.FailMessage("Workspace relocation failed", err, tracepkg.String("root", root))
		return Workspace{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	canonical = m.canonicalIDLocked(strings.TrimSpace(id))
	item, ok := m.items[canonical]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, id)
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}
	if identity.ID != item.ID {
		err := fmt.Errorf("workspace identity mismatch at destination: found %s, expected %s", identity.ID, item.ID)
		span.FailMessage("Workspace relocation failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("root", root))
		return Workspace{}, err
	}

	oldRoot := item.Path
	sameRoot := sameCanonicalRoot(oldRoot, root)
	for _, existing := range m.items {
		if existing.ID != item.ID && sameCanonicalRoot(existing.Path, root) {
			err := fmt.Errorf("workspace already registered for destination: %s", root)
			span.FailMessage("Workspace relocation failed", err, tracepkg.String("destination_workspace_id", existing.ID))
			return Workspace{}, err
		}
	}
	if !sameRoot {
		if sourceIdentity, sourceErr := workspacestate.New(oldRoot).LoadIdentity(); sourceErr == nil && sourceIdentity.ID == item.ID {
			err := &DuplicateWorkspaceIdentityError{
				WorkspaceID:     item.ID,
				RegisteredRoot:  oldRoot,
				DestinationRoot: root,
			}
			span.FailMessage("Workspace relocation failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("source_root", oldRoot), tracepkg.String("root", root))
			return Workspace{}, err
		}
	}

	active := m.runtime != nil && m.runtime.active
	if active {
		lock := m.runtime.locks[item.ID]
		if lock == nil {
			err := fmt.Errorf("workspace runtime lock missing during relocation: %s", item.ID)
			span.FailMessage("Workspace relocation failed", err)
			return Workspace{}, err
		}
		same, sameErr := lock.SameFile(local.RuntimeLockPath())
		if sameErr != nil {
			err := fmt.Errorf("verify workspace runtime lock during relocation: %w", sameErr)
			span.FailMessage("Workspace relocation failed", err)
			return Workspace{}, err
		}
		if !same {
			err := fmt.Errorf("active workspace relocation requires existing %s state to move with the workspace: %s", LocalDirName, item.ID)
			span.FailMessage("Workspace relocation failed", err)
			return Workspace{}, err
		}
	} else {
		lock, acquired, lockErr := oslock.TryAcquire(local.RuntimeLockPath(), oslock.Exclusive)
		if lockErr != nil {
			span.FailMessage("Workspace relocation failed", lockErr)
			return Workspace{}, lockErr
		}
		if !acquired {
			err := fmt.Errorf("%w: %s", ErrAlreadyActive, item.ID)
			span.FailMessage("Workspace relocation failed", err)
			return Workspace{}, err
		}
		defer lock.Release()
	}

	previous := item
	item.Path = root
	item.AllowDirs = append([]string(nil), item.AllowDirs...)
	for index, allowDir := range item.AllowDirs {
		if relocated, ok := relocateAbsolutePath(allowDir, oldRoot, root); ok {
			item.AllowDirs[index] = relocated
		}
	}
	item.AllowDirs = normalizeRoots(item.AllowDirs)
	item.Error = ""
	m.items[item.ID] = item
	if err := relocateRegistrySave(m); err != nil {
		m.items[item.ID] = previous
		span.FailMessage("Workspace relocation failed", err)
		return Workspace{}, err
	}
	if active {
		m.runtime.roots[item.ID] = root
	}
	span.EndMessage("Workspace relocated", tracepkg.String("workspace_id", item.ID), tracepkg.String("previous_root", oldRoot), tracepkg.String("root", root), tracepkg.Bool("changed", !sameRoot))
	return item, nil
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
