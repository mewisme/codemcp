package page

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/interface/tui/testutil"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/upstream"
)

type mcpPageClient struct {
	mu           sync.Mutex
	tools        []upstream.Tool
	blockTools   bool
	toolsStarted chan struct{}
	closed       []string
}

func (*mcpPageClient) Connect(context.Context, upstream.Server) error { return nil }
func (client *mcpPageClient) Close(_ context.Context, id string) error {
	client.mu.Lock()
	client.closed = append(client.closed, id)
	client.mu.Unlock()
	return nil
}
func (client *mcpPageClient) Tools(ctx context.Context, _ string) ([]upstream.Tool, error) {
	if client.blockTools {
		select {
		case client.toolsStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return append([]upstream.Tool(nil), client.tools...), nil
}
func (*mcpPageClient) Call(context.Context, string, string, map[string]any) (upstream.CallResult, error) {
	return upstream.CallResult{}, nil
}
func (*mcpPageClient) PID(string) int { return 4242 }

func newMCPPageTestHarness(t *testing.T, client *mcpPageClient) (*MCPPage, *upstream.Manager, *mcpoauth.Store, *upstream.Store) {
	t.Helper()
	root := t.TempDir()
	serverStore := upstream.NewStore(filepath.Join(root, "upstreams.json"))
	manager := upstream.NewManagerWithClient(serverStore, client)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	oauthStore := mcpoauth.NewStore(filepath.Join(root, "oauth.json"))
	page, err := newMCPPage(t.Context(), "", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	return page, manager, oauthStore, serverStore
}

func TestMCPMutationNoticeRendersBesidePageTitle(t *testing.T) {
	page, _, _, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	page.notice = "Upstream added"
	line := strings.Split(ansi.Strip(page.View(100, 24)), "\n")[0]
	if !strings.Contains(line, "Upstreams  · Upstream added") {
		t.Fatalf("MCP title notice=%q", line)
	}
}

func TestMCPResourceUsesRoutedChildDetailPage(t *testing.T) {
	client := &mcpPageClient{}
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, client)
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Auth: upstream.AuthConfig{Type: "oauth"}, Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	page, err := newMCPRoutePage(t.Context(), "docs", "", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	if page.OverlayActive() {
		t.Fatalf("resource detail state overlay=%t resource=%q", page.OverlayActive(), page.resourceID)
	}
	view := ansi.Strip(page.View(110, 28))
	for _, want := range []string{"ID  docs", "https://example.test/mcp", "h health", "v tools", "u oauth", "? more"} {
		if !strings.Contains(view, want) {
			t.Fatalf("MCP detail missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "Overview") {
		t.Fatalf("MCP child repeated breadcrumb title: %q", view)
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	page = updated.(*MCPPage)
	view = ansi.Strip(page.View(110, 28))
	for _, want := range []string{"configure", "toggle", "health", "tools", "login", "less"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded MCP detail missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "Overview   Health") || strings.Contains(view, "╭") {
		t.Fatalf("MCP detail retained tab/modal chrome: %q", view)
	}
	updated, action := page.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	page = updated.(*MCPPage)
	if action == nil {
		t.Fatal("remove detail action returned no command")
	}
	remove, ok := action().(UpstreamCommandMsg)
	if !ok || remove.Command != UpstreamServerRemove || remove.ResourceID != "docs" {
		t.Fatalf("remove action=%#v", remove)
	}
	_, cmd := page.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	if cmd == nil {
		t.Fatal("health child navigation returned no command")
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "upstream/docs/health" {
		t.Fatalf("health navigation=%#v", navigate)
	}
	health, err := newMCPRoutePage(t.Context(), "docs", "health", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	if got := ansi.Strip(health.View(110, 28)); !strings.Contains(got, "Not checked yet") || strings.Contains(got, "u oauth") || strings.Contains(got, "v tools") {
		t.Fatalf("health child=%q", got)
	}
}

func TestMCPRoutedServerEditorsAndSecretRedaction(t *testing.T) {
	_, manager, oauthStore, store := newMCPPageTestHarness(t, &mcpPageClient{})
	create, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = create.Init()
	updated, _ := create.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	create = updated.(*MCPPage)
	if create.editor == nil || create.OverlayActive() || !create.InputActive() || create.Dirty() {
		t.Fatalf("create editor=%v overlay=%t input=%t dirty=%t", create.editor != nil, create.OverlayActive(), create.InputActive(), create.Dirty())
	}
	view := ansi.Strip(create.View(100, 28))
	for _, want := range []string{"General", "Connection", "Authentication", "Tools", "enter next"} {
		if !strings.Contains(view, want) {
			t.Fatalf("create editor missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "Create Upstream") {
		t.Fatalf("create editor retained redundant page title: %q", view)
	}
	testutil.AssertLinesFit(t, create.View(40, 18), 40)
	updated, _ = create.Update(tea.KeyPressMsg{Code: 'd', Text: "docs"})
	create = updated.(*MCPPage)
	updated, _ = create.Update(huh.NextField())
	create = updated.(*MCPPage)
	updated, _ = create.Update(tea.KeyPressMsg{Code: 'D', Text: "Docs"})
	create = updated.(*MCPPage)
	updated, _ = create.Update(huh.NextField())
	create = updated.(*MCPPage)
	updated, _ = create.Update(huh.NextField())
	create = updated.(*MCPPage)
	updated, _ = create.Update(huh.NextField())
	create = updated.(*MCPPage)
	updated, _ = create.Update(tea.KeyPressMsg{Code: 'h', Text: "https://example.test/mcp"})
	create = updated.(*MCPPage)
	if create.serverForm.ID != "docs" || create.serverForm.Transport != "http" || create.serverForm.URL != "https://example.test/mcp" || !create.Dirty() {
		t.Fatalf("create draft=%#v dirty=%t", create.serverForm, create.Dirty())
	}
	updated, submit := create.Update(component.EditorSubmitMsg{})
	create = updated.(*MCPPage)
	if submit == nil || create.Dirty() {
		t.Fatalf("create submit=%v dirty=%t", submit != nil, create.Dirty())
	}
	server, ok := manager.Get("docs")
	if !ok || server.Transport != "http" || server.URL != "https://example.test/mcp" || server.Name != "Docs" {
		t.Fatalf("created server=%#v ok=%t", server, ok)
	}

	server.Headers = map[string]string{"Authorization": "Bearer top-secret", "X-Mode": "read"}
	server.Expose = "allowlist"
	server.Tools = []string{"read"}
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "top-secret") {
		t.Fatalf("secret leaked to upstream store: %s", data)
	}
	edit, err := newMCPRoutePageAction(t.Context(), "docs", "", "edit", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = edit.Init()
	editView := edit.View(120, 32)
	if strings.Contains(editView, "top-secret") {
		t.Fatalf("secret leaked to editor: %q", editView)
	}
	_, save := edit.Update(component.EditorSubmitMsg{})
	if save == nil {
		t.Fatal("edit submit returned no navigation command")
	}
	server, _ = manager.Get("docs")
	if server.Headers["Authorization"] != "Bearer top-secret" || server.Expose != "allowlist" {
		t.Fatalf("edit did not preserve server state: %#v", server)
	}

	detail, err := newMCPRoutePage(t.Context(), "docs", "", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	view = detail.View(120, 32)
	if strings.Contains(view, "top-secret") || !strings.Contains(view, "<redacted>") {
		t.Fatalf("detail secret redaction=%q", view)
	}
	if _, err := detail.openCommand(UpstreamServerDisable, "docs"); err != nil {
		t.Fatal(err)
	}
	server, _ = manager.Get("docs")
	if server.Enabled {
		t.Fatal("server remained enabled")
	}
	if _, err := detail.openCommand(UpstreamServerEnable, "docs"); err != nil {
		t.Fatal(err)
	}
	server, _ = manager.Get("docs")
	if !server.Enabled {
		t.Fatal("server remained disabled")
	}
	if _, err := detail.openCommand(UpstreamServerRemove, "docs"); err != nil {
		t.Fatal(err)
	}
	detail.confirm = component.NewConfirmButtons("Remove", "Cancel", true)
	detail.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, ok := manager.Get("docs"); ok {
		t.Fatal("server remained after confirmed removal")
	}
}

func TestMCPDetailRemovalKeepsDetailUntilParentNavigation(t *testing.T) {
	client := &mcpPageClient{}
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, client)
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	page, err := newMCPRoutePage(t.Context(), "docs", "", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.openCommand(UpstreamServerRemove, "docs"); err != nil {
		t.Fatal(err)
	}
	page.confirm = component.NewConfirmButtons("Remove", "Cancel", true)
	cmd := page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || page.resourceID != "docs" {
		t.Fatalf("navigation=%v resource=%q", cmd != nil, page.resourceID)
	}
	if got := ansi.Strip(page.View(100, 24)); !strings.Contains(got, "ID  docs") || strings.Contains(got, "Overview") {
		t.Fatalf("intermediate MCP detail render=%q", got)
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "upstream" || !message.Replace {
		t.Fatalf("navigation=%#v", message)
	}
}

func TestMCPPageHealthAndToolsRunAsCommands(t *testing.T) {
	client := &mcpPageClient{tools: []upstream.Tool{{Name: "read", Description: "Read docs"}}}
	page, manager, _, _ := newMCPPageTestHarness(t, client)
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	if err := page.reload(); err != nil {
		t.Fatal(err)
	}
	cmd, err := page.openCommand(UpstreamServerHealth, "docs")
	if err != nil || cmd == nil || page.overlay != mcpOverlayOperation {
		t.Fatalf("health cmd=%v err=%v overlay=%d", cmd, err, page.overlay)
	}
	updated, _ := page.Update(cmd())
	page = updated.(*MCPPage)
	if page.status["docs"].Health != upstream.HealthConnected || page.status["docs"].ToolCount != 1 {
		t.Fatalf("health=%#v", page.status["docs"])
	}
	cmd, err = page.openCommand(UpstreamServerTools, "docs")
	if err != nil || cmd == nil {
		t.Fatalf("tools cmd=%v err=%v", cmd, err)
	}
	updated, _ = page.Update(cmd())
	page = updated.(*MCPPage)
	if len(page.tools["docs"]) != 1 || page.tools["docs"][0].Name != "read" {
		t.Fatalf("tools=%#v", page.tools["docs"])
	}
}

func TestMCPPageToolRefreshIsCancellable(t *testing.T) {
	client := &mcpPageClient{blockTools: true, toolsStarted: make(chan struct{}, 1)}
	page, manager, _, _ := newMCPPageTestHarness(t, client)
	if err := manager.Add(upstream.Server{ID: "slow", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	cmd, err := page.openCommand(UpstreamServerTools, "slow")
	if err != nil || cmd == nil {
		t.Fatalf("cmd=%v err=%v", cmd, err)
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-client.toolsStarted:
	case <-time.After(time.Second):
		t.Fatal("tool refresh did not start")
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	page = updated.(*MCPPage)
	if page.overlay != mcpOverlayNone || !page.operationCancelled {
		t.Fatalf("cancel state overlay=%d cancelled=%t", page.overlay, page.operationCancelled)
	}
	select {
	case message := <-result:
		updated, _ = page.Update(message)
		page = updated.(*MCPPage)
	case <-time.After(time.Second):
		t.Fatal("cancelled tool command did not return")
	}
	if page.operationCancelled || !strings.Contains(page.notice, "cancel") {
		t.Fatalf("cancel completion notice=%q cancelled=%t", page.notice, page.operationCancelled)
	}
}

func TestMCPPageOAuthEmitsURLStoresCredentialAndLogoutPreservesServer(t *testing.T) {
	client := &mcpPageClient{tools: []upstream.Tool{{Name: "read"}}}
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, client)
	if err := manager.Add(upstream.Server{ID: "secure", Name: "Secure", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Auth: upstream.AuthConfig{Type: "oauth", Scope: "read"}, Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	page, err := newMCPRoutePageAction(t.Context(), "secure", "oauth", "login", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	view := ansi.Strip(page.View(100, 28))
	for _, want := range []string{"Open authorization URL in browser", "enter next"} {
		if !strings.Contains(view, want) {
			t.Fatalf("OAuth editor missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "Authorize Upstream") {
		t.Fatalf("OAuth editor retained redundant page title: %q", view)
	}
	if page.OverlayActive() || page.oauthForm == nil || !page.oauthForm.OpenBrowser {
		t.Fatalf("OAuth editor overlay=%t data=%#v", page.OverlayActive(), page.oauthForm)
	}
	opened := make(chan string, 1)
	page.openBrowser = func(raw string) error {
		opened <- raw
		return nil
	}
	page.oauthLogin = func(ctx context.Context, config mcpoauth.LoginConfig, options mcpoauth.LoginOptions) (mcpoauth.Credential, error) {
		if err := options.OnURL("https://auth.example/authorize"); err != nil {
			return mcpoauth.Credential{}, err
		}
		credential := mcpoauth.Credential{ServerID: config.ServerID, ServerURL: config.ServerURL, Issuer: "https://auth.example", ClientID: "client", Scopes: []string{"read"}, AccessToken: "access-secret", RefreshToken: "refresh-secret"}
		if err := oauthStore.Put(credential); err != nil {
			return mcpoauth.Credential{}, err
		}
		return credential, nil
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*MCPPage)
	if cmd == nil || page.overlay != mcpOverlayOperation {
		t.Fatalf("OAuth submit cmd=%v overlay=%d", cmd != nil, page.overlay)
	}
	updated, next := page.Update(cmd())
	page = updated.(*MCPPage)
	openedURL := ""
	select {
	case openedURL = <-opened:
	case <-time.After(time.Second):
		t.Fatal("browser opener was not called")
	}
	if page.operationURL != "https://auth.example/authorize" || openedURL != "https://auth.example/authorize" || next == nil {
		t.Fatalf("url=%q opened=%q next=%v", page.operationURL, openedURL, next)
	}
	updated, finish := page.Update(next())
	page = updated.(*MCPPage)
	if finish == nil || page.OverlayActive() || page.Dirty() {
		t.Fatalf("OAuth finish cmd=%v overlay=%t dirty=%t", finish != nil, page.OverlayActive(), page.Dirty())
	}
	if strings.Contains(page.View(120, 32), "access-secret") || strings.Contains(page.View(120, 32), "refresh-secret") {
		t.Fatal("OAuth token leaked into TUI")
	}
	status, err := oauthStore.Status("secure")
	if err != nil || !status.Configured || !status.HasRefreshToken {
		t.Fatalf("oauth status=%#v err=%v", status, err)
	}
	batch, ok := finish().(tea.BatchMsg)
	if !ok {
		t.Fatalf("OAuth finish message=%T", finish())
	}
	foundNavigation := false
	for _, next := range batch {
		if next == nil {
			continue
		}
		if navigate, ok := next().(NavigateMsg); ok && strings.Join(navigate.Path, "/") == "upstream/secure/oauth" {
			foundNavigation = true
		}
	}
	if !foundNavigation {
		t.Fatal("OAuth success did not navigate to OAuth detail")
	}

	detail, err := newMCPRoutePage(t.Context(), "secure", "oauth", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := detail.openCommand(UpstreamAuthLogout, "secure"); err != nil {
		t.Fatal(err)
	}
	detail.confirm = component.NewConfirmButtons("Logout", "Cancel", true)
	detail.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	status, err = oauthStore.Status("secure")
	if err != nil || status.Configured {
		t.Fatalf("oauth remained after logout: %#v err=%v", status, err)
	}
	if _, ok := manager.Get("secure"); !ok {
		t.Fatal("OAuth logout removed Upstream configuration")
	}
}

func TestMCPPageOAuthBrowserFailureDoesNotAbortLogin(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	if err := manager.Add(upstream.Server{ID: "secure", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Auth: upstream.AuthConfig{Type: "oauth"}, Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	page, err := newMCPRoutePageAction(t.Context(), "secure", "oauth", "login", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	page.openBrowser = func(string) error { return errors.New("no browser") }
	page.oauthLogin = func(_ context.Context, config mcpoauth.LoginConfig, options mcpoauth.LoginOptions) (mcpoauth.Credential, error) {
		_ = options.OnURL("https://auth.example/authorize")
		return mcpoauth.Credential{ServerID: config.ServerID}, nil
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*MCPPage)
	updated, next := page.Update(cmd())
	page = updated.(*MCPPage)
	if next == nil {
		t.Fatal("missing next OAuth event")
	}
	updated, next = page.Update(next())
	page = updated.(*MCPPage)
	if !strings.Contains(page.notice, "no browser") || next == nil {
		t.Fatalf("browser failure notice=%q next=%v", page.notice, next)
	}
	updated, finish := page.Update(next())
	page = updated.(*MCPPage)
	if finish == nil || page.OverlayActive() {
		t.Fatalf("browser failure aborted OAuth finish=%v overlay=%t", finish != nil, page.OverlayActive())
	}
}

func TestMCPOAuthFailureKeepsEditorDraft(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	if err := manager.Add(upstream.Server{ID: "secure", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Auth: upstream.AuthConfig{Type: "oauth"}, Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	page, err := newMCPRoutePageAction(t.Context(), "secure", "oauth", "login", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	updated, _ := page.Update(tea.KeyPressMsg{Code: 'i', Text: "https://issuer.example"})
	page = updated.(*MCPPage)
	if page.oauthForm.Issuer != "https://issuer.example" || !page.Dirty() {
		t.Fatalf("OAuth draft=%#v dirty=%t", page.oauthForm, page.Dirty())
	}
	page.oauthLogin = func(context.Context, mcpoauth.LoginConfig, mcpoauth.LoginOptions) (mcpoauth.Credential, error) {
		return mcpoauth.Credential{}, errors.New("authorization failed")
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*MCPPage)
	if cmd == nil || page.overlay != mcpOverlayOperation {
		t.Fatalf("OAuth failure submit cmd=%v overlay=%d", cmd != nil, page.overlay)
	}
	updated, _ = page.Update(cmd())
	page = updated.(*MCPPage)
	if page.OverlayActive() || page.oauthForm == nil || page.oauthForm.Issuer != "https://issuer.example" || !page.Dirty() {
		t.Fatalf("OAuth failure lost draft overlay=%t draft=%#v dirty=%t", page.OverlayActive(), page.oauthForm, page.Dirty())
	}
	if plain := ansi.Strip(page.View(90, 26)); !strings.Contains(plain, "authorization failed") {
		t.Fatalf("OAuth failure feedback missing: %q", plain)
	}
}

func TestMCPCreateEditorValidationFailureKeepsDraft(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	page, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	updated, _ := page.Update(tea.KeyPressMsg{Code: 'd', Text: "docs"})
	page = updated.(*MCPPage)
	for range 3 {
		updated, _ = page.Update(huh.NextField())
		page = updated.(*MCPPage)
	}
	updated, _ = page.Update(huh.NextField())
	page = updated.(*MCPPage)
	updated, _ = page.Update(tea.KeyPressMsg{Code: 'h', Text: "https://example.test/mcp"})
	page = updated.(*MCPPage)
	updated, _ = page.Update(huh.NextField())
	page = updated.(*MCPPage)
	updated, _ = page.Update(huh.NextField())
	page = updated.(*MCPPage)
	updated, _ = page.Update(tea.KeyPressMsg{Code: '{', Text: "{"})
	page = updated.(*MCPPage)
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*MCPPage)
	if cmd != nil || page.editor == nil || !page.Dirty() || page.serverForm.ID != "docs" || page.serverForm.SensitiveHeaders != "{" {
		t.Fatalf("validation failure cmd=%v editor=%v dirty=%t draft=%#v", cmd != nil, page.editor != nil, page.Dirty(), page.serverForm)
	}
	plain := ansi.Strip(page.View(80, 24))
	if !strings.Contains(plain, "decode sensitive header JSON") {
		t.Fatalf("validation feedback missing: %q", plain)
	}
	if _, ok := manager.Get("docs"); ok {
		t.Fatal("invalid draft created a server")
	}
}

func TestMCPCreateEditorExistingIDFailureKeepsDraft(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Existing", Transport: "http", URL: "https://old.example/mcp", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	page, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	updated, _ := page.Update(tea.KeyPressMsg{Code: 'd', Text: "docs"})
	page = updated.(*MCPPage)
	for range 4 {
		updated, _ = page.Update(huh.NextField())
		page = updated.(*MCPPage)
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: 'h', Text: "https://new.example/mcp"})
	page = updated.(*MCPPage)
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*MCPPage)
	if cmd != nil || page.editor == nil || !page.Dirty() || page.serverForm.ID != "docs" || page.serverForm.URL != "https://new.example/mcp" {
		t.Fatalf("existing-ID failure cmd=%v editor=%v dirty=%t draft=%#v", cmd != nil, page.editor != nil, page.Dirty(), page.serverForm)
	}
	if plain := ansi.Strip(page.View(80, 24)); !strings.Contains(plain, "upstream server already exists: docs") {
		t.Fatalf("existing-ID feedback missing: %q", plain)
	}
	stored, _ := manager.Get("docs")
	if stored.URL != "https://old.example/mcp" || stored.Name != "Existing" {
		t.Fatalf("existing server mutated: %#v", stored)
	}
}

func TestUpstreamServerEditorCancelReturnsToParent(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	create, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := create.Update(component.EditorCancelMsg{})
	if cmd == nil {
		t.Fatal("create cancel returned no navigation")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "upstream" {
		t.Fatalf("create cancel=%#v", message)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Transport: "http", URL: "https://example.test/mcp", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	edit, err := newMCPRoutePageAction(t.Context(), "docs", "", "edit", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_, cmd = edit.Update(component.EditorCancelMsg{})
	if cmd == nil {
		t.Fatal("edit cancel returned no navigation")
	}
	message, ok = cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "upstream/docs" {
		t.Fatalf("edit cancel=%#v", message)
	}
}

func fillMCPCreateHTTPDraft(page *MCPPage, id, name, url string) *MCPPage {
	updated, _ := page.Update(tea.KeyPressMsg{Code: []rune(id)[0], Text: id})
	page = updated.(*MCPPage)
	updated, _ = page.Update(huh.NextField())
	page = updated.(*MCPPage)
	updated, _ = page.Update(tea.KeyPressMsg{Code: []rune(name)[0], Text: name})
	page = updated.(*MCPPage)
	for range 3 {
		updated, _ = page.Update(huh.NextField())
		page = updated.(*MCPPage)
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: []rune(url)[0], Text: url})
	return updated.(*MCPPage)
}

func TestMCPCreateEditorFormJSONTabsKeyboardMouseAndRoundTrip(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	page, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	page = fillMCPCreateHTTPDraft(page, "docs", "Docs", "https://example.test/mcp")
	expected, err := serverFromMCPForm(page.serverForm, upstream.Server{}, true)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: '2', Text: "2", Mod: tea.ModAlt})
	page = updated.(*MCPPage)
	if page.serverEditorMode != mcpServerEditorJSON || page.jsonEditor == nil || !page.Dirty() {
		t.Fatalf("JSON mode=%d editor=%v dirty=%t", page.serverEditorMode, page.jsonEditor != nil, page.Dirty())
	}
	parsed, err := upstream.ParseMCPServersJSON([]byte(page.jsonEditor.Value()))
	if err != nil || len(parsed) != 1 || !reflect.DeepEqual(parsed[0], expected) {
		t.Fatalf("Form -> JSON parsed=%#v expected=%#v err=%v", parsed, expected, err)
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: '1', Text: "1", Mod: tea.ModAlt})
	page = updated.(*MCPPage)
	if page.serverEditorMode != mcpServerEditorForm {
		t.Fatalf("Form mode=%d", page.serverEditorMode)
	}
	roundTrip, err := serverFromMCPForm(page.serverForm, upstream.Server{}, true)
	if err != nil || !reflect.DeepEqual(roundTrip, expected) {
		t.Fatalf("JSON -> Form=%#v expected=%#v err=%v", roundTrip, expected, err)
	}
	plain := ansi.Strip(page.View(90, 26))
	for _, want := range []string{"Form", "JSON", "Alt+1 Form", "Alt+2 JSON"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("mode tabs missing %q: %q", want, plain)
		}
	}
	lines := strings.Split(plain, "\n")
	modeLine := -1
	for index, line := range lines {
		if strings.Contains(line, "Form") && strings.Contains(line, "JSON") {
			modeLine = index
			break
		}
	}
	if modeLine < 0 || modeLine+1 >= len(lines) || strings.TrimSpace(lines[modeLine+1]) != "" {
		t.Fatalf("MCP editor mode tabs missing top padding before section: line=%d view=%q", modeLine, plain)
	}
	var jsonTarget *component.MouseTarget
	for _, target := range page.MouseTargets(0, 0, 5) {
		if target.ID != "mcp.editor.mode" {
			continue
		}
		message, ok := target.Handle(component.MouseEvent{Button: tea.MouseLeft}).(mcpServerEditorModeMsg)
		if ok && message.Mode == mcpServerEditorJSON {
			selected := target
			jsonTarget = &selected
			break
		}
	}
	if jsonTarget == nil {
		t.Fatal("JSON mode mouse target not found")
	}
	message := jsonTarget.Handle(component.MouseEvent{Button: tea.MouseLeft})
	updated, _ = page.Update(message)
	page = updated.(*MCPPage)
	if page.serverEditorMode != mcpServerEditorJSON {
		t.Fatal("JSON mouse target did not select JSON mode")
	}
}

func TestMCPCreateJSONSingleSyncsToFormWithoutLosingDisabled(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	page, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(mcpServerEditorModeMsg{Mode: mcpServerEditorJSON})
	page = updated.(*MCPPage)
	draft := `{"mcpServers":{"local":{"command":"node","args":["server.js"],"disabled":true}}}`
	page.jsonEditor.SetValue(draft)
	updated, _ = page.Update(mcpServerEditorModeMsg{Mode: mcpServerEditorForm})
	page = updated.(*MCPPage)
	if page.serverEditorMode != mcpServerEditorForm || page.serverForm.ID != "local" || page.serverForm.Transport != "stdio" || page.serverForm.Command != "node" || page.serverForm.Enabled {
		t.Fatalf("JSON -> Form draft=%#v mode=%d", page.serverForm, page.serverEditorMode)
	}
	server, err := serverFromMCPForm(page.serverForm, upstream.Server{}, true)
	if err != nil || server.Enabled || server.Transport != "stdio" || server.Command != "node" {
		t.Fatalf("JSON -> Form normalized=%#v err=%v", server, err)
	}
}

func TestMCPCreateJSONMultipleStaysAuthoritativeAndCreatesAtomically(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	page, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(mcpServerEditorModeMsg{Mode: mcpServerEditorJSON})
	page = updated.(*MCPPage)
	draft := `{"mcpServers":{"local":{"command":"node"},"docs":{"url":"https://example.test/mcp"}}}`
	page.jsonEditor.SetValue(draft)
	updated, _ = page.Update(mcpServerEditorModeMsg{Mode: mcpServerEditorForm})
	page = updated.(*MCPPage)
	if page.serverEditorMode != mcpServerEditorJSON || !strings.Contains(page.modeNotice, "supports exactly one server") || page.jsonEditor.Value() != draft {
		t.Fatalf("multi JSON switch mode=%d notice=%q draft=%q", page.serverEditorMode, page.modeNotice, page.jsonEditor.Value())
	}
	updated, cmd := page.Update(component.TextAreaSavedMsg{Value: draft})
	page = updated.(*MCPPage)
	if cmd == nil || len(manager.List()) != 2 || page.Dirty() {
		t.Fatalf("multi create cmd=%v servers=%#v dirty=%t err=%v", cmd != nil, manager.List(), page.Dirty(), page.modeErr)
	}
	message := cmd()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		t.Fatalf("multi create command=%T", message)
	}
	foundNavigation := false
	for _, next := range batch {
		if next == nil {
			continue
		}
		if navigation, ok := next().(NavigateMsg); ok {
			foundNavigation = strings.Join(navigation.Path, "/") == "upstream"
		}
	}
	if !foundNavigation {
		t.Fatal("multi create did not navigate to Upstream list")
	}
}

func TestMCPCreateJSONInvalidAndExistingBatchKeepExactDraft(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	page, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(mcpServerEditorModeMsg{Mode: mcpServerEditorJSON})
	page = updated.(*MCPPage)
	secret := "TOP_SECRET_VALUE"
	invalid := `{"mcpServers":{"good":{"command":"node"},"bad":{"transport":"wat","headers":{"Authorization":"` + secret + `"}}}}`
	page.jsonEditor.SetValue(invalid)
	updated, cmd := page.Update(component.TextAreaSavedMsg{Value: invalid})
	page = updated.(*MCPPage)
	if cmd != nil || len(manager.List()) != 0 || page.modeErr == nil || strings.Contains(page.modeErr.Error(), secret) || page.jsonEditor.Value() != invalid {
		t.Fatalf("invalid JSON cmd=%v servers=%#v err=%v exact=%t", cmd != nil, manager.List(), page.modeErr, page.jsonEditor.Value() == invalid)
	}
	if err := manager.Add(upstream.Server{ID: "existing", Transport: "http", URL: "https://old.example/mcp", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	existing := `{"mcpServers":{"new":{"command":"node"},"existing":{"url":"https://new.example/mcp"}}}`
	page.jsonEditor.SetValue(existing)
	updated, cmd = page.Update(component.TextAreaSavedMsg{Value: existing})
	page = updated.(*MCPPage)
	if cmd != nil || page.modeErr == nil || !strings.Contains(page.modeErr.Error(), "already exists: existing") || page.jsonEditor.Value() != existing {
		t.Fatalf("existing batch cmd=%v err=%v exact=%t", cmd != nil, page.modeErr, page.jsonEditor.Value() == existing)
	}
	if _, ok := manager.Get("new"); ok {
		t.Fatal("existing-ID batch partially created new server")
	}
	stored, _ := manager.Get("existing")
	if stored.URL != "https://old.example/mcp" {
		t.Fatalf("existing server mutated: %#v", stored)
	}
}

func TestMCPCreateJSONEnterNewlineWrapsAndDivergedTargetIsPreserved(t *testing.T) {
	_, manager, oauthStore, _ := newMCPPageTestHarness(t, &mcpPageClient{})
	page, err := newMCPRoutePageAction(t.Context(), "", "", "create", manager, oauthStore)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(mcpServerEditorModeMsg{Mode: mcpServerEditorJSON})
	page = updated.(*MCPPage)
	page.jsonEditor.SetValue("{")
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	page = updated.(*MCPPage)
	if page.jsonEditor.Value() != "{\n" {
		t.Fatalf("JSON Enter value=%q", page.jsonEditor.Value())
	}
	longDraft := `{"id":"long","url":"https://example.test/` + strings.Repeat("segment/", 30) + `mcp"}`
	page.jsonEditor.SetValue(longDraft)
	testutil.AssertLinesFit(t, page.View(36, 18), 36)

	page.serverEditorMode = mcpServerEditorForm
	page.serverForm.Name = "Form draft"
	page.jsonEditor.SetValue(`{"id":"json","command":"node"}`)
	jsonBefore := page.jsonEditor.Value()
	updated, _ = page.Update(mcpServerEditorModeMsg{Mode: mcpServerEditorJSON})
	page = updated.(*MCPPage)
	if page.serverEditorMode != mcpServerEditorJSON || page.jsonEditor.Value() != jsonBefore || !strings.Contains(page.modeNotice, "both changed") {
		t.Fatalf("diverged switch mode=%d json=%q notice=%q", page.serverEditorMode, page.jsonEditor.Value(), page.modeNotice)
	}
}
