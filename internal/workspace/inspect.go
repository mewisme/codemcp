package workspace

import (
	"errors"
	"os"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
)

type RegistryInspection struct {
	Exists         bool
	Version        int
	Workspaces     []Workspace
	Containers     int
	ContainerItems []WorkspaceContainer
}

func RegistryVersion() int { return storeVersion }

func (m *Manager) InspectRegistry() (RegistryInspection, error) {
	if m == nil {
		return RegistryInspection{}, errors.New("workspace manager is unavailable")
	}
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return RegistryInspection{Workspaces: []Workspace{}}, nil
	}
	if err != nil {
		return RegistryInspection{}, err
	}
	var stored storeFile
	if err := configformat.Unmarshal(configformat.JSON, data, &stored); err != nil {
		return RegistryInspection{}, err
	}
	if stored.Version < 1 || stored.Version > storeVersion {
		return RegistryInspection{}, errors.New("unsupported workspace registry version")
	}
	workspaces := make([]Workspace, 0, len(stored.Workspaces))
	idMap := map[string]string{}
	for _, item := range stored.Workspaces {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Path) == "" {
			return RegistryInspection{}, errors.New("workspace registry contains invalid entry")
		}
		if stored.Version < 5 {
			previousID := item.ID
			item.ID = workspaceID(item.Path)
			item.LegacyIDs = appendUniqueString(item.LegacyIDs, previousID)
			idMap[previousID] = item.ID
		}
		idMap[item.ID] = item.ID
		item.LegacyIDs = normalizeIDs(item.LegacyIDs, item.ID)
		workspaces = append(workspaces, item)
	}
	containers := make([]WorkspaceContainer, 0, len(stored.Containers))
	for _, container := range stored.Containers {
		container.ID = strings.TrimSpace(container.ID)
		container.Name = strings.TrimSpace(container.Name)
		if container.ID == "" || container.Name == "" {
			return RegistryInspection{}, errors.New("workspace registry contains invalid container entry")
		}
		ids := make([]string, 0, len(container.WorkspaceIDs))
		for _, id := range container.WorkspaceIDs {
			id = strings.TrimSpace(id)
			if mapped := idMap[id]; mapped != "" {
				id = mapped
			}
			if id != "" {
				ids = appendUniqueString(ids, id)
			}
		}
		sort.Strings(ids)
		container.WorkspaceIDs = ids
		containers = append(containers, container)
	}
	return RegistryInspection{
		Exists:         true,
		Version:        stored.Version,
		Workspaces:     workspaces,
		Containers:     len(stored.Containers),
		ContainerItems: containers,
	}, nil
}

func WriteRegistrySnapshot(path string, workspaces []Workspace, containers []WorkspaceContainer) error {
	items := make([]Workspace, 0, len(workspaces))
	seen := map[string]bool{}
	for _, item := range workspaces {
		item.ID = strings.TrimSpace(item.ID)
		item.Path = strings.TrimSpace(item.Path)
		if item.ID == "" || item.Path == "" {
			return errors.New("workspace registry contains invalid entry")
		}
		if seen[item.ID] {
			return errors.New("workspace registry contains duplicate workspace id")
		}
		seen[item.ID] = true
		item.Error = ""
		item.LegacyIDs = normalizeIDs(item.LegacyIDs, item.ID)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	containerItems := make([]WorkspaceContainer, 0, len(containers))
	for _, container := range containers {
		container.ID = strings.TrimSpace(container.ID)
		container.Name = strings.TrimSpace(container.Name)
		if container.ID == "" || container.Name == "" {
			return errors.New("workspace registry contains invalid container entry")
		}
		ids := make([]string, 0, len(container.WorkspaceIDs))
		for _, id := range container.WorkspaceIDs {
			id = strings.TrimSpace(id)
			if id == "" || !seen[id] {
				continue
			}
			ids = appendUniqueString(ids, id)
		}
		sort.Strings(ids)
		container.WorkspaceIDs = ids
		containerItems = append(containerItems, container)
	}
	sort.Slice(containerItems, func(i, j int) bool {
		if strings.EqualFold(containerItems[i].Name, containerItems[j].Name) {
			return containerItems[i].ID < containerItems[j].ID
		}
		return strings.ToLower(containerItems[i].Name) < strings.ToLower(containerItems[j].Name)
	})
	data, err := state.MarshalJSON(storeFile{Version: storeVersion, Workspaces: items, Containers: containerItems})
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(path, data, 0600)
}
