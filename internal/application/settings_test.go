package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestSettingServiceReadListDiffWhyAndDefaultReset(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()

	result, err := service.Set(t.Context(), "server.port", "40123")
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "40123" {
		t.Fatalf("set value=%q", result.Value)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != 40123 {
		t.Fatalf("persisted port=%d", loaded.Server.Port)
	}

	listed, err := service.List(t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	foundPort := false
	for _, item := range listed {
		if item.Spec.InternalOnly || item.Spec.Key == "auth.mcp_token_hash" || item.Spec.Key == "auth.admin_token_hash" {
			t.Fatalf("internal setting leaked into list: %#v", item)
		}
		if item.Spec.Key == "server.port" {
			foundPort = item.Value == "40123"
		}
	}
	if !foundPort {
		t.Fatalf("server.port missing from list: %#v", listed)
	}

	diff, err := service.Diff(t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	foundDiff := false
	for _, item := range diff {
		if item.Spec.Key == "server.port" {
			foundDiff = item.Value == "40123" && item.Baseline == "37421"
		}
	}
	if !foundDiff {
		t.Fatalf("server.port missing from diff: %#v", diff)
	}

	why, err := service.Why(t.Context(), "server.port")
	if err != nil {
		t.Fatal(err)
	}
	if why.Spec.Key != "server.port" || !why.HasBaseline || why.Baseline != "37421" {
		t.Fatalf("why=%#v", why)
	}
	if _, err := service.Why(t.Context(), "auth.mcp_token_hash"); err == nil {
		t.Fatal("internal auth hash was accepted as a user setting")
	}

	if _, err := service.Unset(t.Context(), "server.port"); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != config.Default().Server.Port {
		t.Fatalf("unset port=%d want=%d", loaded.Server.Port, config.Default().Server.Port)
	}
}

func TestSettingServiceStaticMutationMatchesCanonicalConfigAuthority(t *testing.T) {
	isolateSettingServiceConfig(t)
	initial, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSettingService().Set(t.Context(), "server.port", "40123"); err != nil {
		t.Fatal(err)
	}
	generic, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	if err := config.Save(initial); err != nil {
		t.Fatal(err)
	}
	if _, err := SetConfigField(t.Context(), "server.port", "40123"); err != nil {
		t.Fatal(err)
	}
	scoped, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(generic, scoped) {
		t.Fatalf("generic/scoped config mismatch\ngeneric=%#v\nscoped=%#v", generic, scoped)
	}
}

func TestEveryWritableStaticSettingMutatesFromFreshConfig(t *testing.T) {
	for _, spec := range config.Settings() {
		if !spec.Writable || spec.InternalOnly || spec.Selector != nil {
			continue
		}
		spec := spec
		t.Run(strings.ReplaceAll(spec.Key, ".", "_"), func(t *testing.T) {
			isolateSettingServiceConfig(t)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := config.RawValue(cfg, spec.Key)
			if err != nil && !spec.Secret {
				t.Fatalf("baseline value for %q: %v", spec.Key, err)
			}
			switch spec.Key {
			case "tunnel.api_key":
				raw = "sk-runtime-fresh-setting"
			case "tunnel.admin.key":
				raw = "sk-admin-fresh-setting"
			case "tunnel.admin.organization_id":
				raw = "org_fresh"
			case "tunnel.admin.workspace_id":
				raw = "ws_fresh"
			case "tunnel.admin.tenant_id":
				raw = "tenant_fresh"
			}
			if _, err := NewSettingService().Set(t.Context(), spec.Key, raw); err != nil {
				t.Fatalf("fresh mutation for %q failed: %v", spec.Key, err)
			}
		})
	}
}

func TestSettingServiceNormalMutationReloadsRunningRuntimeExactlyOnce(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/reload" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.ReloadResult{PID: os.Getpid(), ServerEnabled: true, ServerPort: 40123})
	}))
	defer server.Close()
	writeRuntimeState(t, root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	result, err := NewSettingService().Set(t.Context(), "server.port", "40123")
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 || !result.RuntimeReloaded {
		t.Fatalf("reload calls=%d result=%#v", got, result)
	}
}

func TestSettingServiceAuthRotateUsesCredentialAuthority(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()

	rotated, err := service.Rotate(t.Context(), "auth.mcp_token")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Value == "" || rotated.Configured == nil || !*rotated.Configured {
		t.Fatalf("rotated=%#v", rotated)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.MCPTokenHash == "" || cfg.Auth.MCPTokenHash == rotated.Value {
		t.Fatalf("credential authority did not persist a one-way hash")
	}

	presented, err := service.Present(t.Context(), "auth.mcp_token")
	if err != nil {
		t.Fatal(err)
	}
	if presented.Value != "configured" || presented.Configured == nil || !*presented.Configured || strings.Contains(presented.Value, rotated.Value) {
		t.Fatalf("presented=%#v", presented)
	}
	if _, err := service.Read(t.Context(), "auth.mcp_token"); err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("raw secret read err=%v", err)
	}
	if _, err := service.Set(t.Context(), "auth.mcp_token", "caller-selected-secret"); err == nil {
		t.Fatal("generated auth token accepted arbitrary set")
	}
	if _, err := service.Reveal(t.Context(), "auth.mcp_token"); err == nil {
		t.Fatal("non-revealable auth token was revealed")
	}
}

func TestSettingServiceTunnelSecretPresentationAndTraceAreSafe(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	const secret = "sk-runtime-setting-secret-value"
	var events []tracepkg.Event
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) {
		events = append(events, event)
	})

	result, err := service.Set(ctx, "tunnel.api_key", secret)
	if err != nil {
		t.Fatal(err)
	}
	if result.Configured == nil || !*result.Configured || result.Value == secret || !strings.Contains(result.Value, "********") {
		t.Fatalf("secret setting result=%#v", result)
	}
	if _, err := service.Read(ctx, "tunnel.api_key"); err == nil {
		t.Fatal("raw tunnel secret read succeeded")
	}
	presented, err := service.Present(ctx, "tunnel.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if presented.Value == secret || !strings.Contains(presented.Value, "********") {
		t.Fatalf("secret presentation=%#v", presented)
	}
	const shortSecret = "tiny-secret"
	shortResult, err := service.Set(ctx, "tunnel.api_key", shortSecret)
	if err != nil {
		t.Fatal(err)
	}
	if shortResult.Value != "********" {
		t.Fatalf("short secret presentation can reveal material: %#v", shortResult)
	}
	shortPresented, err := service.Present(ctx, "tunnel.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if shortPresented.Value != "********" {
		t.Fatalf("short secret masked preview=%#v", shortPresented)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	traceText := string(encoded)
	if strings.Contains(traceText, secret) || strings.Contains(traceText, shortSecret) {
		t.Fatalf("setting trace leaked secret: %s", traceText)
	}
	if !strings.Contains(traceText, "setting.set") || !strings.Contains(traceText, "tunnel.api_key") {
		t.Fatalf("setting trace missing canonical identity: %s", traceText)
	}

	if _, err := service.Unset(ctx, "tunnel.api_key"); err != nil {
		t.Fatal(err)
	}
	presented, err = service.Present(ctx, "tunnel.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if presented.Value != "not configured" || presented.Configured == nil || *presented.Configured {
		t.Fatalf("cleared secret presentation=%#v", presented)
	}
}

func TestSettingServiceTunnelAdminConfiguredInputsAreOfflineAndInvalidateDerivedState(t *testing.T) {
	isolateSettingServiceConfig(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer admin-secret" && authorization != "Bearer admin-secret-next" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","description":"Managed"}]}`))
	}))
	defer server.Close()

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.ControlPlaneBaseURL = server.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	service := NewSettingService()
	if _, err := service.Set(t.Context(), "tunnel.admin.key", "admin-secret"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("admin key set contacted control plane: %d", calls.Load())
	}
	keyConfigured, err := service.Read(t.Context(), "tunnel.admin.key_configured")
	if err != nil || keyConfigured.Value != "true" {
		t.Fatalf("key configured=%#v err=%v", keyConfigured, err)
	}
	adminConfigured, err := service.Read(t.Context(), "tunnel.admin.configured")
	if err != nil || adminConfigured.Value != "false" {
		t.Fatalf("admin configured before scope=%#v err=%v", adminConfigured, err)
	}

	if _, err := service.Set(t.Context(), "tunnel.admin.workspace_id", "ws_admin"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("admin scope set contacted control plane: %d", calls.Load())
	}
	adminConfigured, err = service.Read(t.Context(), "tunnel.admin.configured")
	if err != nil || adminConfigured.Value != "true" {
		t.Fatalf("admin configured after scope=%#v err=%v", adminConfigured, err)
	}

	if _, err := service.Verify(t.Context(), "tunnel.admin.key"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("explicit verification calls=%d want=1", calls.Load())
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Tunnel.Admin.Verified || !loaded.Tunnel.Admin.ReadAccess || !loaded.Tunnel.Admin.ManageAccess {
		t.Fatalf("verification state was not persisted: %#v", loaded.Tunnel)
	}

	if _, err := service.Set(t.Context(), "tunnel.admin.key", "admin-secret-next"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("admin key replacement contacted control plane: %d", calls.Load())
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.Admin.Verified || loaded.Tunnel.Admin.ReadAccess || loaded.Tunnel.Admin.ManageAccess {
		t.Fatalf("admin key replacement retained stale derived state: %#v", loaded.Tunnel)
	}
	if _, err := service.Verify(t.Context(), "tunnel.admin.key"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("second explicit verification calls=%d want=2", calls.Load())
	}

	if _, err := service.Set(t.Context(), "tunnel.admin.organization_id", "org_admin"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("scope replacement contacted control plane: %d", calls.Load())
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.Admin.OrganizationID != "org_admin" || loaded.Tunnel.Admin.WorkspaceID != "" || loaded.Tunnel.Admin.TenantID != "" {
		t.Fatalf("scope replacement was not exclusive: %#v", loaded.Tunnel)
	}
	if loaded.Tunnel.Admin.Verified || loaded.Tunnel.Admin.ReadAccess || loaded.Tunnel.Admin.ManageAccess {
		t.Fatalf("scope replacement retained stale derived state: %#v", loaded.Tunnel)
	}

	for _, key := range []string{"tunnel.admin.configured", "tunnel.admin.verified", "tunnel.admin.read_access", "tunnel.admin.manage_access"} {
		if _, err := service.Set(t.Context(), key, "true"); err == nil || !strings.Contains(err.Error(), "not writable") {
			t.Fatalf("derived setting %q mutation error=%v", key, err)
		}
	}

	if _, err := service.Set(t.Context(), "tunnel.admin.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.Admin.Key != "admin-secret-next" || loaded.Tunnel.Admin.OrganizationID != "org_admin" {
		t.Fatalf("disabling admin discarded configured credentials/scope: %#v", loaded.Tunnel)
	}
	beforeDisabledUse := calls.Load()
	if _, err := ListManagedTunnels(t.Context()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled admin management err=%v", err)
	}
	if calls.Load() != beforeDisabledUse {
		t.Fatalf("disabled normal management contacted control plane: before=%d after=%d", beforeDisabledUse, calls.Load())
	}
	if _, err := service.Verify(t.Context(), "tunnel.admin.key"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != beforeDisabledUse+1 {
		t.Fatalf("explicit verify was blocked while admin disabled: calls=%d", calls.Load())
	}
}

func TestSettingServiceTunnelAdminScopeCanBeConfiguredBeforeKey(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	if _, err := service.Set(t.Context(), "tunnel.admin.tenant_id", "tenant_first"); err != nil {
		t.Fatal(err)
	}
	status, err := TunnelAdminKeyStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.KeyConfigured || status.Configured || status.Scope.TenantID != "tenant_first" {
		t.Fatalf("scope-first status=%#v", status)
	}
	if _, err := service.Set(t.Context(), "tunnel.admin.key", "admin-secret"); err != nil {
		t.Fatal(err)
	}
	status, err = TunnelAdminKeyStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !status.KeyConfigured || !status.Configured || status.Verified || status.Scope.TenantID != "tenant_first" {
		t.Fatalf("key-after-scope status=%#v", status)
	}
}

func TestSettingServiceDynamicUpstreamUsesCanonicalServiceAndReconcilesOnce(t *testing.T) {
	isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs.v2", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	var reconciles atomic.Int32
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager, func(context.Context) error {
		reconciles.Add(1)
		return nil
	})

	set, err := service.Set(t.Context(), "upstream.servers[docs%2Ev2].command", "bun")
	if err != nil {
		t.Fatal(err)
	}
	if set.Spec.Key != "upstream.servers[docs.v2].command" || set.Value != "bun" {
		t.Fatalf("set=%#v", set)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconcile count=%d", got)
	}
	current, ok := manager.Get("docs.v2")
	if !ok || current.Command != "bun" {
		t.Fatalf("upstream=%#v ok=%t", current, ok)
	}

	if _, err := service.Set(t.Context(), "upstream.servers[docs.v2].auth.scope", "tools.read"); err != nil {
		t.Fatal(err)
	}
	if got := reconciles.Load(); got != 2 {
		t.Fatalf("reconcile count after second logical mutation=%d", got)
	}
	current, _ = manager.Get("docs.v2")
	if current.Auth.Scope != "tools.read" {
		t.Fatalf("auth scope=%q", current.Auth.Scope)
	}

	read, err := service.Read(t.Context(), "upstream.servers[docs.v2].command")
	if err != nil || read.Value != "bun" {
		t.Fatalf("read=%#v err=%v", read, err)
	}
	why, err := service.Why(t.Context(), "upstream.servers[docs.v2].command")
	if err != nil || why.Spec.Key != "upstream.servers[docs.v2].command" || why.HasBaseline {
		t.Fatalf("why=%#v err=%v", why, err)
	}
}

func TestSettingServiceDynamicSelectorsCannotEscapeResourceDomain(t *testing.T) {
	isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs.v2", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager)

	for _, key := range []string{
		"upstream.servers[<id>].command",
		"upstream.servers[docs/v2].command",
		"upstream.servers[docs%2Fv2].command",
		"tunnel.managed[<id>].name",
		"tunnel.managed[tun%2Fbad].name",
		"mcp.profiles[default].enabled",
	} {
		if _, err := service.Set(t.Context(), key, "mutated"); err == nil {
			t.Fatalf("unsafe selector accepted: %s", key)
		}
	}
	current, ok := manager.Get("docs.v2")
	if !ok || current.Command != "node" {
		t.Fatalf("invalid selectors mutated resource: %#v ok=%t", current, ok)
	}
	if _, err := service.Read(t.Context(), "upstream.servers[missing].command"); err == nil {
		t.Fatal("missing resource selector did not resolve through Upstream inventory")
	}
}

func TestManagedTunnelSettingUpdateRequestMatchesDomainSemantics(t *testing.T) {
	name, err := managedTunnelUpdateRequest("name", "  Demo tunnel  ")
	if err != nil || name.Name == nil || *name.Name != "Demo tunnel" {
		t.Fatalf("name request=%#v err=%v", name, err)
	}
	description, err := managedTunnelUpdateRequest("description", "  preserve description spacing  ")
	if err != nil || description.Description == nil || *description.Description != "  preserve description spacing  " {
		t.Fatalf("description request=%#v err=%v", description, err)
	}
	organizations, err := managedTunnelUpdateRequest("organization_ids", " org_a,org_b,org_a ")
	if err != nil || organizations.OrganizationIDs == nil || strings.Join(*organizations.OrganizationIDs, ",") != "org_a,org_b" {
		t.Fatalf("organizations request=%#v err=%v", organizations, err)
	}
}

func isolateSettingServiceConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = configformat.SetRootPath(previous)
	})
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-configured-hash"
	cfg.Auth.AdminTokenHash = "admin-configured-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return root
}
