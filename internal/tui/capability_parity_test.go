package tui

import (
	"runtime"
	"sort"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tui/action"
	"go.mewis.me/codemcp/internal/tui/palette"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestEveryPublicCapabilityHasTUIRepresentation(t *testing.T) {
	registry := defaultActionRegistry()
	represented := map[capability.ID][]action.Action{}
	for _, item := range registry.All() {
		for _, id := range item.Capabilities {
			represented[id] = append(represented[id], item)
		}
	}
	missing := []string{}
	for _, spec := range capability.All() {
		if len(represented[spec.ID]) == 0 {
			missing = append(missing, spec.CanonicalPath)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("missing TUI mappings:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestMappedCapabilitiesArePaletteDiscoverableByCanonicalCLIPath(t *testing.T) {
	registry := defaultActionRegistry()
	for _, spec := range capability.All() {
		var mapped []action.Action
		for _, item := range registry.All() {
			for _, id := range item.Capabilities {
				if id == spec.ID {
					mapped = append(mapped, item)
					break
				}
			}
		}
		if len(mapped) == 0 {
			continue
		}
		found := false
		for _, item := range mapped {
			if len(palette.Rank([]action.Action{item}, spec.CanonicalPath, action.Context{})) > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("capability %s is not discoverable by %q", spec.ID, spec.CanonicalPath)
		}
	}
}

func TestCapabilityActionsHaveReachableContexts(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tunnel = tunnel.Config{AdminKey: "admin-secret", AdminWorkspaceID: "ws_admin", AdminReadAccess: true, AdminManageAccess: true}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	contexts := []action.Context{
		{},
		{Route: string(RouteWorkspaces)}, {Route: string(RouteWorkspaces), ResourceID: "resource"},
		{Route: string(RouteContainers)}, {Route: string(RouteContainers), ResourceID: "resource"},
		{Route: string(RouteMCP)}, {Route: string(RouteMCP), ResourceID: "resource"},
		{Route: string(RouteTunnel)}, {Route: string(RouteTunnels)}, {Route: string(RouteTunnels), ResourceID: "resource"},
		{Route: string(RouteRequests)}, {Route: string(RouteRequests), ResourceID: "resource"},
		{Route: string(RouteLogs)}, {Route: string(RouteConfig)}, {Route: string(RouteRuntime)}, {Route: string(RouteAbout)},
	}
	for _, item := range defaultActionRegistry().All() {
		if len(item.Capabilities) == 0 || (runtime.GOOS == "windows" && (item.ID == "runtime.up.system" || item.ID == "runtime.down.system" || item.ID == "runtime.restart.system")) {
			continue
		}
		reachable := false
		for _, ctx := range contexts {
			if item.IsAvailable(ctx) {
				reachable = true
				break
			}
		}
		if !reachable {
			t.Errorf("capability action %s has no reachable route context", item.ID)
		}
	}
}
