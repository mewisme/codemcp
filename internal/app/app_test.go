package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/controlguard"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/notification"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

type lifecycleNotificationProvider struct {
	called chan struct{}
}

func (p *lifecycleNotificationProvider) Name() string { return notification.ProviderDesktop }
func (p *lifecycleNotificationProvider) Notify(context.Context, notification.Message) error {
	select {
	case p.called <- struct{}{}:
	default:
	}
	return nil
}

type failingCompletionNotificationProvider struct {
	called chan notification.Message
}

func (p *failingCompletionNotificationProvider) Name() string { return notification.ProviderDesktop }
func (p *failingCompletionNotificationProvider) Notify(_ context.Context, message notification.Message) error {
	select {
	case p.called <- message:
	default:
	}
	return errors.New("provider failed with private delivery detail")
}

func TestBootstrapRegistersTelegramNotificationProvider(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Notifications.Stop()
	status := app.Notifications.Status(map[string]bool{notification.ProviderTelegram: true})
	for _, provider := range status.Providers {
		if provider.Provider != notification.ProviderTelegram {
			continue
		}
		if !provider.Registered || provider.Available || provider.Health != notification.ProviderHealthUnavailable {
			t.Fatalf("telegram provider status=%#v", provider)
		}
		return
	}
	t.Fatal("telegram provider is missing from notification status")
}

func TestStartAllowsDefaultOnCapabilitiesWithoutOptionalPrerequisites(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatalf("default-on optional prerequisites blocked runtime start: %v", err)
	}
	t.Cleanup(func() { _ = app.Stop() })

	snapshot := app.Tunnel.Snapshot()
	if !snapshot.Status.Enabled || snapshot.Configured || snapshot.Status.Running {
		t.Fatalf("fresh tunnel runtime state=%#v", snapshot)
	}
	telegramHealth := app.Telegram.Health()
	if !telegramHealth.Enabled || telegramHealth.TokenConfigured || telegramHealth.AuthorizationConfigured || telegramHealth.Running {
		t.Fatalf("fresh Telegram runtime state=%#v", telegramHealth)
	}
	semanticHealth := app.Tools.Semantic.Health()
	if semanticHealth.Available || semanticHealth.LastErrorCategory == "" {
		t.Fatalf("fresh semantic runtime should report unavailable provider: %#v", semanticHealth)
	}
}

func TestAcceptedCompletionNotificationFailureDoesNotChangeCompletionTruth(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Notifications.Completion.TelegramEnabled = false
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		app.Tools.CompletionHooks.Stop()
		app.Notifications.Stop()
	}()

	provider := &failingCompletionNotificationProvider{called: make(chan notification.Message, 1)}
	app.Notifications.Register(provider)
	item, err := app.Tools.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentID := agentcompletion.DeriveAgentID("completion-notification-caller", "completion-notification-generation")
	record, created, err := app.Tools.Completions.Accept(
		agentcompletion.Identity{AgentID: agentID, Source: "mcp"},
		agentcompletion.Input{
			WorkspaceID: item.ID,
			Status:      agentcompletion.StatusCompleted,
			Title:       "Finished token=title-secret",
			Summary:     "Verified changes Authorization: Bearer summary-secret",
		},
	)
	if err != nil || !created {
		t.Fatalf("record=%#v created=%t err=%v", record, created, err)
	}
	current, found, err := app.Tools.Completions.Current(agentID, item.ID)
	if err != nil || !found || current.ID != record.ID || current.Status != agentcompletion.StatusCompleted {
		t.Fatalf("completion truth changed after notification dispatch: current=%#v found=%t err=%v", current, found, err)
	}

	var message notification.Message
	select {
	case message = <-provider.called:
	case <-time.After(time.Second):
		t.Fatal("completion notification provider was not called")
	}
	if message.CompletionID != record.ID || message.WorkspaceID != item.ID {
		t.Fatalf("completion message=%#v", message)
	}
	for _, secret := range []string{"title-secret", "summary-secret", "private delivery detail"} {
		if strings.Contains(message.Body, secret) || strings.Contains(message.Title, secret) {
			t.Fatalf("completion notification leaked %q: %#v", secret, message)
		}
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		diagnostics := app.Notifications.Diagnostics()
		if len(diagnostics) > 0 {
			last := diagnostics[len(diagnostics)-1]
			if last.CompletionID == record.ID {
				if last.Status != notification.DiagnosticFailed {
					t.Fatalf("completion notification diagnostic=%#v", last)
				}
				current, found, err = app.Tools.Completions.Current(agentID, item.ID)
				if err != nil || !found || current.ID != record.ID {
					t.Fatalf("completion truth changed after provider failure: current=%#v found=%t err=%v", current, found, err)
				}
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("completion notification failure diagnostic was not recorded")
}

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
	cfg.HTTP.MCP.Enabled = false
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
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.HTTP.MCP.Enabled = false
	next.Tunnel.Enabled = true
	next.Tunnel.ID = "tunnel_test"
	next.Tunnel.APIKey = "runtime-secret"
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if app.MCP != nil {
		t.Fatal("MCP HTTP runtime survived transport disable")
	}
	next.HTTP.MCP.Enabled = true
	next.Tunnel.Enabled = false
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if app.MCP == nil || app.MCP.Server == nil || app.MCP.Server.Tools != app.Tools {
		t.Fatal("MCP HTTP runtime was not restored with shared tools")
	}
}

func TestReloadConfigReconcilesExistingSingleTunnelClient(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Enabled = false
	cfg.Tunnel.ID = "tunnel_old"
	cfg.Tunnel.APIKey = "runtime-old"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	original := app.Tunnel
	if original == nil {
		t.Fatal("single tunnel client was not initialized")
	}

	next := cfg
	next.Tunnel.ID = "tunnel_new"
	next.Tunnel.APIKey = "runtime-new"
	next.Tunnel.Admin = tunnel.AdminConfig{
		Key: "admin-secret", WorkspaceID: "ws_admin",
		Verified: true, ReadAccess: true, ManageAccess: true,
	}
	metadata := tunnel.Metadata{ID: next.Tunnel.ID, Name: "Selected"}
	if _, err := config.SaveTunnelMetadata(metadata); err != nil {
		t.Fatal(err)
	}

	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if app.Tunnel != original {
		t.Fatal("reload replaced the canonical tunnel client")
	}
	if got := app.Tunnel.Config(); got != next.Tunnel {
		t.Fatalf("reconciled config=%#v want=%#v", got, next.Tunnel)
	}
	snapshot := app.Tunnel.Snapshot()
	if !snapshot.Configured || snapshot.Status.Running || snapshot.Status.ID != next.Tunnel.ID {
		t.Fatalf("reconciled snapshot=%#v", snapshot)
	}
	if snapshot.Status.Metadata == nil || snapshot.Status.Metadata.ID != next.Tunnel.ID || snapshot.Status.Metadata.Name != metadata.Name {
		t.Fatalf("cached metadata was not reconciled: %#v", snapshot.Status.Metadata)
	}
	if !snapshot.Status.Admin.Verified || !snapshot.Status.Admin.ReadAccess || !snapshot.Status.Admin.ManageAccess {
		t.Fatalf("derived admin state was not projected from canonical config: %#v", snapshot.Status.Admin)
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
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
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
	cfg.HTTP.MCP.Auth.TokenHash = auth.HashToken("mcp-test")
	cfg.HTTP.Admin.Auth.TokenHash = auth.HashToken("admin-test")
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
	cfg.HTTP.MCP.Auth.TokenHash = auth.HashToken("mcp-test")
	cfg.HTTP.Admin.Auth.TokenHash = auth.HashToken("admin-test")
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mcpHandler := app.MCPHandler()
	adminHandler := app.AdminHandler()

	if _, err := app.Config.Update(func(next config.Config) (config.Config, error) {
		next.HTTP.MCP.Auth.Enabled = false
		next.HTTP.Admin.Auth.Enabled = false
		next.HTTP.Security.AllowUnauthenticatedLoopback = true
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
	cfg.HTTP.Admin.Auth.Enabled = false
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
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Tunnel.Enabled = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Tunnel.Start(); err == nil {
		t.Fatal("expected explicit tunnel start without runtime configuration to fail")
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

func TestStopShutsDownCompletionHookBus(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := tools.NewRuntime()
	workspaceItem, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	subscription, _ := runtime.Completions.SubscribeSnapshot(0)
	application := &App{Tools: runtime}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-subscription.Events; ok {
		t.Fatal("completion subscription remained active after app stop")
	}
	if _, created, err := runtime.Completions.Accept(
		agentcompletion.Identity{AgentID: agentcompletion.DeriveAgentID("stop-caller", "stop-generation"), Source: "mcp"},
		agentcompletion.Input{WorkspaceID: workspaceItem.ID, Status: agentcompletion.StatusCompleted, Title: "Stopped"},
	); err == nil || created || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("completion service remained writable after app stop: created=%t err=%v", created, err)
	}
	event := agentcompletion.Event{
		ID: "completion-event:completion_stop", Name: agentcompletion.EventAccepted,
		Record: agentcompletion.Record{ID: "completion_stop", WorkspaceID: "ws_stop"},
	}
	if err := runtime.CompletionHooks.Dispatch(event); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("completion hook bus remained active after app stop: %v", err)
	}
}

func TestStopCancelsPendingApprovalsBeforeRuntimeTeardown(t *testing.T) {
	manager := approval.NewManager("instance-shutdown-approval")
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		CallerID: "caller-shutdown", RequestCorrelationID: "shutdown", SessionHash: "hash-shutdown", WorkspaceID: "ws_shutdown",
		Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"command": "cm update"},
		GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow shutdown test",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-shutdown", "ws_shutdown", "Allow shutdown test")
	if err != nil {
		t.Fatal(err)
	}
	application := &App{Tools: &tools.Runtime{Approvals: manager}}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if pending := manager.List(approval.Filter{Status: approval.StatusPending}); len(pending) != 0 {
		t.Fatalf("pending approvals remained after shutdown: %#v", pending)
	}
	resolved, ok := manager.Get(request.ID)
	if !ok || resolved.Status != approval.StatusCancelled || resolved.Reason != "runtime shutdown" {
		t.Fatalf("shutdown approval=%#v ok=%t", resolved, ok)
	}
}

func TestStopPublishesProcessTerminalTruthBeforeClosingBroker(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	runtime := tools.NewRuntime()
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := "sleep 30"
	started, err := runtime.Processes.Start(context.Background(), item.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	owner := backgrounddelivery.Owner{ID: "shutdown-owner", Generation: "shutdown-generation"}
	if !runtime.BackgroundDeliveries.RegisterStart(backgrounddelivery.Registration{
		WorkspaceID: item.ID, ProcessID: started.ID, ExecutionID: started.ExecutionID, Owner: owner,
	}) {
		t.Fatal("background delivery registration failed")
	}
	application := &App{Tools: runtime}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	deliveries, err := runtime.BackgroundDeliveries.List(item.ID, owner)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("shutdown deliveries=%#v err=%v", deliveries, err)
	}
	if deliveries[0].ProcessID != started.ID || deliveries[0].Reason != shellruntime.BackgroundTerminalShutdown || deliveries[0].Status != shellruntime.ExecutionStatusCancelled {
		t.Fatalf("shutdown delivery=%#v", deliveries[0])
	}
	snapshot, err := runtime.Executions.Get(item.ID, started.ExecutionID)
	if err != nil || snapshot.Execution.Status != shellruntime.ExecutionStatusCancelled {
		t.Fatalf("shutdown execution=%#v err=%v", snapshot, err)
	}
}

func TestStopDetachesApprovalNotifications(t *testing.T) {
	manager := approval.NewManager("instance-stop-notifications")
	provider := &lifecycleNotificationProvider{called: make(chan struct{}, 1)}
	coordinator := notification.NewCoordinator(notification.CoordinatorOptions{Attempts: 1})
	coordinator.Register(provider)
	bridge := notification.NewApprovalBridge(manager.Events(), coordinator, notification.ApprovalBridgeOptions{Policy: func() notification.ApprovalPolicy {
		return notification.ApprovalPolicy{Enabled: true, Pending: true, Providers: map[string]bool{notification.ProviderDesktop: true}}
	}})
	if err := bridge.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	application := &App{Notifications: coordinator, ApprovalNotifications: bridge}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		CallerID: "caller-stop", RequestCorrelationID: "stop", SessionHash: "hash-stop", WorkspaceID: "ws_stop",
		Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"command": "echo stop"},
		GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow stop test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-stop", "ws_stop", "Allow stop test"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.called:
		t.Fatal("runtime shutdown left approval notification subscription active")
	case <-time.After(50 * time.Millisecond):
	}
	status := coordinator.Status(map[string]bool{notification.ProviderDesktop: true})
	if !status.Stopped {
		t.Fatalf("notification coordinator remained active after runtime shutdown: %#v", status)
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
