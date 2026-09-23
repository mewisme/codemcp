package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
)

func TestTunnelRuntimeKeyReplacementRecoversMissingStoredSecret(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	writeTunnelRecoveryFixture(t, configPath, secretPath, tunnelSecret{RuntimeKeyConfigured: true})

	if _, err := loadAt(configPath, secretPath); err == nil || !strings.Contains(err.Error(), "tunnel runtime key is configured but missing from secret file store") {
		t.Fatalf("strict load err=%v", err)
	}
	cfg, err := loadAtWithTunnelSecretPolicy(configPath, secretPath, tunnelSecretLoadPolicy{allowMissingRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.APIKey != "" {
		t.Fatalf("runtime key=%q want empty replacement slot", cfg.Tunnel.APIKey)
	}
}

func TestTunnelAdminKeyReplacementRecoversMissingStoredSecret(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	writeTunnelRecoveryFixture(t, configPath, secretPath, tunnelSecret{AdminKeyConfigured: true})

	if _, err := loadAt(configPath, secretPath); err == nil || !strings.Contains(err.Error(), "tunnel admin key is configured but missing from secret file store") {
		t.Fatalf("strict load err=%v", err)
	}
	cfg, err := loadAtWithTunnelSecretPolicy(configPath, secretPath, tunnelSecretLoadPolicy{allowMissingAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.AdminKey != "" {
		t.Fatalf("admin key=%q want empty replacement slot", cfg.Tunnel.AdminKey)
	}
}

func TestTunnelReplacementDoesNotRelaxOtherMissingSecret(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	writeTunnelRecoveryFixture(t, configPath, secretPath, tunnelSecret{RuntimeKeyConfigured: true, AdminKeyConfigured: true})

	_, err := loadAtWithTunnelSecretPolicy(configPath, secretPath, tunnelSecretLoadPolicy{allowMissingRuntime: true})
	if err == nil || !strings.Contains(err.Error(), "tunnel admin key is configured but missing from secret file store") {
		t.Fatalf("runtime replacement unexpectedly relaxed admin secret: %v", err)
	}
}

func TestRuntimeLoadAllowsMissingSecretsWhenTunnelDisabled(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	writeTunnelRecoveryFixture(t, configPath, secretPath, tunnelSecret{RuntimeKeyConfigured: true, AdminKeyConfigured: true})

	if _, err := loadAt(configPath, secretPath); err == nil {
		t.Fatal("strict load unexpectedly accepted missing tunnel secrets")
	}
	cfg, err := loadRuntimeAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.Enabled || cfg.Tunnel.APIKey != "" || cfg.Tunnel.AdminKey != "" {
		t.Fatalf("runtime config = %#v", cfg.Tunnel)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("disabled tunnel blocked runtime validation: %v", err)
	}
}

func TestRuntimeLoadStillRequiresRuntimeSecretWhenTunnelEnabled(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	cfg := Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.ID = "tunnel_test"
	configData, err := configformat.MarshalPath(configPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteFileAtomic(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	secretData, err := configformat.MarshalPath(secretPath, tunnelSecret{RuntimeKeyConfigured: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteFileAtomic(secretPath, secretData, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadRuntimeAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(loaded); err == nil || !strings.Contains(err.Error(), "OpenAI tunnel is enabled but API key is empty") {
		t.Fatalf("enabled tunnel missing runtime key validation err=%v", err)
	}
}

func writeTunnelRecoveryFixture(t *testing.T, configPath, secretPath string, stored tunnelSecret) {
	t.Helper()
	cfg := Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	configData, err := configformat.MarshalPath(configPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteFileAtomic(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	secretData, err := configformat.MarshalPath(secretPath, stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteFileAtomic(secretPath, secretData, 0600); err != nil {
		t.Fatal(err)
	}
}
