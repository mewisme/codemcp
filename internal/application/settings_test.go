package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestSettingServiceReadListDiffWhyAndDefaultReset(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()

	result, err := service.Set(t.Context(), "http.mcp.port", "40123")
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
	if loaded.HTTP.MCP.Port != 40123 {
		t.Fatalf("persisted port=%d", loaded.HTTP.MCP.Port)
	}

	listed, err := service.List(t.Context(), "http")
	if err != nil {
		t.Fatal(err)
	}
	foundPort := false
	for _, item := range listed {
		if item.Spec.InternalOnly || item.Spec.Key == "http.mcp.auth.token_hash" || item.Spec.Key == "http.admin.auth.token_hash" {
			t.Fatalf("internal setting leaked into list: %#v", item)
		}
		if item.Spec.Key == "http.mcp.port" {
			foundPort = item.Value == "40123"
		}
	}
	if !foundPort {
		t.Fatalf("http.mcp.port missing from list: %#v", listed)
	}

	diff, err := service.Diff(t.Context(), "http")
	if err != nil {
		t.Fatal(err)
	}
	foundDiff := false
	for _, item := range diff {
		if item.Spec.Key == "http.mcp.port" {
			foundDiff = item.Value == "40123" && item.Baseline == "37421"
		}
	}
	if !foundDiff {
		t.Fatalf("http.mcp.port missing from diff: %#v", diff)
	}

	why, err := service.Why(t.Context(), "http.mcp.port")
	if err != nil {
		t.Fatal(err)
	}
	if why.Spec.Key != "http.mcp.port" || !why.HasBaseline || why.Baseline != "37421" {
		t.Fatalf("why=%#v", why)
	}
	if _, err := service.Why(t.Context(), "http.mcp.auth.token_hash"); err == nil {
		t.Fatal("internal auth hash was accepted as a user setting")
	}

	if _, err := service.Unset(t.Context(), "http.mcp.port"); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HTTP.MCP.Port != config.Default().HTTP.MCP.Port {
		t.Fatalf("unset port=%d want=%d", loaded.HTTP.MCP.Port, config.Default().HTTP.MCP.Port)
	}
}

func TestSettingServiceStaticMutationMatchesCanonicalConfigAuthority(t *testing.T) {
	isolateSettingServiceConfig(t)
	initial, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSettingService().Set(t.Context(), "http.mcp.port", "40123"); err != nil {
		t.Fatal(err)
	}
	generic, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	if err := config.Save(initial); err != nil {
		t.Fatal(err)
	}
	if _, err := SetConfigField(t.Context(), "http.mcp.port", "40123"); err != nil {
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
			service := NewSettingService()
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := config.RawValue(cfg, spec.Key)
			if err != nil && !spec.Secret {
				if !spec.Virtual {
					t.Fatalf("baseline value for %q: %v", spec.Key, err)
				}
				presented, presentErr := service.Present(t.Context(), spec.Key)
				if presentErr != nil {
					t.Fatalf("virtual baseline value for %q: %v", spec.Key, presentErr)
				}
				raw = presented.Value
			}
			switch spec.Key {
			case "telegram.token":
				raw = "123456:telegram-fresh-setting"
			case "tunnel.api_key":
				raw = "sk-runtime-fresh-setting"
			case "tunnel.admin.key":
				raw = "sk-admin-fresh-setting"
			case "integrations.typesafe.api_key":
				raw = "ts-fresh-setting"
			case "llm.api_key":
				raw = "sk-llm-fresh-setting"
			case "tunnel.admin.organization_id":
				raw = "org_fresh"
			case "tunnel.admin.workspace_id":
				raw = "ws_fresh"
			case "tunnel.admin.tenant_id":
				raw = "tenant_fresh"
			}
			if _, err := service.Set(t.Context(), spec.Key, raw); err != nil {
				t.Fatalf("fresh mutation for %q failed: %v", spec.Key, err)
			}
		})
	}
}

func TestTelegramTokenManagedSecretSetting(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	service := NewSettingService()

	initial, err := service.Present(t.Context(), "telegram.token")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Value != "not configured" || initial.Configured == nil || *initial.Configured {
		t.Fatalf("initial token presentation=%#v", initial)
	}
	if _, err := service.Read(t.Context(), "telegram.token"); err == nil {
		t.Fatal("raw Telegram token became readable")
	}

	const secret = "123456:telegram-secret-sentinel"
	result, err := service.Set(t.Context(), "telegram.token", secret)
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != tracepkg.MaskSecret(secret, true) || result.Configured == nil || !*result.Configured || strings.Contains(result.Value, secret) {
		t.Fatalf("configured token presentation=%#v", result)
	}
	state, err := service.Read(t.Context(), "telegram.token_configured")
	if err != nil || state.Value != "true" {
		t.Fatalf("configured state=%#v err=%v", state, err)
	}
	stored, err := secretstore.New(root).Get(telegramBotTokenSecretName)
	if err != nil || stored != secret {
		t.Fatalf("stored token=%q err=%v", stored, err)
	}
	data, err := os.ReadFile(config.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("Telegram bot token leaked into config.json")
	}

	if _, err := service.Apply(t.Context(), []SettingChange{
		{Key: "telegram.token", Value: secret},
		{Key: "http.mcp.port", Value: "40123"},
	}); err == nil {
		t.Fatal("Telegram token mutation was combined with normal config mutation")
	}

	cleared, err := service.Unset(t.Context(), "telegram.token")
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Value != "not configured" || cleared.Configured == nil || *cleared.Configured {
		t.Fatalf("cleared token presentation=%#v", cleared)
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

	result, err := NewSettingService().Set(t.Context(), "http.mcp.port", "40123")
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 || !result.RuntimeReloaded {
		t.Fatalf("reload calls=%d result=%#v", got, result)
	}
}

func TestSettingServiceApplyIsAtomicAndReloadsRunningRuntimeOnce(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/reload" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.ReloadResult{PID: os.Getpid(), ServerEnabled: true, ServerPort: 40123, AdminPort: 40124})
	}))
	defer server.Close()
	writeRuntimeState(t, root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	service := NewSettingService()
	applied, err := service.Apply(t.Context(), []SettingChange{
		{Key: "http.mcp.port", Value: "40123"},
		{Key: "http.admin.port", Value: "40124"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.RuntimeReloaded || calls.Load() != 1 || len(applied.Results) != 2 {
		t.Fatalf("applied=%#v reloads=%d", applied, calls.Load())
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HTTP.MCP.Port != 40123 || loaded.HTTP.Admin.Port != 40124 {
		t.Fatalf("multi-setting mutation was not persisted atomically: %#v", loaded)
	}

	before := loaded
	_, err = service.Apply(t.Context(), []SettingChange{
		{Key: "http.mcp.port", Value: "40223"},
		{Key: "http.admin.port", Value: "70000"},
	})
	if err == nil {
		t.Fatal("invalid multi-setting mutation unexpectedly succeeded")
	}
	after, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed multi-setting mutation changed persisted config\nbefore=%#v\nafter=%#v", before, after)
	}
	if calls.Load() != 1 {
		t.Fatalf("failed mutation triggered runtime reload: %d", calls.Load())
	}
}

func TestSettingServiceApplyStaticNoOpDoesNotReloadRunningRuntime(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.ReloadResult{PID: os.Getpid()})
	}))
	defer server.Close()
	writeRuntimeState(t, root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	defaults := config.Default()
	applied, err := NewSettingService().Apply(t.Context(), []SettingChange{
		{Key: "http.mcp.port", Value: strconv.Itoa(defaults.HTTP.MCP.Port)},
		{Key: "http.admin.port", Value: strconv.Itoa(defaults.HTTP.Admin.Port)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.RuntimeReloaded || calls.Load() != 0 {
		t.Fatalf("no-op applied=%#v reloads=%d", applied, calls.Load())
	}
}

func TestSettingServiceApplyStaticStoppedRuntimeReportsNoReload(t *testing.T) {
	isolateSettingServiceConfig(t)
	applied, err := NewSettingService().Apply(t.Context(), []SettingChange{
		{Key: "http.mcp.port", Value: "40123"},
		{Key: "http.admin.port", Value: "40124"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.RuntimeReloaded {
		t.Fatalf("stopped runtime reported reload: %#v", applied)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HTTP.MCP.Port != 40123 || loaded.HTTP.Admin.Port != 40124 {
		t.Fatalf("stopped-runtime mutation not persisted: %#v", loaded)
	}
}

func TestSettingServiceApplyStaticReloadFailureRollsBack(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	before, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "reload failed", http.StatusInternalServerError)
	}))
	defer server.Close()
	writeRuntimeState(t, root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	_, err = NewSettingService().Apply(t.Context(), []SettingChange{
		{Key: "http.mcp.port", Value: "40123"},
		{Key: "http.admin.port", Value: "40124"},
	})
	if err == nil || !strings.Contains(err.Error(), "persisted configuration rolled back") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("reload calls=%d", calls.Load())
	}
	after, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("reload failure did not restore persisted config\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestSettingServiceApplyStaticRollbackFailureRequiresManualReconciliation(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove config root: %v", err)
		}
		if err := os.WriteFile(root, []byte("block rollback"), 0600); err != nil {
			t.Errorf("block config root: %v", err)
		}
		http.Error(w, "reload failed", http.StatusInternalServerError)
	}))
	defer server.Close()
	writeRuntimeState(t, root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	_, err := NewSettingService().Apply(t.Context(), []SettingChange{{Key: "http.mcp.port", Value: "40123"}})
	if err == nil || !strings.Contains(err.Error(), "manual reconciliation required") || !strings.Contains(err.Error(), "rollback persisted configuration") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("reload calls=%d", calls.Load())
	}
}

func TestTunnelAdminGenericBatchAndDomainFacadeConverge(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	batch, err := service.Apply(t.Context(), []SettingChange{
		{Key: "tunnel.admin.key", Value: "admin-secret"},
		{Key: "tunnel.admin.workspace_id", Value: "ws_admin"},
		{Key: "tunnel.admin.enabled", Value: "false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	batchAdmin := batch.Config.Tunnel.Admin

	base := config.Default()
	base.HTTP.MCP.Auth.TokenHash = "mcp-configured-hash"
	base.HTTP.Admin.Auth.TokenHash = "admin-configured-hash"
	if err := config.Save(base); err != nil {
		t.Fatal(err)
	}
	scope := tunnel.AdminScope{WorkspaceID: "ws_admin"}
	if _, err := SetTunnelAdminKey(t.Context(), TunnelAdminKeyInput{Key: "admin-secret", Scope: &scope}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetTunnelAdminEnabled(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	domain, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batchAdmin, domain.Tunnel.Admin) {
		t.Fatalf("batch/domain admin state mismatch\nbatch=%#v\ndomain=%#v", batchAdmin, domain.Tunnel.Admin)
	}

	if err := config.Save(base); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(t.Context(), "tunnel.admin.key", "admin-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(t.Context(), "tunnel.admin.workspace_id", "ws_admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(t.Context(), "tunnel.admin.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	generic, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batchAdmin, generic.Tunnel.Admin) {
		t.Fatalf("batch/generic admin state mismatch\nbatch=%#v\ngeneric=%#v", batchAdmin, generic.Tunnel.Admin)
	}
}

func TestTunnelAdminBatchRejectsCompetingScopesBeforePersistence(t *testing.T) {
	isolateSettingServiceConfig(t)
	before, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewSettingService().Apply(t.Context(), []SettingChange{
		{Key: "tunnel.admin.key", Value: "admin-secret"},
		{Key: "tunnel.admin.organization_id", Value: "org_one"},
		{Key: "tunnel.admin.workspace_id", Value: "ws_one"},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("competing scope mutation err=%v", err)
	}
	after, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("competing scope mutation persisted partial state\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestSettingServiceAuthRotateUsesCredentialAuthority(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	service := NewSettingService()

	rotated, err := service.Rotate(t.Context(), "http.mcp.auth.token")
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
	if cfg.HTTP.MCP.Auth.TokenHash == "" || cfg.HTTP.MCP.Auth.TokenHash == rotated.Value {
		t.Fatalf("credential authority did not persist a one-way hash")
	}
	stored, err := auth.LoadToken(root, "mcp")
	if err != nil || stored != rotated.Value {
		t.Fatalf("auth token secretstore value=%q err=%v", stored, err)
	}

	presented, err := service.Present(t.Context(), "http.mcp.auth.token")
	if err != nil {
		t.Fatal(err)
	}
	if presented.Value != tracepkg.MaskSecret(rotated.Value, true) || presented.Configured == nil || !*presented.Configured || strings.Contains(presented.Value, rotated.Value) {
		t.Fatalf("presented=%#v", presented)
	}
	if _, err := service.Read(t.Context(), "http.mcp.auth.token"); err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("raw secret read err=%v", err)
	}
	if _, err := service.Set(t.Context(), "http.mcp.auth.token", "caller-selected-secret"); err == nil {
		t.Fatal("generated auth token accepted arbitrary set")
	}
	if _, err := service.Reveal(t.Context(), "http.mcp.auth.token"); err == nil {
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
	if result.Configured == nil || !*result.Configured || result.Value != tracepkg.MaskSecret(secret, true) {
		t.Fatalf("secret setting result=%#v", result)
	}
	if _, err := service.Read(ctx, "tunnel.api_key"); err == nil {
		t.Fatal("raw tunnel secret read succeeded")
	}
	presented, err := service.Present(ctx, "tunnel.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if presented.Value != tracepkg.MaskSecret(secret, true) {
		t.Fatalf("secret presentation=%#v", presented)
	}
	const shortSecret = "tiny-secret"
	shortResult, err := service.Set(ctx, "tunnel.api_key", shortSecret)
	if err != nil {
		t.Fatal(err)
	}
	if shortResult.Value != tracepkg.MaskSecret(shortSecret, true) {
		t.Fatalf("short secret presentation can reveal material: %#v", shortResult)
	}
	shortPresented, err := service.Present(ctx, "tunnel.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if shortPresented.Value != tracepkg.MaskSecret(shortSecret, true) {
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

func TestSettingServiceLegacyAuthHashUsesExplicitLegacyMaskedPreview(t *testing.T) {
	isolateSettingServiceConfig(t)
	presented, err := NewSettingService().Present(t.Context(), "http.mcp.auth.token")
	if err != nil {
		t.Fatal(err)
	}
	if presented.Value != "mcp_********legacy" || presented.Configured == nil || !*presented.Configured {
		t.Fatalf("legacy auth presentation=%#v", presented)
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
	if _, err := service.Verify(t.Context(), "tunnel.admin.key"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("verification after scope replacement calls=%d want=3", calls.Load())
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Tunnel.Admin.Verified || !loaded.Tunnel.Admin.ReadAccess || !loaded.Tunnel.Admin.ManageAccess || loaded.Tunnel.Admin.OrganizationID != "org_admin" {
		t.Fatalf("verification after scope replacement state=%#v", loaded.Tunnel.Admin)
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

func TestSettingServiceApplyStagesAtomicUpstreamMultiFieldBatch(t *testing.T) {
	isolateSettingServiceConfig(t)
	storePath := filepath.Join(t.TempDir(), "upstreams.json")
	manager := upstream.NewManager(upstream.NewStore(storePath))
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

	applied, err := service.Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs%2Ev2].command", Value: "bun"},
		{Key: "upstream.servers[docs.v2].name", Value: "Docs Next"},
		{Key: "upstream.servers[docs.v2].args", Value: "--watch,index.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reconciles.Load() != 1 || !applied.RuntimeReloaded || len(applied.Results) != 3 {
		t.Fatalf("applied=%#v reconciles=%d", applied, reconciles.Load())
	}
	current, ok := manager.Get("docs.v2")
	if !ok || current.Command != "bun" || current.Name != "Docs Next" || strings.Join(current.Args, ",") != "--watch,index.ts" {
		t.Fatalf("upstream=%#v ok=%t", current, ok)
	}
	reloaded := upstream.NewManager(upstream.NewStore(storePath))
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	persisted, ok := reloaded.Get("docs.v2")
	if !ok || !reflect.DeepEqual(current, persisted) {
		t.Fatalf("persisted=%#v current=%#v ok=%t", persisted, current, ok)
	}
}

func TestSettingServiceApplyDefaultUpstreamReconcilesRunningRuntimeOnce(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/upstreams/reload" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.UpstreamReloadResult{PID: os.Getpid(), Count: 1})
	}))
	defer server.Close()
	writeRuntimeState(t, root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	applied, err := NewSettingService().Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs].command", Value: "bun"},
		{Key: "upstream.servers[docs].name", Value: "Docs Next"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.RuntimeReloaded || calls.Load() != 1 {
		t.Fatalf("applied=%#v reconciles=%d", applied, calls.Load())
	}
	reloaded := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	current, ok := reloaded.Get("docs")
	if !ok || current.Command != "bun" || current.Name != "Docs Next" {
		t.Fatalf("persisted upstream=%#v ok=%t", current, ok)
	}
}

func TestSettingServiceApplyDefaultUpstreamStoppedRuntimeReportsNoReload(t *testing.T) {
	isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}

	applied, err := NewSettingService().Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs].command", Value: "bun"},
		{Key: "upstream.servers[docs].name", Value: "Docs Next"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.RuntimeReloaded {
		t.Fatalf("stopped runtime reported upstream reload: %#v", applied)
	}
	reloaded := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	current, ok := reloaded.Get("docs")
	if !ok || current.Command != "bun" || current.Name != "Docs Next" {
		t.Fatalf("persisted upstream=%#v ok=%t", current, ok)
	}
}

func TestSettingServiceApplyDefaultUpstreamRuntimeFailureRollsBackStore(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	initial, _ := manager.Get("docs")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "upstream refresh failed", http.StatusInternalServerError)
	}))
	defer server.Close()
	writeRuntimeState(t, root, runtimecontrol.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	_, err := NewSettingService().Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs].command", Value: "bun"},
		{Key: "upstream.servers[docs].name", Value: "Docs Next"},
	})
	if err == nil || !strings.Contains(err.Error(), "upstream configuration rolled back") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("runtime reconcile calls=%d", calls.Load())
	}
	reloaded := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	current, ok := reloaded.Get("docs")
	if !ok || !reflect.DeepEqual(current, initial) {
		t.Fatalf("persisted upstream not rolled back: %#v ok=%t", current, ok)
	}
}

func TestSettingServiceApplyValidatesEntireUpstreamBatchBeforeMutation(t *testing.T) {
	isolateSettingServiceConfig(t)
	storePath := filepath.Join(t.TempDir(), "upstreams.json")
	manager := upstream.NewManager(upstream.NewStore(storePath))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs.v2", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	initial, _ := manager.Get("docs.v2")
	var reconciles atomic.Int32
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager, func(context.Context) error {
		reconciles.Add(1)
		return nil
	})

	_, err := service.Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs.v2].command", Value: "bun"},
		{Key: "upstream.servers[docs.v2].idle_timeout_sec", Value: "not-an-int"},
	})
	if err == nil || !strings.Contains(err.Error(), "idle_timeout_sec") {
		t.Fatalf("err=%v", err)
	}
	if reconciles.Load() != 0 {
		t.Fatalf("invalid batch reconciled runtime %d time(s)", reconciles.Load())
	}
	current, _ := manager.Get("docs.v2")
	if !reflect.DeepEqual(current, initial) {
		t.Fatalf("invalid later field preserved earlier mutation: %#v", current)
	}
	reloaded := upstream.NewManager(upstream.NewStore(storePath))
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	persisted, _ := reloaded.Get("docs.v2")
	if !reflect.DeepEqual(persisted, initial) {
		t.Fatalf("invalid batch changed persisted upstream: %#v", persisted)
	}
}

func TestSettingServiceApplyRejectsCanonicalDynamicDuplicateBeforeMutation(t *testing.T) {
	isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs.v2", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	initial, _ := manager.Get("docs.v2")
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager)

	_, err := service.Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs%2Ev2].command", Value: "bun"},
		{Key: "upstream.servers[docs.v2].command", Value: "deno"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate setting mutation") {
		t.Fatalf("err=%v", err)
	}
	current, _ := manager.Get("docs.v2")
	if !reflect.DeepEqual(current, initial) {
		t.Fatalf("duplicate batch mutated upstream: %#v", current)
	}
}

func TestSettingServiceApplyRejectsMixedOwnersBeforeMutation(t *testing.T) {
	isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	initial, _ := manager.Get("docs")
	beforeConfig, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var reconciles atomic.Int32
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager, func(context.Context) error {
		reconciles.Add(1)
		return nil
	})

	_, err = service.Apply(t.Context(), []SettingChange{
		{Key: "http.mcp.port", Value: "40123"},
		{Key: "upstream.servers[docs].command", Value: "bun"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported transaction owners") {
		t.Fatalf("err=%v", err)
	}
	afterConfig, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(beforeConfig, afterConfig) {
		t.Fatalf("mixed-owner rejection changed config: before=%#v after=%#v", beforeConfig, afterConfig)
	}
	current, _ := manager.Get("docs")
	if !reflect.DeepEqual(current, initial) || reconciles.Load() != 0 {
		t.Fatalf("mixed-owner rejection changed upstream=%#v reconciles=%d", current, reconciles.Load())
	}
}

func TestSettingServiceApplyRejectsMultipleDynamicOwnersAndRemoteManagedTunnelBatch(t *testing.T) {
	isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	for _, server := range []upstream.Server{
		{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"},
		{ID: "search", Name: "Search", Enabled: true, Transport: "stdio", Command: "node"},
	} {
		if err := manager.Add(server); err != nil {
			t.Fatal(err)
		}
	}
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager)

	_, err := service.Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs].command", Value: "bun"},
		{Key: "upstream.servers[search].command", Value: "deno"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported transaction owners") {
		t.Fatalf("multi-resource err=%v", err)
	}
	if current, _ := manager.Get("docs"); current.Command != "node" {
		t.Fatalf("docs mutated: %#v", current)
	}
	if current, _ := manager.Get("search"); current.Command != "node" {
		t.Fatalf("search mutated: %#v", current)
	}

	_, err = service.Apply(t.Context(), []SettingChange{
		{Key: "tunnel.managed[tun_demo].name", Value: "Demo"},
		{Key: "tunnel.managed[tun_demo].description", Value: "Description"},
	})
	if err == nil || !strings.Contains(err.Error(), "remote mutations") {
		t.Fatalf("managed tunnel multi-setting err=%v", err)
	}
}

func TestSettingServiceApplyUpstreamNoOpDoesNotReconcile(t *testing.T) {
	isolateSettingServiceConfig(t)
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	var reconciles atomic.Int32
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager, func(context.Context) error {
		reconciles.Add(1)
		return nil
	})

	applied, err := service.Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs].command", Value: "node"},
		{Key: "upstream.servers[docs].name", Value: "Docs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.RuntimeReloaded || reconciles.Load() != 0 {
		t.Fatalf("no-op applied=%#v reconciles=%d", applied, reconciles.Load())
	}
}

func TestSettingServiceApplyUpstreamReconcileFailureRollsBack(t *testing.T) {
	isolateSettingServiceConfig(t)
	storePath := filepath.Join(t.TempDir(), "upstreams.json")
	manager := upstream.NewManager(upstream.NewStore(storePath))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	initial, _ := manager.Get("docs")
	var reconciles atomic.Int32
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager, func(context.Context) error {
		reconciles.Add(1)
		return errors.New("proxy refresh failed")
	})

	_, err := service.Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs].command", Value: "bun"},
		{Key: "upstream.servers[docs].name", Value: "Docs Next"},
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err=%v", err)
	}
	if reconciles.Load() != 1 {
		t.Fatalf("reconciles=%d", reconciles.Load())
	}
	current, _ := manager.Get("docs")
	if !reflect.DeepEqual(current, initial) {
		t.Fatalf("manager state not rolled back: %#v", current)
	}
	reloaded := upstream.NewManager(upstream.NewStore(storePath))
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	persisted, _ := reloaded.Get("docs")
	if !reflect.DeepEqual(persisted, initial) {
		t.Fatalf("persisted state not rolled back: %#v", persisted)
	}
}

func TestSettingServiceApplyUpstreamRollbackFailureRequiresManualReconciliation(t *testing.T) {
	isolateSettingServiceConfig(t)
	storeRoot := t.TempDir()
	storePath := filepath.Join(storeRoot, "upstreams.json")
	manager := upstream.NewManager(upstream.NewStore(storePath))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager, func(context.Context) error {
		if err := os.RemoveAll(storeRoot); err != nil {
			return err
		}
		if err := os.WriteFile(storeRoot, []byte("block rollback"), 0600); err != nil {
			return err
		}
		return errors.New("proxy refresh failed")
	})

	_, err := service.Apply(t.Context(), []SettingChange{{Key: "upstream.servers[docs].command", Value: "bun"}})
	if err == nil || !strings.Contains(err.Error(), "manual reconciliation required") || !strings.Contains(err.Error(), "rollback upstream configuration") {
		t.Fatalf("err=%v", err)
	}
	current, _ := manager.Get("docs")
	if current.Command != "bun" {
		t.Fatalf("failed rollback was reported but manager state was not pending reconciliation: %#v", current)
	}
}

type settingCleanupFailureClient struct {
	calls atomic.Int32
}

func (*settingCleanupFailureClient) Connect(context.Context, upstream.Server) error { return nil }
func (*settingCleanupFailureClient) Close(context.Context, string) error            { return nil }
func (*settingCleanupFailureClient) Tools(context.Context, string) ([]upstream.Tool, error) {
	return nil, nil
}
func (*settingCleanupFailureClient) Call(context.Context, string, string, map[string]any) (upstream.CallResult, error) {
	return upstream.CallResult{}, nil
}
func (*settingCleanupFailureClient) PID(string) int { return 0 }
func (client *settingCleanupFailureClient) ClearOAuthCredential(string) error {
	if client.calls.Add(1) == 1 {
		return errors.New("cleanup failed")
	}
	return nil
}

func TestSettingServiceApplyUpstreamPostPersistFailureRollsBackBeforeReconcile(t *testing.T) {
	isolateSettingServiceConfig(t)
	storePath := filepath.Join(t.TempDir(), "upstreams.json")
	client := &settingCleanupFailureClient{}
	manager := upstream.NewManagerWithClient(upstream.NewStore(storePath), client)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{
		ID: "docs", Name: "Docs", Enabled: true, Transport: "http",
		URL: "https://one.example/mcp", Auth: upstream.AuthConfig{Type: "oauth", Scope: "read"},
	}); err != nil {
		t.Fatal(err)
	}
	initial, _ := manager.Get("docs")
	var reconciles atomic.Int32
	service := NewSettingService()
	service.upstream = NewUpstreamService(manager, func(context.Context) error {
		reconciles.Add(1)
		return nil
	})

	_, err := service.Apply(t.Context(), []SettingChange{
		{Key: "upstream.servers[docs].url", Value: "https://two.example/mcp"},
		{Key: "upstream.servers[docs].name", Value: "Docs Next"},
	})
	if err == nil || !strings.Contains(err.Error(), "cleanup failed") || !strings.Contains(err.Error(), "upstream configuration rolled back") {
		t.Fatalf("err=%v", err)
	}
	if reconciles.Load() != 0 {
		t.Fatalf("post-persist failure reached runtime reconcile %d time(s)", reconciles.Load())
	}
	if client.calls.Load() != 2 {
		t.Fatalf("OAuth cleanup calls=%d want=2 (failed commit cleanup + successful rollback cleanup)", client.calls.Load())
	}
	current, ok := manager.Get("docs")
	if !ok || !reflect.DeepEqual(current, initial) {
		t.Fatalf("manager state not rolled back: %#v ok=%t", current, ok)
	}
	reloaded := upstream.NewManager(upstream.NewStore(storePath))
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	persisted, ok := reloaded.Get("docs")
	if !ok || !reflect.DeepEqual(persisted, initial) {
		t.Fatalf("persisted state not rolled back: %#v ok=%t", persisted, ok)
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
	cfg.Tunnel.Enabled = false
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-configured-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-configured-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return root
}
