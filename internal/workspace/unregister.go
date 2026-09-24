package workspace

import (
	"fmt"

	"go.mewis.me/codemcp/internal/oslock"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func (m *Manager) Unregister(id string) error {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.unregister", "Unregistering workspace", tracepkg.String("workspace_id", id))
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
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
	if owned {
		if err := m.validateRuntimeOwnership(canonical); err != nil {
			span.FailMessage("Workspace unregistration failed", err)
			return err
		}
	} else if err := probeWorkspaceRuntimeLock(item); err != nil {
		span.FailMessage("Workspace unregistration failed", err)
		return err
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
	span.EndMessage("Workspace unregistered", tracepkg.String("canonical_workspace_id", canonical), tracepkg.String("root", item.Path), tracepkg.Int("containers_updated", len(previousContainers)), tracepkg.Int("aliases_removed", len(removedAliases)), tracepkg.Bool("project_files_removed", false))
	return nil
}
