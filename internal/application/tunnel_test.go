package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestTunnelRuntimeConfigureSyncAndSecretPersistence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_runtime" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"id":"tunnel_runtime","name":"Runtime","description":"Runtime tunnel","organization_ids":["org_runtime"]}`))
	}))
	defer server.Close()
	setupTunnelApplicationRoot(t, tunnel.Config{})

	enabled, id, key, baseURL, organization := true, "tunnel_runtime", "runtime-secret", server.URL, "org_runtime"
	dashboard, err := ConfigureTunnelRuntime(t.Context(), TunnelRuntimeInput{Enabled: &enabled, ID: &id, APIKey: &key, ControlPlaneBaseURL: &baseURL, OrganizationID: &organization})
	if err != nil {
		t.Fatal(err)
	}
	if !dashboard.Config.Enabled || dashboard.Config.ID != id || dashboard.Config.APIKey != key || dashboard.Config.OrganizationID != organization || dashboard.Status.Metadata == nil || dashboard.Status.Metadata.Name != "Runtime" {
		t.Fatalf("dashboard=%#v", dashboard)
	}
	metadata, err := config.LoadTunnelMetadata(id)
	if err != nil || metadata.Name != "Runtime" {
		t.Fatalf("metadata=%#v err=%v", metadata, err)
	}
	assertTunnelSecretNotInManagedFiles(t, "runtime-secret")

	disabled := false
	if _, err := ConfigureTunnelRuntime(t.Context(), TunnelRuntimeInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.Enabled || loaded.Tunnel.ID != id || loaded.Tunnel.APIKey != key || loaded.Tunnel.ControlPlaneBaseURL != server.URL {
		t.Fatalf("patch configure overwrote unchanged fields: %#v", loaded.Tunnel)
	}
}

func TestTunnelAdminAndManagedLifecycle(t *testing.T) {
	created := false
	updated := false
	deleted := false
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels":
			if r.URL.Query().Get("workspace_id") != "ws_admin" {
				t.Fatalf("scope query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","description":"First","workspace_ids":["ws_admin"]}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels/tunnel_one":
			_, _ = w.Write([]byte(`{"id":"tunnel_one","name":"One","description":"First","workspace_ids":["ws_admin"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tunnels":
			created = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] != "Created" || body["description"] != "Created tunnel" {
				t.Fatalf("create body=%#v", body)
			}
			_, _ = w.Write([]byte(`{"id":"tunnel_created","name":"Created","description":"Created tunnel","workspace_ids":["ws_admin"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tunnels/tunnel_created":
			updated = true
			_, _ = w.Write([]byte(`{"id":"tunnel_created","name":"Renamed","description":"Created tunnel","workspace_ids":["ws_admin"]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/tunnels/tunnel_created":
			deleted = true
			_, _ = w.Write([]byte(`{"id":"tunnel_created","name":"Renamed","description":"Created tunnel","workspace_ids":["ws_admin"]}`))
		default:
			t.Fatalf("unexpected request=%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()
	setupTunnelApplicationRoot(t, tunnel.Config{ControlPlaneBaseURL: server.URL})

	scope := tunnel.AdminScope{WorkspaceID: "ws_admin"}
	storedScope, err := SetTunnelAdminKey(t.Context(), TunnelAdminKeyInput{Key: "admin-secret", Scope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	if storedScope.WorkspaceID != "ws_admin" || requests.Load() != 0 {
		t.Fatalf("set must only persist configured inputs: scope=%#v requests=%d", storedScope, requests.Load())
	}
	status, err := TunnelAdminKeyStatus()
	if err != nil || !status.Configured || status.Verified || status.Access.Read || status.Access.Manage || status.Scope.WorkspaceID != "ws_admin" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	assertTunnelSecretNotInManagedFiles(t, "admin-secret")

	count, verifiedScope, err := VerifyTunnelAdminKey(t.Context())
	if err != nil || count != 1 || verifiedScope.WorkspaceID != "ws_admin" {
		t.Fatalf("verify count=%d scope=%#v err=%v", count, verifiedScope, err)
	}
	status, err = TunnelAdminKeyStatus()
	if err != nil || !status.Verified || !status.Access.Read || !status.Access.Manage {
		t.Fatalf("verified status=%#v err=%v", status, err)
	}

	items, err := ListManagedTunnels(t.Context())
	if err != nil || len(items) != 1 || items[0].ID != "tunnel_one" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	got, err := GetManagedTunnel(t.Context(), "tunnel_one", ManagedTunnelOptions{})
	if err != nil || got.Metadata.Name != "One" {
		t.Fatalf("get=%#v err=%v", got, err)
	}
	used, err := UseManagedTunnel(t.Context(), "tunnel_one", ManagedTunnelUseOptions{RuntimeAPIKey: "runtime-secret"})
	if err != nil || !used.Configured {
		t.Fatalf("use=%#v err=%v", used, err)
	}
	selected, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if selected.Tunnel.ID != "tunnel_one" || selected.Tunnel.APIKey != "runtime-secret" {
		t.Fatalf("selected tunnel config=%#v", selected.Tunnel)
	}

	createdResult, err := CreateManagedTunnel(t.Context(), tunnel.CreateRequest{Name: "Created", Description: "Created tunnel"}, ManagedTunnelOptions{})
	if err != nil || !created || createdResult.Metadata.ID != "tunnel_created" {
		t.Fatalf("create=%#v called=%t err=%v", createdResult, created, err)
	}
	name := "Renamed"
	updatedResult, err := UpdateManagedTunnel(t.Context(), "tunnel_created", tunnel.UpdateRequest{Name: &name}, ManagedTunnelOptions{})
	if err != nil || !updated || updatedResult.Metadata.Name != "Renamed" {
		t.Fatalf("update=%#v called=%t err=%v", updatedResult, updated, err)
	}

	result, err := DeleteManagedTunnel(t.Context(), "tunnel_created", false)
	if err != nil || !deleted || result.Cleared {
		t.Fatalf("delete=%#v called=%t err=%v", result, deleted, err)
	}
	if _, err := config.LoadTunnelMetadata("tunnel_created"); !os.IsNotExist(err) {
		t.Fatalf("deleted metadata remains: %v", err)
	}
	if err := RemoveTunnelAdminKey(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err = TunnelAdminKeyStatus()
	if err != nil || status.Configured {
		t.Fatalf("admin key remained configured: %#v err=%v", status, err)
	}
}

func TestUseManagedTunnelAutoGeneratesRuntimeKey(t *testing.T) {
	const tunnelID = "tunnel_00000000000000000000000000000003"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels/"+tunnelID:
			_, _ = w.Write([]byte(`{"id":"` + tunnelID + `","name":"Auto","description":"Auto","organization_ids":["org_admin"]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organization/projects":
			_, _ = w.Write([]byte(`{"data":[{"id":"proj_default","name":"Default project","status":"active"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts":
			_, _ = w.Write([]byte(`{"data":[{"id":"svc_runtime","name":"codemcp tunnel runtime"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts/svc_runtime/api_keys":
			var body struct {
				Scopes []string `json:"scopes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Scopes) != 2 || body.Scopes[0] != "api.organization.tunnel.read" || body.Scopes[1] != "api.organization.tunnel.use" {
				t.Fatalf("scopes=%#v", body.Scopes)
			}
			_, _ = w.Write([]byte(`{"id":"key_runtime","value":"sk-runtime-generated"}`))
		default:
			t.Fatalf("unexpected request=%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()
	setupTunnelApplicationRoot(t, tunnel.Config{Admin: tunnel.AdminConfig{Key: "admin-secret", OrganizationID: "org_admin", ReadAccess: true, ManageAccess: true}, ControlPlaneBaseURL: server.URL})

	result, err := UseManagedTunnel(t.Context(), tunnelID, ManagedTunnelUseOptions{AutoGenerateRuntimeKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured {
		t.Fatalf("result=%#v", result)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Tunnel.Enabled || loaded.Tunnel.ID != tunnelID || loaded.Tunnel.APIKey != "sk-runtime-generated" || loaded.Tunnel.OrganizationID != "org_admin" {
		t.Fatalf("tunnel=%#v", loaded.Tunnel)
	}
	assertTunnelSecretNotInManagedFiles(t, "sk-runtime-generated")
}

func TestDeleteManagedTunnelCanClearSelectedRuntimeConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/tunnels/tunnel_selected" {
			_, _ = w.Write([]byte(`{"id":"tunnel_selected","name":"Selected","description":"Selected","organization_ids":["org_admin"]}`))
			return
		}
		t.Fatalf("unexpected request=%s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	setupTunnelApplicationRoot(t, tunnel.Config{Enabled: true, ID: "tunnel_selected", APIKey: "runtime-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", OrganizationID: "org_admin", ReadAccess: true, ManageAccess: true}, ControlPlaneBaseURL: server.URL, OrganizationID: "org_admin"})
	if _, err := config.SaveTunnelMetadata(tunnel.Metadata{ID: "tunnel_selected", Name: "Selected"}); err != nil {
		t.Fatal(err)
	}
	result, err := DeleteManagedTunnel(t.Context(), "tunnel_selected", true)
	if err != nil || !result.Cleared {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.Enabled || loaded.Tunnel.ID != "" || loaded.Tunnel.APIKey != "" || loaded.Tunnel.OrganizationID != "" || loaded.Tunnel.Admin.Key != "admin-secret" {
		t.Fatalf("runtime config not cleared safely: %#v", loaded.Tunnel)
	}
}

func TestTunnelOnlyConfigCannotDisableTunnel(t *testing.T) {
	setupTunnelApplicationRoot(t, tunnel.Config{Enabled: true, ID: "tunnel_only", APIKey: "runtime-secret"})
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.Enabled = false
	cfg.Admin.Enabled = false
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := SetTunnelEnabled(t.Context(), false); err == nil || !strings.Contains(err.Error(), "at least one MCP transport") {
		t.Fatalf("disable sole tunnel err=%v", err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Tunnel.Enabled || loaded.Server.Enabled {
		t.Fatalf("invalid transport mutation persisted: server=%#v tunnel=%#v", loaded.Server, loaded.Tunnel)
	}
}

func TestManagedCreateFailureDoesNotChangeRuntimeConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()
	setupTunnelApplicationRoot(t, tunnel.Config{ID: "tunnel_existing", APIKey: "runtime-secret", Admin: tunnel.AdminConfig{Key: "admin-secret", WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL})
	_, err := CreateManagedTunnel(context.Background(), tunnel.CreateRequest{Name: "Broken", Description: "Broken", WorkspaceIDs: []string{"ws_admin"}}, ManagedTunnelOptions{Configure: true, RuntimeAPIKey: "replacement", Enable: true})
	if err == nil {
		t.Fatal("create unexpectedly succeeded")
	}
	loaded, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Tunnel.ID != "tunnel_existing" || loaded.Tunnel.APIKey != "runtime-secret" || loaded.Tunnel.Enabled {
		t.Fatalf("failed remote mutation changed local runtime config: %#v", loaded.Tunnel)
	}
}

func setupTunnelApplicationRoot(t *testing.T, tunnelConfig tunnel.Config) {
	t.Helper()
	deferRoot := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(deferRoot) })
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel = tunnelConfig
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func assertTunnelSecretNotInManagedFiles(t *testing.T, secret string) {
	t.Helper()
	for _, path := range []string{config.Path(), configformat.StructuredPath(config.RootPath(), "tunnel")} {
		data, err := os.ReadFile(path)
		if err != nil && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret leaked in %s: %s", path, data)
		}
	}
}
