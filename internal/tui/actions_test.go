package tui

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tui/action"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestWorkspaceActionAvailabilityFollowsRouteContext(t *testing.T) {
	registry := defaultActionRegistry()
	has := func(ctx action.Context, id string) bool {
		for _, item := range registry.Actions(ctx) {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	if !has(action.Context{Route: string(RouteHome)}, "workspace.register") || !has(action.Context{Route: string(RouteHome)}, "workspace.container.create") {
		t.Fatal("global workspace actions are unavailable")
	}
	if has(action.Context{Route: string(RouteWorkspaces)}, "workspace.unregister") {
		t.Fatal("workspace unregister available without a resource")
	}
	if !has(action.Context{Route: string(RouteWorkspaces), ResourceID: "ws_demo"}, "workspace.unregister") || !has(action.Context{Route: string(RouteWorkspaces), ResourceID: "ws_demo"}, "workspace.relocate") || !has(action.Context{Route: string(RouteWorkspaces), ResourceID: "ws_demo"}, "workspace.access.add") {
		t.Fatal("workspace context actions missing")
	}
	if has(action.Context{Route: string(RouteWorkspaces)}, "workspace.context.configure") || has(action.Context{Route: string(RouteHome), ResourceID: "ws_demo"}, "workspace.context.preview") {
		t.Fatal("workspace context navigation actions leaked outside a workspace resource")
	}
	workspace := action.Context{Route: string(RouteWorkspaces), ResourceID: "ws_demo"}
	for _, id := range []string{"workspace.context.configure", "workspace.context.preview"} {
		if !has(workspace, id) {
			t.Fatalf("workspace context navigation action missing: %s", id)
		}
	}
	if !has(action.Context{Route: string(RouteContainers), ResourceID: "wsc_demo"}, "workspace.container.rename") || !has(action.Context{Route: string(RouteContainers), ResourceID: "wsc_demo"}, "workspace.container.remove") {
		t.Fatal("container context actions missing")
	}
}

func TestInstructionAndWorkspaceContextNavigationActions(t *testing.T) {
	registry := defaultActionRegistry()
	for id, section := range map[string]string{
		"instruction.open.context": "context",
		"instruction.open.rules":   "rules",
		"instruction.open.sources": "sources",
	} {
		cmd, err := registry.Execute(context.Background(), id, action.Context{Route: string(RouteHome)})
		if err != nil || cmd == nil {
			t.Fatalf("execute %s cmd=%v err=%v", id, cmd != nil, err)
		}
		message, ok := cmd().(navigateMsg)
		if !ok || message.route != (Route{Kind: RouteInstruction, Section: section}) || !message.sibling {
			t.Fatalf("%s navigation=%#v", id, message)
		}
	}
	cmd, err := registry.Execute(context.Background(), "workspace.context.preview", action.Context{Route: string(RouteWorkspaces), ResourceID: "ws_demo"})
	if err != nil || cmd == nil {
		t.Fatalf("workspace preview cmd=%v err=%v", cmd != nil, err)
	}
	message, ok := cmd().(navigateMsg)
	if !ok || message.route != (Route{Kind: RouteWorkspaces, ResourceID: "ws_demo", Section: "context-preview"}) || message.sibling {
		t.Fatalf("workspace preview navigation=%#v", message)
	}
}

func TestGuideActionsNavigateDirectlyToEmbeddedTopics(t *testing.T) {
	registry := defaultActionRegistry()
	for id, want := range map[string]Route{
		"app.go.guide":                 {Kind: RouteGuide},
		"guide.mcp":                    {Kind: RouteGuide, ResourceID: "mcp"},
		"guide.requests":               {Kind: RouteGuide, ResourceID: "requests"},
		"guide.config.storage.bundles": {Kind: RouteGuide, ResourceID: "config/storage/bundles"},
	} {
		cmd, err := registry.Execute(context.Background(), id, action.Context{Route: string(RouteHome)})
		if err != nil || cmd == nil {
			t.Fatalf("execute %s cmd=%v err=%v", id, cmd != nil, err)
		}
		message, ok := cmd().(navigateMsg)
		if !ok || message.route != want {
			t.Fatalf("%s navigation=%#v want=%#v", id, message, want)
		}
	}
	for _, item := range registry.Actions(action.Context{Route: string(RouteHome)}) {
		if strings.HasPrefix(item.ID, "guide.") && strings.HasPrefix(item.Title, "Guide:") {
			t.Fatalf("guide action title repeats category prefix: %#v", item)
		}
	}
}

func TestEditorActionsNavigateToEditorRoutes(t *testing.T) {
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
	registry := defaultActionRegistry()
	tests := []struct {
		id   string
		ctx  action.Context
		want Route
	}{
		{"workspace.register", action.Context{Route: string(RouteHome)}, Route{Kind: RouteWorkspaces, Action: "register"}},
		{"workspace.relocate", action.Context{Route: string(RouteWorkspaces), ResourceID: "ws_demo"}, Route{Kind: RouteWorkspaces, ResourceID: "ws_demo", Action: "relocate"}},
		{"workspace.access.add", action.Context{Route: string(RouteWorkspaces), ResourceID: "ws_demo"}, Route{Kind: RouteWorkspaces, ResourceID: "ws_demo", Section: "access", Action: "add"}},
		{"workspace.container.create", action.Context{Route: string(RouteHome)}, Route{Kind: RouteContainers, Action: "create"}},
		{"workspace.container.rename", action.Context{Route: string(RouteContainers), ResourceID: "wsc_demo"}, Route{Kind: RouteContainers, ResourceID: "wsc_demo", Action: "edit"}},
		{"mcp.server.add", action.Context{Route: string(RouteHome)}, Route{Kind: RouteMCP, Action: "create"}},
		{"mcp.server.configure", action.Context{Route: string(RouteMCP), ResourceID: "github"}, Route{Kind: RouteMCP, ResourceID: "github", Action: "edit"}},
		{"mcp.server.auth.login", action.Context{Route: string(RouteMCP), ResourceID: "github"}, Route{Kind: RouteMCP, ResourceID: "github", Section: "oauth", Action: "login"}},
		{"tunnel.configure", action.Context{Route: string(RouteTunnel)}, Route{Kind: RouteTunnel, Action: "edit"}},
		{"tunnel.admin.key.set", action.Context{Route: string(RouteTunnel)}, Route{Kind: RouteTunnel, Section: "admin-key", Action: "edit"}},
		{"tunnel.managed.create", action.Context{Route: string(RouteTunnels)}, Route{Kind: RouteTunnels, Action: "create"}},
		{"tunnel.managed.update", action.Context{Route: string(RouteTunnels), ResourceID: "tun_demo"}, Route{Kind: RouteTunnels, ResourceID: "tun_demo", Action: "edit"}},
		{"config.convert", action.Context{Route: string(RouteConfig)}, Route{Kind: RouteConfig, Section: "storage", Action: "convert"}},
		{"config.export", action.Context{Route: string(RouteConfig)}, Route{Kind: RouteConfig, Section: "storage", Action: "export"}},
		{"config.import", action.Context{Route: string(RouteConfig)}, Route{Kind: RouteConfig, Section: "storage", Action: "import"}},
		{"logs.filter", action.Context{Route: string(RouteLogs)}, Route{Kind: RouteLogs, Action: "filter"}},
		{"request.create.test", action.Context{Route: string(RouteRequests)}, Route{Kind: RouteRequests, Action: "create-test"}},
		{"install.run", action.Context{Route: string(RouteRuntime)}, Route{Kind: RouteRuntime, Action: "install"}},
		{"update.apply", action.Context{Route: string(RouteRuntime)}, Route{Kind: RouteRuntime, Action: "update"}},
	}
	for _, test := range tests {
		cmd, err := registry.Execute(context.Background(), test.id, test.ctx)
		if err != nil || cmd == nil {
			t.Fatalf("execute %s cmd=%v err=%v", test.id, cmd != nil, err)
		}
		message, ok := cmd().(navigateMsg)
		if !ok || message.route != test.want || message.sibling {
			t.Fatalf("%s navigation=%#v want=%#v", test.id, message, test.want)
		}
	}
}

func TestMCPActionAvailabilityFollowsRouteContext(t *testing.T) {
	registry := defaultActionRegistry()
	has := func(ctx action.Context, id string) bool {
		for _, item := range registry.Actions(ctx) {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	if !has(action.Context{Route: string(RouteHome)}, "mcp.server.add") || !has(action.Context{Route: string(RouteHome)}, "mcp.server.status") {
		t.Fatal("global MCP actions are unavailable")
	}
	if has(action.Context{Route: string(RouteMCP)}, "mcp.server.configure") || has(action.Context{Route: string(RouteMCP)}, "mcp.server.tools") {
		t.Fatal("resource MCP actions available without a resource")
	}
	ctx := action.Context{Route: string(RouteMCP), ResourceID: "github"}
	for _, id := range []string{"mcp.server.configure", "mcp.server.remove", "mcp.server.enable", "mcp.server.disable", "mcp.server.tools", "mcp.server.auth.login", "mcp.server.auth.logout"} {
		if !has(ctx, id) {
			t.Fatalf("MCP context action missing: %s", id)
		}
	}
}

func TestTunnelActionAvailabilityFollowsRouteContext(t *testing.T) {
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
	registry := defaultActionRegistry()
	has := func(ctx action.Context, id string) bool {
		for _, item := range registry.Actions(ctx) {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	if !has(action.Context{Route: string(RouteTunnel)}, "tunnel.configure") || !has(action.Context{Route: string(RouteTunnel)}, "tunnel.admin.key.set") {
		t.Fatal("runtime tunnel actions are unavailable on tunnel route")
	}
	if has(action.Context{Route: string(RouteHome)}, "tunnel.configure") || has(action.Context{Route: string(RouteTunnel)}, "tunnel.managed.create") {
		t.Fatal("tunnel actions leaked into the wrong route")
	}
	if !has(action.Context{Route: string(RouteTunnels)}, "tunnel.managed.create") || !has(action.Context{Route: string(RouteTunnels)}, "tunnel.managed.refresh") {
		t.Fatal("managed tunnel list actions are unavailable")
	}
	if has(action.Context{Route: string(RouteTunnels)}, "tunnel.managed.update") || has(action.Context{Route: string(RouteTunnels)}, "tunnel.managed.delete") {
		t.Fatal("managed tunnel resource actions available without a resource")
	}
	ctx := action.Context{Route: string(RouteTunnels), ResourceID: "tunnel_demo"}
	for _, id := range []string{"tunnel.managed.update", "tunnel.managed.configure", "tunnel.managed.delete"} {
		if !has(ctx, id) {
			t.Fatalf("managed tunnel context action missing: %s", id)
		}
	}
}

func TestRequestActionAvailabilityFollowsRouteContext(t *testing.T) {
	registry := defaultActionRegistry()
	has := func(ctx action.Context, id string) bool {
		for _, item := range registry.Actions(ctx) {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	list := action.Context{Route: string(RouteRequests)}
	for _, id := range []string{"request.refresh", "request.create.test", "request.show.pending", "request.show.history", "request.show.all"} {
		if !has(list, id) {
			t.Fatalf("request list action missing: %s", id)
		}
	}
	if !has(list, "request.approve") || !has(list, "request.deny") {
		t.Fatal("request resolution actions unavailable on requests list")
	}
	resource := action.Context{Route: string(RouteRequests), ResourceID: "req_demo"}
	if !has(resource, "request.approve") || !has(resource, "request.deny") {
		t.Fatal("request resource resolution actions unavailable")
	}
	if has(action.Context{Route: string(RouteHome)}, "request.refresh") {
		t.Fatal("request actions leaked outside requests route")
	}
	for _, item := range registry.Actions(list) {
		if item.ID == "request.create.test" {
			if item.Title != "Create test request" || !strings.Contains(strings.Join(item.Keywords, " "), "dummy") {
				t.Fatalf("test request action=%#v", item)
			}
			return
		}
	}
	t.Fatal("create test request action missing")
}

func TestConfigActionAvailabilityFollowsRouteContext(t *testing.T) {
	registry := defaultActionRegistry()
	has := func(ctx action.Context, id string) bool {
		for _, item := range registry.Actions(ctx) {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	ctx := action.Context{Route: string(RouteConfig)}
	for _, id := range []string{"config.refresh", "config.edit", "config.verify", "config.migrate", "config.migrate.secrets", "config.convert", "config.export", "config.import"} {
		if !has(ctx, id) {
			t.Fatalf("config action missing: %s", id)
		}
	}
	if has(action.Context{Route: string(RouteHome)}, "config.verify") {
		t.Fatal("config actions leaked outside config route")
	}
}

func TestLogsActionAvailabilityFollowsRouteContext(t *testing.T) {
	registry := defaultActionRegistry()
	has := func(ctx action.Context, id string) bool {
		for _, item := range registry.Actions(ctx) {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	ctx := action.Context{Route: string(RouteLogs)}
	for _, id := range []string{"logs.refresh", "logs.filter", "logs.toggle", "logs.info", "logs.clear"} {
		if !has(ctx, id) {
			t.Fatalf("logs action missing: %s", id)
		}
	}
	if has(action.Context{Route: string(RouteHome)}, "logs.clear") {
		t.Fatal("logs actions leaked outside logs route")
	}
	if !has(action.Context{Route: string(RouteHome)}, "app.go.logs-exec") {
		t.Fatal("command execution navigation action missing")
	}
}

func TestSystemActionAvailabilityFollowsRouteAndPlatform(t *testing.T) {
	registry := defaultActionRegistry()
	has := func(ctx action.Context, id string) bool {
		for _, item := range registry.Actions(ctx) {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	ctx := action.Context{Route: string(RouteRuntime)}
	for _, id := range []string{"system.refresh", "runtime.up.user", "runtime.down.user", "runtime.restart.user", "runtime.foreground", "mcp.stdio.foreground", "mcp.http.foreground", "auth.mcp.rotate", "auth.admin.rotate", "install.run", "update.check", "update.apply"} {
		if !has(ctx, id) {
			t.Fatalf("system action missing: %s", id)
		}
	}
	if has(action.Context{Route: string(RouteHome)}, "update.apply") {
		t.Fatal("system action leaked outside runtime route")
	}
	if runtime.GOOS == "windows" {
		if has(ctx, "runtime.up.system") || has(ctx, "runtime.down.system") || has(ctx, "runtime.restart.system") {
			t.Fatal("system-scope actions exposed on Windows")
		}
	} else if !has(ctx, "runtime.up.system") || !has(ctx, "runtime.down.system") || !has(ctx, "runtime.restart.system") {
		t.Fatal("system-scope actions missing on supported platform")
	}
	if !has(action.Context{Route: string(RouteHome)}, "app.go.about") {
		t.Fatal("About navigation action missing")
	}
}
