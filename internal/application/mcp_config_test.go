package application

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/mcpconfig"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestMCPConfigReadServiceRequiresOptInAndSanitizesCanonicalSettings(t *testing.T) {
	isolateSettingServiceConfig(t)
	provider := NewMCPConfigReadService()
	if _, code := provider.Get(t.Context(), "server.port"); code != mcpconfig.ErrorAccessDenied {
		t.Fatalf("read without opt-in code=%q", code)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Permissions.MCPConfigRead = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	shortSecret := "s3cr3t"
	longSecret := strings.Repeat("long-secret-material-", 128)
	unsafeURL := "https://operator:credential@example.invalid/control"
	settings := NewSettingService()
	for _, change := range []struct {
		key   string
		value string
	}{
		{"tunnel.api_key", shortSecret},
		{"tunnel.admin.key", longSecret},
		{"tunnel.control_plane_base_url", unsafeURL},
	} {
		if _, err := settings.Set(t.Context(), change.key, change.value); err != nil {
			t.Fatalf("set %s: %v", change.key, err)
		}
	}

	for _, key := range []string{"tunnel.api_key", "tunnel.admin.key"} {
		setting, code := provider.Get(t.Context(), key)
		if code != "" {
			t.Fatalf("get %s code=%q", key, code)
		}
		if !setting.Secret || setting.Value != nil || setting.Configured == nil || !*setting.Configured {
			t.Fatalf("secret projection %s=%#v", key, setting)
		}
	}

	listed, code := provider.List(t.Context(), "tunnel")
	if code != "" {
		t.Fatalf("list code=%q", code)
	}
	data, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{shortSecret, longSecret, unsafeURL, "operator:credential"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("safe list leaked %q: %s", forbidden, text)
		}
	}
	if strings.Contains(text, "tunnel.control_plane_base_url") {
		t.Fatalf("unsafe URL setting appeared in safe list: %s", text)
	}

	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{
		ID: "remote", Name: "Remote", Enabled: true, Transport: "http",
		URL: "https://example.invalid/mcp?token=nested-secret",
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"auth.mcp_token_hash",
		"tunnel.control_plane_base_url",
		"upstream.servers[remote].url",
		"upstream.servers[remote].command",
		"upstream.servers[remote].args",
		"upstream.servers[missing].enabled",
	} {
		if _, code := provider.Get(t.Context(), key); code != mcpconfig.ErrorUnsupportedSetting {
			t.Fatalf("unsafe/unavailable key %q code=%q", key, code)
		}
	}

	port, code := provider.Get(t.Context(), "server.port")
	if code != "" || port.Value == nil || *port.Value == "" || port.Secret {
		t.Fatalf("safe non-secret projection=%#v code=%q", port, code)
	}
}
