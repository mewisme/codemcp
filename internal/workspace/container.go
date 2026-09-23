package workspace

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var ErrContainerNotFound = errors.New("workspace container not found")

type ContainerContext struct {
	Container  WorkspaceContainer
	Workspaces []Workspace
}

func (m *Manager) CreateContainer(name string) (WorkspaceContainer, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.container.create", "Creating workspace container", tracepkg.String("name", strings.TrimSpace(name)))
	name = strings.TrimSpace(name)
	if name == "" {
		err := errors.New("workspace container name is required")
		span.FailMessage("Workspace container creation failed", err)
		return WorkspaceContainer{}, err
	}
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Workspace container creation failed", err)
		return WorkspaceContainer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for attempts := 0; attempts < 8; attempts++ {
		id, err := workspaceContainerID()
		if err != nil {
			span.FailMessage("Workspace container creation failed", err, tracepkg.Int("allocation_attempts", attempts+1))
			return WorkspaceContainer{}, err
		}
		if _, exists := m.containers[id]; exists {
			continue
		}
		container := WorkspaceContainer{ID: id, Name: name, WorkspaceIDs: []string{}}
		m.containers[id] = container
		if err := m.saveLocked(); err != nil {
			delete(m.containers, id)
			span.FailMessage("Workspace container creation failed", err, tracepkg.String("container_id", id), tracepkg.Int("allocation_attempts", attempts+1))
			return WorkspaceContainer{}, err
		}
		span.EndMessage("Workspace container created", tracepkg.String("container_id", id), tracepkg.String("name", name), tracepkg.Int("workspace_count", 0), tracepkg.Int("allocation_attempts", attempts+1))
		return container, nil
	}
	err := errors.New("failed to allocate unique workspace container id")
	span.FailMessage("Workspace container creation failed", err, tracepkg.Int("allocation_attempts", 8))
	return WorkspaceContainer{}, err
}

func (m *Manager) GetContainer(id string) (WorkspaceContainer, error) {
	if err := m.ensureLoaded(); err != nil {
		return WorkspaceContainer{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	container, ok := m.containers[strings.TrimSpace(id)]
	if !ok {
		return WorkspaceContainer{}, fmt.Errorf("%w: %s", ErrContainerNotFound, id)
	}
	container.WorkspaceIDs = append([]string(nil), container.WorkspaceIDs...)
	return container, nil
}

func (m *Manager) ResolveContainer(id string) (ContainerContext, error) {
	if err := m.ensureLoaded(); err != nil {
		return ContainerContext{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	id = strings.TrimSpace(id)
	container, ok := m.containers[id]
	if !ok {
		return ContainerContext{}, fmt.Errorf("%w: %s", ErrContainerNotFound, id)
	}
	container.WorkspaceIDs = append([]string(nil), container.WorkspaceIDs...)
	values := make([]Workspace, 0, len(container.WorkspaceIDs))
	for _, workspaceID := range container.WorkspaceIDs {
		item, exists := m.items[workspaceID]
		if !exists {
			continue
		}
		item.AllowDirs = append([]string(nil), item.AllowDirs...)
		item.LegacyIDs = append([]string(nil), item.LegacyIDs...)
		values = append(values, item)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Path == values[j].Path {
			return values[i].ID < values[j].ID
		}
		return values[i].Path < values[j].Path
	})
	return ContainerContext{Container: container, Workspaces: values}, nil
}

func (m *Manager) ListContainers() ([]WorkspaceContainer, error) {
	if err := m.ensureLoaded(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	values := make([]WorkspaceContainer, 0, len(m.containers))
	for _, container := range m.containers {
		container.WorkspaceIDs = append([]string(nil), container.WorkspaceIDs...)
		values = append(values, container)
	}
	sort.Slice(values, func(i, j int) bool {
		left, right := strings.ToLower(values[i].Name), strings.ToLower(values[j].Name)
		if left == right {
			return values[i].ID < values[j].ID
		}
		return left < right
	})
	return values, nil
}

func (m *Manager) RenameContainer(id, name string) (WorkspaceContainer, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.container.rename", "Renaming workspace container", tracepkg.String("container_id", strings.TrimSpace(id)), tracepkg.String("name", strings.TrimSpace(name)))
	name = strings.TrimSpace(name)
	if name == "" {
		err := errors.New("workspace container name is required")
		span.FailMessage("Workspace container rename failed", err)
		return WorkspaceContainer{}, err
	}
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Workspace container rename failed", err)
		return WorkspaceContainer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id = strings.TrimSpace(id)
	container, ok := m.containers[id]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrContainerNotFound, id)
		span.FailMessage("Workspace container rename failed", err)
		return WorkspaceContainer{}, err
	}
	previous := container
	container.Name = name
	m.containers[id] = container
	if err := m.saveLocked(); err != nil {
		m.containers[id] = previous
		span.FailMessage("Workspace container rename failed", err, tracepkg.String("previous_name", previous.Name))
		return WorkspaceContainer{}, err
	}
	span.EndMessage("Workspace container renamed", tracepkg.String("container_id", id), tracepkg.String("previous_name", previous.Name), tracepkg.String("name", container.Name), tracepkg.Int("workspace_count", len(container.WorkspaceIDs)))
	return container, nil
}

func (m *Manager) DeleteContainer(id string) error {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.container.delete", "Deleting workspace container", tracepkg.String("container_id", strings.TrimSpace(id)))
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Workspace container deletion failed", err)
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id = strings.TrimSpace(id)
	container, ok := m.containers[id]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrContainerNotFound, id)
		span.FailMessage("Workspace container deletion failed", err)
		return err
	}
	delete(m.containers, id)
	if err := m.saveLocked(); err != nil {
		m.containers[id] = container
		span.FailMessage("Workspace container deletion failed", err, tracepkg.String("name", container.Name), tracepkg.Int("workspace_count", len(container.WorkspaceIDs)))
		return err
	}
	span.EndMessage("Workspace container deleted", tracepkg.String("container_id", id), tracepkg.String("name", container.Name), tracepkg.Int("workspace_count", len(container.WorkspaceIDs)))
	return nil
}

func (m *Manager) AddWorkspaceToContainer(containerID, workspaceID string) (WorkspaceContainer, error) {
	return m.updateWorkspaceContainerMembership(containerID, []string{workspaceID}, true)
}

func (m *Manager) RemoveWorkspaceFromContainer(containerID, workspaceID string) (WorkspaceContainer, error) {
	return m.updateWorkspaceContainerMembership(containerID, []string{workspaceID}, false)
}

func (m *Manager) AddWorkspacesToContainer(containerID string, workspaceIDs []string) (WorkspaceContainer, error) {
	return m.updateWorkspaceContainerMembership(containerID, workspaceIDs, true)
}

func (m *Manager) RemoveWorkspacesFromContainer(containerID string, workspaceIDs []string) (WorkspaceContainer, error) {
	return m.updateWorkspaceContainerMembership(containerID, workspaceIDs, false)
}

func (m *Manager) AddWorkspaceToContainers(workspaceID string, containerIDs []string) ([]WorkspaceContainer, error) {
	return m.updateWorkspaceContainersForWorkspace(workspaceID, containerIDs, true)
}

func (m *Manager) RemoveWorkspaceFromContainers(workspaceID string, containerIDs []string) ([]WorkspaceContainer, error) {
	return m.updateWorkspaceContainersForWorkspace(workspaceID, containerIDs, false)
}

func (m *Manager) ContainersForWorkspace(workspaceID string) ([]WorkspaceContainer, error) {
	if err := m.ensureLoaded(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	canonical := m.canonicalIDLocked(strings.TrimSpace(workspaceID))
	if _, ok := m.items[canonical]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, workspaceID)
	}
	values := []WorkspaceContainer{}
	for _, container := range m.containers {
		if containsString(container.WorkspaceIDs, canonical) {
			container.WorkspaceIDs = append([]string(nil), container.WorkspaceIDs...)
			values = append(values, container)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		left, right := strings.ToLower(values[i].Name), strings.ToLower(values[j].Name)
		if left == right {
			return values[i].ID < values[j].ID
		}
		return left < right
	})
	return values, nil
}

func (m *Manager) WorkspacesForContainer(containerID string) ([]Workspace, error) {
	if err := m.ensureLoaded(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	container, ok := m.containers[strings.TrimSpace(containerID)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrContainerNotFound, containerID)
	}
	values := make([]Workspace, 0, len(container.WorkspaceIDs))
	for _, id := range container.WorkspaceIDs {
		if item, exists := m.items[id]; exists {
			values = append(values, item)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Path < values[j].Path })
	return values, nil
}

func (m *Manager) updateWorkspaceContainerMembership(containerID string, workspaceIDs []string, add bool) (WorkspaceContainer, error) {
	action := "remove"
	if add {
		action = "add"
	}
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.container.membership", "Updating workspace container membership", tracepkg.String("container_id", strings.TrimSpace(containerID)), tracepkg.String("action", action), tracepkg.Int("requested_workspaces", len(workspaceIDs)))
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Workspace container membership update failed", err)
		return WorkspaceContainer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	containerID = strings.TrimSpace(containerID)
	container, ok := m.containers[containerID]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrContainerNotFound, containerID)
		span.FailMessage("Workspace container membership update failed", err)
		return WorkspaceContainer{}, err
	}
	canonical, err := m.validateWorkspaceIDsLocked(workspaceIDs)
	if err != nil {
		span.FailMessage("Workspace container membership update failed", err)
		return WorkspaceContainer{}, err
	}
	previous := append([]string(nil), container.WorkspaceIDs...)
	if add {
		container.WorkspaceIDs = append(container.WorkspaceIDs, canonical...)
	} else {
		container.WorkspaceIDs = removeStrings(container.WorkspaceIDs, canonical)
	}
	container.WorkspaceIDs = normalizeContainerWorkspaceIDs(container.WorkspaceIDs, m.items)
	m.containers[containerID] = container
	if err := m.saveLocked(); err != nil {
		container.WorkspaceIDs = previous
		m.containers[containerID] = container
		span.FailMessage("Workspace container membership update failed", err, tracepkg.Int("previous_count", len(previous)), tracepkg.Int("candidate_count", len(container.WorkspaceIDs)))
		return WorkspaceContainer{}, err
	}
	span.EndMessage("Workspace container membership updated", tracepkg.String("container_id", containerID), tracepkg.String("action", action), tracepkg.Int("validated_workspaces", len(canonical)), tracepkg.Int("previous_count", len(previous)), tracepkg.Int("count", len(container.WorkspaceIDs)))
	return container, nil
}

func (m *Manager) updateWorkspaceContainersForWorkspace(workspaceID string, containerIDs []string, add bool) ([]WorkspaceContainer, error) {
	action := "remove"
	if add {
		action = "add"
	}
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.containers.membership", "Updating workspace membership across containers", tracepkg.String("workspace_id", strings.TrimSpace(workspaceID)), tracepkg.String("action", action), tracepkg.Int("requested_containers", len(containerIDs)))
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Workspace container membership update failed", err)
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	canonical := m.canonicalIDLocked(strings.TrimSpace(workspaceID))
	if _, ok := m.items[canonical]; !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, workspaceID)
		span.FailMessage("Workspace container membership update failed", err)
		return nil, err
	}
	ids := normalizeIDs(containerIDs, "")
	for _, id := range ids {
		if _, ok := m.containers[id]; !ok {
			err := fmt.Errorf("%w: %s", ErrContainerNotFound, id)
			span.FailMessage("Workspace container membership update failed", err)
			return nil, err
		}
	}
	previous := make(map[string]WorkspaceContainer, len(ids))
	for _, id := range ids {
		container := m.containers[id]
		previous[id] = container
		if add {
			container.WorkspaceIDs = append(container.WorkspaceIDs, canonical)
		} else {
			container.WorkspaceIDs = removeStrings(container.WorkspaceIDs, []string{canonical})
		}
		container.WorkspaceIDs = normalizeContainerWorkspaceIDs(container.WorkspaceIDs, m.items)
		m.containers[id] = container
	}
	if err := m.saveLocked(); err != nil {
		for id, container := range previous {
			m.containers[id] = container
		}
		span.FailMessage("Workspace container membership update failed", err, tracepkg.Int("validated_containers", len(ids)))
		return nil, err
	}
	result := make([]WorkspaceContainer, 0, len(ids))
	for _, id := range ids {
		result = append(result, m.containers[id])
	}
	span.EndMessage("Workspace container membership updated", tracepkg.String("workspace_id", canonical), tracepkg.String("action", action), tracepkg.Int("containers", len(ids)))
	return result, nil
}

func (m *Manager) validateWorkspaceIDsLocked(ids []string) ([]string, error) {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		canonical := m.canonicalIDLocked(strings.TrimSpace(id))
		if canonical == "" {
			continue
		}
		if _, ok := m.items[canonical]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		result = appendUniqueString(result, canonical)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeContainerWorkspaceIDs(ids []string, workspaces map[string]Workspace) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := workspaces[id]; !ok {
			continue
		}
		result = appendUniqueString(result, id)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return []string{}
	}
	return result
}

func removeStrings(values, removals []string) []string {
	if len(values) == 0 || len(removals) == 0 {
		return append([]string(nil), values...)
	}
	remove := make(map[string]struct{}, len(removals))
	for _, value := range removals {
		remove[value] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := remove[value]; !ok {
			result = append(result, value)
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
