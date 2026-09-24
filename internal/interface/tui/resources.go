package tui

import (
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/interface/tui/quickopen"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

func loadQuickOpenResources() ([]quickopen.Resource, error) {
	resources := pageQuickOpenResources()
	manager := workspace.NewManager(workspace.DefaultStorePath())
	workspaces, err := manager.List()
	if err != nil {
		return nil, err
	}
	for _, item := range workspaces {
		resources = append(resources, quickopen.Resource{ID: item.ID, Title: item.ID, Kind: "Workspace", Description: item.Path, Keywords: append([]string{item.Path}, item.LegacyIDs...), Path: []string{"workspace", item.ID}})
	}
	containers, err := manager.ListContainers()
	if err != nil {
		return nil, err
	}
	for _, item := range containers {
		resources = append(resources, quickopen.Resource{ID: item.ID, Title: item.Name, Kind: "Container", Description: fmt.Sprintf("%s · %d workspaces", item.ID, len(item.WorkspaceIDs)), Keywords: append([]string{item.ID}, item.WorkspaceIDs...), Path: []string{"containers", item.ID}})
	}
	upstreams := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := upstreams.Load(); err != nil {
		return nil, err
	}
	for _, item := range upstreams.List() {
		endpoint := item.URL
		if item.Transport == "stdio" {
			endpoint = item.Command
		}
		resources = append(resources, quickopen.Resource{ID: item.ID, Title: item.ID, Kind: "Upstream", Description: endpoint, Keywords: []string{item.Name, item.Transport, item.Expose, endpoint}, Path: []string{"upstream", item.ID}})
	}
	source, err := config.Source()
	if err != nil {
		return nil, err
	}
	if source.Exists {
		_, err := config.Load()
		if err != nil {
			return nil, err
		}
		metadata, err := config.ListTunnelMetadata()
		if err != nil {
			return nil, err
		}
		for _, item := range metadata {
			title := item.ID
			if strings.TrimSpace(item.Name) != "" {
				title = item.Name
			}
			keywords := append(append(append([]string{item.ID}, item.OrganizationIDs...), item.WorkspaceIDs...), item.TenantIDs...)
			resources = append(resources, quickopen.Resource{ID: item.ID, Title: title, Kind: "Tunnel", Description: item.Description, Keywords: keywords, Path: []string{"tunnels", item.ID}})
		}
	}
	return resources, nil
}

func pageQuickOpenResources() []quickopen.Resource {
	return []quickopen.Resource{
		{ID: "home", Title: "Home", Kind: "Page", Path: []string{"home"}},
		{ID: "workspaces", Title: "Workspaces", Kind: "Page", Path: []string{"workspaces"}},
		{ID: "containers", Title: "Workspaces · Containers", Kind: "Page", Keywords: []string{"workspace", "container", "containers"}, Path: []string{"containers"}},
		{ID: "mcp", Title: "Upstreams", Kind: "Page", Path: []string{"upstream"}},
		{ID: "tunnel", Title: "Tunnel", Kind: "Page", Path: []string{"tunnel"}},
		{ID: "tunnels", Title: "Managed Tunnels", Kind: "Page", Path: []string{"tunnels"}},
		{ID: "requests", Title: "Requests", Kind: "Page", Path: []string{"requests"}},
		{ID: "logs", Title: "Logs", Kind: "Page", Path: []string{"logs"}},
		{ID: "config", Title: "Config", Kind: "Page", Path: []string{"config"}},
		{ID: "runtime", Title: "Runtime", Kind: "Page", Path: []string{"runtime"}},
		{ID: "about", Title: "About", Kind: "Page", Path: []string{"about"}},
	}
}
