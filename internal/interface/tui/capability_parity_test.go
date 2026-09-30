package tui

import (
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/interface/tui/action"
	"go.mewis.me/codemcp/internal/interface/tui/palette"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestDeclaredTUIAdapterEvidenceHasRepresentation(t *testing.T) {
	registry := defaultActionRegistry()
	represented := map[capability.ID][]action.Action{}
	for _, item := range registry.All() {
		for _, id := range item.Capabilities {
			represented[id] = append(represented[id], item)
		}
	}
	report := capability.ProductParityReportSnapshot()
	for _, row := range report.Operations {
		for _, mapping := range row.Surfaces {
			if mapping.Surface != capability.SurfaceTUI || !mapping.Reachable {
				continue
			}
			if len(represented[row.Operation]) == 0 {
				t.Errorf("declared reachable TUI adapter %s has no action representation; evidence=%v", row.Operation, mapping.EntryPoints)
			}
		}
	}
}

func TestEveryTUIActionCapabilityIsDeclaredRequired(t *testing.T) {
	for _, item := range defaultActionRegistry().All() {
		for _, id := range item.Capabilities {
			spec, ok := capability.Lookup(id)
			if !ok {
				t.Fatalf("TUI action %s references unknown operation %s", item.ID, id)
			}
			contract, ok := spec.Surface(capability.SurfaceTUI)
			if !ok || contract.State != capability.SurfaceRequired {
				t.Fatalf("TUI action %s silently owns non-required operation %s: %#v", item.ID, id, contract)
			}
		}
	}
}

func TestExecutableTUIActionsCarryCanonicalOperationIDs(t *testing.T) {
	for _, item := range defaultActionRegistry().All() {
		if len(item.CommandPath) == 0 {
			continue
		}
		want, mapped := capability.ForPath(strings.Join(item.CommandPath, " "))
		if !mapped {
			if item.Operation != "" {
				t.Errorf("action %s has operation %s for unmapped path %q", item.ID, item.Operation, strings.Join(item.CommandPath, " "))
			}
			continue
		}
		if item.Operation != want {
			t.Errorf("action %s operation=%s want=%s for %q", item.ID, item.Operation, want, strings.Join(item.CommandPath, " "))
		}
	}
}

func TestMappedCapabilitiesArePaletteDiscoverableByCanonicalCLIPath(t *testing.T) {
	registry := defaultActionRegistry()
	for _, spec := range capability.All() {
		surface, ok := spec.Surface(capability.SurfaceTUI)
		if !ok || surface.State != capability.SurfaceRequired {
			continue
		}
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
			if len(palette.Rank([]action.Action{item}, spec.CLI.CanonicalPath, action.Context{})) > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("capability %s is not discoverable by %q", spec.ID, spec.CLI.CanonicalPath)
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
	cfg.Tunnel = tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin", ReadAccess: true, ManageAccess: true}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	contexts := []action.Context{
		{},
		{Route: string(RouteWorkspaces)}, {Route: string(RouteWorkspaces), ResourceID: "resource"},
		{Route: string(RouteContainers)}, {Route: string(RouteContainers), ResourceID: "resource"},
		{Route: string(RouteMCP)}, {Route: string(RouteMCP), ResourceID: "resource"},
		{Route: string(RouteTunnel)}, {Route: string(RouteTools)}, {Route: string(RouteIntegrations)}, {Route: string(RouteDoctor)},
		{Route: string(RouteExecutions), Mode: "resource"}, {Route: string(RouteProcesses), Mode: "resource"},
		{Route: string(RouteRequests)}, {Route: string(RouteRequests), ResourceID: "resource"},
		{Route: string(RouteLLM)}, {Route: string(RouteLLM), ResourceID: "resource"},
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
