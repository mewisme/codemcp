package workspace

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/oslock"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

var ErrPurgeNotConfirmed = errors.New("workspace purge requires explicit confirmation")

func (m *Manager) Unregister(id string) error {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.unregister", "Unregistering workspace", tracepkg.String("workspace_id", id))
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	if err := m.validateOwnedWorkspaceBeforeMutation(id); err != nil {
		span.FailMessage("Workspace unregistration failed", err)
		return err
	}
	mutation, err := m.beginRegistryMutation()
	if err != nil {
		span.FailMessage("Workspace unregistration failed", err)
		return err
	}
	defer mutation.Release()

	m.mu.RLock()
	canonical := m.canonicalIDLocked(id)
	item, ok := m.items[canonical]
	active := m.runtime != nil && m.runtime.active
	owned := active && m.runtime.locks[canonical] != nil
	m.mu.RUnlock()
	if !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, id)
		span.FailMessage("Workspace unregistration failed", err)
		return err
	}
	if !owned {
		if err := probeWorkspaceRuntimeLock(item); err != nil {
			span.FailMessage("Workspace unregistration failed", err)
			return err
		}
	}

	var released *oslock.Lock
	m.mu.Lock()
	delete(m.items, canonical)
	previousContainers := make(map[string]WorkspaceContainer)
	for containerID, container := range m.containers {
		if !containsString(container.WorkspaceIDs, canonical) {
			continue
		}
		previousContainers[containerID] = container
		container.WorkspaceIDs = removeStrings(container.WorkspaceIDs, []string{canonical})
		m.containers[containerID] = container
	}
	removedAliases := map[string]string{}
	for alias, target := range m.aliases {
		if target == canonical {
			removedAliases[alias] = target
			delete(m.aliases, alias)
		}
	}
	if err := m.saveLocked(); err != nil {
		m.items[canonical] = item
		for containerID, container := range previousContainers {
			m.containers[containerID] = container
		}
		for alias, target := range removedAliases {
			m.aliases[alias] = target
		}
		m.mu.Unlock()
		span.FailMessage("Workspace unregistration failed", err, tracepkg.String("canonical_workspace_id", canonical), tracepkg.Int("containers_updated", len(previousContainers)), tracepkg.Int("aliases_removed", len(removedAliases)))
		return err
	}
	if owned {
		released = m.runtime.locks[canonical]
		delete(m.runtime.locks, canonical)
		delete(m.runtime.roots, canonical)
	}
	m.mu.Unlock()
	if released != nil {
		if err := released.Release(); err != nil {
			span.FailMessage("Workspace unregistered but runtime lock release failed", err, tracepkg.String("canonical_workspace_id", canonical))
			return err
		}
	}
	span.EndMessage("Workspace unregistered", tracepkg.String("canonical_workspace_id", canonical), tracepkg.String("root", item.Path), tracepkg.Int("containers_updated", len(previousContainers)), tracepkg.Int("aliases_removed", len(removedAliases)), tracepkg.Bool("project_files_removed", false), tracepkg.Bool("local_state_removed", false))
	return nil
}

func (m *Manager) DeleteState(target string) (Workspace, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.delete-state", "Deleting workspace local state", tracepkg.String("target", target))
	item, registered, lookupErr := m.lookupWorkspace(target)
	root := ""
	expectedID := ""
	if registered {
		root = item.Path
		expectedID = item.ID
		if err := validateLocalStateShape(root, expectedID); err != nil {
			span.FailMessage("Workspace state deletion failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("root", root))
			return Workspace{}, err
		}
		if err := m.Unregister(item.ID); err != nil {
			span.FailMessage("Workspace state deletion failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("root", root))
			return Workspace{}, err
		}
	} else {
		var err error
		root, err = canonicalExistingDirectory(target)
		if err != nil {
			switch {
			case lookupErr != nil:
				err = lookupErr
			case strings.HasPrefix(strings.TrimSpace(target), "ws_"):
				err = fmt.Errorf("%w: %s", ErrNotFound, target)
			}
			span.FailMessage("Workspace state deletion failed", err, tracepkg.String("target", target))
			return Workspace{}, err
		}
		identity, err := workspacestate.New(root).LoadIdentity()
		if err == nil {
			item.ID = identity.ID
			expectedID = identity.ID
		}
	}

	if err := removeLocalState(root, expectedID); err != nil {
		span.FailMessage("Workspace state deletion failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("root", root), tracepkg.Bool("unregistered", registered))
		return Workspace{}, err
	}
	if item.Path == "" {
		item.Path = root
	}
	item.Error = "workspace local state deleted"
	span.EndMessage("Workspace local state deleted", tracepkg.String("workspace_id", item.ID), tracepkg.String("root", root), tracepkg.Bool("unregistered", registered), tracepkg.Bool("project_files_removed", false), tracepkg.Bool("local_state_removed", true))
	return item, nil
}

func (m *Manager) lookupWorkspace(target string) (Workspace, bool, error) {
	if err := m.ensureLoaded(); err != nil {
		return Workspace{}, false, err
	}
	m.mu.RLock()
	canonical := m.canonicalIDLocked(strings.TrimSpace(target))
	if item, ok := m.items[canonical]; ok {
		m.mu.RUnlock()
		return item, true, nil
	}
	items := make([]Workspace, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	m.mu.RUnlock()

	root, err := canonicalExistingDirectory(target)
	if err != nil {
		return Workspace{}, false, nil
	}
	for _, item := range items {
		if sameCanonicalRoot(item.Path, root) {
			return item, true, nil
		}
	}
	return Workspace{}, false, nil
}

func validateLocalStateShape(root, expectedID string) error {
	local := workspacestate.New(root)
	info, err := os.Lstat(local.Root())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to delete workspace state through a non-directory or symlink: %s", local.Root())
	}
	identity, err := local.LoadIdentity()
	if err != nil {
		return fmt.Errorf("refusing to delete unverified workspace state: %w", err)
	}
	if expectedID != "" && identity.ID != expectedID {
		return fmt.Errorf("workspace identity mismatch before purge: found %s, expected %s", identity.ID, expectedID)
	}
	return nil
}

func removeLocalState(root, expectedID string) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := validateLocalStateShape(root, expectedID); err != nil {
		return err
	}
	identity, err := workspacestate.New(root).LoadIdentity()
	if err == nil {
		if err := probeWorkspaceRuntimeLock(Workspace{ID: identity.ID, Path: root}); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	workspaceRoot, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer workspaceRoot.Close()
	if _, err := workspaceRoot.Lstat(LocalDirName); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := workspaceRoot.RemoveAll(LocalDirName); err != nil {
		return fmt.Errorf("remove workspace local state: %w", err)
	}
	return nil
}
