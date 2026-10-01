package page

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	managed "go.mewis.me/codemcp/internal/service"
	updatepkg "go.mewis.me/codemcp/internal/update"
)

func TestRuntimePageBuildsSystemRows(t *testing.T) {
	page, err := NewRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page.loaded = true
	page.runtime = application.RuntimeOverview{UserService: application.ServiceOverview{Scope: managed.ScopeUser, Supported: true}}
	if runtime.GOOS != "windows" {
		page.runtime.SystemService = application.ServiceOverview{Scope: managed.ScopeSystem, Supported: true}
	}
	page.auth = application.AuthStatus{MCPConfigured: true, MCPLegacyBearer: true, AdminConfigured: true}
	page.install = application.InstallationOverview{}
	page.about = application.AboutInfo{Version: "v1.2.3"}
	page.rebuildBrowser("")
	ids := map[string]bool{}
	for _, id := range []string{"runtime", "transport.mcp-http", "service.user", "auth.mcp", "auth.admin", "installation", "update", "about"} {
		if !page.browser.SelectID(id) {
			t.Fatalf("row missing: %s", id)
		}
		ids[id] = true
	}
	if runtime.GOOS != "windows" && !page.browser.SelectID("service.system") {
		t.Fatal("system service row missing")
	}
}

func TestRuntimeRowsUseDescriptiveTitlesAndDescriptions(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	page.runtime = application.RuntimeOverview{Running: true, Status: runtimecontrol.RuntimeStatus{PID: 4242, Managed: true, ServiceScope: "user"}, UserService: application.ServiceOverview{Scope: managed.ScopeUser, Supported: true, Installed: true, Running: true, Backend: "systemd --user", PID: 4242}}
	runtimeRow := page.runtimeRow()
	if runtimeRow.Title != "MCP runtime process" || !strings.Contains(runtimeRow.Description, "running · pid 4242 · managed / user") {
		t.Fatalf("runtime row=%#v", runtimeRow)
	}
	page.runtime.Status.Starting = true
	runtimeItem := page.runtimeItem()
	runtimeRow = runtimeItem.row
	if !strings.Contains(runtimeRow.Description, "starting · pid 4242 · managed / user") || !strings.Contains(runtimeItem.detail, "starting") {
		t.Fatalf("starting runtime item=%#v", runtimeItem)
	}
	if !strings.Contains(page.statusView(80), "STARTING") {
		t.Fatalf("starting runtime summary=%q", page.statusView(80))
	}
	serviceRow := page.serviceRow(page.runtime.UserService)
	if serviceRow.Title != "User managed service" || !strings.Contains(serviceRow.Description, "systemd --user") || !strings.Contains(serviceRow.Description, "pid 4242") {
		t.Fatalf("service row=%#v", serviceRow)
	}
	mcpHTTP := page.mcpHTTPRow()
	if mcpHTTP.Title != "MCP HTTP server" || !strings.Contains(mcpHTTP.Description, "port closed") {
		t.Fatalf("MCP HTTP row=%#v", mcpHTTP)
	}
	mcpAuthItem, adminAuthItem := page.authItem("mcp"), page.authItem("admin")
	mcpAuth, adminAuth := mcpAuthItem.row, adminAuthItem.row
	if mcpAuth.Title != "MCP HTTP authentication" || adminAuth.Title != "Admin UI authentication" || !strings.Contains(mcpAuth.Description, "auth only") || !strings.Contains(mcpAuthItem.detail, "listener is controlled by MCP HTTP server") || !strings.Contains(mcpAuthItem.detail, "Legacy bearer") {
		t.Fatalf("authentication rows MCP=%#v admin=%#v", mcpAuth, adminAuth)
	}
}

func TestRuntimeMCPHTTPRowDistinguishesConfigFromLiveListener(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	page.runtime = application.RuntimeOverview{Running: true, MCPHTTPEnabled: false, MCPHTTPPort: 37421, TunnelEnabled: true, Status: runtimecontrol.RuntimeStatus{ServerEnabled: true, ServerPort: 37421}}
	item := page.mcpHTTPItem()
	row := item.row
	if !strings.Contains(row.Description, "disabled · listening · reload required") || !strings.Contains(item.detail, "http://127.0.0.1:37421/mcp") || !strings.Contains(item.detail, "Tunnel") {
		t.Fatalf("MCP HTTP mismatch item=%#v", item)
	}
	page.runtime.Status.ServerEnabled = false
	item = page.mcpHTTPItem()
	row = item.row
	if !strings.Contains(row.Description, "disabled · port closed") || strings.Contains(item.detail, "http://127.0.0.1:37421/mcp") {
		t.Fatalf("MCP HTTP disabled item=%#v", item)
	}
}

func TestRuntimeMCPHTTPToggleUsesTransportAction(t *testing.T) {
	page, _ := NewRuntimeRoute(t.Context(), "transport.mcp-http")
	page.loaded = true
	page.runtime = application.RuntimeOverview{MCPHTTPEnabled: true, MCPHTTPPort: 37421, TunnelEnabled: true}
	page.rebuildBrowser("")
	detailCommand := func(key tea.KeyPressMsg) SystemCommandMsg {
		t.Helper()
		_, cmd := page.Update(key)
		if cmd == nil {
			t.Fatal("detail action returned no command")
		}
		message := cmd()
		if batch, ok := message.(tea.BatchMsg); ok {
			if len(batch) == 0 || batch[0] == nil {
				t.Fatalf("detail action batch=%#v", batch)
			}
			message = batch[0]()
		}
		value, ok := message.(SystemCommandMsg)
		if !ok {
			t.Fatalf("detail action message=%T", message)
		}
		return value
	}
	message := detailCommand(tea.KeyPressMsg{Code: tea.KeySpace})
	_, cmd := page.Update(message)
	if cmd == nil || page.pending != MCPHTTPDisable || page.overlay != systemOverlayOperation {
		t.Fatalf("disable toggle cmd=%v pending=%q overlay=%d", cmd, page.pending, page.overlay)
	}
	page.closeOverlay()
	page.runtime.MCPHTTPEnabled = false
	page.rebuildBrowser("")
	message = detailCommand(tea.KeyPressMsg{Code: tea.KeySpace})
	_, cmd = page.Update(message)
	if cmd == nil || page.pending != MCPHTTPEnable || page.overlay != systemOverlayOperation {
		t.Fatalf("enable toggle cmd=%v pending=%q overlay=%d", cmd, page.pending, page.overlay)
	}
	page.closeOverlay()
}

func TestRuntimeResourceUsesFullChildDetailPage(t *testing.T) {
	page, err := NewRuntimeRoute(t.Context(), "service.user")
	if err != nil {
		t.Fatal(err)
	}
	page.loaded = true
	page.runtime = application.RuntimeOverview{UserService: application.ServiceOverview{Scope: managed.ScopeUser, Supported: true, Installed: true, Running: true, Backend: "systemd --user", PID: 4242}}
	page.rebuildBrowser("")
	if page.OverlayActive() {
		t.Fatal("runtime detail incorrectly reports overlay active")
	}
	view := ansi.Strip(page.View(110, 28))
	for _, want := range []string{"Scope  user", "systemd --user", "u up", "x restart", "d down", "r refresh"} {
		if !strings.Contains(view, want) {
			t.Fatalf("runtime detail missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "User managed service") {
		t.Fatalf("runtime child repeated breadcrumb title: %q", view)
	}
	if strings.Contains(view, "╭") {
		t.Fatalf("runtime detail retained modal chrome: %q", view)
	}
	_, cmd := page.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd == nil {
		t.Fatal("restart detail action returned no command")
	}
	message, ok := cmd().(SystemCommandMsg)
	if !ok || message.Command != RuntimeRestartUser {
		t.Fatalf("restart action=%#v", message)
	}
}

func TestRuntimeUnknownResourceRendersUnavailableChild(t *testing.T) {
	page, err := NewRuntimeRoute(t.Context(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	page.loaded = true
	page.rebuildBrowser("")
	view := page.View(90, 20)
	if page.err == nil || !strings.Contains(view, "not found") || strings.Contains(view, "Unavailable") {
		t.Fatalf("unknown runtime detail err=%v view=%q", page.err, view)
	}
}

func TestRuntimeMCPHTTPStoppedTogglePersistsAndRespectsTransportInvariant(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Enabled, cfg.Tunnel.ID, cfg.Tunnel.APIKey = true, "tunnel_test", "tunnel-key"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	page, _ := NewRuntime(t.Context())
	page.runtime = application.RuntimeOverview{MCPHTTPEnabled: true, MCPHTTPPort: cfg.HTTP.MCP.Port, TunnelEnabled: true}
	cmd, err := page.openCommand(MCPHTTPDisable)
	if err != nil || cmd == nil {
		t.Fatalf("disable command err=%v cmd=%v", err, cmd)
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) < 2 {
		t.Fatalf("disable command message=%T", msg)
	}
	var result systemOperationMsg
	for _, next := range batch {
		if next == nil {
			continue
		}
		if value, ok := next().(systemOperationMsg); ok {
			result = value
			break
		}
	}
	if result.err != nil || !strings.Contains(result.notice, "applies on next runtime start") {
		t.Fatalf("disable result=%#v err=%v", result, result.err)
	}
	loaded, err := config.Load()
	if err != nil || loaded.HTTP.MCP.Enabled {
		t.Fatalf("server enabled=%t err=%v", loaded.HTTP.MCP.Enabled, err)
	}
	loaded.Tunnel.Enabled = false
	loaded.HTTP.MCP.Enabled = true
	if err := config.Save(loaded); err != nil {
		t.Fatal(err)
	}
	page.runtime.MCPHTTPEnabled, page.runtime.TunnelEnabled = true, false
	cmd, err = page.openCommand(MCPHTTPDisable)
	if err != nil || cmd == nil {
		t.Fatalf("invariant command err=%v cmd=%v", err, cmd)
	}
	msg = cmd()
	batch, ok = msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("invariant message=%T", msg)
	}
	result = systemOperationMsg{}
	for _, next := range batch {
		if next == nil {
			continue
		}
		if value, ok := next().(systemOperationMsg); ok {
			result = value
			break
		}
	}
	if result.err == nil || !strings.Contains(result.err.Error(), "at least one MCP transport") {
		t.Fatalf("invariant result=%#v", result)
	}
}

func TestRuntimeTokenRotationRequiresConfirmAndSecretIsTransient(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	cmd, err := page.openCommand(AuthMCPRotate)
	if err != nil || cmd != nil || page.overlay != systemOverlayConfirm {
		t.Fatalf("rotation did not open confirmation: overlay=%v cmd=%v err=%v", page.overlay, cmd, err)
	}
	if page.confirm.AffirmativeSelected() {
		t.Fatal("token rotation confirmation defaulted affirmative")
	}
	page.operationID = 9
	const token = "secret-token-value"
	page.finishOperation(systemOperationMsg{id: 9, command: AuthMCPRotate, token: token})
	if page.overlay != systemOverlaySecret || page.secret != token {
		t.Fatalf("secret overlay missing: overlay=%v secret=%q", page.overlay, page.secret)
	}
	if !strings.Contains(page.View(100, 30), token) {
		t.Fatal("one-time token not shown")
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	page = updated.(*RuntimePage)
	if page.secret != "" || page.secretKind != "" || page.overlay != systemOverlayNone {
		t.Fatalf("secret survived close: overlay=%v secret=%q kind=%q", page.overlay, page.secret, page.secretKind)
	}
	if strings.Contains(page.notice, token) || strings.Contains(page.View(100, 30), token) {
		t.Fatal("token leaked outside one-time overlay")
	}
}

func TestRuntimeConfigLifecycleUsesNativeTUIFlows(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	page, err := NewRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := page.openCommand(ConfigInitialize)
	if err != nil || cmd == nil || page.overlay != systemOverlayOperation || page.external != nil {
		t.Fatalf("initialize flow cmd=%v overlay=%v external=%#v err=%v", cmd != nil, page.overlay, page.external, err)
	}
	page.cancelOperation()
	cmd, err = page.openCommand(ConfigUninitialize)
	if err != nil || cmd != nil || page.overlay != systemOverlayConfirm || page.external != nil {
		t.Fatalf("uninitialize flow cmd=%v overlay=%v external=%#v err=%v", cmd != nil, page.overlay, page.external, err)
	}
	if page.confirm.AffirmativeSelected() {
		t.Fatal("uninitialize confirmation defaulted affirmative")
	}
}

func TestRuntimeExternalCommandUsesExplicitOverlay(t *testing.T) {
	page, _ := NewRuntime(context.Background())
	page.operationID = 4
	external := &application.ExternalCommand{Command: "cm upgrade", Reason: "requires elevation"}
	page.finishOperation(systemOperationMsg{id: 4, command: UpdateApply, external: external})
	if page.overlay != systemOverlayExternal || page.external != external {
		t.Fatalf("external workflow not surfaced: overlay=%v external=%#v", page.overlay, page.external)
	}
	view := page.View(100, 30)
	if !strings.Contains(view, external.Command) || !strings.Contains(view, external.Reason) {
		t.Fatalf("external workflow missing from view: %q", view)
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	page = updated.(*RuntimePage)
	if page.external != nil || page.overlay != systemOverlayNone {
		t.Fatal("external command survived overlay close")
	}
}

func TestRuntimeForegroundUsesExplicitExternalWorkflow(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	cmd, err := page.openCommand(RuntimeForeground)
	if err != nil || cmd != nil {
		t.Fatalf("foreground workflow err=%v cmd=%v", err, cmd)
	}
	if page.overlay != systemOverlayExternal || page.external == nil {
		t.Fatalf("foreground workflow not external: overlay=%v external=%#v", page.overlay, page.external)
	}
	if page.external.Command != "cm serve" || !strings.Contains(page.external.Reason, "Exit the TUI") {
		t.Fatalf("foreground workflow=%#v", page.external)
	}
}

func TestRuntimeMCPForegroundCommandsUseExplicitExternalWorkflow(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	for _, test := range []struct {
		command SystemCommand
		want    string
	}{
		{command: MCPStdioForeground, want: "cm mcp stdio"},
		{command: MCPHTTPForeground, want: "cm mcp http"},
	} {
		cmd, err := page.openCommand(test.command)
		if err != nil || cmd != nil {
			t.Fatalf("command=%s err=%v cmd=%v", test.command, err, cmd)
		}
		if page.overlay != systemOverlayExternal || page.external == nil || page.external.Command != test.want {
			t.Fatalf("command=%s overlay=%v external=%#v", test.command, page.overlay, page.external)
		}
		page.closeOverlay()
	}
}

func TestRuntimeInstallAndUpdateUseRoutedEditorsAndFailureKeepsDraft(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	for command, want := range map[SystemCommand]string{InstallRun: "runtime/install", UpdateApply: "runtime/update"} {
		cmd, err := page.openCommand(command)
		if err != nil || cmd == nil {
			t.Fatalf("command=%s cmd=%v err=%v", command, cmd != nil, err)
		}
		navigate, ok := cmd().(NavigateMsg)
		if !ok || strings.Join(navigate.Path, "/") != want {
			t.Fatalf("command=%s navigation=%#v", command, navigate)
		}
	}
	update, err := NewRuntimeRouteAction(t.Context(), "", "update")
	if err != nil {
		t.Fatal(err)
	}
	updated, initEditor := update.Update(update.Init()())
	update = updated.(*RuntimePage)
	if update.editor == nil || update.updateForm == nil || initEditor == nil || update.OverlayActive() {
		t.Fatalf("editor=%v data=%v init=%v overlay=%t", update.editor != nil, update.updateForm != nil, initEditor != nil, update.OverlayActive())
	}
	update.updateForm.TargetVersion = "v-draft"
	update.editor.SetSubmitting(true)
	update.operationID = 7
	follow := update.finishOperation(systemOperationMsg{id: 7, command: UpdateApply, err: fmt.Errorf("update failed")})
	if follow != nil || update.editor == nil || update.updateForm.TargetVersion != "v-draft" || update.editor.Submitting() || !strings.Contains(ansi.Strip(update.View(44, 18)), "update failed") {
		t.Fatalf("follow=%v draft=%#v submitting=%t view=%q", follow != nil, update.updateForm, update.editor.Submitting(), ansi.Strip(update.View(44, 18)))
	}
}

func TestRuntimeCloseCancelsOperation(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	ctx, cancel := context.WithCancel(context.Background())
	page.operationCancel = cancel
	page.Close()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("page close did not cancel operation")
	}
}

func TestRuntimeUpdateOperationsUseInlineTitleNotice(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	for _, test := range []struct {
		name string
		msg  systemOperationMsg
		want string
	}{
		{name: "check", msg: systemOperationMsg{id: 1, command: UpdateCheck, update: updatepkg.CheckResult{Status: updatepkg.StatusUpToDate, Latest: "v1.2.3"}}, want: "latest v1.2.3"},
		{name: "apply", msg: systemOperationMsg{id: 2, command: UpdateApply, notice: "Updated to v1.2.3"}, want: "Updated to v1.2.3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			page.operationID = test.msg.id
			cmd := page.finishOperation(test.msg)
			if !strings.Contains(page.notice, test.want) {
				t.Fatalf("update title notice=%q", page.notice)
			}
			if cmd == nil {
				t.Fatal("update completion did not schedule reload")
			}
			view := page.View(100, 30)
			if !strings.Contains(view, page.notice) {
				t.Fatalf("update notice missing beside page title: %q", view)
			}
		})
	}
}

func TestRuntimeUpdateFailureRemainsErrorFeedback(t *testing.T) {
	page, _ := NewRuntime(t.Context())
	page.operationID = 3
	page.finishOperation(systemOperationMsg{id: 3, command: UpdateApply, err: errors.New("update failed")})
	if page.err == nil || page.err.Error() != "update failed" || page.notice != "" {
		t.Fatalf("failure err=%v notice=%q", page.err, page.notice)
	}
}
