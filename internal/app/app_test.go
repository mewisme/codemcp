package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestNewSharesToolRuntime(t *testing.T) {
	cfg := config.Default()
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if app.Tools == nil || app.MCP == nil || app.MCP.Server == nil {
		t.Fatal("app runtime was not initialized")
	}
	if app.MCP.Server.Tools != app.Tools {
		t.Fatal("MCP and Admin do not share the same tool runtime")
	}
	if app.Upstream != app.Tools.Upstream {
		t.Fatal("Admin and tool runtime do not share the same upstream manager")
	}
	if _, ok := app.Tools.Registry.Schema("ponytail_turn"); !ok {
		t.Fatal("default app missing ponytail controller tool")
	}
	if _, ok := app.Tools.Registry.Schema("caveman_turn"); !ok {
		t.Fatal("default app missing caveman controller tool")
	}
}

func TestTunnelOnlyRuntimeDoesNotCreateMCPHTTPRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Enabled = false
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.ID = "tunnel_test"
	cfg.Tunnel.APIKey = "runtime-secret"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if app.MCP != nil || app.Tools == nil || app.Tunnel == nil {
		t.Fatalf("tunnel-only runtime MCP=%#v tools=%#v tunnel=%#v", app.MCP, app.Tools, app.Tunnel)
	}
	recorder := httptest.NewRecorder()
	app.MCPHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled MCP HTTP status=%d", recorder.Code)
	}
}

func TestReloadConfigSwitchesMCPHTTPRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Server.Enabled = false
	next.Tunnel.Enabled = true
	next.Tunnel.ID = "tunnel_test"
	next.Tunnel.APIKey = "runtime-secret"
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if app.MCP != nil {
		t.Fatal("MCP HTTP runtime survived transport disable")
	}
	next.Server.Enabled = true
	next.Tunnel.Enabled = false
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if app.MCP == nil || app.MCP.Server == nil || app.MCP.Server.Tools != app.Tools {
		t.Fatal("MCP HTTP runtime was not restored with shared tools")
	}
}

func TestNewKeepsControllerToolsWhenFeatureInactive(t *testing.T) {
	cfg := config.Default()
	cfg.Integrations.Ponytail.Active = false
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := app.Tools.Registry.Schema("ponytail_turn"); !ok {
		t.Fatal("inactive ponytail controller tool missing")
	}
	if _, ok := app.Tools.Registry.Schema("caveman_turn"); !ok {
		t.Fatal("caveman controller tool missing")
	}
	if app.Tools.Integrations().Ponytail.Active {
		t.Fatal("ponytail active state was not preserved")
	}
}

func TestHandlersHonorDisabledAuthentication(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	mcpRecorder := httptest.NewRecorder()
	app.MCPHandler().ServeHTTP(mcpRecorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if mcpRecorder.Code != http.StatusOK {
		t.Fatalf("MCP auth-disabled health = %d", mcpRecorder.Code)
	}
	if body := mcpRecorder.Body.String(); body != `{"ok":true}` || strings.Contains(body, "auth_enabled") {
		t.Fatalf("MCP health body = %q", body)
	}

	adminRecorder := httptest.NewRecorder()
	app.AdminHandler().ServeHTTP(adminRecorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if adminRecorder.Code != http.StatusOK {
		t.Fatalf("admin auth-disabled health = %d", adminRecorder.Code)
	}
}

func TestHandlersRequireEnabledAuthentication(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = auth.HashToken("mcp-test")
	cfg.Auth.AdminTokenHash = auth.HashToken("admin-test")
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	mcpRecorder := httptest.NewRecorder()
	app.MCPHandler().ServeHTTP(mcpRecorder, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if mcpRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("MCP auth-enabled status = %d", mcpRecorder.Code)
	}

	adminRecorder := httptest.NewRecorder()
	app.AdminHandler().ServeHTTP(adminRecorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if adminRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("admin auth-enabled status = %d", adminRecorder.Code)
	}
}

func TestHandlersReadAuthenticationFromRuntimeConfigStore(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = auth.HashToken("mcp-test")
	cfg.Auth.AdminTokenHash = auth.HashToken("admin-test")
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mcpHandler := app.MCPHandler()
	adminHandler := app.AdminHandler()

	if _, err := app.Config.Update(func(next config.Config) (config.Config, error) {
		next.Auth.MCPEnabled = false
		next.Auth.AdminEnabled = false
		next.Server.AllowUnauthenticatedLoopback = true
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	mcpRecorder := httptest.NewRecorder()
	mcpHandler.ServeHTTP(mcpRecorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if mcpRecorder.Code != http.StatusOK {
		t.Fatalf("updated MCP auth status = %d", mcpRecorder.Code)
	}
	adminRecorder := httptest.NewRecorder()
	adminHandler.ServeHTTP(adminRecorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if adminRecorder.Code != http.StatusOK {
		t.Fatalf("updated admin auth status = %d", adminRecorder.Code)
	}
}

func TestBootstrapRewiresToolRuntime(t *testing.T) {
	app := &App{}
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if app.Config == nil {
		t.Fatal("bootstrap did not initialize runtime config store")
	}
	if app.Tools == nil || app.MCP == nil || app.MCP.Server == nil {
		t.Fatal("bootstrap did not initialize runtime")
	}
	if app.MCP.Server.Tools != app.Tools {
		t.Fatal("bootstrap did not wire shared tool runtime")
	}
	if app.Upstream != app.Tools.Upstream {
		t.Fatal("bootstrap did not wire shared upstream manager")
	}
	if app.Activity == nil || app.Logger == nil || app.MCP.Activity != app.Activity {
		t.Fatal("bootstrap did not wire runtime telemetry")
	}
}

func TestAdminHandlerSharesApprovalManager(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.AdminEnabled = false
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	challenge, _, err := app.Tools.Approvals.CreateChallenge(approval.ChallengeInput{CallerID: "session-a", SessionHash: "hash-a", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update"}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow cm update"})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := app.Tools.Approvals.CreateRequest(challenge.ID, "session-a", "ws_test")
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodGet, "/api/requests?status=pending", nil)
	httpRequest.RemoteAddr = "127.0.0.1:43123"
	recorder := httptest.NewRecorder()
	app.AdminHandler().ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), request.ID) {
		t.Fatalf("approval API status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTunnelLifecyclePublishesActivityFromSourceObserver(t *testing.T) {
	cfg := config.Default()
	cfg.Tunnel.Enabled = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("expected invalid tunnel configuration to fail")
	}
	recent := app.Activity.Recent(10)
	if len(recent) == 0 {
		t.Fatal("tunnel lifecycle failure did not publish activity")
	}
	last := recent[len(recent)-1]
	if last.Kind != "tunnel.degraded" || last.Status != "degraded" || last.Source != "tunnel" {
		t.Fatalf("activity = %#v", last)
	}
}

type lifecycleUpstreamClient struct {
	closed []string
}

func (*lifecycleUpstreamClient) Connect(context.Context, upstream.Server) error { return nil }
func (c *lifecycleUpstreamClient) Close(_ context.Context, id string) error {
	c.closed = append(c.closed, id)
	return nil
}
func (*lifecycleUpstreamClient) Tools(context.Context, string) ([]upstream.Tool, error) {
	return nil, nil
}
func (*lifecycleUpstreamClient) Call(context.Context, string, string, map[string]any) (upstream.CallResult, error) {
	return upstream.CallResult{}, nil
}
func (*lifecycleUpstreamClient) PID(string) int { return 0 }

func TestStopShutsDownUpstreamConnections(t *testing.T) {
	client := &lifecycleUpstreamClient{}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{ID: "one", Name: "One", Enabled: true, Transport: "http", URL: "https://example.test/mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := (&App{Upstream: manager}).Stop(); err != nil {
		t.Fatal(err)
	}
	if len(client.closed) != 1 || client.closed[0] != "one" {
		t.Fatalf("closed = %#v", client.closed)
	}
}

func TestAppLifecycleOwnsWorkspaceRuntimeLocks(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	cfg := config.Default()
	cfg.Tunnel.Enabled = false
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	item, err := application.Tools.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	competitor := workspace.NewManager(workspace.DefaultStorePath())
	if err := competitor.Activate(); !errors.Is(err, workspace.ErrAlreadyActive) {
		t.Fatalf("competitor activation error=%v", err)
	}
	if diagnostics := application.Tools.Workspaces.RuntimeDiagnostics(); !diagnostics.Active || diagnostics.Owned != 1 || diagnostics.Workspaces[0].WorkspaceID != item.ID {
		t.Fatalf("runtime diagnostics=%#v", diagnostics)
	}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := competitor.Activate(); err != nil {
		t.Fatalf("competitor activation after stop: %v", err)
	}
	if err := competitor.Deactivate(); err != nil {
		t.Fatal(err)
	}
}
