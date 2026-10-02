package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestValidateRequiresAuthTokens(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Enabled = false
	if err := Validate(cfg); err == nil {
		t.Fatal("expected missing auth token validation error")
	}
	cfg.HTTP.MCP.Auth.TokenHash = "configured"
	cfg.HTTP.Admin.Auth.TokenHash = "configured"
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequiresAtLeastOneMCPTransport(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Enabled = false
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.HTTP.MCP.Enabled = false
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "at least one MCP transport") {
		t.Fatalf("both transports disabled err=%v", err)
	}
	cfg.Tunnel.Enabled = true
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "at least one MCP transport") {
		t.Fatalf("enabled unconfigured tunnel satisfied transport requirement err=%v", err)
	}
	cfg.Tunnel.ID = "tunnel_test"
	cfg.Tunnel.APIKey = "runtime-secret"
	if err := Validate(cfg); err != nil {
		t.Fatalf("tunnel-only config rejected: %v", err)
	}
	cfg.HTTP.MCP.Enabled = true
	cfg.Tunnel.Enabled = false
	if err := Validate(cfg); err != nil {
		t.Fatalf("HTTP-only config rejected: %v", err)
	}
}

func TestConfigSaveRejectsDisablingAllMCPTransports(t *testing.T) {
	root := t.TempDir()
	cfg := Default()
	cfg.HTTP.MCP.Enabled = false
	cfg.Tunnel.Enabled = false
	err := saveAt(filepath.Join(root, "config.json"), filepath.Join(root, "tunnel.json"), cfg)
	if err == nil || !strings.Contains(err.Error(), "at least one MCP transport") {
		t.Fatalf("save err=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "config.json")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid transport config was persisted: %v", statErr)
	}
}

func TestValidateNetworkExposureRequiresAuth(t *testing.T) {
	for _, exposure := range []ExposureConfig{
		{Mode: ExposureAll, Interfaces: []string{}},
		{Mode: ExposureWildcard, Interfaces: []string{}},
		{Mode: ExposureInterfaces, Interfaces: []string{"eth0"}},
	} {
		t.Run(string(exposure.Mode), func(t *testing.T) {
			cfg := Default()
			cfg.Tunnel.Enabled = false
			cfg.HTTP.Exposure = exposure
			cfg.HTTP.Security.AllowInsecure = true
			cfg.HTTP.MCP.Auth.TokenHash = "mcp"
			cfg.HTTP.Admin.Auth.TokenHash = "admin"
			if err := Validate(cfg); err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				name   string
				mutate func(*Config)
			}{
				{name: "mcp disabled", mutate: func(cfg *Config) { cfg.HTTP.MCP.Auth.Enabled = false }},
				{name: "admin auth disabled", mutate: func(cfg *Config) { cfg.HTTP.Admin.Auth.Enabled = false }},
				{name: "mcp token missing", mutate: func(cfg *Config) { cfg.HTTP.MCP.Auth.TokenHash = "" }},
				{name: "admin token missing", mutate: func(cfg *Config) { cfg.HTTP.Admin.Auth.TokenHash = "" }},
			} {
				t.Run(test.name, func(t *testing.T) {
					candidate := cfg
					test.mutate(&candidate)
					if err := Validate(candidate); err == nil {
						t.Fatal("network exposure accepted without required authentication")
					}
				})
			}
			cfg.HTTP.Admin.Enabled = false
			cfg.HTTP.Admin.Auth.Enabled = false
			cfg.HTTP.Admin.Auth.TokenHash = ""
			if err := Validate(cfg); err != nil {
				t.Fatalf("disabled admin endpoint unnecessarily required admin auth: %v", err)
			}
		})
	}
}

func TestValidateNetworkExposureRequiresExplicitInsecureHTTPOptIn(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Enabled = false
	cfg.HTTP.Exposure = ExposureConfig{Mode: ExposureAll, Interfaces: []string{}}
	cfg.HTTP.MCP.Auth.TokenHash = "mcp"
	cfg.HTTP.Admin.Auth.TokenHash = "admin"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "http.security.allow_insecure") {
		t.Fatalf("network exposure without insecure HTTP opt-in = %v", err)
	}
	cfg.HTTP.Security.AllowInsecure = true
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestValidateUnauthenticatedLoopbackRequiresAcknowledgement(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Enabled = false
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "allow_unauthenticated_loopback") {
		t.Fatalf("auth-off without acknowledgement = %v", err)
	}
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := Validate(cfg); err != nil {
		t.Fatalf("auth-off with acknowledgement and expose none rejected: %v", err)
	}
	cfg.HTTP.Exposure = ExposureConfig{Mode: ExposureAll, Interfaces: []string{}}
	cfg.HTTP.Security.AllowInsecure = true
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "http.exposure.mode=none") {
		t.Fatalf("auth-off with acknowledgement and expose all = %v", err)
	}
}

func TestValidateUnauthenticatedLoopbackAppliesPerEnabledEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Enabled = false
	cfg.HTTP.MCP.Auth.TokenHash = "mcp"
	cfg.HTTP.Admin.Auth.Enabled = false
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "allow_unauthenticated_loopback") {
		t.Fatalf("admin auth-off without acknowledgement = %v", err)
	}
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.HTTP.Admin.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = false
	if err := Validate(cfg); err != nil {
		t.Fatalf("disabled admin endpoint unnecessarily required acknowledgement: %v", err)
	}
}

func TestSecurityWarningsIncludeCleartextHTTP(t *testing.T) {
	cfg := Default()
	if warnings := SecurityWarnings(cfg); len(warnings) != 0 {
		t.Fatalf("default security warnings = %#v", warnings)
	}
	cfg.HTTP.Exposure.Mode = ExposureAll
	cfg.HTTP.Security.AllowInsecure = true
	warnings := SecurityWarnings(cfg)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "cleartext HTTP") {
		t.Fatalf("cleartext warnings = %#v", warnings)
	}
	if !CleartextHTTPActive(cfg) {
		t.Fatal("expected cleartext HTTP active")
	}
}

func TestValidateBuiltinOpenAITunnel(t *testing.T) {
	cfg := Default()
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Enabled = true
	if err := Validate(cfg); err != nil {
		t.Fatalf("enabled unconfigured tunnel blocked generic validation: %v", err)
	}
	cfg.Tunnel.ID = "tunnel_0123456789abcdef0123456789abcdef"
	if err := Validate(cfg); err != nil {
		t.Fatalf("missing optional runtime key blocked generic validation: %v", err)
	}
	cfg.Tunnel.APIKey = "sk-test"
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.ControlPlaneBaseURL = "not-a-url"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid control plane URL")
	}
	cfg.Tunnel.ControlPlaneBaseURL = "http://api.openai.com"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected non-loopback http control plane URL")
	}
	cfg.Tunnel.ControlPlaneBaseURL = "http://127.0.0.1:8080"
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestConfigSaveSeparatesTunnelSecrets(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	cfg := Default()
	cfg.Tunnel.ID = "tunnel_0123456789abcdef0123456789abcdef"
	cfg.Tunnel.APIKey = "tunnel-secret"
	cfg.Tunnel.Admin.Key = "admin-secret"
	cfg.Tunnel.Admin.WorkspaceID = "ws-admin"

	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), "tunnel-secret") || strings.Contains(string(configData), "admin-secret") || strings.Contains(string(configData), `"api_key"`) || strings.Contains(string(configData), `"admin_key"`) || strings.Contains(string(configData), "ws-admin") {
		t.Fatalf("config.json leaked tunnel secret: %s", configData)
	}
	secretData, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	secretText := string(secretData)
	if strings.Contains(secretText, "tunnel-secret") || strings.Contains(secretText, "admin-secret") || !strings.Contains(secretText, "ws-admin") || !strings.Contains(secretText, "runtime_key_configured") || !strings.Contains(secretText, `"admin"`) || !strings.Contains(secretText, `"key_configured": true`) {
		t.Fatalf("tunnel.json did not contain nested marker-only secret metadata: %s", secretData)
	}
	for _, legacy := range []string{"admin_key", "admin_workspace_id", "admin_organization_id", "admin_tenant_id", "admin_verified", "admin_read_access", "admin_manage_access"} {
		if strings.Contains(secretText, legacy) {
			t.Fatalf("tunnel.json retained flat admin field %q: %s", legacy, secretData)
		}
	}
	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.APIKey != "tunnel-secret" || loaded.Tunnel.Admin.Key != "admin-secret" || loaded.Tunnel.Admin.WorkspaceID != "ws-admin" || loaded.Tunnel.ID != cfg.Tunnel.ID {
		t.Fatalf("loaded tunnel = %#v", loaded.Tunnel)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(secretPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("tunnel secret mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestConfigJSONRoundTrip(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	cfg := Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-hash"
	cfg.Tunnel.ID = "tunnel_0123456789abcdef0123456789abcdef"
	cfg.Tunnel.APIKey = "tunnel-secret"
	cfg.Tunnel.Admin.Key = "admin-secret"
	cfg.Tunnel.Admin.OrganizationID = "org-admin"
	cfg.Shell.Path = []string{filepath.Join(root, "bin")}
	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HTTP.MCP.Port != cfg.HTTP.MCP.Port || loaded.HTTP.MCP.Auth.TokenHash != cfg.HTTP.MCP.Auth.TokenHash || loaded.Tunnel.APIKey != cfg.Tunnel.APIKey || loaded.Tunnel.Admin.Key != cfg.Tunnel.Admin.Key || loaded.Tunnel.Admin.OrganizationID != cfg.Tunnel.Admin.OrganizationID || len(loaded.Shell.Path) != 1 || loaded.Shell.Path[0] != cfg.Shell.Path[0] {
		t.Fatalf("round trip = %#v", loaded)
	}
	mainData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainData), "tunnel-secret") || strings.Contains(string(mainData), "admin-secret") || strings.Contains(string(mainData), "org-admin") {
		t.Fatalf("main JSON config leaked tunnel secret: %s", mainData)
	}
	secretData, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secretData), "tunnel-secret") || strings.Contains(string(secretData), "admin-secret") || !strings.Contains(string(secretData), "org-admin") {
		t.Fatalf("tunnel JSON file leaked secret or lost scope: %s", secretData)
	}
}

func TestLegacyTunnelAPIKeyMigratesOnSave(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	cfg := Default()
	cfg.Tunnel.APIKey = "legacy-secret"
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.APIKey != "legacy-secret" {
		t.Fatalf("legacy API key = %q", loaded.Tunnel.APIKey)
	}
	if err := saveAt(configPath, secretPath, loaded); err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), "legacy-secret") || !strings.Contains(string(configData), `"api_key": "<secret-file>"`) {
		t.Fatalf("legacy secret was not replaced by a safe marker in config.json: %s", configData)
	}
	secret, err := loadTunnelSecretAt(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if secret.APIKey != "" || !secret.RuntimeKeyConfigured {
		t.Fatalf("legacy tunnel file was not replaced by secret-file marker: %#v", secret)
	}
	key, err := secretstore.New(root).Get(tunnelRuntimeSecretName)
	if err != nil || key != "legacy-secret" {
		t.Fatalf("migrated stored secret = %q err=%v", key, err)
	}
}

func TestLegacyGenericTunnelFieldsArePreservedOnSave(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	data := []byte(`{
		"server":{"host":"127.0.0.1","port":37421},
		"admin":{"enabled":true,"port":37422},
		"auth":{"mcp_enabled":false,"admin_enabled":false},
		"tunnel":{"enabled":false,"id":"tunnel_test","command":"cloudflared","args":["tunnel"],"origin":"http://127.0.0.1:37421","public_url":"https://old.example"}
	}`)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.ID != "tunnel_test" {
		t.Fatalf("tunnel id = %q", loaded.Tunnel.ID)
	}
	if err := saveAt(configPath, secretPath, loaded); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{`"command"`, `"args"`, `"origin"`, `"public_url"`} {
		if !strings.Contains(string(saved), legacy) {
			t.Fatalf("legacy generic tunnel field %s was removed: %s", legacy, saved)
		}
	}
}

func TestLegacyHTTPRootsMigrateOnceAndRewriteCanonicalTree(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	legacy := []byte(`{
		"server":{"enabled":true,"port":41021,"expose":{"mode":"none","interfaces":[]},"allow_insecure_http":false,"allow_unauthenticated_loopback":true},
		"admin":{"enabled":false,"port":41022},
		"auth":{"mcp_enabled":false,"mcp_legacy_bearer":false,"admin_enabled":false,"mcp_token_hash":"legacy-mcp-hash"},
		"tunnel":{"enabled":false}
	}`)
	if err := os.WriteFile(configPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HTTP.MCP.Enabled || cfg.HTTP.MCP.Port != 41021 || cfg.HTTP.Admin.Enabled || cfg.HTTP.Admin.Port != 41022 {
		t.Fatalf("legacy endpoint migration = %#v", cfg.HTTP)
	}
	if cfg.HTTP.MCP.Auth.Enabled || cfg.HTTP.MCP.Auth.LegacyBearer || cfg.HTTP.MCP.Auth.TokenHash != "legacy-mcp-hash" || cfg.HTTP.Admin.Auth.Enabled {
		t.Fatalf("legacy auth migration = %#v", cfg.HTTP)
	}
	if !cfg.HTTP.Security.AllowUnauthenticatedLoopback || cfg.HTTP.Security.AllowInsecure || cfg.HTTP.Exposure.Mode != ExposureNone {
		t.Fatalf("legacy shared HTTP policy migration = %#v", cfg.HTTP)
	}

	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := configformat.DecodeGeneric(configformat.JSON, saved)
	if err != nil {
		t.Fatal(err)
	}
	rootValue := decoded.(map[string]any)
	if _, ok := rootValue["http"]; !ok {
		t.Fatalf("canonical HTTP root missing after migration: %s", saved)
	}
	for _, legacyRoot := range []string{"server", "admin", "auth"} {
		if _, ok := rootValue[legacyRoot]; ok {
			t.Fatalf("legacy root %q survived migration: %s", legacyRoot, saved)
		}
	}
}

func TestHTTPMigrationRejectsCanonicalAndLegacyRootsTogether(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	data := []byte(`{
		"http":{"mcp":{"enabled":true,"port":37421,"auth":{"enabled":true,"legacy_bearer":true}},"admin":{"enabled":true,"port":37422,"auth":{"enabled":true}},"exposure":{"mode":"none","interfaces":[]},"security":{}},
		"server":{"enabled":true,"port":37421}
	}`)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAt(configPath, secretPath); err == nil || !strings.Contains(err.Error(), "both canonical http and legacy server/admin/auth roots") {
		t.Fatalf("canonical/legacy HTTP conflict error = %v", err)
	}
}

func TestHTTPMigrationRejectsUnknownLegacyKeysInsteadOfDroppingThem(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{
			name: "legacy root field",
			data: `{"server":{"enabled":true,"port":37421,"custom":"keep"}}`,
			want: "legacy server configuration contains unsupported keys",
		},
		{
			name: "legacy exposure field",
			data: `{"server":{"enabled":true,"port":37421,"expose":{"mode":"none","interfaces":[],"custom":"keep"}}}`,
			want: "legacy server.expose configuration contains unsupported keys",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.json")
			secretPath := filepath.Join(root, "tunnel.json")
			if err := os.WriteFile(configPath, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadAt(configPath, secretPath); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("legacy migration error = %v, want %q", err, test.want)
			}
			saved, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(saved) != test.data {
				t.Fatalf("rejected legacy config was rewritten:\n%s", saved)
			}
		})
	}
}

func TestLegacyApprovalExplainMigratesToCanonicalExplainRoot(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	if err := os.WriteFile(configPath, []byte(`{"approval":{"explain":{"mode":"auto"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Explain.Mode != ExplainAuto {
		t.Fatalf("explain mode=%q want=%q", cfg.Explain.Mode, ExplainAuto)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(saved, &document); err != nil {
		t.Fatal(err)
	}
	explainRoot, ok := document["explain"].(map[string]any)
	if !ok || explainRoot["mode"] != "auto" {
		t.Fatalf("canonical explain root=%#v", document["explain"])
	}
	approvalRoot, _ := document["approval"].(map[string]any)
	if _, exists := approvalRoot["explain"]; exists {
		t.Fatalf("legacy approval.explain survived migration: %#v", approvalRoot)
	}
}

func TestExplainMigrationRejectsCanonicalAndLegacyConflict(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	data := []byte(`{"explain":{"mode":"manual"},"approval":{"explain":{"mode":"auto"}}}`)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAt(configPath, secretPath); err == nil || !strings.Contains(err.Error(), "both canonical explain and legacy approval.explain roots") {
		t.Fatalf("conflict error=%v", err)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(data) {
		t.Fatalf("conflicting config was rewritten:\n%s", saved)
	}
}

func TestDefaultServerUsesExposurePolicy(t *testing.T) {
	cfg := Default()
	if !cfg.HTTP.MCP.Enabled || cfg.HTTP.MCP.Port != 37421 || cfg.HTTP.Exposure.Mode != ExposureNone || len(cfg.HTTP.Exposure.Interfaces) != 0 {
		t.Fatalf("http mcp = %#v", cfg.HTTP.MCP)
	}
}

func TestDefaultIntegrationsActive(t *testing.T) {
	cfg := Default()
	if !cfg.Integrations.Ponytail.Active || cfg.Integrations.Ponytail.Mode != "full" || !cfg.Integrations.Caveman.Active || cfg.Integrations.Caveman.Mode != "full" || !cfg.Integrations.RTK.Enabled || cfg.Integrations.RTK.Path != "" || !cfg.Integrations.CodeGraph.Enabled || cfg.Integrations.CodeGraph.Path != "" {
		t.Fatalf("integrations = %#v", cfg.Integrations)
	}
}

func TestValidatePonytailDefaultMode(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Enabled = false
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	for _, mode := range []string{"lite", "full", "ultra"} {
		cfg.Integrations.Ponytail.Mode = mode
		if err := Validate(cfg); err != nil {
			t.Fatalf("mode %q rejected: %v", mode, err)
		}
	}
	for _, mode := range []string{"", "off", "review", "max"} {
		cfg.Integrations.Ponytail.Mode = mode
		if err := Validate(cfg); err == nil {
			t.Fatalf("mode %q accepted", mode)
		}
	}
}

func TestValidateCavemanDefaultMode(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Enabled = false
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	for _, mode := range []string{"lite", "full", "ultra", "wenyan-lite", "wenyan-full", "wenyan-ultra"} {
		cfg.Integrations.Caveman.Mode = mode
		if err := Validate(cfg); err != nil {
			t.Fatalf("mode %q rejected: %v", mode, err)
		}
	}
	for _, mode := range []string{"", "off", "wenyan", "commit", "review", "compress", "max"} {
		cfg.Integrations.Caveman.Mode = mode
		if err := Validate(cfg); err == nil {
			t.Fatalf("mode %q accepted", mode)
		}
	}
}

func TestNormalizeShellPath(t *testing.T) {
	first := filepath.Join(t.TempDir(), "tools")
	second := filepath.Join(t.TempDir(), "bin")
	got, err := NormalizeShellPath([]string{first, second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != filepath.Clean(first) || got[1] != filepath.Clean(second) {
		t.Fatalf("shell path = %#v", got)
	}
	if _, err := NormalizeShellPath([]string{"relative/bin"}); err == nil {
		t.Fatal("relative shell path was accepted")
	}
}

func TestLegacyShellPolicyFieldsArePreservedOnSave(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	data := []byte(`{"server":{"enabled":true,"port":37421,"expose":{"mode":"none","interfaces":[]}},"admin":{"enabled":false,"port":37422},"auth":{"mcp_enabled":false,"admin_enabled":false},"shell":{"path":[],"approval_policy":"strict","approval_allow_commands":["go test *"],"approval_deny_commands":["git push **"],"environment_policy":"minimal","environment_allow":["DATABASE_URL"],"sandbox_policy":"required","network_policy":"deny"},"tunnel":{"enabled":false}}`)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Shell.Path) != 0 {
		t.Fatalf("legacy shell config affected runtime: %#v", loaded.Shell)
	}
	if err := saveAt(configPath, secretPath, loaded); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"approval_policy", "approval_allow_commands", "approval_deny_commands", "environment_policy", "environment_allow", "sandbox_policy", "network_policy"} {
		if !strings.Contains(string(saved), legacy) {
			t.Fatalf("legacy shell policy field %q was removed: %s", legacy, saved)
		}
	}
}

func TestLegacyJSONConfigWithoutIntegrationsKeepsEnabledDefaults(t *testing.T) {
	for _, legacyInteractive := range []struct {
		name  string
		value bool
	}{{name: "interactive-true", value: true}, {name: "interactive-false", value: false}} {
		t.Run(legacyInteractive.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.json")
			secretPath := filepath.Join(root, "tunnel.json")
			legacy := map[string]any{
				"interactive": legacyInteractive.value,
				"server":      map[string]any{"port": int64(37421), "expose": map[string]any{"mode": "none", "interfaces": []any{}}},
				"admin":       map[string]any{"enabled": false, "port": int64(37422)},
				"auth":        map[string]any{"mcp_enabled": false, "admin_enabled": false},
				"tunnel":      map[string]any{"enabled": false},
			}
			data, err := configformat.EncodeGeneric(configformat.JSON, legacy)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadAt(configPath, secretPath)
			if err != nil {
				t.Fatal(err)
			}
			if !loaded.HTTP.MCP.Enabled {
				t.Fatal("legacy JSON config disabled MCP HTTP")
			}
			if !loaded.Integrations.Ponytail.Active || loaded.Integrations.Ponytail.Mode != "full" || !loaded.Integrations.Caveman.Active || loaded.Integrations.Caveman.Mode != "full" {
				t.Fatalf("legacy JSON integrations = %#v", loaded.Integrations)
			}
			unchanged, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			unchangedValue, err := configformat.DecodeGeneric(configformat.JSON, unchanged)
			if err != nil {
				t.Fatal(err)
			}
			unchangedRoot := unchangedValue.(map[string]any)
			if _, exists := unchangedRoot["interactive"]; !exists {
				t.Fatal("read-only load rewrote legacy JSON config")
			}
			if err := saveAt(configPath, secretPath, loaded); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			value, err := configformat.DecodeGeneric(configformat.JSON, saved)
			if err != nil {
				t.Fatal(err)
			}
			savedRoot := value.(map[string]any)
			if _, exists := savedRoot["interactive"]; !exists {
				t.Fatalf("legacy interactive key was removed from JSON config: %s", saved)
			}
		})
	}
}

func TestPartialJSONIntegrationsKeepMissingIntegrationDefault(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	partial := map[string]any{
		"server":       map[string]any{"port": int64(37421), "expose": map[string]any{"mode": "none", "interfaces": []any{}}},
		"admin":        map[string]any{"enabled": false, "port": int64(37422)},
		"auth":         map[string]any{"mcp_enabled": false, "admin_enabled": false},
		"integrations": map[string]any{"ponytail": map[string]any{"active": false}},
		"tunnel":       map[string]any{"enabled": false},
	}
	data, err := configformat.EncodeGeneric(configformat.JSON, partial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Integrations.Ponytail.Active || loaded.Integrations.Ponytail.Mode != "full" || !loaded.Integrations.Caveman.Active || loaded.Integrations.Caveman.Mode != "full" {
		t.Fatalf("partial JSON integrations = %#v", loaded.Integrations)
	}
}

func TestIntegrationConfigSerializesActiveOnlyInJSON(t *testing.T) {
	cfg := Default()
	cfg.Integrations.Ponytail.Active = false
	data, err := configformat.Marshal(configformat.JSON, cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		t.Fatal(err)
	}
	root := raw.(map[string]any)
	if _, exists := root["interactive"]; exists {
		t.Fatalf("obsolete interactive key serialized: %#v", root)
	}
	if _, exists := root["features"]; exists {
		t.Fatalf("legacy features key serialized: %#v", root)
	}
	integrationValues := root["integrations"].(map[string]any)
	ponytail := integrationValues["ponytail"].(map[string]any)
	if ponytail["active"] != false || ponytail["mode"] != "full" {
		t.Fatalf("ponytail = %#v", ponytail)
	}
	if _, exists := ponytail["enabled"]; exists {
		t.Fatalf("legacy enabled key was serialized: %#v", ponytail)
	}
	caveman := integrationValues["caveman"].(map[string]any)
	if caveman["active"] != true || caveman["mode"] != "full" {
		t.Fatalf("caveman = %#v", caveman)
	}
	if _, exists := caveman["enabled"]; exists {
		t.Fatalf("legacy enabled key was serialized: %#v", caveman)
	}
}

func TestLegacyServerHostMigratesToExpose(t *testing.T) {
	for _, test := range []struct {
		name   string
		server string
		want   ExposureMode
	}{
		{name: "loopback", server: `{"host":"127.0.0.1","port":37421}`, want: ExposureNone},
		{name: "localhost", server: `{"host":"localhost","port":37421}`, want: ExposureNone},
		{name: "wildcard", server: `{"host":"0.0.0.0","port":37421}`, want: ExposureWildcard},
		{name: "lan address", server: `{"host":"192.168.1.20","port":37421}`, want: ExposureAll},
		{name: "explicit false wins", server: `{"host":"0.0.0.0","port":37421,"expose":false}`, want: ExposureNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.json")
			secretPath := filepath.Join(root, "tunnel.json")
			data := []byte(`{"server":` + test.server + `,"admin":{"enabled":false,"port":37422},"auth":{"mcp_enabled":false,"admin_enabled":false},"tunnel":{"enabled":false}}`)
			if err := os.WriteFile(configPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadAt(configPath, secretPath)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.HTTP.Exposure.Mode != test.want {
				t.Fatalf("expose = %#v, want %s", loaded.HTTP.Exposure, test.want)
			}
			if err := saveAt(configPath, secretPath, loaded); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := configformat.DecodeGeneric(configformat.JSON, saved)
			if err != nil {
				t.Fatal(err)
			}
			rootValue := raw.(map[string]any)
			for _, legacyRoot := range []string{"server", "admin", "auth"} {
				if _, exists := rootValue[legacyRoot]; exists {
					t.Fatalf("legacy HTTP root %q survived canonical rewrite: %s", legacyRoot, saved)
				}
			}
			if !strings.Contains(string(saved), `"http": {`) || !strings.Contains(string(saved), `"exposure": {`) || !strings.Contains(string(saved), `"mode": "`+string(test.want)+`"`) {
				t.Fatalf("saved exposure missing: %s", saved)
			}
		})
	}
}

func TestLegacyBooleanExposureMigratesInJSON(t *testing.T) {
	for _, test := range []struct {
		name  string
		value bool
		want  ExposureMode
	}{{"disabled", false, ExposureNone}, {"enabled", true, ExposureWildcard}} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.json")
			secretPath := filepath.Join(root, "tunnel.json")
			legacy := map[string]any{
				"server": map[string]any{"port": int64(37421), "expose": test.value},
				"admin":  map[string]any{"enabled": false, "port": int64(37422)},
				"auth":   map[string]any{"mcp_enabled": false, "admin_enabled": false},
				"tunnel": map[string]any{"enabled": false},
			}
			data, err := configformat.EncodeGeneric(configformat.JSON, legacy)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadAt(path, secretPath)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.HTTP.Exposure.Mode != test.want || len(loaded.HTTP.Exposure.Interfaces) != 0 {
				t.Fatalf("expose = %#v", loaded.HTTP.Exposure)
			}
		})
	}
}

func TestClearingTunnelAPIKeyPreservesTunnelConfigFile(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	cfg := Default()
	cfg.Tunnel.APIKey = "secret"
	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.APIKey = ""
	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	secretData, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secretData), "secret") || !strings.Contains(string(secretData), `"runtime_key_configured": false`) {
		t.Fatalf("tunnel config was not preserved after clear: %s", secretData)
	}
	if _, err := secretstore.New(root).Get(tunnelRuntimeSecretName); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("runtime key still exists in secret file store: %v", err)
	}
}

func TestConfigJSONSaveDeepMergesUnknownKeys(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	existing := map[string]any{
		"http": map[string]any{
			"mcp":    map[string]any{"port": int64(3000), "legacy_flag": true},
			"custom": map[string]any{"keep": true},
		},
		"custom": map[string]any{"nested": "keep"},
		"shell":  map[string]any{"path": []any{}, "legacy_mode": "keep"},
	}
	data, err := configformat.EncodeGeneric(configformat.JSON, existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.HTTP.MCP.Port = 41001
	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := configformat.DecodeGeneric(configformat.JSON, saved)
	if err != nil {
		t.Fatal(err)
	}
	rootValue := raw.(map[string]any)
	httpValue := rootValue["http"].(map[string]any)
	mcp := httpValue["mcp"].(map[string]any)
	httpCustom := httpValue["custom"].(map[string]any)
	shell := rootValue["shell"].(map[string]any)
	custom := rootValue["custom"].(map[string]any)
	if fmt.Sprint(mcp["port"]) != "41001" || mcp["legacy_flag"] != true || httpCustom["keep"] != true || shell["legacy_mode"] != "keep" || custom["nested"] != "keep" {
		t.Fatalf("merged JSON config = %#v", rootValue)
	}
}

func TestConfigSaveRollsBackMainConfigWhenTunnelSecretWriteFails(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	original := []byte(`{"server":{"port":4100},"admin":{"enabled":false,"port":4200},"auth":{"mcp_enabled":false,"admin_enabled":false},"tunnel":{"enabled":false}}`)
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte(`{"api_key":"old-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.APIKey = "new-secret"
	called := false
	if err := saveAtWithSecretSaver(configPath, secretPath, cfg, func(path string, value tunnel.Config) error {
		called = true
		data, readErr := os.ReadFile(configPath)
		if readErr != nil {
			return readErr
		}
		if string(data) == string(original) {
			t.Fatal("main config was not written before secret persistence")
		}
		if writeErr := os.WriteFile(path, []byte(`{"api_key":"partial-new-secret"}`), 0600); writeErr != nil {
			return writeErr
		}
		return errors.New("injected tunnel secret write failure")
	}); err == nil {
		t.Fatal("expected tunnel secret write failure")
	}
	if !called {
		t.Fatal("secret saver was not called")
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(original) {
		t.Fatalf("main config was not rolled back:\n%s", saved)
	}
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(secret) != `{"api_key":"old-secret"}` {
		t.Fatalf("tunnel secret was not rolled back: %s", secret)
	}
}

func TestConfigSaveDoesNotTouchTunnelSecretWhenMainConfigWriteFails(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	if err := os.MkdirAll(filepath.Join(configPath, "block"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte(`{"api_key":"old-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.APIKey = "new-secret"
	if err := saveAt(configPath, secretPath, cfg); err == nil {
		t.Fatal("expected main config write failure")
	}
	saved, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != `{"api_key":"old-secret"}` {
		t.Fatalf("tunnel secret changed after main config failure: %s", saved)
	}
}

func TestConfigRejectsSymlinkMainFile(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	want := []byte(`{"server":{"port":4100}}`)
	if err := os.WriteFile(outside, want, 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	if err := os.Symlink(outside, configPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := loadAt(configPath, secretPath); err == nil {
		t.Fatal("expected symlink main config to be rejected")
	}
	if err := saveAt(configPath, secretPath, Default()); err == nil {
		t.Fatal("expected save through symlink main config to be rejected")
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("outside config changed: %s", data)
	}
}

func TestConfigRejectsSymlinkTunnelFile(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	outside := filepath.Join(t.TempDir(), "outside.json")
	want := []byte(`{"runtime_key_configured":false}`)
	if err := os.WriteFile(outside, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, secretPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := Default()
	cfg.Tunnel.APIKey = "secret"
	if err := saveAt(configPath, secretPath, cfg); err == nil {
		t.Fatal("expected symlink tunnel config to be rejected")
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("outside tunnel config changed: %s", data)
	}
}

func TestClearingRuntimeKeyPreservesAdminKeySecret(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	cfg := Default()
	cfg.Tunnel.APIKey = "runtime-secret"
	cfg.Tunnel.Admin.Key = "admin-secret"
	cfg.Tunnel.Admin.WorkspaceID = "ws_admin"
	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.APIKey = ""
	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tunnel.APIKey != "" || loaded.Tunnel.Admin.Key != "admin-secret" || loaded.Tunnel.Admin.WorkspaceID != "ws_admin" {
		t.Fatalf("loaded tunnel = %#v", loaded.Tunnel)
	}
}
