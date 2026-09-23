package workspace

import (
	"fmt"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func (m *Manager) Unregister(id string) error {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.unregister", "Unregistering workspace", tracepkg.String("workspace_id", id))
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Workspace unregistration failed", err)
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	canonical := m.canonicalIDLocked(id)
	item, ok := m.items[canonical]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, id)
		span.FailMessage("Workspace unregistration failed", err)
		return err
	}
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
		span.FailMessage("Workspace unregistration failed", err, tracepkg.String("canonical_workspace_id", canonical), tracepkg.Int("containers_updated", len(previousContainers)), tracepkg.Int("aliases_removed", len(removedAliases)))
		return err
	}
	span.EndMessage("Workspace unregistered", tracepkg.String("canonical_workspace_id", canonical), tracepkg.String("root", item.Path), tracepkg.Int("containers_updated", len(previousContainers)), tracepkg.Int("aliases_removed", len(removedAliases)), tracepkg.Bool("project_files_removed", false))
	return nil
}
