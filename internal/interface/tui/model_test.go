package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	tuipage "go.mewis.me/codemcp/internal/interface/tui/page"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestModelWorkspaceContextSessionsAreScopedAndStable(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	first := model.workspaceContextSession("ws_one")
	again := model.workspaceContextSession("ws_one")
	second := model.workspaceContextSession("ws_two")
	if first == nil || first != again || second == nil || first == second {
		t.Fatalf("sessions first=%p again=%p second=%p", first, again, second)
	}
	if len(model.workspaceContexts) != 2 {
		t.Fatalf("session count=%d", len(model.workspaceContexts))
	}
}

func TestModelRemembersLastStableRoutePerHeaderOwner(t *testing.T) {
	route := Route{Kind: RouteInstruction, Section: "rules"}
	model := NewModel(route)
	model.switchPage(Route{Kind: RouteTunnel})
	updated, _ := model.requestNavigation(navigationIntent{route: Route{Kind: RouteInstruction}, replace: true, restoreRemembered: true})
	model = updated.(Model)
	if model.router.Current() != route {
		t.Fatalf("restored route=%#v want=%#v", model.router.Current(), route)
	}
}

func TestModelExplicitReplaceDoesNotRestoreRememberedDetail(t *testing.T) {
	model := NewModel(Route{Kind: RouteWorkspaces, ResourceID: "ws_old"})
	model.router.Switch(Route{Kind: RouteWorkspaces, ResourceID: "ws_old", Action: "relocate"})
	updated, _ := model.Update(tuipage.NavigateMsg{Path: []string{"workspaces"}, Replace: true})
	model = updated.(Model)
	if got := model.router.Current(); got != (Route{Kind: RouteWorkspaces}) {
		t.Fatalf("explicit replace restored stale route: %#v", got)
	}
	if got := model.lastRoutes[RouteWorkspaces]; got != (Route{Kind: RouteWorkspaces}) {
		t.Fatalf("stable route memory=%#v want workspace root", got)
	}
	updated, _ = model.Update(tuipage.NavigateMsg{Path: []string{"containers"}, Replace: true})
	model = updated.(Model)
	if got := model.router.Current(); got != (Route{Kind: RouteContainers}) {
		t.Fatalf("container replace restored stale workspace detail: %#v", got)
	}
}

func TestModelExplicitReplaceDestinationsBypassRememberedRoutes(t *testing.T) {
	tests := []struct {
		name       string
		remembered Route
		path       []string
		want       Route
	}{
		{name: "workspaces", remembered: Route{Kind: RouteWorkspaces, ResourceID: "ws_old"}, path: []string{"workspaces"}, want: Route{Kind: RouteWorkspaces}},
		{name: "containers", remembered: Route{Kind: RouteContainers, ResourceID: "wsc_old"}, path: []string{"containers"}, want: Route{Kind: RouteContainers}},
		{name: "mcp", remembered: Route{Kind: RouteMCP, ResourceID: "server_old"}, path: []string{"mcp"}, want: Route{Kind: RouteMCP}},
		{name: "tunnels", remembered: Route{Kind: RouteTunnels, ResourceID: "tun_old"}, path: []string{"tunnels"}, want: Route{Kind: RouteTunnels}},
		{name: "runtime", remembered: Route{Kind: RouteRuntime, ResourceID: "old"}, path: []string{"runtime"}, want: Route{Kind: RouteRuntime}},
		{name: "config", remembered: Route{Kind: RouteConfig, ResourceID: "server.port"}, path: []string{"config"}, want: Route{Kind: RouteConfig}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(Route{Kind: RouteHome})
			model.lastRoutes[headerOwner(test.remembered.Kind)] = test.remembered
			updated, _ := model.Update(tuipage.NavigateMsg{Path: test.path, Replace: true})
			model = updated.(Model)
			if got := model.router.Current(); got != test.want {
				t.Fatalf("route=%#v want=%#v", got, test.want)
			}
		})
	}
}

func TestModelStableRouteMemoryCoversHeaderOwners(t *testing.T) {
	tests := []struct {
		name   string
		stored Route
		entry  Route
	}{
		{name: "workspace detail", stored: Route{Kind: RouteWorkspaces, ResourceID: "ws_a", Section: "access"}, entry: Route{Kind: RouteWorkspaces}},
		{name: "container detail", stored: Route{Kind: RouteContainers, ResourceID: "wsc_a"}, entry: Route{Kind: RouteWorkspaces}},
		{name: "mcp detail", stored: Route{Kind: RouteMCP, ResourceID: "server_a"}, entry: Route{Kind: RouteMCP}},
		{name: "tunnel section", stored: Route{Kind: RouteTunnel, Section: "admin"}, entry: Route{Kind: RouteTunnel}},
		{name: "managed tunnel detail", stored: Route{Kind: RouteTunnels, ResourceID: "tun_a"}, entry: Route{Kind: RouteTunnel}},
		{name: "request mode", stored: Route{Kind: RouteRequests, Mode: "pending"}, entry: Route{Kind: RouteRequests}},
		{name: "logs execution route", stored: Route{Kind: RouteLogsExec}, entry: Route{Kind: RouteLogs}},
		{name: "config detail", stored: Route{Kind: RouteConfig, ResourceID: "server.port"}, entry: Route{Kind: RouteConfig}},
		{name: "instruction section", stored: Route{Kind: RouteInstruction, Section: "rules"}, entry: Route{Kind: RouteInstruction}},
		{name: "runtime section", stored: Route{Kind: RouteRuntime, Section: "status"}, entry: Route{Kind: RouteRuntime}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := Model{lastRoutes: map[RouteKind]Route{}}
			model.rememberStableRoute(test.stored)
			if got := model.resolveRememberedRoute(test.entry); got != test.stored {
				t.Fatalf("restored route=%#v want=%#v", got, test.stored)
			}
			explicit := test.entry
			explicit.ResourceID = "explicit"
			if got := model.resolveRememberedRoute(explicit); got != explicit {
				t.Fatalf("explicit route was replaced: got=%#v want=%#v", got, explicit)
			}
		})
	}
}

func TestModelDoesNotRememberActionRoutesOrPersistLastRoutes(t *testing.T) {
	action := Route{Kind: RouteMCP, ResourceID: "server", Action: "edit"}
	model := Model{router: NewRouter(action), lastRoutes: map[RouteKind]Route{}}
	model.rememberStableRoute(action)
	if len(model.lastRoutes) != 0 {
		t.Fatalf("action route was remembered: %#v", model.lastRoutes)
	}
	fresh := NewModel(Route{Kind: RouteHome})
	if len(fresh.lastRoutes) != 0 {
		t.Fatalf("fresh TUI inherited session routes: %#v", fresh.lastRoutes)
	}
}

func TestModelRestoresLogsViewStateAcrossTopLevelNavigationAndResumesFollow(t *testing.T) {
	model := NewModel(Route{Kind: RouteLogsExec})
	page, ok := model.currentPage.(tuipage.SessionViewStateModel)
	if !ok {
		t.Fatalf("logs page does not expose session state: %T", model.currentPage)
	}
	page.RestoreSessionViewState(tuipage.LogsSessionViewState{
		Tab: "command-execution", RuntimePaused: true, RuntimeSelectedID: "run:4", ExecutionScope: "workspace", ExecutionWorkspaceID: "ws_a", ExecutionPaused: true, ExecutionYOffset: 6, ToolCallPaused: true, ToolCallYOffset: 5,
	})
	model.switchPage(Route{Kind: RouteTunnel})
	updated, _ := model.requestNavigation(navigationIntent{route: Route{Kind: RouteLogs}, replace: true, restoreRemembered: true})
	model = updated.(Model)
	if model.router.Current() != (Route{Kind: RouteLogsExec}) {
		t.Fatalf("restored route=%#v want command execution", model.router.Current())
	}
	restoredPage, ok := model.currentPage.(tuipage.SessionViewStateModel)
	if !ok {
		t.Fatalf("restored logs page does not expose session state: %T", model.currentPage)
	}
	state, ok := restoredPage.SessionViewState().(tuipage.LogsSessionViewState)
	if !ok {
		t.Fatalf("restored logs state type=%T", restoredPage.SessionViewState())
	}
	if state.Tab != "command-execution" || state.ExecutionScope != "workspace" || state.ExecutionWorkspaceID != "ws_a" || state.RuntimePaused || state.RuntimeSelectedID != "" || state.ExecutionPaused || state.ExecutionYOffset != 0 || state.ToolCallPaused || state.ToolCallYOffset != 0 {
		t.Fatalf("restored logs state=%#v", state)
	}
}

func TestModelSwitchingToToolCallsRestoresToolCallsTab(t *testing.T) {
	model := NewModel(Route{Kind: RouteLogsExec})
	model.switchPage(Route{Kind: RouteLogsTools})
	page, ok := model.currentPage.(tuipage.SessionViewStateModel)
	if !ok {
		t.Fatalf("tool calls page does not expose session state: %T", model.currentPage)
	}
	state, ok := page.SessionViewState().(tuipage.LogsSessionViewState)
	if !ok || state.Tab != "tool-calls" {
		t.Fatalf("tool calls state=%#v", page.SessionViewState())
	}
}

func TestModelMCPCreateEditorUsesDirtyNavigationGuard(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	route := Route{Kind: RouteMCP, Action: "create"}
	model := NewModel(route)
	_ = model.currentPage.Init()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	if model.currentPage.OverlayActive() || !model.currentPage.InputActive() {
		t.Fatalf("MCP create overlay=%t input=%t", model.currentPage.OverlayActive(), model.currentPage.InputActive())
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'd', Text: "draft"})
	model = updated.(Model)
	guard, ok := model.currentPage.(tuipage.NavigationGuardModel)
	if !ok || !guard.Dirty() {
		t.Fatalf("MCP create guard=%t dirty=%t", ok, ok && guard.Dirty())
	}
	updated, cmd := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation == nil || model.router.Current() != route {
		t.Fatalf("dirty MCP create escaped: route=%#v pending=%v cmd=%v", model.router.Current(), model.pendingNavigation != nil, cmd != nil)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Discard changes?") {
		t.Fatal("dirty MCP create navigation did not render discard guard")
	}
}

func TestModelManagedTunnelCreateEditorUsesDirtyNavigationGuard(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tunnel = tunnel.Config{AdminKey: "admin-secret", AdminWorkspaceID: "ws_admin", AdminReadAccess: true, AdminManageAccess: true}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	route := Route{Kind: RouteTunnels, Action: "create"}
	model := NewModel(route)
	_ = model.currentPage.Init()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	if model.currentPage.OverlayActive() || !model.currentPage.InputActive() {
		t.Fatalf("managed create overlay=%t input=%t", model.currentPage.OverlayActive(), model.currentPage.InputActive())
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'd', Text: "draft"})
	model = updated.(Model)
	guard, ok := model.currentPage.(tuipage.NavigationGuardModel)
	if !ok || !guard.Dirty() {
		t.Fatalf("managed create guard=%t dirty=%t", ok, ok && guard.Dirty())
	}
	updated, cmd := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation == nil || model.router.Current() != route {
		t.Fatalf("dirty managed create escaped: route=%#v pending=%v cmd=%v", model.router.Current(), model.pendingNavigation != nil, cmd != nil)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Discard changes?") {
		t.Fatal("dirty managed create navigation did not render discard guard")
	}
}

func TestModelMCPOAuthEditorDeepLinkUsesDirtyNavigationGuard(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Add(upstream.Server{ID: "secure", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Auth: upstream.AuthConfig{Type: "oauth"}, Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	route := Route{Kind: RouteMCP, ResourceID: "secure", Section: "oauth", Action: "login"}
	model := NewModel(route)
	_ = model.currentPage.Init()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	if model.currentPage == nil || model.currentPage.OverlayActive() || !model.currentPage.InputActive() {
		t.Fatalf("OAuth deep link page=%v overlay=%t input=%t", model.currentPage != nil, model.currentPage != nil && model.currentPage.OverlayActive(), model.currentPage != nil && model.currentPage.InputActive())
	}
	if got := ansi.Strip(model.View().Content); !strings.Contains(got, "MCP  /  secure  /  OAuth  /  Login") || !strings.Contains(got, "enter next") {
		t.Fatalf("OAuth deep link view=%q", got)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'i', Text: "https://issuer.example"})
	model = updated.(Model)
	guard, ok := model.currentPage.(tuipage.NavigationGuardModel)
	if !ok || !guard.Dirty() {
		t.Fatalf("OAuth guard=%t dirty=%t", ok, ok && guard.Dirty())
	}
	updated, cmd := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation == nil || model.router.Current() != route {
		t.Fatalf("dirty OAuth editor escaped: route=%#v pending=%v cmd=%v", model.router.Current(), model.pendingNavigation != nil, cmd != nil)
	}
}

func TestModelWorkspaceProjectContextUsesDirtyNavigationGuard(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	route := Route{Kind: RouteWorkspaces, ResourceID: item.ID, Section: "context"}
	model := NewModel(route)
	_ = model.currentPage.Init()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'd', Text: "draft"})
	model = updated.(Model)
	guard, ok := model.currentPage.(tuipage.NavigationGuardModel)
	if !ok || !guard.Dirty() {
		t.Fatalf("project context guard=%t dirty=%t", ok, ok && guard.Dirty())
	}
	updated, cmd := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation == nil || model.router.Current() != route {
		t.Fatalf("dirty project context escaped: route=%#v pending=%v cmd=%v", model.router.Current(), model.pendingNavigation != nil, cmd != nil)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Discard changes?") {
		t.Fatal("project context dirty navigation did not render discard guard")
	}
}

func TestModelWorkspaceProjectContextEscapeCancelsBuildInPlace(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	route := Route{Kind: RouteWorkspaces, ResourceID: item.ID, Section: "context"}
	model := NewModel(route)
	_ = model.currentPage.Init()
	updated, build := model.Update(component.EditorSubmitMsg{})
	model = updated.(Model)
	if build == nil || !model.currentPage.OverlayActive() {
		t.Fatalf("project context build=%v active=%t", build != nil, model.currentPage.OverlayActive())
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if model.router.Current() != route || model.currentPage.OverlayActive() {
		t.Fatalf("escape route=%#v active=%t", model.router.Current(), model.currentPage.OverlayActive())
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Project Context build cancelled") {
		t.Fatal("build cancellation feedback not rendered")
	}
}

func TestConfigEditorRouteLoadsNativePageWithoutCompatibilityShim(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	route := Route{Kind: RouteConfig, ResourceID: "server.port", Action: "edit"}
	model := NewModel(route)
	init := model.currentPage.Init()
	if init == nil {
		t.Fatal("config editor load command missing")
	}
	updated, follow := model.Update(init())
	model = updated.(Model)
	if follow == nil || model.router.Current() != route || !model.currentPage.InputActive() {
		t.Fatalf("route=%#v follow=%v input=%t", model.router.Current(), follow != nil, model.currentPage.InputActive())
	}
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "Config  /  server.port  /  Edit") || !strings.Contains(plain, "enter save") || strings.Contains(plain, "Edit MCP HTTP port") {
		t.Fatalf("config editor view=%q", plain)
	}
}

func TestModelWindowTitleTracksCurrentRoute(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	if got := model.View().WindowTitle; got != "CodeMCP · Home" {
		t.Fatalf("window title=%q", got)
	}
	model.router.Switch(Route{Kind: RouteRuntime})
	if got := model.View().WindowTitle; got != "CodeMCP · Runtime" {
		t.Fatalf("window title after route change=%q", got)
	}
}

func TestModelFillsExactTerminalSizeWithoutMinimumLayout(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, route := range []Route{{Kind: RouteHome}, {Kind: RouteWorkspaces}, {Kind: RouteMCP}, {Kind: RouteLogs}, {Kind: RouteConfig}, {Kind: RouteInstruction}, {Kind: RouteRuntime}, {Kind: RouteAbout}} {
		for _, size := range [][2]int{{120, 40}, {20, 8}, {3, 3}, {1, 1}} {
			model := NewModel(route)
			updated, _ := model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			model = updated.(Model)
			view := model.View().Content
			if width, height := lipgloss.Width(view), lipgloss.Height(view); width != size[0] || height != size[1] {
				t.Fatalf("%s layout=%dx%d want %dx%d", route.Kind, width, height, size[0], size[1])
			}
		}
	}
}

func TestModelResizeAndThemeStressKeepsExactGeometry(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	model := NewModel(Route{Kind: RouteLogs})
	sizes := [][2]int{{160, 50}, {80, 24}, {40, 10}, {20, 6}, {3, 3}, {1, 1}, {100, 32}}
	for round := range 8 {
		updated, _ := model.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
		model = updated.(Model)
		if round%2 == 1 {
			updated, _ = model.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
			model = updated.(Model)
		}
		for _, size := range sizes {
			updated, _ = model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			model = updated.(Model)
			view := model.View().Content
			if width, height := lipgloss.Width(view), lipgloss.Height(view); width != size[0] || height != size[1] {
				t.Fatalf("round=%d layout=%dx%d want=%dx%d", round, width, height, size[0], size[1])
			}
		}
	}
}

func TestModelRendersDeepLinkAndNavigation(t *testing.T) {
	model := NewModel(Route{Kind: RouteMCP, ResourceID: "github"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	model = updated.(Model)
	view := model.View().Content
	plain := ansi.Strip(view)
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "CodeMCP") || strings.Contains(lines[1], "CodeMCP") || !strings.Contains(plain, "Deep-linked resource: github") {
		t.Fatalf("view = %q", view)
	}
	itemsWidth := (100 - 4) - len(headerPages) + 1
	cellWidth := itemsWidth / len(headerPages)
	if 1 < itemsWidth%len(headerPages) {
		cellWidth++
	}
	active := model.theme.navActive.Padding(0).Width(cellWidth).Align(lipgloss.Center).Render("MCP")
	if !strings.Contains(view, active) {
		t.Fatal("MCP header button is not active")
	}
	updated, command := model.Update(navigateMsg{route: Route{Kind: RouteLogs}, sibling: true})
	model = updated.(Model)
	if model.router.Current().Kind != RouteLogs {
		t.Fatalf("route = %#v", model.router.Current())
	}
	if command != nil {
		updated, _ = model.Update(command())
		model = updated.(Model)
	}
}

func TestModelRendersEmbeddedGuideDeepLink(t *testing.T) {
	model := NewModel(Route{Kind: RouteGuide, ResourceID: "mcp"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	model = updated.(Model)
	plain := ansi.Strip(model.View().Content)
	if !strings.Contains(plain, "MCP Servers") || !strings.Contains(plain, "Use Topics for detailed documentation") || strings.Contains(plain, "Shell & Execution") {
		t.Fatalf("guide deep-link=%q", plain)
	}
	if len(model.router.stack) != 2 || model.router.stack[0] != (Route{Kind: RouteGuide}) {
		t.Fatalf("guide route stack=%#v", model.router.stack)
	}
}

func TestModelHeaderCellsFillUsableWidth(t *testing.T) {
	model := NewModel(Route{Kind: RouteRequests})
	for _, width := range []int{52, 73, 96, 117} {
		header, targets := model.header(width, 2, 1)
		if lipgloss.Width(header) != width {
			t.Fatalf("header width=%d want=%d", lipgloss.Width(header), width)
		}
		if len(targets) != len(headerPages) {
			t.Fatalf("targets=%d want=%d", len(targets), len(headerPages))
		}
		x, total := 2, 0
		for index, target := range targets {
			if target.Rect.X != x || target.Rect.Y != 1 || target.Rect.Height != 1 {
				t.Fatalf("target %d rect=%#v want x=%d y=1", index, target.Rect, x)
			}
			x += target.Rect.Width + 1
			total += target.Rect.Width
		}
		if total != width-len(headerPages)+1 || x-1 != width+2 {
			t.Fatalf("navbar coverage total=%d end=%d width=%d", total, x-1, width)
		}
		if count := strings.Count(ansi.Strip(header), "│"); count != len(headerPages)-1 {
			t.Fatalf("navbar dividers=%d want=%d: %q", count, len(headerPages)-1, ansi.Strip(header))
		}
	}
}

func TestModelHidesNavbarWhenTerminalIsTooNarrow(t *testing.T) {
	model := NewModel(Route{Kind: RouteRequests})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	model = updated.(Model)
	if model.frameMetrics(40, 20).showNavbar {
		t.Fatal("narrow terminal kept navbar visible")
	}
	_, targets := model.render()
	for _, target := range targets {
		if strings.HasPrefix(target.ID, "app.header.") {
			t.Fatalf("hidden navbar exposed mouse target %q", target.ID)
		}
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	model = updated.(Model)
	if !model.frameMetrics(60, 20).showNavbar {
		t.Fatal("compact navbar did not restore at usable width")
	}
	header, _ := model.header(56, 2, 1)
	plain := ansi.Strip(header)
	if !strings.Contains(plain, "Instr") || strings.Contains(plain, "Instruction") {
		t.Fatalf("compact header=%q", plain)
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	model = updated.(Model)
	header, _ = model.header(96, 2, 1)
	if plain = ansi.Strip(header); !strings.Contains(plain, "Instruction") {
		t.Fatalf("full header=%q", plain)
	}
}

func TestModelNumberKeysDoNotSwitchHeaderPages(t *testing.T) {
	for _, value := range "1234567" {
		model := NewModel(Route{Kind: RouteHome})
		updated, _ := model.Update(tea.KeyPressMsg{Code: value, Text: string(value)})
		model = updated.(Model)
		query := ""
		if model.homeCommands != nil {
			query = model.homeCommands.Query()
		}
		if model.router.Current().Kind != RouteHome || query != string(value) {
			t.Fatalf("number %q route=%s query=%q", value, model.router.Current().Kind, query)
		}
	}
}

func TestHomeEmbedsCenteredCommandPanel(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	if model.homeCommands == nil || model.palette != nil || model.overlay != overlayNone {
		t.Fatalf("home commands=%v palette=%v overlay=%d", model.homeCommands != nil, model.palette != nil, model.overlay)
	}
	plain := ansi.Strip(model.homeView(100, 28))
	for _, want := range []string{"Commands", "Type a command or resource", "Ctrl+K", "Enter run", "Esc exit", "Alt+←/→ pages"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("home panel missing %q: %q", want, plain)
		}
	}
	lines := strings.Split(plain, "\n")
	first, last := -1, -1
	for index, line := range lines {
		if strings.TrimSpace(line) != "" {
			if first < 0 {
				first = index
			}
			last = index
		}
	}
	if first <= 0 || last >= len(lines)-1 {
		t.Fatalf("command panel is not vertically centered: first=%d last=%d height=%d", first, last, len(lines))
	}
}

func TestHomeCommandPanelKeepsGlobalNavigationAndEscape(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	model = updated.(Model)
	query := "<nil>"
	if model.homeCommands != nil {
		query = model.homeCommands.Query()
	}
	if query != "l" {
		t.Fatalf("home query=%q", query)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
	model = updated.(Model)
	if model.router.Current().Kind != RouteWorkspaces || model.homeCommands != nil {
		t.Fatalf("alt+right route=%s homeCommands=%v", model.router.Current().Kind, model.homeCommands != nil)
	}
	model.switchPage(Route{Kind: RouteHome})
	if model.homeCommands == nil {
		t.Fatal("returning home did not restore embedded command panel")
	}
	_, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("home escape did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("home escape message=%T", cmd())
	}
}

func TestModelQuitAndBack(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	model.router.Navigate(Route{Kind: RouteConfig})
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if model.router.Current().Kind != RouteHome {
		t.Fatalf("route = %#v", model.router.Current())
	}
	updated, _ = model.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	model = updated.(Model)
	query := ""
	if model.homeCommands != nil {
		query = model.homeCommands.Query()
	}
	if model.overlay != overlayNone || query != "q" {
		t.Fatalf("q triggered quit flow: overlay=%d query=%q", model.overlay, query)
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("root escape did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("root escape message=%T", cmd())
	}
}

func TestModelReplaceNavigationDropsDeletedResourceRoute(t *testing.T) {
	model := NewModel(Route{Kind: RouteWorkspaces})
	model.router.Navigate(Route{Kind: RouteWorkspaces, ResourceID: "ws_deleted"})
	updated, cmd := model.Update(tuipage.NavigateMsg{Path: []string{"workspaces"}, Replace: true})
	model = updated.(Model)
	if current := model.router.Current(); current != (Route{Kind: RouteWorkspaces}) {
		t.Fatalf("replace current=%#v", current)
	}
	if len(model.router.stack) != 1 || model.router.stack[0] != (Route{Kind: RouteWorkspaces}) {
		t.Fatalf("replace stack=%#v", model.router.stack)
	}
	if cmd != nil {
		updated, _ = model.Update(cmd())
		model = updated.(Model)
	}
}

func TestModelEscBacksToCurrentMainThenHomeThenQuits(t *testing.T) {
	model := NewModel(Route{Kind: RouteWorkspaces, ResourceID: "ws_previous"})
	model.navigate(Route{Kind: RouteLogs})
	model.router.Navigate(Route{Kind: RouteLogs, ResourceID: "event_current"})
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if model.router.Current() != (Route{Kind: RouteLogs}) {
		t.Fatalf("first escape route=%#v stack=%#v", model.router.Current(), model.router.stack)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if model.router.Current() != (Route{Kind: RouteHome}) {
		t.Fatalf("second escape route=%#v stack=%#v", model.router.Current(), model.router.stack)
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("third escape did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("third escape message=%T", cmd())
	}
}

func TestModelInstructionTabDeepLinkIsRoot(t *testing.T) {
	route := Route{Kind: RouteInstruction, Section: "rules"}
	model := NewModel(route)
	if model.router.Current() != route || len(model.router.stack) != 1 {
		t.Fatalf("instruction route=%#v stack=%#v", model.router.Current(), model.router.stack)
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if cmd != nil || model.router.Current() != (Route{Kind: RouteHome}) {
		t.Fatalf("escape route=%#v cmd=%v", model.router.Current(), cmd != nil)
	}
}

func TestModelInstructionRuleEditorDeepLinkLoadsRoutedEditor(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	value := instructionpolicy.DefaultConfig()
	value.Rules = []instructionpolicy.GlobalRule{{ID: "rule_one", Name: "One rule", Enabled: true, Content: "Always verify."}}
	if err := instructionpolicy.DefaultStore().Save(value); err != nil {
		t.Fatal(err)
	}
	route := Route{Kind: RouteInstruction, Section: "rules", ResourceID: "rule_one", Action: "edit"}
	model := NewModel(route)
	if model.notice != "" || model.router.Current() != route || len(model.router.stack) != 2 {
		t.Fatalf("route=%#v stack=%#v notice=%q", model.router.Current(), model.router.stack, model.notice)
	}
	plain := ansi.Strip(model.View().Content)
	for _, want := range []string{"Rules", "Edit rule_one", "enter next"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rule editor missing %q: %q", want, plain)
		}
	}
	guard, ok := model.currentPage.(tuipage.NavigationGuardModel)
	if !ok || guard.Dirty() || !model.currentPage.InputActive() {
		t.Fatalf("editor guard=%t dirty=%t input=%t", ok, ok && guard.Dirty(), model.currentPage.InputActive())
	}
}

func TestModelInstructionContextEditorNavigationUsesDirtyGuard(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	model := NewModel(Route{Kind: RouteInstruction, Section: "context", Action: "edit"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	model = updated.(Model)
	guard, ok := model.currentPage.(tuipage.NavigationGuardModel)
	if !ok || !guard.Dirty() {
		t.Fatalf("context guard=%t dirty=%t", ok, ok && guard.Dirty())
	}
	page, ok := model.currentPage.(mousePage)
	if !ok {
		t.Fatal("instruction page does not expose mouse targets")
	}
	targets := page.MouseTargets(0, 0, 10)
	var tabs []component.MouseTarget
	for _, target := range targets {
		if target.ID == "instruction.tab" {
			tabs = append(tabs, target)
		}
	}
	if len(tabs) < 2 {
		t.Fatalf("instruction tab targets=%d", len(tabs))
	}
	updated, cmd := model.Update(tabs[1].Handle(component.MouseEvent{Button: tea.MouseLeft}))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("rules tab click produced no navigation command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.pendingNavigation == nil || model.router.Current() != (Route{Kind: RouteInstruction, Section: "context", Action: "edit"}) {
		t.Fatalf("dirty context navigation escaped: route=%#v pending=%v", model.router.Current(), model.pendingNavigation != nil)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Discard changes?") {
		t.Fatal("dirty context navigation did not render discard guard")
	}
}

func TestModelBackspaceStaysInsideDirtyInputEditor(t *testing.T) {
	model := NewModel(Route{Kind: RouteMCP})
	page := &navigationGuardTestPage{dirty: true, input: true}
	model.currentPage = page
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation != nil || model.router.Current().Kind != RouteMCP {
		t.Fatalf("backspace escaped editor: route=%s pending=%v cmd=%v", model.router.Current().Kind, model.pendingNavigation != nil, cmd != nil)
	}
	if strings.Join(page.keys, ",") != "backspace" {
		t.Fatalf("editor keys=%v", page.keys)
	}
}

func TestModelOpensAndRunsCommands(t *testing.T) {
	model := NewModel(Route{Kind: RouteAbout})
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	model = updated.(Model)
	if model.palette == nil {
		t.Fatal("Ctrl+K did not open commands")
	}
	model.palette.SetQuery("logs")
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("palette enter returned no command")
	}
	updated, command = model.Update(command())
	model = updated.(Model)
	if command == nil {
		t.Fatal("palette action returned no navigation command")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.router.Current().Kind != RouteLogs || model.palette != nil {
		t.Fatalf("route=%#v palette=%v", model.router.Current(), model.palette != nil)
	}
}

func TestModelCommandsOnlyUsesCtrlK(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: 'p', Mod: tea.ModCtrl}, {Code: 'o', Mod: tea.ModCtrl}, {Code: 'k', Mod: tea.ModCtrl | tea.ModShift}, {Text: ":", Code: ':'}} {
		model := NewModel(Route{Kind: RouteHome})
		updated, _ := model.Update(key)
		model = updated.(Model)
		if model.palette != nil {
			t.Fatalf("%q unexpectedly opened palette", key.String())
		}
	}
}

func TestModelFooterKeepsOnlyGlobalShortcuts(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	footer := ansi.Strip(model.shortcutFooter())
	for _, want := range []string{"ctrl+k commands", "alt+←/→ pages", "esc quit"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q: %q", want, footer)
		}
	}
	if strings.Contains(footer, "ctrl+p") || strings.Contains(footer, "ctrl+o") || strings.Contains(footer, " open") {
		t.Fatalf("footer retained old command/open shortcuts: %q", footer)
	}
	if strings.Contains(footer, "Commands") || strings.Contains(footer, "Quit") || strings.Contains(footer, "esc back") || strings.Contains(footer, "q quit") {
		t.Fatalf("footer=%q", footer)
	}
	model.router.Navigate(Route{Kind: RouteLogs})
	footer = ansi.Strip(model.shortcutFooter())
	if !strings.Contains(footer, "esc home") {
		t.Fatalf("main page footer=%q", footer)
	}
	model.router.Navigate(Route{Kind: RouteLogs, ResourceID: "event_demo"})
	footer = ansi.Strip(model.shortcutFooter())
	if !strings.Contains(footer, "esc back") {
		t.Fatalf("child footer=%q", footer)
	}
}

func TestModelHeaderPageCyclingWrapsWithoutGrowingHistory(t *testing.T) {
	model := NewModel(Route{Kind: RouteWorkspaces})
	for range 3 {
		updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
		model = updated.(Model)
		if model.router.Current().Kind != RouteRuntime {
			t.Fatalf("left wrap route=%#v", model.router.Current())
		}
		updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
		model = updated.(Model)
		if model.router.Current().Kind != RouteWorkspaces {
			t.Fatalf("right wrap route=%#v", model.router.Current())
		}
	}
	if len(model.router.stack) != 1 {
		t.Fatalf("sibling page cycling grew route history: %#v", model.router.stack)
	}
}

func TestModelHeaderMouseClickUsesTypedNavigation(t *testing.T) {
	model := NewModel(Route{Kind: RouteWorkspaces})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	model = updated.(Model)
	view := model.View()
	if view.OnMouse == nil || view.MouseMode == tea.MouseModeNone {
		t.Fatal("mouse support is not enabled")
	}
	_, targets := model.render()
	var target *component.MouseTarget
	for index := range targets {
		if targets[index].ID == "app.header.config" {
			target = &targets[index]
			break
		}
	}
	if target == nil {
		t.Fatal("config header hitbox not found")
	}
	cmd := view.OnMouse(tea.MouseClickMsg(tea.Mouse{X: target.Rect.X, Y: target.Rect.Y, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("header click produced no command")
	}
	message := cmd()
	navigation, ok := message.(navigateMsg)
	if !ok || navigation.route.Kind != RouteConfig || !navigation.sibling {
		t.Fatalf("header click message=%#v", message)
	}
	updated, _ = model.Update(message)
	model = updated.(Model)
	if model.router.Current().Kind != RouteConfig {
		t.Fatalf("route=%#v", model.router.Current())
	}
}

func TestModelBreadcrumbRendersAndNavigatesAncestors(t *testing.T) {
	model := NewModel(Route{Kind: RouteWorkspaces, ResourceID: "ws_demo", Section: "context"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	model = updated.(Model)
	plain := ansi.Strip(model.View().Content)
	for _, want := range []string{"Workspaces", "ws_demo", "Project Context", " / "} {
		if !strings.Contains(plain, want) {
			t.Fatalf("breadcrumb missing %q: %q", want, plain)
		}
	}
	metrics := model.frameMetrics(120, 32)
	if !metrics.showBreadcrumb || metrics.bodyY != metrics.breadcrumbY+2 {
		t.Fatalf("breadcrumb spacing y=%d body=%d visible=%t", metrics.breadcrumbY, metrics.bodyY, metrics.showBreadcrumb)
	}
	lines := strings.Split(plain, "\n")
	for index, line := range lines {
		if !strings.Contains(line, "Project Context") {
			continue
		}
		if index+1 >= len(lines) || strings.TrimSpace(strings.Trim(lines[index+1], "│")) != "" {
			t.Fatalf("breadcrumb is not followed by one empty row: index=%d next=%q", index, lines[index+1])
		}
		break
	}
	_, targets := model.render()
	var middle tea.Msg
	for _, target := range targets {
		if target.ID != "app.breadcrumb" {
			continue
		}
		message := target.Handle(component.MouseEvent{Button: tea.MouseLeft})
		if navigation, ok := message.(navigateMsg); ok && navigation.route.ResourceID == "ws_demo" && navigation.route.Section == "" {
			middle = message
			break
		}
	}
	if middle == nil {
		t.Fatal("workspace breadcrumb ancestor target not found")
	}
	updated, _ = model.Update(middle)
	model = updated.(Model)
	if model.router.Current() != (Route{Kind: RouteWorkspaces, ResourceID: "ws_demo"}) || len(model.router.stack) != 2 {
		t.Fatalf("middle breadcrumb route=%#v stack=%#v", model.router.Current(), model.router.stack)
	}
	_, targets = model.render()
	var root tea.Msg
	for _, target := range targets {
		if target.ID != "app.breadcrumb" {
			continue
		}
		message := target.Handle(component.MouseEvent{Button: tea.MouseLeft})
		if navigation, ok := message.(navigateMsg); ok && navigation.route == (Route{Kind: RouteWorkspaces}) {
			root = message
			break
		}
	}
	if root == nil {
		t.Fatal("workspace breadcrumb root target not found")
	}
	updated, _ = model.Update(root)
	model = updated.(Model)
	if model.router.Current() != (Route{Kind: RouteWorkspaces}) || len(model.router.stack) != 1 {
		t.Fatalf("root breadcrumb route=%#v stack=%#v", model.router.Current(), model.router.stack)
	}
}

func TestModelBreadcrumbNavigationRespectsDirtyGuard(t *testing.T) {
	model := NewModel(Route{Kind: RouteInstruction, Section: "context", Action: "edit"})
	model.currentPage = &navigationGuardTestPage{dirty: true, input: true}
	_, targets := model.breadcrumb(96, 2, 3)
	var message tea.Msg
	for _, target := range targets {
		candidate := target.Handle(component.MouseEvent{Button: tea.MouseLeft})
		if navigation, ok := candidate.(navigateMsg); ok && navigation.route == (Route{Kind: RouteInstruction, Section: "context"}) {
			message = candidate
			break
		}
	}
	if message == nil {
		t.Fatal("instruction context breadcrumb root target not found")
	}
	updated, cmd := model.Update(message)
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation == nil || model.router.Current() != (Route{Kind: RouteInstruction, Section: "context", Action: "edit"}) {
		t.Fatalf("breadcrumb bypassed dirty guard: route=%#v pending=%v cmd=%v", model.router.Current(), model.pendingNavigation != nil, cmd != nil)
	}
}

func TestModelBreadcrumbReleaseDoesNotDismissDirtyGuard(t *testing.T) {
	model := NewModel(Route{Kind: RouteContainers, ResourceID: "wsc_demo", Section: "workspaces", Action: "edit"})
	model.currentPage = &navigationGuardTestPage{dirty: true, input: true}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	model = updated.(Model)
	view := model.View()
	_, targets := model.render()
	var target *component.MouseTarget
	for index := range targets {
		if targets[index].ID == "app.breadcrumb" {
			target = &targets[index]
			break
		}
	}
	if target == nil {
		t.Fatal("container breadcrumb target not found")
	}
	x, y := target.Rect.X, target.Rect.Y
	cmd := view.OnMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("breadcrumb click produced no command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.pendingNavigation == nil {
		t.Fatal("breadcrumb click did not open discard guard")
	}
	guardedView := model.View()
	if cmd := guardedView.OnMouse(tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})); cmd != nil {
		t.Fatal("breadcrumb release dismissed discard guard")
	}
	if model.pendingNavigation == nil || model.router.Current() != (Route{Kind: RouteContainers, ResourceID: "wsc_demo", Section: "workspaces", Action: "edit"}) {
		t.Fatalf("discard guard changed after release: route=%#v pending=%v", model.router.Current(), model.pendingNavigation != nil)
	}
}

func TestModelPreservePageNavigationReflowsForBreadcrumb(t *testing.T) {
	model := NewModel(Route{Kind: RouteLogs})
	page := &navigationGuardTestPage{}
	model.currentPage = page
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	rootHeight := page.height
	if rootHeight != model.frameMetrics(100, 30).bodyHeight {
		t.Fatalf("root page height=%d metrics=%d", rootHeight, model.frameMetrics(100, 30).bodyHeight)
	}

	updated, cmd := model.Update(tuipage.NavigateMsg{Path: []string{"logs", "filter"}, Replace: true, PreservePage: true})
	model = updated.(Model)
	childMetrics := model.frameMetrics(100, 30)
	if cmd != nil || model.router.Current() != (Route{Kind: RouteLogs, Action: "filter"}) || !childMetrics.showBreadcrumb {
		t.Fatalf("child route=%#v breadcrumb=%t cmd=%v", model.router.Current(), childMetrics.showBreadcrumb, cmd != nil)
	}
	if page.height != childMetrics.bodyHeight || page.height >= rootHeight {
		t.Fatalf("child page height=%d metrics=%d root=%d", page.height, childMetrics.bodyHeight, rootHeight)
	}

	updated, cmd = model.Update(tuipage.NavigateMsg{Path: []string{"logs"}, Replace: true, PreservePage: true})
	model = updated.(Model)
	rootMetrics := model.frameMetrics(100, 30)
	if cmd != nil || model.router.Current() != (Route{Kind: RouteLogs}) || rootMetrics.showBreadcrumb {
		t.Fatalf("restored route=%#v breadcrumb=%t cmd=%v", model.router.Current(), rootMetrics.showBreadcrumb, cmd != nil)
	}
	if page.height != rootHeight || page.height != rootMetrics.bodyHeight {
		t.Fatalf("restored page height=%d root=%d metrics=%d", page.height, rootHeight, rootMetrics.bodyHeight)
	}
}

func TestModelTopLevelTabRouteDoesNotRenderBreadcrumbParent(t *testing.T) {
	model := NewModel(Route{Kind: RouteLogsExec})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	metrics := model.frameMetrics(100, 30)
	if metrics.showBreadcrumb {
		t.Fatalf("command execution root rendered breadcrumb: route=%#v", model.router.Current())
	}
	plain := ansi.Strip(model.View().Content)
	if strings.Contains(plain, "Logs  /  Command Execution") {
		t.Fatalf("command execution root rendered parent breadcrumb: %q", plain)
	}
}

func TestModelSuppressesInheritedTraceObserver(t *testing.T) {
	events := 0
	ctx := tracepkg.WithObserver(context.Background(), func(tracepkg.Event) { events++ })
	model := NewModelWithContext(ctx, Route{Kind: RouteHome})
	if tracepkg.ObserverFromContext(model.ctx) != nil {
		t.Fatal("TUI retained inherited trace observer")
	}
	span := tracepkg.Start(model.ctx, "CONFIG", "config.persist", "Persisting configuration")
	span.EndMessage("Configuration persisted")
	if events != 0 {
		t.Fatalf("TUI emitted %d CLI trace events", events)
	}
}

func TestModelCommandsNavigateResource(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	model := NewModel(Route{Kind: RouteAbout})
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	model = updated.(Model)
	if model.palette == nil || model.overlay != overlayCommands {
		t.Fatal("Ctrl+K did not open merged commands")
	}
	model.palette.SetQuery("config")
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("Commands enter returned no command")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.router.Current().Kind != RouteConfig || model.palette != nil {
		t.Fatalf("route=%#v overlay=%d", model.router.Current(), model.overlay)
	}
}

func TestModelRoutesKeysToActiveDialogBeforeGlobalShortcuts(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	page := &captureOverlayPage{overlay: true}
	model.currentPage = page
	for _, message := range []tea.KeyPressMsg{
		{Code: 'k', Mod: tea.ModCtrl},
		{Code: 'c', Mod: tea.ModCtrl},
		{Code: 'q', Text: "q"},
		{Code: tea.KeyRight, Mod: tea.ModAlt},
		{Code: 'e', Text: "e"},
	} {
		updated, cmd := model.Update(message)
		model = updated.(Model)
		if cmd != nil {
			t.Fatalf("overlay key %q escaped to global command", message.String())
		}
		if model.palette != nil || model.router.Current().Kind != RouteHome {
			t.Fatalf("overlay key %q changed global UI palette=%v route=%s", message.String(), model.palette != nil, model.router.Current().Kind)
		}
	}
	want := []string{"ctrl+k", "ctrl+c", "q", "alt+right", "e"}
	if strings.Join(page.keys, ",") != strings.Join(want, ",") {
		t.Fatalf("captured keys=%v want=%v", page.keys, want)
	}
}

func TestModelRoutesKeysToActiveInputBeforeGlobalShortcuts(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	page := &captureOverlayPage{input: true}
	model.currentPage = page
	for _, message := range []tea.KeyPressMsg{
		{Code: 'k', Mod: tea.ModCtrl},
		{Code: 'c', Mod: tea.ModCtrl},
		{Code: 'q', Text: "q"},
		{Code: tea.KeyRight, Mod: tea.ModAlt},
		{Code: 'e', Text: "e"},
	} {
		updated, cmd := model.Update(message)
		model = updated.(Model)
		if cmd != nil || model.palette != nil || model.router.Current().Kind != RouteHome {
			t.Fatalf("input key %q escaped capture: cmd=%v palette=%v route=%s", message.String(), cmd, model.palette != nil, model.router.Current().Kind)
		}
	}
	want := []string{"ctrl+k", "ctrl+c", "q", "alt+right", "e"}
	if strings.Join(page.keys, ",") != strings.Join(want, ",") {
		t.Fatalf("captured keys=%v want=%v", page.keys, want)
	}
}

func TestModelSingleEscapeQuitsFromHome(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	_, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("escape did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("escape message=%T", cmd())
	}
}

type captureOverlayPage struct {
	overlay bool
	input   bool
	keys    []string
}

type confirmCapturePage struct{ affirmative *bool }

type navigationGuardTestPage struct {
	dirty      bool
	submitting bool
	input      bool
	keys       []string
	width      int
	height     int
}

func (*navigationGuardTestPage) Init() tea.Cmd { return nil }
func (page *navigationGuardTestPage) Update(message tea.Msg) (tuipage.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		page.keys = append(page.keys, key.String())
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		page.width, page.height = size.Width, size.Height
	}
	return page, nil
}
func (*navigationGuardTestPage) View(width, height int) string { return "editor" }
func (*navigationGuardTestPage) OverlayActive() bool           { return false }
func (page *navigationGuardTestPage) InputActive() bool        { return page.input }
func (page *navigationGuardTestPage) Dirty() bool              { return page.dirty }
func (page *navigationGuardTestPage) Submitting() bool         { return page.submitting }

func (*confirmCapturePage) Init() tea.Cmd { return nil }
func (page *confirmCapturePage) Update(message tea.Msg) (tuipage.Model, tea.Cmd) {
	if choice, ok := message.(component.ConfirmChoiceMsg); ok {
		value := choice.Affirmative
		page.affirmative = &value
	}
	return page, nil
}
func (*confirmCapturePage) View(width, height int) string { return "" }
func (*confirmCapturePage) OverlayActive() bool           { return true }
func (*confirmCapturePage) InputActive() bool             { return false }

func TestModelRoutesNonApprovalConfirmChoiceToPage(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	page := &confirmCapturePage{}
	model.currentPage = page
	updated, cmd := model.Update(component.ConfirmChoiceMsg{Affirmative: true})
	model = updated.(Model)
	if cmd != nil || page.affirmative == nil || !*page.affirmative {
		t.Fatalf("confirm was not routed to page: cmd=%v affirmative=%v", cmd, page.affirmative)
	}
}

func TestModelGuardsDirtyEditorNavigationAndCanKeepEditing(t *testing.T) {
	model := NewModel(Route{Kind: RouteMCP})
	page := &navigationGuardTestPage{dirty: true, input: true}
	model.currentPage = page
	updated, cmd := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	if cmd != nil || model.router.Current().Kind != RouteMCP || model.pendingNavigation == nil {
		t.Fatalf("dirty navigation escaped: route=%s pending=%v cmd=%v", model.router.Current().Kind, model.pendingNavigation != nil, cmd)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Discard changes?") {
		t.Fatal("discard confirmation not rendered")
	}
	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation != nil || model.router.Current().Kind != RouteMCP {
		t.Fatalf("keep editing failed: route=%s pending=%v cmd=%v", model.router.Current().Kind, model.pendingNavigation != nil, cmd)
	}
	updated, cmd = model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	model = updated.(Model)
	if cmd != nil || strings.Join(page.keys, ",") != "x" {
		t.Fatalf("ordinary editor input escaped guard routing: keys=%v cmd=%v", page.keys, cmd)
	}
}

func TestModelDiscardConfirmationPerformsPendingNavigation(t *testing.T) {
	model := NewModel(Route{Kind: RouteMCP})
	model.currentPage = &navigationGuardTestPage{dirty: true, input: true}
	updated, _ := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	updated, cmd := model.Update(component.ConfirmChoiceMsg{Affirmative: true})
	model = updated.(Model)
	if model.pendingNavigation != nil || model.router.Current().Kind != RouteAbout {
		t.Fatalf("discard route=%s pending=%v", model.router.Current().Kind, model.pendingNavigation != nil)
	}
	if cmd != nil {
		updated, _ = model.Update(cmd())
		model = updated.(Model)
	}
}

func TestModelSubmittingEditorBlocksNavigationWithoutDiscardDialog(t *testing.T) {
	model := NewModel(Route{Kind: RouteMCP})
	model.currentPage = &navigationGuardTestPage{dirty: true, submitting: true, input: true}
	updated, cmd := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	if cmd != nil || model.pendingNavigation != nil || model.router.Current().Kind != RouteMCP {
		t.Fatalf("submitting navigation route=%s pending=%v cmd=%v", model.router.Current().Kind, model.pendingNavigation != nil, cmd)
	}
}

func TestModelCleanEditorNavigatesImmediately(t *testing.T) {
	model := NewModel(Route{Kind: RouteMCP})
	model.currentPage = &navigationGuardTestPage{input: true}
	updated, cmd := model.Update(navigateMsg{route: Route{Kind: RouteAbout}, sibling: true})
	model = updated.(Model)
	if model.pendingNavigation != nil || model.router.Current().Kind != RouteAbout {
		t.Fatalf("clean navigation route=%s pending=%v", model.router.Current().Kind, model.pendingNavigation != nil)
	}
	if cmd != nil {
		updated, _ = model.Update(cmd())
		model = updated.(Model)
	}
}

func TestModelHeaderAndAltNavigationRespectDirtyEditorGuard(t *testing.T) {
	model := NewModel(Route{Kind: RouteMCP})
	page := &navigationGuardTestPage{dirty: true, input: true}
	model.currentPage = page
	_, targets := model.header(96, 2, 1)
	var headerMessage tea.Msg
	for _, target := range targets {
		if target.ID == "app.header.logs" {
			headerMessage = target.Handle(component.MouseEvent{Button: tea.MouseLeft})
			break
		}
	}
	if headerMessage == nil {
		t.Fatal("logs header target not found")
	}
	updated, _ := model.Update(headerMessage)
	model = updated.(Model)
	if model.pendingNavigation == nil || model.router.Current().Kind != RouteMCP {
		t.Fatalf("header bypassed guard: route=%s pending=%v", model.router.Current().Kind, model.pendingNavigation != nil)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
	model = updated.(Model)
	if model.pendingNavigation == nil || model.router.Current().Kind != RouteMCP {
		t.Fatalf("alt navigation bypassed guard: route=%s pending=%v", model.router.Current().Kind, model.pendingNavigation != nil)
	}
}

func (page *captureOverlayPage) Init() tea.Cmd { return nil }

func (page *captureOverlayPage) Update(message tea.Msg) (tuipage.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		page.keys = append(page.keys, key.String())
	}
	return page, nil
}

func (page *captureOverlayPage) View(width, height int) string { return "" }
func (page *captureOverlayPage) OverlayActive() bool           { return page.overlay }
func (page *captureOverlayPage) InputActive() bool             { return page.input }

func TestModelPendingApprovalOverlaysEveryRoute(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	request := testPendingApproval("req_global")
	for _, route := range []Route{{Kind: RouteHome}, {Kind: RouteWorkspaces}, {Kind: RouteContainers}, {Kind: RouteMCP}, {Kind: RouteTunnel}, {Kind: RouteTunnels}, {Kind: RouteRequests}, {Kind: RouteLogs}, {Kind: RouteConfig}, {Kind: RouteInstruction}, {Kind: RouteRuntime}, {Kind: RouteAbout}} {
		t.Run(string(route.Kind), func(t *testing.T) {
			model := NewModel(route)
			updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			model = updated.(Model)
			model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
			plain := ansi.Strip(model.View().Content)
			for _, want := range []string{"Approval request", request.ID, request.WorkspaceID, request.TargetTool, "echo hello"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("route %s approval overlay missing %q: %q", route.Kind, want, plain)
				}
			}
		})
	}
}

func TestModelPendingApprovalSupersedesEveryInteractiveState(t *testing.T) {
	request := testPendingApproval("req_blocking")
	for _, test := range []struct {
		name  string
		setup func(*Model) *captureOverlayPage
	}{
		{name: "plain"},
		{name: "palette", setup: func(model *Model) *captureOverlayPage { _ = model.openCommands(); return nil }},
		{name: "page-overlay", setup: func(model *Model) *captureOverlayPage {
			page := &captureOverlayPage{overlay: true}
			model.currentPage = page
			return page
		}},
		{name: "page-input", setup: func(model *Model) *captureOverlayPage {
			page := &captureOverlayPage{input: true}
			model.currentPage = page
			return page
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(Route{Kind: RouteHome})
			var page *captureOverlayPage
			if test.setup != nil {
				page = test.setup(&model)
			}
			model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
			beforeRoute, beforeOverlay, beforePalette := model.router.Current(), model.overlay, model.palette
			for _, key := range []tea.KeyPressMsg{{Code: 'p', Mod: tea.ModCtrl}, {Code: 'o', Mod: tea.ModCtrl}, {Code: tea.KeyRight, Mod: tea.ModAlt}, {Code: tea.KeyEscape}} {
				updated, cmd := model.Update(key)
				model = updated.(Model)
				if cmd != nil {
					t.Fatalf("approval key %q escaped with command", key.String())
				}
			}
			if model.router.Current() != beforeRoute || model.overlay != beforeOverlay || model.palette != beforePalette {
				t.Fatalf("approval changed underlying state: route=%#v overlay=%d paletteChanged=%t", model.router.Current(), model.overlay, model.palette != beforePalette)
			}
			if page != nil && len(page.keys) != 0 {
				t.Fatalf("approval leaked keys to page: %v", page.keys)
			}
			if !strings.Contains(ansi.Strip(model.View().Content), request.ID) {
				t.Fatal("approval overlay disappeared")
			}
		})
	}
}

func TestModelApprovalResolvesDirectlyAndAdvancesQueue(t *testing.T) {
	first, second := testPendingApproval("req_first"), testPendingApproval("req_second")
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{first, second}})
	type resolution struct {
		id      string
		approve bool
		reason  string
	}
	var resolutions []resolution
	model.approvalResolve = func(_ context.Context, id string, approve bool, reason string) (approval.Request, error) {
		resolutions = append(resolutions, resolution{id: id, approve: approve, reason: reason})
		return approval.Request{ID: id}, nil
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	model = updated.(Model)
	if cmd == nil || model.approvalStage != approvalStageResolving || !model.approvalApprove || len(resolutions) != 0 {
		t.Fatalf("approve did not resolve directly: stage=%d approve=%t cmd=%v resolutions=%v", model.approvalStage, model.approvalApprove, cmd, resolutions)
	}
	updated, follow := model.Update(cmd())
	model = updated.(Model)
	if follow == nil || model.activeApprovalID() != second.ID || model.approvalStage != approvalStageChoice || model.toast.message != "Approved "+first.ID {
		t.Fatalf("queue did not advance: active=%q stage=%d follow=%v", model.activeApprovalID(), model.approvalStage, follow)
	}
	if len(resolutions) != 1 || resolutions[0].id != first.ID || !resolutions[0].approve || resolutions[0].reason != "" {
		t.Fatalf("approval resolution=%v", resolutions)
	}
	updated, _ = model.Update(toastCloseMsg{})
	model = updated.(Model)
	updated, cmd = model.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	model = updated.(Model)
	if cmd == nil || model.approvalStage != approvalStageResolving || model.approvalApprove {
		t.Fatalf("deny did not resolve directly: stage=%d approve=%t cmd=%v", model.approvalStage, model.approvalApprove, cmd)
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.approvalActive() || len(model.approvals) != 0 {
		t.Fatalf("approval queue not cleared: %#v", model.approvals)
	}
	if len(resolutions) != 2 || resolutions[1].id != second.ID || resolutions[1].approve {
		t.Fatalf("deny resolution=%v", resolutions)
	}
}

func TestModelApprovalAllowsSimilarCommandsForRuntimeSession(t *testing.T) {
	request := testPendingApproval("req_similar")
	request.SimilarCommandPattern = "git push **"
	request.Command = "git push origin main"
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
	if view := model.approvalDialogView(96); !strings.Contains(view, "Similar pattern") || !strings.Contains(view, "git push **") || !strings.Contains(view, "s allow similar for all MCP sessions") {
		t.Fatalf("runtime-session approval option missing:\n%s", view)
	}
	called := false
	model.approvalResolveSimilar = func(_ context.Context, id string, approve, similar bool, reason string) (approval.Request, error) {
		called = id == request.ID && approve && similar && reason == ""
		return approval.Request{ID: id}, nil
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	model = updated.(Model)
	if cmd == nil || model.approvalStage != approvalStageResolving || !model.approvalApprove || !model.approvalSimilar {
		t.Fatalf("similar approval did not enter resolving state: stage=%d approve=%t similar=%t", model.approvalStage, model.approvalApprove, model.approvalSimilar)
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if !called || model.toast.message != "Approved similar commands for all MCP sessions (1h) "+request.ID {
		t.Fatalf("runtime-session resolution called=%t toast=%q", called, model.toast.message)
	}
}

func TestModelApprovalPollFiltersStatusesAndSurvivesErrors(t *testing.T) {
	pending, resolved := testPendingApproval("req_pending"), testPendingApproval("req_resolved")
	resolved.Status = approval.StatusApproved
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{resolved, pending}})
	if len(model.approvals) != 1 || model.activeApprovalID() != pending.ID || !model.approvalActive() {
		t.Fatalf("pending filter=%#v", model.approvals)
	}
	model.applyApprovalPoll(approvalPollMsg{err: errors.New("runtime temporarily unavailable")})
	if model.activeApprovalID() != pending.ID || !model.approvalActive() {
		t.Fatal("transient poll error cleared pending approval")
	}
	model.applyApprovalPoll(approvalPollMsg{requests: nil})
	if model.approvalActive() || len(model.approvals) != 0 {
		t.Fatalf("empty successful poll did not clear dialog: %#v", model.approvals)
	}
}

func TestModelApprovalDialogShowsLiveExpiryCountdown(t *testing.T) {
	now := time.Date(2026, 9, 8, 11, 0, 0, 0, time.Local)
	request := testPendingApproval("req_countdown")
	request.ExpiresAt = now.Add(65 * time.Second)
	model := NewModel(Route{Kind: RouteHome})
	model.approvalNow = func() time.Time { return now }
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
	plain := ansi.Strip(model.approvalDialogView(80))
	if !strings.Contains(plain, "Expires in") || !strings.Contains(plain, "00:01:05") || !strings.Contains(plain, request.ExpiresAt.Local().Format("15:04:05")) {
		t.Fatalf("approval countdown view=%q", plain)
	}
	now = now.Add(5 * time.Second)
	plain = ansi.Strip(model.approvalDialogView(80))
	if !strings.Contains(plain, "00:01:00") {
		t.Fatalf("approval countdown did not advance: %q", plain)
	}
}

func TestModelApprovalTickExpiresRequestAndAdvancesQueue(t *testing.T) {
	now := time.Date(2026, 9, 8, 11, 0, 0, 0, time.Local)
	first, second := testPendingApproval("req_expiring"), testPendingApproval("req_next")
	first.ExpiresAt = now.Add(time.Second)
	second.ExpiresAt = now.Add(time.Minute)
	model := NewModel(Route{Kind: RouteHome})
	model.approvalNow = func() time.Time { return now }
	model.approvalList = func(context.Context) ([]approval.Request, error) { return []approval.Request{second}, nil }
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{first, second}})
	if model.activeApprovalID() != first.ID {
		t.Fatalf("active approval=%q", model.activeApprovalID())
	}
	now = now.Add(time.Second)
	updated, poll := model.Update(approvalPollTickMsg{})
	model = updated.(Model)
	if model.activeApprovalID() != second.ID || model.approvalStage != approvalStageChoice {
		t.Fatalf("expired approval did not advance: active=%q stage=%d approvals=%#v", model.activeApprovalID(), model.approvalStage, model.approvals)
	}
	if poll == nil {
		t.Fatal("expiry tick did not continue runtime poll")
	}
}

func TestModelApprovalCannotResolveAfterExpiry(t *testing.T) {
	now := time.Date(2026, 9, 8, 11, 0, 0, 0, time.Local)
	request := testPendingApproval("req_expired_action")
	request.ExpiresAt = now.Add(time.Second)
	model := NewModel(Route{Kind: RouteHome})
	model.approvalNow = func() time.Time { return now }
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
	resolved := false
	model.approvalResolve = func(context.Context, string, bool, string) (approval.Request, error) {
		resolved = true
		return approval.Request{}, nil
	}
	now = now.Add(time.Second)
	updated, poll := model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	model = updated.(Model)
	if resolved || model.approvalActive() || len(model.approvals) != 0 {
		t.Fatalf("expired approval resolved=%t active=%t approvals=%#v", resolved, model.approvalActive(), model.approvals)
	}
	if poll == nil {
		t.Fatal("expired action did not refresh approval state")
	}
}

func TestApprovalCountdownRoundsPositiveRemainderUp(t *testing.T) {
	now := time.Unix(0, 0)
	if got := approvalCountdown(now.Add(1500*time.Millisecond), now); got != "00:00:02" {
		t.Fatalf("countdown=%q", got)
	}
	if got := approvalCountdown(now, now); got != "00:00:00" {
		t.Fatalf("expired countdown=%q", got)
	}
}

func TestModelApprovalOverlayKeepsExactGeometry(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{testPendingApproval("req_geometry")}})
	for _, size := range [][2]int{{120, 40}, {20, 8}, {3, 3}, {1, 1}} {
		updated, _ := model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model = updated.(Model)
		view := model.View().Content
		if width, height := lipgloss.Width(view), lipgloss.Height(view); width != size[0] || height != size[1] {
			t.Fatalf("approval layout=%dx%d want=%dx%d", width, height, size[0], size[1])
		}
	}
}

func TestModelApprovalDialogCapsHeightAndScrollsContent(t *testing.T) {
	request := testPendingApproval("req_scroll")
	request.Title = strings.Repeat("Long approval title ", 8)
	request.Arguments = json.RawMessage(`{"command":"` + strings.Repeat("echo very-long-argument ", 40) + `"}`)
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 76, Height: 18})
	model = updated.(Model)
	view := model.View().Content
	plain := ansi.Strip(view)
	if width, height := lipgloss.Width(view), lipgloss.Height(view); width != 76 || height != 18 {
		t.Fatalf("approval geometry=%dx%d want=76x18", width, height)
	}
	for _, want := range []string{"Approve", "Deny", "j/k scroll"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("approval fixed footer missing %q: %q", want, plain)
		}
	}
	if model.approvalViewport.TotalLineCount() <= model.approvalViewport.Height() {
		t.Fatalf("approval content did not become scrollable: lines=%d height=%d", model.approvalViewport.TotalLineCount(), model.approvalViewport.Height())
	}
	before := model.approvalViewport.YOffset()
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(Model)
	if model.approvalViewport.YOffset() <= before {
		t.Fatalf("approval viewport did not scroll: before=%d after=%d", before, model.approvalViewport.YOffset())
	}
}

func TestModelApprovalResolutionErrorKeepsRequestVisible(t *testing.T) {
	request := testPendingApproval("req_error")
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
	model.approvalResolve = func(context.Context, string, bool, string) (approval.Request, error) {
		return approval.Request{}, errors.New("resolution failed")
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	model = updated.(Model)
	updated, follow := model.Update(cmd())
	model = updated.(Model)
	if follow != nil || model.activeApprovalID() != request.ID || model.approvalStage != approvalStageChoice || model.approvalErr == nil {
		t.Fatalf("failed resolution state: active=%q stage=%d follow=%v err=%v", model.activeApprovalID(), model.approvalStage, follow, model.approvalErr)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "resolution failed") {
		t.Fatal("resolution error not rendered")
	}
}

func testPendingApproval(id string) approval.Request {
	return approval.Request{ID: id, Status: approval.StatusPending, WorkspaceID: "ws_demo", Source: "tunnel", TargetTool: "run_command", Title: "Allow command", Command: "echo hello", Arguments: json.RawMessage(`{"command":"echo hello","workspace_id":"ws_demo"}`)}
}

func TestModelInitPollsPendingApprovals(t *testing.T) {
	request := testPendingApproval("req_init")
	model := NewModel(Route{Kind: RouteHome})
	model.approvalList = func(context.Context) ([]approval.Request, error) { return []approval.Request{request}, nil }
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("model init did not start approval poll")
	}
	message, ok := cmd().(approvalPollMsg)
	if !ok {
		t.Fatalf("init message=%T", cmd())
	}
	updated, tick := model.Update(message)
	model = updated.(Model)
	if !model.approvalActive() || model.activeApprovalID() != request.ID || tick == nil {
		t.Fatalf("init approval state active=%t id=%q tick=%v", model.approvalActive(), model.activeApprovalID(), tick)
	}
}

func TestModelApprovalPollKeepsActiveRequestStableAcrossReorder(t *testing.T) {
	first, second := testPendingApproval("req_first"), testPendingApproval("req_second")
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{first, second}})
	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	model = updated.(Model)
	if cmd == nil || model.approvalStage != approvalStageResolving {
		t.Fatalf("approval did not begin resolving: stage=%d cmd=%v", model.approvalStage, cmd)
	}
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{second, first}})
	if model.activeApprovalID() != first.ID || model.approvalStage != approvalStageResolving || !model.approvalApprove {
		t.Fatalf("active approval changed after reorder: active=%q stage=%d approve=%t", model.activeApprovalID(), model.approvalStage, model.approvalApprove)
	}
}

func TestModelToastRendersAsDialogAcrossRoutes(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, route := range []Route{{Kind: RouteWorkspaces}, {Kind: RouteContainers}, {Kind: RouteMCP}, {Kind: RouteTunnel}, {Kind: RouteTunnels}, {Kind: RouteRequests}, {Kind: RouteLogs}, {Kind: RouteConfig}, {Kind: RouteInstruction}, {Kind: RouteRuntime}} {
		t.Run(string(route.Kind), func(t *testing.T) {
			model := NewModel(route)
			updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			model = updated.(Model)
			updated, dismiss := model.Update(tuipage.ToastMsg{Title: "Update", Message: "toast-inline", Tone: component.ToneSuccess})
			model = updated.(Model)
			if dismiss == nil || model.toast.id == 0 {
				t.Fatal("toast did not schedule dismissal")
			}
			plain := ansi.Strip(model.View().Content)
			for _, want := range []string{"✓ Update", "toast-inline", "Close"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("route %s dialog missing %q: %q", route.Kind, want, plain)
				}
			}
			if strings.Contains(plain, "· toast-inline") || strings.Count(plain, "Close") != 1 {
				t.Fatalf("route %s toast retained inline rendering or multiple close actions: %q", route.Kind, plain)
			}
			if width, height := lipgloss.Width(model.View().Content), lipgloss.Height(model.View().Content); width != 120 || height != 40 {
				t.Fatalf("route %s geometry=%dx%d", route.Kind, width, height)
			}
		})
	}
}

func TestModelToastDismissalDoesNotClearNewerToast(t *testing.T) {
	model := NewModel(Route{Kind: RouteRuntime})
	updated, _ := model.Update(tuipage.ToastMsg{Title: "First", Message: "one", Tone: component.ToneSuccess})
	model = updated.(Model)
	firstID, firstTimer := model.toast.id, model.toast.timer
	updated, _ = model.Update(tuipage.ToastMsg{Title: "Second", Message: "two", Tone: component.ToneWarning})
	model = updated.(Model)
	secondID, secondTimer := model.toast.id, model.toast.timer
	updated, _ = model.Update(toastDismissMsg{id: firstID, timer: firstTimer})
	model = updated.(Model)
	if model.toast.id != secondID || model.toast.message != "two" || pageNotice(model.currentPage) != "" {
		t.Fatalf("stale dismiss cleared newer toast: %#v", model.toast)
	}
	updated, _ = model.Update(toastDismissMsg{id: secondID, timer: secondTimer})
	model = updated.(Model)
	if model.toast.id != 0 || pageNotice(model.currentPage) != "" {
		t.Fatalf("matching dismiss did not clear toast: toast=%#v notice=%q", model.toast, pageNotice(model.currentPage))
	}
}

func TestModelToastKeepsExactGeometryAtTinySizes(t *testing.T) {
	model := NewModel(Route{Kind: RouteRuntime})
	updated, _ := model.Update(tuipage.ToastMsg{Title: "Update", Message: "done", Tone: component.ToneSuccess})
	model = updated.(Model)
	for _, size := range [][2]int{{40, 10}, {20, 6}, {3, 3}, {1, 1}} {
		updated, _ = model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model = updated.(Model)
		view := model.View().Content
		if width, height := lipgloss.Width(view), lipgloss.Height(view); width != size[0] || height != size[1] {
			t.Fatalf("toast layout=%dx%d want=%dx%d", width, height, size[0], size[1])
		}
	}
}

func TestModelPageToastAutoDismissDuration(t *testing.T) {
	if toastDuration != 3*time.Second {
		t.Fatalf("toast duration=%s", toastDuration)
	}
	model := NewModel(Route{Kind: RouteHome})
	model.currentPage = &noticeTestPage{}
	updated, dismiss := model.updatePage(noticeTestMsg("Created"))
	model = updated.(Model)
	if dismiss == nil || model.toast.id == 0 || model.toast.message != "Created" || pageNotice(model.currentPage) != "" {
		t.Fatalf("page notice did not start toast timer: toast=%#v notice=%q", model.toast, pageNotice(model.currentPage))
	}
	updated, _ = model.Update(toastDismissMsg{id: model.toast.id, timer: model.toast.timer})
	model = updated.(Model)
	if pageNotice(model.currentPage) != "" || model.toast.id != 0 {
		t.Fatalf("toast did not auto-clear state: toast=%#v notice=%q", model.toast, pageNotice(model.currentPage))
	}
}

func TestModelToastHoverPausesAndRestartsAutoDismiss(t *testing.T) {
	model := NewModel(Route{Kind: RouteRuntime})
	updated, _ := model.Update(tuipage.ToastMsg{Title: "Update", Message: "done", Tone: component.ToneSuccess})
	model = updated.(Model)
	id, initialTimer := model.toast.id, model.toast.timer
	updated, cmd := model.Update(toastHoverMsg{id: id, hovered: true})
	model = updated.(Model)
	if cmd != nil || !model.toast.hovered || model.toast.timer == initialTimer {
		t.Fatalf("hover did not pause timer: toast=%#v cmd=%v", model.toast, cmd)
	}
	updated, _ = model.Update(toastDismissMsg{id: id, timer: initialTimer})
	model = updated.(Model)
	if model.toast.id != id {
		t.Fatal("stale pre-hover timer dismissed toast")
	}
	pausedTimer := model.toast.timer
	updated, cmd = model.Update(toastHoverMsg{id: id, hovered: false})
	model = updated.(Model)
	if cmd == nil || model.toast.hovered || model.toast.timer == pausedTimer {
		t.Fatalf("leaving toast did not restart timer: toast=%#v cmd=%v", model.toast, cmd)
	}
	updated, _ = model.Update(toastDismissMsg{id: id, timer: pausedTimer})
	model = updated.(Model)
	if model.toast.id != id {
		t.Fatal("stale paused timer dismissed toast")
	}
	updated, _ = model.Update(toastDismissMsg{id: id, timer: model.toast.timer})
	model = updated.(Model)
	if model.toast.id != 0 {
		t.Fatalf("matching resumed timer did not dismiss toast: %#v", model.toast)
	}
}

func TestModelToastMouseHoverOutsideAndClose(t *testing.T) {
	model := NewModel(Route{Kind: RouteRuntime})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	updated, _ = model.Update(tuipage.ToastMsg{Title: "Update", Message: "done", Tone: component.ToneSuccess})
	model = updated.(Model)
	dialog := component.NewToastDialog(model.toast.title, model.toast.message, model.toast.tone)
	modalWidth := min(72, model.width-4)
	foreground := component.Modal(dialog.ViewWidth(component.ModalContentWidth(modalWidth)), modalWidth)
	_, x, y := component.CenteredOverlayTargets(foreground, model.width, model.height, 0, 0, 299, toastCloseMsg{})
	view := model.View()
	cmd := view.OnMouse(tea.MouseMotionMsg(tea.Mouse{X: x, Y: y}))
	if cmd == nil {
		t.Fatal("motion inside toast returned no command")
	}
	hover, ok := cmd().(toastHoverMsg)
	if !ok || !hover.hovered {
		t.Fatalf("inside motion=%#v", hover)
	}
	updated, _ = model.Update(hover)
	model = updated.(Model)
	view = model.View()
	cmd = view.OnMouse(tea.MouseMotionMsg(tea.Mouse{X: 0, Y: 0}))
	if cmd == nil {
		t.Fatal("motion outside toast returned no command")
	}
	leave, ok := cmd().(toastHoverMsg)
	if !ok || leave.hovered {
		t.Fatalf("outside motion=%#v", leave)
	}
	view = model.View()
	cmd = view.OnMouse(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("outside click returned no command")
	}
	closeMsg := cmd()
	if _, ok := closeMsg.(toastCloseMsg); !ok {
		t.Fatalf("outside click=%#v", closeMsg)
	}
	updated, _ = model.Update(closeMsg)
	model = updated.(Model)
	if model.toast.id != 0 {
		t.Fatalf("outside click did not close toast: %#v", model.toast)
	}
	updated, _ = model.Update(tuipage.ToastMsg{Title: "Update", Message: "done", Tone: component.ToneSuccess})
	model = updated.(Model)
	dialog = component.NewToastDialog(model.toast.title, model.toast.message, model.toast.tone)
	modalWidth = min(72, model.width-4)
	foreground = component.Modal(dialog.ViewWidth(component.ModalContentWidth(modalWidth)), modalWidth)
	_, x, y = component.CenteredOverlayTargets(foreground, model.width, model.height, 0, 0, 299, toastCloseMsg{})
	view = model.View()
	if rect, ok := component.FindRenderedRect(foreground, dialog.CloseButtonView()); ok {
		cmd = view.OnMouse(tea.MouseClickMsg(tea.Mouse{X: x + rect.X, Y: y + rect.Y, Button: tea.MouseLeft}))
		if cmd == nil {
			t.Fatal("close button click returned no command")
		}
		closeMsg = cmd()
		if _, ok := closeMsg.(toastCloseMsg); !ok {
			t.Fatalf("close button click=%#v", closeMsg)
		}
		updated, _ = model.Update(closeMsg)
		model = updated.(Model)
		if model.toast.id != 0 {
			t.Fatalf("close button did not close toast: %#v", model.toast)
		}
	} else {
		t.Fatal("close button rect not found")
	}
}

type noticeTestMsg string

type noticeTestPage struct{ notice string }

func (*noticeTestPage) Init() tea.Cmd { return nil }
func (page *noticeTestPage) Update(message tea.Msg) (tuipage.Model, tea.Cmd) {
	if value, ok := message.(noticeTestMsg); ok {
		page.notice = string(value)
	}
	return page, nil
}
func (page *noticeTestPage) View(width, height int) string {
	return component.PageTitleNotice("Test", page.notice, width)
}
func (*noticeTestPage) OverlayActive() bool         { return false }
func (*noticeTestPage) InputActive() bool           { return false }
func (page *noticeTestPage) Notice() string         { return page.notice }
func (page *noticeTestPage) SetNotice(value string) { page.notice = value }

type statusNoticeTestPage struct{ notice string }

func (*statusNoticeTestPage) Init() tea.Cmd { return nil }
func (page *statusNoticeTestPage) Update(message tea.Msg) (tuipage.Model, tea.Cmd) {
	if value, ok := message.(noticeTestMsg); ok {
		page.notice = string(value)
	}
	return page, nil
}
func (page *statusNoticeTestPage) View(width, height int) string {
	return component.PageTitleNotice("Test", page.notice, width)
}
func (*statusNoticeTestPage) OverlayActive() bool         { return false }
func (*statusNoticeTestPage) InputActive() bool           { return false }
func (page *statusNoticeTestPage) Notice() string         { return page.notice }
func (page *statusNoticeTestPage) SetNotice(value string) { page.notice = value }
func (*statusNoticeTestPage) ShouldToastNotice() bool     { return false }

func TestModelKeepsStatusOnlyNoticeOutOfToastDialog(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	model.currentPage = &statusNoticeTestPage{}
	updated, cmd := model.updatePage(noticeTestMsg("Live stream disconnected; reconnecting"))
	model = updated.(Model)
	if cmd != nil || model.toast.id != 0 || pageNotice(model.currentPage) == "" {
		t.Fatalf("status notice promoted to toast: toast=%#v notice=%q cmd=%v", model.toast, pageNotice(model.currentPage), cmd)
	}
}

func TestApprovalDialogWrapsLongArgumentsWithoutTruncation(t *testing.T) {
	token := strings.Repeat("z", 72)
	request := testPendingApproval("req_" + token)
	request.Title = "Run " + token
	request.WorkspaceID = "ws_" + token
	request.Arguments = json.RawMessage(`{"command":"` + token + `","cwd":"/very/long/` + token + `"}`)
	model := NewModel(Route{Kind: RouteHome})
	model.applyApprovalPoll(approvalPollMsg{requests: []approval.Request{request}})
	view := model.approvalDialogView(24)
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 24 {
			t.Fatalf("approval line width=%d want <=24: %q", got, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(view)
	if strings.Count(plain, "z") < len(token)*5 {
		t.Fatalf("approval content was truncated: %q", plain)
	}
}
