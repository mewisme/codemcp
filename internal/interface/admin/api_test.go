package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/notification"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

type adminNotificationProvider struct {
	name      string
	available bool
}

func (p adminNotificationProvider) Name() string                                       { return p.name }
func (p adminNotificationProvider) Available() bool                                    { return p.available }
func (p adminNotificationProvider) Notify(context.Context, notification.Message) error { return nil }

func TestCompletionNotificationPolicyEnablesSharedProviderStatus(t *testing.T) {
	cfg := config.Default()
	cfg.Notifications.Approval.Enabled = false
	cfg.Notifications.Completion.Enabled = true
	cfg.Notifications.Completion.DesktopEnabled = true
	cfg.Notifications.Completion.TelegramEnabled = false

	coordinator := notification.NewCoordinator(notification.CoordinatorOptions{})
	coordinator.Register(adminNotificationProvider{name: notification.ProviderDesktop, available: true})
	defer coordinator.Stop()

	handler := New(API{Config: config.NewRuntimeStore(cfg), Notifications: coordinator})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var snapshot notification.StatusSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]notification.ProviderStatus{}
	for _, status := range snapshot.Providers {
		statuses[status.Provider] = status
	}
	if desktop := statuses[notification.ProviderDesktop]; !desktop.Enabled || !desktop.Registered || desktop.Health != notification.ProviderHealthHealthy {
		t.Fatalf("desktop=%#v", desktop)
	}
	if telegram := statuses[notification.ProviderTelegram]; telegram.Enabled {
		t.Fatalf("telegram=%#v", telegram)
	}
}

func TestNotificationStatusEndpointExposesProviderHealth(t *testing.T) {
	cfg := config.Default()
	cfg.Notifications.Approval.Enabled = true
	cfg.Notifications.Approval.DesktopEnabled = true
	cfg.Notifications.Approval.TelegramEnabled = true
	coordinator := notification.NewCoordinator(notification.CoordinatorOptions{})
	coordinator.Register(adminNotificationProvider{name: notification.ProviderDesktop, available: true})
	defer coordinator.Stop()
	handler := New(API{Config: config.NewRuntimeStore(cfg), Notifications: coordinator})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var snapshot notification.StatusSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Providers) != 2 {
		t.Fatalf("providers=%#v", snapshot.Providers)
	}
	statuses := map[string]notification.ProviderStatus{}
	for _, status := range snapshot.Providers {
		statuses[status.Provider] = status
	}
	if desktop := statuses[notification.ProviderDesktop]; !desktop.Enabled || !desktop.Registered || desktop.Health != notification.ProviderHealthHealthy {
		t.Fatalf("desktop=%#v", desktop)
	}
	if telegram := statuses[notification.ProviderTelegram]; !telegram.Enabled || telegram.Registered || telegram.Health != notification.ProviderHealthUnavailable {
		t.Fatalf("telegram=%#v", telegram)
	}
}

type adminUpstreamClient struct {
	tools    []upstream.Tool
	toolsErr error
}

func (*adminUpstreamClient) Connect(context.Context, upstream.Server) error { return nil }
func (*adminUpstreamClient) Close(context.Context, string) error            { return nil }
func (c *adminUpstreamClient) Tools(context.Context, string) ([]upstream.Tool, error) {
	if c.toolsErr != nil {
		return nil, c.toolsErr
	}
	return append([]upstream.Tool(nil), c.tools...), nil
}
func (*adminUpstreamClient) Call(context.Context, string, string, map[string]any) (upstream.CallResult, error) {
	return upstream.CallResult{}, nil
}
func (*adminUpstreamClient) PID(string) int { return 0 }

func TestTunnelConfigRedactsSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.Tunnel = tunnel.Config{Enabled: true, ID: "tunnel_test", APIKey: "runtime-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}}
	handler := New(API{Tunnel: tunnel.NewConfigured(cfg.Tunnel, nil), Config: config.NewRuntimeStore(cfg)})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/tunnel/config", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "runtime-secret") || strings.Contains(recorder.Body.String(), "admin-secret") {
		t.Fatalf("tunnel API leaked a secret: %s", recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"id":"tunnel_test"`) || !strings.Contains(body, `"runtime_key_configured":true`) || !strings.Contains(body, `"admin":{"enabled":true,"key_configured":true,"configured":true,"workspace_id":"ws_admin"`) {
		t.Fatalf("tunnel config fields missing: %s", body)
	}
	for _, legacy := range []string{"admin_key", "admin_workspace_id", "admin_key_configured"} {
		if strings.Contains(body, legacy) {
			t.Fatalf("tunnel config exposed flat admin field %q: %s", legacy, body)
		}
	}
}

func TestTunnelConfigureRollsBackRuntimeAndMemoryWhenPersistenceFails(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	server := tunnelMetadataServer(t, "new-secret")
	defer server.Close()
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel = tunnel.Config{Enabled: false, ID: "tunnel_old", APIKey: "old-secret", ControlPlaneBaseURL: server.URL}
	client := tunnel.NewConfigured(cfg.Tunnel, nil)
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Tunnel: client, Config: store, saveConfig: func(config.Config) error { return errors.New("persistence failed") }})
	recorder := httptest.NewRecorder()
	body := fmt.Sprintf(`{"enabled":false,"id":"tunnel_new","api_key":"new-secret","control_plane_base_url":%q}`, server.URL)
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/tunnel", strings.NewReader(body)))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Tunnel; got.ID != "tunnel_old" || got.APIKey != "old-secret" {
		t.Fatalf("in-memory config changed after persistence failure: %#v", got)
	}
	if got := client.Config(); got.ID != "tunnel_old" || got.APIKey != "old-secret" {
		t.Fatalf("runtime config changed after persistence failure: %#v", got)
	}
}

func TestTunnelConfigurePreservesSecretFromSerializedConfigStore(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	server := tunnelMetadataServer(t, "store-secret")
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel = tunnel.Config{Enabled: false, ID: "tunnel_store", APIKey: "store-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	client := tunnel.NewConfigured(tunnel.Config{Enabled: false, ID: "tunnel_runtime", APIKey: "stale-runtime-secret"}, nil)
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Tunnel: client, Config: store})
	recorder := httptest.NewRecorder()
	body := fmt.Sprintf(`{"enabled":false,"id":"tunnel_new","control_plane_base_url":%q}`, server.URL)
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/tunnel", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Tunnel; got.ID != "tunnel_new" || got.APIKey != "store-secret" || got.Admin.Key != "admin-secret" || got.Admin.WorkspaceID != "ws_admin" {
		t.Fatalf("stored tunnel config = %#v", got)
	}
	if got := client.Config(); got.ID != "tunnel_new" || got.APIKey != "store-secret" || got.Admin.Key != "admin-secret" || got.Admin.WorkspaceID != "ws_admin" {
		t.Fatalf("runtime tunnel config = %#v", got)
	}
}

func TestConfigAPIHidesTokenHashes(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-secret-hash"
	cfg.Auth.AdminTokenHash = "admin-secret-hash"
	recorder := httptest.NewRecorder()
	New(API{Config: config.NewRuntimeStore(cfg)}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if strings.Contains(body, "secret-hash") || strings.Contains(body, "token_hash") {
		t.Fatalf("config API leaked token hashes: %s", body)
	}
	if !strings.Contains(body, `"mcp_token_configured":true`) || !strings.Contains(body, `"admin_token_configured":true`) {
		t.Fatalf("configured state missing: %s", body)
	}
	if strings.Contains(body, `"host"`) || !strings.Contains(body, `"server":{"enabled":true`) || !strings.Contains(body, `"expose":{"mode":"none","interfaces":[]}`) {
		t.Fatalf("server exposure view is invalid: %s", body)
	}
}

func TestConfigAPIMutationAutomaticallyReloadsRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	store := config.NewRuntimeStore(cfg)
	saved, reloaded := false, false
	handler := New(API{
		Config: store,
		saveConfig: func(next config.Config) error {
			saved = true
			if next.Server.Port != 41021 {
				t.Fatalf("saved port=%d", next.Server.Port)
			}
			return nil
		},
		ReloadConfig: func(next config.Config) error {
			reloaded = true
			if next.Server.Port != 41021 {
				t.Fatalf("reloaded port=%d", next.Server.Port)
			}
			return nil
		},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"server":{"port":41021}}`)))
	if recorder.Code != http.StatusOK || !saved || !reloaded {
		t.Fatalf("status=%d saved=%t reloaded=%t body=%s", recorder.Code, saved, reloaded, recorder.Body.String())
	}
}

func TestConfigAPIRejectsDisablingLastMCPTransport(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store, saveConfig: func(config.Config) error { t.Fatal("invalid transport config must not persist"); return nil }})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"server":{"enabled":false}}`)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "at least one MCP transport") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !store.Snapshot().Server.Enabled {
		t.Fatal("invalid transport config mutated store")
	}
}

func TestTunnelAPICannotStopLastMCPTransport(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Enabled = false
	cfg.Admin.Enabled = false
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel = tunnel.Config{Enabled: true, ID: "tunnel_only", APIKey: "runtime-secret"}
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Tunnel: tunnel.NewConfigured(cfg.Tunnel, nil), Config: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/tunnel", nil))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "MCP HTTP is disabled") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestConfigAPIWildcardExposureRequiresBothAuth(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store, saveConfig: func(config.Config) error { return nil }})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"server":{"port":37421,"expose":{"mode":"0.0.0.0","interfaces":[]}}}`)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "allow_insecure_http") {
		t.Fatalf("wildcard without HTTP opt-in status = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"server":{"port":37421,"expose":{"mode":"0.0.0.0","interfaces":[]},"allow_insecure_http":true}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("wildcard status = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"auth":{"mcp_enabled":false,"admin_enabled":true}}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("disable auth status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot(); got.Server.Expose.Mode != config.ExposureWildcard || !got.Auth.MCPEnabled || !got.Auth.AdminEnabled {
		t.Fatalf("invalid wildcard auth state committed: %#v", got)
	}
}

func TestConfigAPIPartialAuthPatchPreservesOmittedSetting(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store, saveConfig: func(config.Config) error { return nil }})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"auth":{"mcp_enabled":true}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	got := store.Snapshot()
	if !got.Auth.MCPEnabled || !got.Auth.AdminEnabled {
		t.Fatalf("partial auth patch changed omitted setting: %#v", got.Auth)
	}
}

func TestHealthReportsAdminAuthState(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.AdminEnabled = false
	recorder := httptest.NewRecorder()
	New(API{Config: config.NewRuntimeStore(cfg)}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"auth_enabled":false`) {
		t.Fatalf("health = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestConfigAPIUsesCurrentIntegrationReadModel(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store, saveConfig: func(config.Config) error { return nil }})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, legacy := range []string{`"interactive"`, `"features"`, `"builtins"`} {
		if strings.Contains(body, legacy) {
			t.Fatalf("legacy config authority %s exposed: %s", legacy, body)
		}
	}
	for _, want := range []string{`"integrations"`, `"ponytail"`, `"caveman"`, `"rtk"`, `"codegraph"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("integration read model missing %s: %s", want, body)
		}
	}
}

func TestConfigAPIIntegrationPatchUpdatesRuntimeActiveState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithIntegrations(cfg.Integrations)
	workspaceItem, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Config: store, Tools: runtime})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"caveman":{"active":false}}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Integrations; !got.Ponytail.Active || got.Caveman.Active || !got.RTK.Enabled || got.RTK.Path != "" || got.CodeGraph.Enabled || got.CodeGraph.Path != "" {
		t.Fatalf("stored integrations = %#v", got)
	}
	if _, ok := runtime.Registry.Schema("ponytail_turn"); !ok {
		t.Fatal("ponytail controller tool disappeared")
	}
	if _, ok := runtime.Registry.Schema("caveman_turn"); !ok {
		t.Fatal("caveman controller tool disappeared")
	}
	result, err := runtime.Call(context.Background(), "caveman_turn", map[string]any{"workspace_id": workspaceItem.ID, "prompt": "continue"})
	if err != nil || result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, `"active":false`) {
		t.Fatalf("caveman runtime result = %#v err=%v", result, err)
	}
	if !strings.Contains(recorder.Body.String(), `"integrations":{"ponytail":{"active":true,"mode":"full"},"caveman":{"active":false,"mode":"full"},"rtk":{"enabled":true,"path":""},"codegraph":{"enabled":false,"path":""}}`) {
		t.Fatalf("integration config missing from response: %s", recorder.Body.String())
	}
}

func TestConfigAPIPonytailModeUpdatesLiveRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithIntegrations(cfg.Integrations)
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Config: store, Tools: runtime})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"ponytail":{"active":true,"mode":"ULTRA"}}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Integrations.Ponytail; !got.Active || got.Mode != "ultra" {
		t.Fatalf("stored ponytail = %#v", got)
	}
	result, err := runtime.Call(context.Background(), "ponytail_turn", map[string]any{"workspace_id": item.ID, "prompt": "continue"})
	if err != nil || result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, `"mode":"ultra"`) || !strings.Contains(result.Content[0].Text, "PONYTAIL MODE ACTIVE") {
		t.Fatalf("ponytail runtime result = %#v err=%v", result, err)
	}
}

func TestConfigAPIRejectsInvalidPonytailMode(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"ponytail":{"mode":"review"}}}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if got := store.Snapshot().Integrations.Ponytail.Mode; got != "full" {
		t.Fatalf("invalid mode mutated store: %q", got)
	}
}

func TestConfigAPICavemanModeUpdatesLiveRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithIntegrations(cfg.Integrations)
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Config: store, Tools: runtime})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"caveman":{"active":true,"mode":"WENYAN-ULTRA"}}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Integrations.Caveman; !got.Active || got.Mode != "wenyan-ultra" {
		t.Fatalf("stored caveman = %#v", got)
	}
	result, err := runtime.Call(context.Background(), "caveman_turn", map[string]any{"workspace_id": item.ID, "prompt": "continue"})
	if err != nil || result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, `"mode":"wenyan-ultra"`) || !strings.Contains(result.Content[0].Text, "CAVEMAN MODE ACTIVE") {
		t.Fatalf("caveman runtime result = %#v err=%v", result, err)
	}
}

func TestConfigAPIRejectsInvalidCavemanMode(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"caveman":{"mode":"wenyan"}}}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if got := store.Snapshot().Integrations.Caveman.Mode; got != "full" {
		t.Fatalf("invalid mode mutated store: %q", got)
	}
}

func TestConfigAPIIntegrationPersistenceFailureRollsBackRuntimeState(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithIntegrations(cfg.Integrations)
	handler := New(API{Config: store, Tools: runtime, saveConfig: func(config.Config) error { return errors.New("persistence failed") }})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"ponytail":{"active":false}}}`)))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Integrations; !got.Ponytail.Active || !got.Caveman.Active {
		t.Fatalf("store changed after persistence failure: %#v", got)
	}
	if got := runtime.Integrations(); !got.Ponytail.Active || !got.Caveman.Active {
		t.Fatalf("runtime integrations changed after persistence failure: %#v", got)
	}
	if _, ok := runtime.Registry.Schema("ponytail_turn"); !ok {
		t.Fatal("ponytail tool was not restored after persistence failure")
	}
	if _, ok := runtime.Registry.Schema("caveman_turn"); !ok {
		t.Fatal("caveman tool disappeared after persistence failure")
	}
}

func TestConfigAPIIntegrationActivePatchUsesCanonicalField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithIntegrations(cfg.Integrations)
	handler := New(API{Config: store, Tools: runtime})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"caveman":{"active":false}}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Integrations; !got.Ponytail.Active || got.Caveman.Active {
		t.Fatalf("integration patch did not apply: %#v", got)
	}
	if strings.Contains(recorder.Body.String(), `"caveman":{"enabled"`) || !strings.Contains(recorder.Body.String(), `"caveman":{"active":false,"mode":"full"}`) {
		t.Fatalf("non-canonical integration field leaked into response: %s", recorder.Body.String())
	}
}

func TestConfigAPIExecutableIntegrationPatchUpdatesRuntimeState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithIntegrations(cfg.Integrations)
	handler := New(API{Config: store, Tools: runtime, saveConfig: func(config.Config) error { return nil }})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"integrations":{"rtk":{"enabled":false},"codegraph":{"enabled":true}}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored := store.Snapshot().Integrations
	if stored.RTK.Enabled || !stored.CodeGraph.Enabled {
		t.Fatalf("stored executable integrations = %#v", stored)
	}
	live := runtime.Integrations()
	if live.RTK.Enabled || !live.CodeGraph.Enabled {
		t.Fatalf("runtime executable integrations = %#v", live)
	}
	for _, want := range []string{
		`"rtk":{"enabled":false,"path":""}`,
		`"codegraph":{"enabled":true,"path":""}`,
	} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("integration response missing %q: %s", want, recorder.Body.String())
		}
	}
}

func TestConfigAPIExecutableIntegrationPatchRejectsRelativePaths(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	for _, body := range []string{
		`{"integrations":{"rtk":{"path":"relative/rtk"}}}`,
		`{"integrations":{"codegraph":{"path":"relative/codegraph"}}}`,
	} {
		store := config.NewRuntimeStore(cfg)
		handler := New(API{Config: store})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d want=%d response=%s", body, recorder.Code, http.StatusBadRequest, recorder.Body.String())
		}
		if got := store.Snapshot().Integrations; got != cfg.Integrations {
			t.Fatalf("invalid executable integration patch mutated store: %#v", got)
		}
	}
}

func TestConfigAPIPermissionsPatchUpdatesRuntimeAccess(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithAccess(cfg.Integrations, cfg.Permissions.AllowDirs)
	item, err := runtime.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.Workspaces.ResolveDirectory(item.ID, allowed); err == nil {
		t.Fatal("unconfigured directory was accessible")
	}
	handler := New(API{Config: store, Tools: runtime, saveConfig: func(config.Config) error { return nil }})
	body := fmt.Sprintf(`{"permissions":{"allow_dirs":[%q]}}`, allowed)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, _, err := runtime.Workspaces.ResolveDirectory(item.ID, allowed); err != nil {
		t.Fatalf("runtime access was not updated: %v", err)
	}
	canonicalAllowed, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		t.Fatal(err)
	}
	canonicalAllowed = filepath.Clean(canonicalAllowed)
	if got := store.Snapshot().Permissions.AllowDirs; len(got) != 1 || got[0] != canonicalAllowed {
		t.Fatalf("stored permissions = %#v", got)
	}
	if !strings.Contains(recorder.Body.String(), `"permissions":{"allow_dirs":[`) {
		t.Fatalf("permissions missing from response: %s", recorder.Body.String())
	}
}

func TestConfigAPIShellPathPatch(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store, saveConfig: func(config.Config) error { return nil }})
	path := filepath.Join(t.TempDir(), "bin")
	recorder := httptest.NewRecorder()
	body := fmt.Sprintf(`{"shell":{"path":[%q]}}`, path)
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := store.Snapshot().Shell.Path; len(got) != 1 || got[0] != filepath.Clean(path) {
		t.Fatalf("shell path = %#v", got)
	}
	if !strings.Contains(recorder.Body.String(), `"shell":{"path":[`) {
		t.Fatalf("shell missing from response: %s", recorder.Body.String())
	}
}

func TestConfigAPIPermissionsPersistenceFailureKeepsRuntimeAccess(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	store := config.NewRuntimeStore(cfg)
	runtime := tools.NewRuntimeWithAccess(cfg.Integrations, cfg.Permissions.AllowDirs)
	item, err := runtime.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Config: store, Tools: runtime, saveConfig: func(config.Config) error { return errors.New("persistence failed") }})
	body := fmt.Sprintf(`{"permissions":{"allow_dirs":[%q]}}`, allowed)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body)))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, _, err := runtime.Workspaces.ResolveDirectory(item.ID, allowed); err == nil {
		t.Fatal("runtime access changed after persistence failure")
	}
	if len(store.Snapshot().Permissions.AllowDirs) != 0 {
		t.Fatalf("store changed after persistence failure: %#v", store.Snapshot().Permissions)
	}
}

func TestWorkspaceAPICRUD(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	root := t.TempDir()
	handler := New(API{Workspaces: manager})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"path":`+jsonString(root)+`}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("register status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var item workspace.Workspace
	if err := json.Unmarshal(recorder.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.ID == "" || item.Path == "" {
		t.Fatalf("workspace = %#v", item)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), item.ID) {
		t.Fatalf("list = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+item.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("show = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+item.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := manager.Get(item.ID); err == nil {
		t.Fatal("workspace still registered")
	}
}

func TestWorkspaceAPIRelocate(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	parent := t.TempDir()
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	if err := os.Mkdir(oldRoot, 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Workspaces: manager})
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+item.ID+"/relocate", strings.NewReader(`{"path":`+jsonString(newRoot)+`}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("relocate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var relocated workspace.Workspace
	if err := json.Unmarshal(recorder.Body.Bytes(), &relocated); err != nil {
		t.Fatal(err)
	}
	if relocated.ID != item.ID || relocated.Path == item.Path {
		t.Fatalf("relocated=%#v", relocated)
	}
	resolved, err := manager.Get(item.ID)
	if err != nil || resolved.ID != item.ID || resolved.Path != relocated.Path {
		t.Fatalf("stable lookup=%#v err=%v", resolved, err)
	}
}

func TestWorkspaceAPIPurgeRequiresConfirmationAndKeepsProjectFiles(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "project.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := New(API{Workspaces: manager})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+item.ID+"/purge", strings.NewReader(`{"confirm":false}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed purge status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, workspace.LocalDirName)); err != nil {
		t.Fatalf("local state removed without confirmation: %v", err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+item.ID+"/purge", strings.NewReader(`{"confirm":true}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, workspace.LocalDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local state still exists: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("project file changed: data=%q err=%v", data, err)
	}
}

func TestUpstreamAPIManagementAndRedaction(t *testing.T) {
	client := &adminUpstreamClient{tools: []upstream.Tool{{Name: "echo", Description: "Echo", InputSchema: map[string]any{"type": "object"}}}}
	manager := upstream.NewManagerWithClient(nil, client)
	handler := New(API{Upstream: manager})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/upstream", strings.NewReader(`{"id":"server-1","name":"Server","transport":"http","url":"https://example.test/mcp","enabled":true,"headers":{"Authorization":"Bearer secret","X-Test":"ok"},"env":{"API_TOKEN":"secret","MODE":"test"}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("add status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "Bearer secret") || strings.Contains(recorder.Body.String(), `"API_TOKEN":"secret"`) {
		t.Fatalf("server response leaked secrets: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/upstream/server-1/status?refresh=true", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"health":"connected"`) {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/upstream/server-1/tools", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"echo"`) {
		t.Fatalf("tools = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/upstream/server-1", strings.NewReader(`{"name":"Updated","transport":"http","url":"https://example.test/mcp","enabled":false,"expose":"none"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", recorder.Code, recorder.Body.String())
	}
	updated, ok := manager.Get("server-1")
	if !ok || updated.Name != "Updated" || updated.Enabled {
		t.Fatalf("updated = %#v", updated)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/upstream/server-1", strings.NewReader(`{"name":"Updated","transport":"http","url":"https://example.test/mcp","enabled":false,"expose":"none","headers":{"Authorization":"<redacted>"},"env":{"API_TOKEN":"<redacted>"}}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("redacted update = %d: %s", recorder.Code, recorder.Body.String())
	}
	updated, _ = manager.Get("server-1")
	if updated.Headers["Authorization"] != "Bearer secret" || updated.Env["API_TOKEN"] != "secret" {
		t.Fatalf("redacted values overwrote stored secrets: %#v %#v", updated.Headers, updated.Env)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/upstream/server-1", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if len(manager.List()) != 0 {
		t.Fatalf("server was not removed: %+v", manager.List())
	}
}

func TestUpstreamAPIRejectsInvalidConfig(t *testing.T) {
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	handler := New(API{Upstream: manager})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/upstream", strings.NewReader(`{"id":"server-1","name":"Server","transport":"http","enabled":true}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if len(manager.List()) != 0 {
		t.Fatalf("invalid server was persisted: %+v", manager.List())
	}
}

func TestUpstreamAPIReportsProxyRefreshFailureAndPreservesCatalog(t *testing.T) {
	client := &adminUpstreamClient{tools: []upstream.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	manager := upstream.NewManagerWithClient(nil, client)
	server := upstream.Server{ID: "server-1", Name: "Server", Transport: "http", URL: "https://example.test/mcp", Enabled: true, Expose: "all"}
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	runtime := &tools.Runtime{Registry: tools.NewRegistry(), Upstream: manager}
	if err := tools.RefreshUpstreamProxies(context.Background(), runtime.Registry, manager, false); err != nil {
		t.Fatal(err)
	}
	client.toolsErr = errors.New("upstream unavailable")
	handler := New(API{Upstream: manager, Tools: runtime})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/upstream/server-1", strings.NewReader(`{"name":"Updated","transport":"http","url":"https://example.test/mcp","enabled":true,"expose":"all"}`)))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "proxy refresh failed") {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, ok := runtime.Registry.Schema("server-1__echo"); !ok {
		t.Fatal("previous proxy catalog was removed after Admin refresh failure")
	}
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func tunnelMetadataServer(t *testing.T, key string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/tunnels/") {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/tunnels/")
		_, _ = fmt.Fprintf(w, `{"id":%q,"name":"Persisted tunnel","description":"Test metadata","workspace_ids":["ws_test"]}`, id)
	}))
}

func TestTunnelAdminKeyPutStoresWithoutVerificationAndPostVerifies(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" || r.URL.Query().Get("workspace_id") != "ws_admin" {
			t.Fatalf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer sk-admin" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","description":"Managed"}]}`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.ControlPlaneBaseURL = server.URL
	client := tunnel.NewConfigured(cfg.Tunnel, nil)
	store := config.NewRuntimeStore(cfg)
	var saved config.Config
	handler := New(API{Tunnel: client, Config: store, saveConfig: func(next config.Config) error { saved = next; return nil }})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/tunnel/admin/key", strings.NewReader(`{"admin_key":"sk-admin","workspace_id":"ws_admin"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("PUT unexpectedly verified remotely: calls=%d", calls.Load())
	}
	if got := store.Snapshot().Tunnel; got.Admin.Key != "sk-admin" || got.Admin.WorkspaceID != "ws_admin" || !tunnel.AdminConfigured(got) {
		t.Fatalf("stored admin tunnel config = %#v", got)
	}
	if saved.Tunnel.Admin.Key != "sk-admin" || saved.Tunnel.Admin.WorkspaceID != "ws_admin" || client.Config().Admin.Key != "sk-admin" {
		t.Fatalf("admin key was not persisted/synced: saved=%#v client=%#v", saved.Tunnel, client.Config())
	}
	if strings.Contains(recorder.Body.String(), "sk-admin") || !strings.Contains(recorder.Body.String(), `"configured":true`) || !strings.Contains(recorder.Body.String(), `"verified":false`) {
		t.Fatalf("unexpected admin key response: %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/tunnel/admin/key", nil))
	if recorder.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("verify status=%d calls=%d body=%s", recorder.Code, calls.Load(), recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"verified":true`) || !strings.Contains(recorder.Body.String(), `"tunnels":1`) {
		t.Fatalf("unexpected verify response: %s", recorder.Body.String())
	}
}

func TestTunnelAdminKeyFailedExplicitVerificationDoesNotDiscardConfiguredInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "forbidden", http.StatusForbidden) }))
	defer server.Close()
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.ControlPlaneBaseURL = server.URL
	store := config.NewRuntimeStore(cfg)
	var saves atomic.Int32
	handler := New(API{Tunnel: tunnel.NewConfigured(cfg.Tunnel, nil), Config: store, saveConfig: func(config.Config) error { saves.Add(1); return nil }})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/tunnel/admin/key", strings.NewReader(`{"admin_key":"sk-denied","workspace_id":"ws_admin"}`)))
	if recorder.Code != http.StatusOK || store.Snapshot().Tunnel.Admin.Key != "sk-denied" || saves.Load() != 1 {
		t.Fatalf("status=%d config=%#v body=%s", recorder.Code, store.Snapshot().Tunnel, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/tunnel/admin/key", nil))
	if recorder.Code != http.StatusBadRequest || store.Snapshot().Tunnel.Admin.Key != "sk-denied" || store.Snapshot().Tunnel.Admin.Verified || saves.Load() != 1 {
		t.Fatalf("verify status=%d saves=%d config=%#v body=%s", recorder.Code, saves.Load(), store.Snapshot().Tunnel, recorder.Body.String())
	}
}

func TestManagedTunnelAPIListsWithStoredAdminKey(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" || r.URL.Query().Get("workspace_id") != "ws_admin" {
			t.Fatalf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer sk-admin" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","description":"Primary"},{"id":"tunnel_two","name":"Two","description":"Secondary"}]}`))
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel = tunnel.Config{Admin: tunnel.AdminConfig{Key: "sk-admin", WorkspaceID: "ws_admin", ReadAccess: true, ManageAccess: true}, ControlPlaneBaseURL: server.URL}
	handler := New(API{Tunnel: tunnel.NewConfigured(cfg.Tunnel, nil), Config: config.NewRuntimeStore(cfg)})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/tunnel/managed", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"tunnel_two"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestManagedTunnelUseReusesRuntimeKeyAndSwitchesConfig(t *testing.T) {
	const selectedID = "tunnel_00000000000000000000000000000002"
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	var adminFetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/poll") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"commands":[]}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/"+selectedID {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") == "Bearer sk-admin" {
			adminFetches.Add(1)
		}
		_, _ = w.Write([]byte(`{"id":"` + selectedID + `","name":"Two","description":"Secondary","organization_ids":["org_two"],"workspace_ids":["ws_admin"]}`))
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel = tunnel.Config{Enabled: false, ID: "tunnel_one", APIKey: "runtime-key", Admin: tunnel.AdminConfig{Key: "sk-admin", WorkspaceID: "ws_admin", ReadAccess: true, ManageAccess: true}, ControlPlaneBaseURL: server.URL, OrganizationID: "org_one"}
	runtime := tools.NewRuntimeWithAccess(cfg.Integrations, cfg.Permissions.AllowDirs, nil)
	client := tunnel.NewConfigured(cfg.Tunnel, runtime)
	defer client.Stop()
	store := config.NewRuntimeStore(cfg)
	var saved config.Config
	handler := New(API{Tunnel: client, Config: store, saveConfig: func(next config.Config) error { saved = next; return nil }})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/tunnel/managed/use", strings.NewReader(`{"id":"`+selectedID+`"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if adminFetches.Load() == 0 {
		t.Fatal("managed tunnel selection did not fetch metadata with the admin key")
	}
	for label, got := range map[string]tunnel.Config{"store": store.Snapshot().Tunnel, "saved": saved.Tunnel, "runtime": client.Config()} {
		if !got.Enabled || got.ID != selectedID || got.APIKey != "runtime-key" || got.OrganizationID != "org_two" {
			t.Fatalf("%s tunnel config = %#v", label, got)
		}
	}
	if strings.Contains(recorder.Body.String(), "runtime-key") || strings.Contains(recorder.Body.String(), "sk-admin") {
		t.Fatalf("managed use response leaked credential: %s", recorder.Body.String())
	}
}

func TestManagedTunnelAPIReadOnlyAccessLimitsMutations(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_one" {
			t.Fatalf("unexpected upstream request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"tunnel_one","name":"One","description":"Readable","workspace_ids":["ws_admin"]}`))
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel = tunnel.Config{Admin: tunnel.AdminConfig{Key: "sk-read", WorkspaceID: "ws_admin", ReadAccess: true}, ControlPlaneBaseURL: server.URL}
	handler := New(API{Tunnel: tunnel.NewConfigured(cfg.Tunnel, nil), Config: config.NewRuntimeStore(cfg)})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
		return recorder
	}
	if recorder := request(http.MethodGet, "/api/tunnel/managed/tunnel_one", ""); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"name":"One"`) {
		t.Fatalf("read status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, call := range []struct{ method, path, body string }{{http.MethodGet, "/api/tunnel/managed", ""}, {http.MethodPost, "/api/tunnel/managed", `{"name":"New","description":"New","workspace_ids":["ws_admin"]}`}, {http.MethodPut, "/api/tunnel/managed/tunnel_one", `{"name":"Updated"}`}, {http.MethodDelete, "/api/tunnel/managed/tunnel_one", ""}} {
		if recorder := request(call.method, call.path, call.body); recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s status=%d body=%s", call.method, call.path, recorder.Code, recorder.Body.String())
		}
	}
}
