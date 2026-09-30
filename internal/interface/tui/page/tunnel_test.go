package page

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/interface/tui/testutil"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestTunnelRuntimeEditorsRedactSecretsAndBlankRuntimeKeyPreservesSecret(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{ID: "tunnel_demo", APIKey: "runtime-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}})
	dashboard, err := NewTunnelDashboard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view := dashboard.View(120, 32); strings.Contains(view, "runtime-secret") || strings.Contains(view, "admin-secret") {
		t.Fatalf("secret leaked in tunnel dashboard: %q", view)
	}
	runtimeEditor, err := NewTunnelDashboardRoute(t.Context(), "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = runtimeEditor.Init()
	if runtimeEditor.OverlayActive() || runtimeEditor.runtimeForm == nil || runtimeEditor.runtimeForm.RuntimeAPIKey != "" {
		t.Fatalf("runtime editor overlay=%t draft=%#v", runtimeEditor.OverlayActive(), runtimeEditor.runtimeForm)
	}
	if input := runtimeInputFromForm(runtimeEditor.runtimeForm); input.APIKey != nil {
		t.Fatalf("blank runtime key should preserve existing secret: %#v", input.APIKey)
	}
	view := ansi.Strip(runtimeEditor.View(100, 28))
	if strings.Contains(view, "runtime-secret") || !strings.Contains(view, "Configure the selected runtime tunnel") || !strings.Contains(view, "enter next") || strings.Contains(view, "Configure Runtime Tunnel") {
		t.Fatalf("runtime editor view=%q", view)
	}
	adminEditor, err := NewTunnelDashboardRoute(t.Context(), "admin-key", "edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = adminEditor.Init()
	view = ansi.Strip(adminEditor.View(100, 28))
	if adminEditor.OverlayActive() || adminEditor.adminForm == nil || adminEditor.adminForm.AdminKey != "" || strings.Contains(view, "admin-secret") || !strings.Contains(view, "enter next") {
		t.Fatalf("admin editor overlay=%t draft=%#v view=%q", adminEditor.OverlayActive(), adminEditor.adminForm, view)
	}
}

func TestTunnelRuntimeTitleStartsAtWorkspaceTitlePosition(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{})
	page, err := NewTunnelDashboard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(ansi.Strip(page.View(100, 32)), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "OpenAI Secure MCP Tunnel") || strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("tunnel title lines=%q", lines[:min(2, len(lines))])
	}
}

func TestManagedTunnelMutationNoticeRendersWithoutDuplicateChildTitle(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{})
	page, err := NewManagedTunnels(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	page.notice = "Managed tunnel created"
	view := ansi.Strip(page.View(100, 24))
	if !strings.Contains(view, "Managed tunnel created") || strings.Contains(view, "Managed tunnels") {
		t.Fatalf("managed tunnel child view=%q", view)
	}
}

func TestManagedTunnelBrowserUsesSelectedTunnelShortcut(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin", ReadAccess: true, ManageAccess: true}})
	item := tunnel.Metadata{ID: "tunnel_one", Name: "One", Description: "primary"}
	if _, err := config.SaveTunnelMetadata(item); err != nil {
		t.Fatal(err)
	}
	page, err := NewManagedTunnels(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(page.View(100, 24))
	if !strings.Contains(view, "u use") {
		t.Fatalf("managed browser missing use shortcut: %q", view)
	}
	_, cmd := page.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if cmd == nil {
		t.Fatal("managed browser use shortcut returned no command")
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "tunnel/managed/tunnel_one/configure" {
		t.Fatalf("managed browser use navigation=%#v", navigate)
	}
}

func TestManagedTunnelResourceUsesRoutedChildDetailPage(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin", ReadAccess: true, ManageAccess: true}})
	item := tunnel.Metadata{ID: "tunnel_one", Name: "One", Description: "primary", OrganizationIDs: []string{"org_one"}, WorkspaceIDs: []string{"ws_one"}, TenantIDs: []string{"tenant_one"}}
	if _, err := config.SaveTunnelMetadata(item); err != nil {
		t.Fatal(err)
	}
	page, err := NewManagedTunnelsRoute(t.Context(), item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.OverlayActive() {
		t.Fatal("managed tunnel detail incorrectly reports overlay active")
	}
	view := ansi.Strip(page.View(100, 26))
	for _, want := range []string{"One", "primary", "s scope", "r refresh", "u use", "? more"} {
		if !strings.Contains(view, want) {
			t.Fatalf("managed detail missing %q: %q", want, view)
		}
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	page = updated.(*TunnelPage)
	view = ansi.Strip(page.View(100, 26))
	for _, want := range []string{"update", "delete", "less"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded managed detail missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "Overview   Scope") || strings.Contains(view, "╭") {
		t.Fatalf("managed detail retained tab/modal chrome: %q", view)
	}
	_, cmd := page.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if cmd == nil {
		t.Fatal("scope child navigation returned no command")
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "tunnel/managed/tunnel_one/scope" {
		t.Fatalf("scope navigation=%#v", navigate)
	}
	scope, err := NewManagedTunnelsRoute(t.Context(), item.ID, "scope")
	if err != nil {
		t.Fatal(err)
	}
	scopeView := ansi.Strip(scope.View(100, 26))
	for _, want := range []string{"org_one", "ws_one", "tenant_one"} {
		if !strings.Contains(scopeView, want) {
			t.Fatalf("scope detail missing %q: %q", want, scopeView)
		}
	}
}

func TestTunnelRuntimeKeyHintsStayAtBottom(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{})
	page, err := NewTunnelDashboard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(ansi.Strip(page.View(120, 32)), "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	plain := strings.Join(lines, "\n")
	if last != 31 || strings.Contains(plain, "managed tunnels") || strings.Contains(lines[last], "? more") {
		t.Fatalf("tunnel help line=%d want=31 without managed collection navigation view=%q", last, plain)
	}
}

func TestTunnelRuntimeConfigureEditorSwitchValidationAndCancel(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Enabled: true, ID: "tunnel_demo", APIKey: "runtime-secret"})
	page, err := NewTunnelDashboardRoute(t.Context(), "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	plain := ansi.Strip(page.View(100, 28))
	if !strings.Contains(plain, "Enabled (at least one MCP transport must remain enabled) [ ENABLED ]") || strings.Contains(plain, "runtime-secret") {
		t.Fatalf("configure editor view=%q", plain)
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	page = updated.(*TunnelPage)
	if page.runtimeForm.Enabled || !page.Dirty() || !strings.Contains(ansi.Strip(page.View(100, 28)), "[ DISABLED ]") {
		t.Fatalf("switch draft=%#v dirty=%t", page.runtimeForm, page.Dirty())
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*TunnelPage)
	if cmd == nil || page.overlay != tunnelOverlayOperation || page.runtimeForm == nil || page.runtimeForm.Enabled {
		t.Fatalf("runtime submit cmd=%v overlay=%d draft=%#v", cmd != nil, page.overlay, page.runtimeForm)
	}
	updated, _ = page.Update(tunnelOperationMsg{command: TunnelConfigure, err: fmt.Errorf("save failed")})
	page = updated.(*TunnelPage)
	if page.OverlayActive() || page.runtimeForm == nil || page.runtimeForm.Enabled || !page.Dirty() || !strings.Contains(ansi.Strip(page.View(100, 28)), "save failed") {
		t.Fatalf("runtime failure overlay=%t draft=%#v dirty=%t", page.OverlayActive(), page.runtimeForm, page.Dirty())
	}
	_, cancel := page.Update(component.EditorCancelMsg{})
	if cancel == nil {
		t.Fatal("editor cancel returned no navigation")
	}
	navigate, ok := cancel().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "tunnel" {
		t.Fatalf("cancel navigation=%#v", navigate)
	}
}

func TestTunnelRuntimeEditorPasswordLabelAlignsWithOtherFields(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{ID: "tunnel_demo", APIKey: "runtime-secret"})
	page, err := NewTunnelDashboardRoute(t.Context(), "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	plain := ansi.Strip(page.View(100, 30))
	if strings.Count(plain, "Blank keeps the current key.") != 1 || !strings.Contains(plain, "> Blank keeps the current key.") {
		t.Fatalf("runtime key placeholder=%q", plain)
	}
	labelColumn := func(label string) int {
		for _, line := range strings.Split(plain, "\n") {
			if column := strings.Index(line, label); column >= 0 {
				return column
			}
		}
		return -1
	}
	idColumn, keyColumn := labelColumn("Tunnel ID"), labelColumn("Runtime API key")
	if idColumn < 0 || keyColumn < 0 || idColumn != keyColumn {
		t.Fatalf("tunnel label columns id=%d runtime-key=%d view=%q", idColumn, keyColumn, plain)
	}
}

func TestTunnelAdminEditorFailureKeepsDraft(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{})
	page, err := NewTunnelDashboardRoute(t.Context(), "admin-key", "edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	updated, _ := page.Update(tea.KeyPressMsg{Code: 's', Text: "secret-draft"})
	page = updated.(*TunnelPage)
	if page.adminForm.AdminKey != "secret-draft" || !page.Dirty() {
		t.Fatalf("admin draft=%#v dirty=%t", page.adminForm, page.Dirty())
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*TunnelPage)
	if cmd == nil || page.overlay != tunnelOverlayOperation {
		t.Fatalf("admin submit cmd=%v overlay=%d", cmd != nil, page.overlay)
	}
	updated, _ = page.Update(tunnelOperationMsg{command: TunnelAdminKeySet, err: fmt.Errorf("verification failed")})
	page = updated.(*TunnelPage)
	if page.OverlayActive() || page.adminForm == nil || page.adminForm.AdminKey != "secret-draft" || !page.Dirty() {
		t.Fatalf("admin failure lost draft overlay=%t draft=%#v dirty=%t", page.OverlayActive(), page.adminForm, page.Dirty())
	}
	plain := ansi.Strip(page.View(90, 26))
	if !strings.Contains(plain, "verification failed") || strings.Contains(plain, "secret-draft") {
		t.Fatalf("admin failure feedback/secret view=%q", plain)
	}
}

func TestTunnelRuntimeOperationOverlayBlocksEditorMouse(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Enabled: true, ID: "tunnel_demo"})
	page, err := NewTunnelDashboardRoute(t.Context(), "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	page.width, page.height = 100, 28
	page.overlay = tunnelOverlayOperation
	progress := component.NewProgress("Saving tunnel configuration")
	page.progress = &progress
	targets := page.MouseTargets(0, 0, 1)
	if len(targets) != 1 || targets[0].ID != "page.overlay" {
		t.Fatalf("operation mouse targets=%#v", targets)
	}
}

func TestTunnelRuntimeLayoutUsesHierarchyAndGroupWrapping(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Enabled: true, ID: "tunnel_6a9462c95f008191a665c3330bcd8368", APIKey: "runtime-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", OrganizationID: "org_demo"}})
	page, err := NewTunnelDashboard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page.dashboard.Status.Metadata = &tunnel.Metadata{ID: page.dashboard.Config.ID, Name: "MCP_Tunnel_WSL", FetchedAt: time.Now()}
	wide := ansi.Strip(page.runtimeView(120))
	for _, want := range []string{"Status", "Tunnel", "Admin", "Metadata", "● ON", "Configured", "Runtime key", "MCP_Tunnel_WSL", "e configure", "space toggle", "? more"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide tunnel layout missing %q: %q", want, wide)
		}
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	page = updated.(*TunnelPage)
	expanded := ansi.Strip(page.runtimeView(120))
	for _, want := range []string{"remove admin", "less"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded tunnel help missing %q: %q", want, expanded)
		}
	}
	if strings.Contains(expanded, "managed tunnels") {
		t.Fatalf("expanded tunnel help exposed removed managed tunnel collection: %q", expanded)
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	page = updated.(*TunnelPage)
	if strings.Contains(wide, "Enabled        true") || strings.Contains(wide, " · ") {
		t.Fatalf("wide tunnel layout retained raw boolean or dot-joined hints: %q", wide)
	}
	wideLines := strings.Split(wide, "\n")
	foundPair := false
	for _, line := range wideLines {
		if strings.Contains(line, "Status") && strings.Contains(line, "Tunnel") {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Fatalf("wide layout did not place Status and Tunnel in two columns: %q", wideLines)
	}

	narrow := ansi.Strip(page.runtimeView(72))
	if !strings.Contains(narrow, "e configure") || !strings.Contains(narrow, "space toggle") {
		t.Fatalf("narrow default help missing core actions: %q", narrow)
	}
}

func TestTunnelRuntimeKeyHintsUseDefaultHelpStyle(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Enabled: true, ID: "tunnel_demo", APIKey: "runtime-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", OrganizationID: "org_demo"}})
	page, err := NewTunnelDashboard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page.height = 32
	view := page.runtimeView(120)
	want := component.DefaultHelp(120, page.runtimeHelpBindings()...)
	if !strings.Contains(view, want) {
		t.Fatalf("tunnel help does not use default help styling\nwant: %q\nview: %q", want, view)
	}
}

func TestTunnelPageActionMouseSpaceUsesSpaceKey(t *testing.T) {
	message := pageActionKeyMsg("space")
	if message.String() != "space" {
		t.Fatalf("space action key=%q", message.String())
	}
}

func TestManagedTunnelRefreshPersistsCacheAndUpdatePrefetchesRemoteState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels":
			if r.URL.Query().Get("workspace_id") != "ws_admin" {
				t.Fatalf("scope=%q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"Cached One","description":"first","workspace_ids":["ws_admin"]},{"id":"tunnel_two","name":"Two","description":"second","workspace_ids":["ws_admin"]}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels/tunnel_one":
			_, _ = w.Write([]byte(`{"id":"tunnel_one","name":"Remote One","description":"fresh","workspace_ids":["ws_admin"]}`))
		default:
			t.Fatalf("unexpected request=%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL})
	if _, err := config.SaveTunnelMetadata(tunnel.Metadata{ID: "stale", Name: "Stale"}); err != nil {
		t.Fatal(err)
	}
	page, err := NewManagedTunnels(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := page.openCommand(TunnelManagedRefresh, "")
	if err != nil || cmd == nil || page.overlay != tunnelOverlayOperation {
		t.Fatalf("refresh cmd=%v err=%v overlay=%d", cmd, err, page.overlay)
	}
	updated, _ := page.Update(cmd())
	page = updated.(*TunnelPage)
	if len(page.items) != 2 || page.items[0].ID != "tunnel_one" || !strings.Contains(page.notice, "2") {
		t.Fatalf("items=%#v notice=%q", page.items, page.notice)
	}
	cached, err := config.LoadTunnelMetadata("tunnel_two")
	if err != nil || cached.Name != "Two" {
		t.Fatalf("cached=%#v err=%v", cached, err)
	}

	cmd, err = page.openCommand(TunnelManagedUpdate, "tunnel_one")
	if err != nil || cmd == nil {
		t.Fatalf("update route cmd=%v err=%v", cmd, err)
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "tunnel/managed/tunnel_one/edit" {
		t.Fatalf("update route=%#v", navigate)
	}
	edit, err := NewManagedTunnelsRouteAction(t.Context(), "tunnel_one", "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	if edit.editor != nil || !edit.managedUpdateFetch || edit.overlay != tunnelOverlayOperation {
		t.Fatalf("prefetch initial editor=%v fetch=%t overlay=%d", edit.editor != nil, edit.managedUpdateFetch, edit.overlay)
	}
	prefetch := edit.Init()
	if prefetch == nil {
		t.Fatal("edit prefetch command missing")
	}
	updated, next := edit.Update(prefetch())
	edit = updated.(*TunnelPage)
	if edit.overlay != tunnelOverlayNone || edit.managedUpdateFetch || edit.editor == nil || edit.managedForm == nil || edit.managedForm.Name != "Remote One" || edit.managedForm.Description != "fresh" || next == nil {
		t.Fatalf("overlay=%d fetch=%t editor=%v form=%#v next=%v", edit.overlay, edit.managedUpdateFetch, edit.editor != nil, edit.managedForm, next)
	}
}

func TestManagedTunnelEditPrefetchEscapeReturnsToDetail(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_one" {
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL.Path)
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL})
	page, err := NewManagedTunnelsRouteAction(t.Context(), "tunnel_one", "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	prefetch := page.Init()
	if prefetch == nil || !page.managedUpdateFetch || page.overlay != tunnelOverlayOperation {
		t.Fatalf("prefetch cmd=%v fetch=%t overlay=%d", prefetch != nil, page.managedUpdateFetch, page.overlay)
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- prefetch() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("managed edit prefetch did not start")
	}
	updated, cmd := page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	page = updated.(*TunnelPage)
	if cmd == nil || page.overlay != tunnelOverlayNone || page.managedUpdateFetch {
		t.Fatalf("escape cmd=%v overlay=%d fetch=%t", cmd != nil, page.overlay, page.managedUpdateFetch)
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "tunnel/managed/tunnel_one" {
		t.Fatalf("escape navigation=%#v", navigate)
	}
	select {
	case message := <-result:
		updated, _ = page.Update(message)
		page = updated.(*TunnelPage)
	case <-time.After(time.Second):
		t.Fatal("cancelled edit prefetch did not return")
	}
	if page.operationCancelled || page.err != nil {
		t.Fatalf("late cancelled prefetch state cancelled=%t err=%v", page.operationCancelled, page.err)
	}
}

func TestManagedTunnelEditPrefetchFailureShowsExplicitWrappedErrorState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_one" {
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL.Path)
		}
		http.Error(w, "remote tunnel unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL})
	page, err := NewManagedTunnelsRouteAction(t.Context(), "tunnel_one", "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	prefetch := page.Init()
	if prefetch == nil {
		t.Fatal("edit prefetch command missing")
	}
	updated, _ := page.Update(prefetch())
	page = updated.(*TunnelPage)
	if page.err == nil || page.editor != nil || page.overlay != tunnelOverlayNone || page.managedUpdateFetch {
		t.Fatalf("prefetch failure err=%v editor=%v overlay=%d fetch=%t", page.err, page.editor != nil, page.overlay, page.managedUpdateFetch)
	}
	view := page.View(36, 16)
	plain := ansi.Strip(view)
	for _, want := range []string{"tunnel_one", "Unable to load managed tunnel"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("prefetch failure missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Edit Managed Tunnel") {
		t.Fatalf("prefetch failure retained redundant page title: %q", plain)
	}
	testutil.AssertLinesFit(t, view, 36)
}

func TestManagedTunnelRefreshCancellationIgnoresLateResult(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" {
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL.Path)
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL})
	page, err := NewManagedTunnels(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := page.openCommand(TunnelManagedRefresh, "")
	if err != nil || cmd == nil {
		t.Fatalf("cmd=%v err=%v", cmd, err)
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("managed refresh did not start")
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	page = updated.(*TunnelPage)
	if page.overlay != tunnelOverlayNone || !page.operationCancelled {
		t.Fatalf("cancel state overlay=%d cancelled=%t", page.overlay, page.operationCancelled)
	}
	select {
	case message := <-result:
		updated, _ = page.Update(message)
		page = updated.(*TunnelPage)
	case <-time.After(time.Second):
		t.Fatal("cancelled managed refresh did not return")
	}
	if page.operationCancelled || page.err != nil || !strings.Contains(page.notice, "cancel") {
		t.Fatalf("completion cancelled=%t err=%v notice=%q", page.operationCancelled, page.err, page.notice)
	}
}

func TestManagedTunnelDeleteSelectedRuntimeOffersClearConfigChoice(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Enabled: true, ID: "tunnel_selected", APIKey: "runtime-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}})
	if _, err := config.SaveTunnelMetadata(tunnel.Metadata{ID: "tunnel_selected", Name: "Selected"}); err != nil {
		t.Fatal(err)
	}
	page, err := NewManagedTunnels(t.Context(), "tunnel_selected")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.openCommand(TunnelManagedDelete, "tunnel_selected"); err != nil {
		t.Fatal(err)
	}
	if page.overlay != tunnelOverlayConfirm || !page.deleteOptions || !page.deleteClear {
		t.Fatalf("delete options overlay=%d options=%t clear=%t", page.overlay, page.deleteOptions, page.deleteClear)
	}
	page.confirm.Select(false)
	page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if page.overlay != tunnelOverlayConfirm || page.deleteOptions || page.deleteClear {
		t.Fatalf("delete choice overlay=%d options=%t clear=%t", page.overlay, page.deleteOptions, page.deleteClear)
	}
}

func TestManagedTunnelCreateEditorSectionsWrapAndFailureKeepsDraft(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin", ReadAccess: true, ManageAccess: true}})
	page, err := NewManagedTunnelsRouteAction(t.Context(), "", "", "create")
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	plain := ansi.Strip(page.View(40, 20))
	for _, want := range []string{"General", "Scope", "Runtime", "enter next"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("create editor missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Create Managed Tunnel") {
		t.Fatalf("create editor retained redundant page title: %q", plain)
	}
	testutil.AssertLinesFit(t, page.View(40, 20), 40)
	updated, _ := page.Update(tea.KeyPressMsg{Code: 'd', Text: "draft-name"})
	page = updated.(*TunnelPage)
	if page.managedForm == nil || page.managedForm.Name != "draft-name" || !page.Dirty() {
		t.Fatalf("create draft=%#v dirty=%t", page.managedForm, page.Dirty())
	}
	updated, _ = page.Update(tunnelOperationMsg{command: TunnelManagedCreate, err: fmt.Errorf("create failed")})
	page = updated.(*TunnelPage)
	if page.editor == nil || page.managedForm == nil || page.managedForm.Name != "draft-name" || !page.Dirty() || page.OverlayActive() {
		t.Fatalf("create failure editor=%v draft=%#v dirty=%t overlay=%t", page.editor != nil, page.managedForm, page.Dirty(), page.OverlayActive())
	}
	if view := ansi.Strip(page.View(40, 20)); !strings.Contains(view, "create failed") {
		t.Fatalf("create failure feedback=%q", view)
	}
}

func TestManagedTunnelUpdateAndConfigureFailuresKeepDraft(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_one" {
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"tunnel_one","name":"Remote","description":"fresh","workspace_ids":["ws_admin"]}`))
	}))
	defer server.Close()
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL})
	edit, err := NewManagedTunnelsRouteAction(t.Context(), "tunnel_one", "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	prefetch := edit.Init()
	if prefetch == nil {
		t.Fatal("edit prefetch missing")
	}
	updated, initEditor := edit.Update(prefetch())
	edit = updated.(*TunnelPage)
	if initEditor == nil || edit.editor == nil || edit.managedForm == nil || edit.managedForm.Name != "Remote" {
		t.Fatalf("edit prefetch editor=%v draft=%#v init=%v", edit.editor != nil, edit.managedForm, initEditor != nil)
	}
	_ = initEditor()
	updated, _ = edit.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	edit = updated.(*TunnelPage)
	name := edit.managedForm.Name
	updated, _ = edit.Update(tunnelOperationMsg{command: TunnelManagedUpdate, targetID: "tunnel_one", err: fmt.Errorf("update failed")})
	edit = updated.(*TunnelPage)
	if edit.managedForm == nil || edit.managedForm.Name != name || !edit.Dirty() || !strings.Contains(ansi.Strip(edit.View(52, 22)), "update failed") {
		t.Fatalf("update failure draft=%#v dirty=%t", edit.managedForm, edit.Dirty())
	}

	configure, err := NewManagedTunnelsRouteAction(t.Context(), "tunnel_one", "", "configure")
	if err != nil {
		t.Fatal(err)
	}
	_ = configure.Init()
	if configure.configureForm == nil || configure.configureForm.RuntimeKeyMode != "auto" {
		t.Fatalf("configure draft=%#v", configure.configureForm)
	}
	configure.configureForm.RuntimeKeyMode = "manual"
	configure.configureForm.RuntimeAPIKey = "runtime-secret-draft"
	updated, _ = configure.Update(tunnelOperationMsg{command: TunnelManagedConfigure, targetID: "tunnel_one", err: fmt.Errorf("configure failed")})
	configure = updated.(*TunnelPage)
	view := ansi.Strip(configure.View(44, 18))
	if configure.configureForm == nil || configure.configureForm.RuntimeKeyMode != "manual" || configure.configureForm.RuntimeAPIKey != "runtime-secret-draft" || !strings.Contains(view, "configure failed") || strings.Contains(view, "runtime-secret-draft") {
		t.Fatalf("configure failure draft=%#v dirty=%t view=%q", configure.configureForm, configure.Dirty(), view)
	}
	testutil.AssertLinesFit(t, configure.View(44, 18), 44)
}

func setupTunnelPageConfig(t *testing.T, value tunnel.Config) {
	t.Helper()
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if value.Admin.Key != "" && !value.Admin.ReadAccess && !value.Admin.ManageAccess {
		value.Admin.ReadAccess, value.Admin.ManageAccess = true, true
	}
	cfg.Tunnel = value
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestManagedTunnelReadOnlyAccessHidesManagementActions(t *testing.T) {
	setupTunnelPageConfig(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-read", WorkspaceID: "ws_admin", ReadAccess: true}})
	item := tunnel.Metadata{ID: "tunnel_one", Name: "One", Description: "read only"}
	if _, err := config.SaveTunnelMetadata(item); err != nil {
		t.Fatal(err)
	}
	page, err := NewManagedTunnels(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(page.View(100, 24))
	if !strings.Contains(view, "u use") || strings.Contains(view, "refresh all") || strings.Contains(view, "a add") {
		t.Fatalf("read-only browser actions=%q", view)
	}
	detail, err := NewManagedTunnelsRoute(t.Context(), item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := detail.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	detail = updated.(*TunnelPage)
	view = ansi.Strip(detail.View(100, 26))
	if !strings.Contains(view, "r refresh") || !strings.Contains(view, "u use") || strings.Contains(view, "update") || strings.Contains(view, "delete") {
		t.Fatalf("read-only detail actions=%q", view)
	}
}
