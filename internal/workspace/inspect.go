package workspace

import (
	"errors"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
)

type RegistryInspection struct {
	Exists     bool
	Version    int
	Workspaces []Workspace
	Containers int
}

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
	for _, item := range stored.Workspaces {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Path) == "" {
			return RegistryInspection{}, errors.New("workspace registry contains invalid entry")
		}
		if stored.Version < 5 {
			previousID := item.ID
			item.ID = workspaceID(item.Path)
			item.LegacyIDs = appendUniqueString(item.LegacyIDs, previousID)
		}
		item.LegacyIDs = normalizeIDs(item.LegacyIDs, item.ID)
		workspaces = append(workspaces, item)
	}
	return RegistryInspection{
		Exists:     true,
		Version:    stored.Version,
		Workspaces: workspaces,
		Containers: len(stored.Containers),
	}, nil
}
