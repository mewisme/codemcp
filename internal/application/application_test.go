package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestInitializeAndAuthLifecycle(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	result, err := Initialize(InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigPath != filepath.Join(root, "config.json") || result.Format != configformat.JSON {
		t.Fatalf("init result = %#v", result)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var configFiles []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "config.") {
			configFiles = append(configFiles, entry.Name())
		}
	}
	if len(configFiles) != 1 || configFiles[0] != "config.json" {
		t.Fatalf("fresh init config files=%v", configFiles)
	}
	if !strings.HasPrefix(result.MCPToken, "mcp_") || !strings.HasPrefix(result.AdminToken, "admin_") {
		t.Fatalf("generated tokens have unexpected format")
	}
	status, err := GetAuthStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !status.MCPEnabled || !status.MCPConfigured || !status.AdminEnabled || !status.AdminConfigured {
		t.Fatalf("status = %#v", status)
	}
	if _, err := SetConfigField(t.Context(), "server.allow_unauthenticated_loopback", "true"); err != nil {
		t.Fatal(err)
	}
	status, err = SetAuthEnabled(t.Context(), "mcp", false)
	if err != nil {
		t.Fatal(err)
	}
	if status.MCPEnabled || !status.MCPConfigured || !status.UnauthenticatedLoopback {
		t.Fatalf("disabled status = %#v", status)
	}
	rotated, status, err := RotateAuthToken(t.Context(), "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if rotated == result.MCPToken || !status.MCPEnabled || !status.MCPConfigured {
		t.Fatalf("rotation did not replace and enable MCP auth")
	}
	if _, _, err := RotateAuthToken(t.Context(), "missing"); err == nil {
		t.Fatal("invalid auth kind unexpectedly accepted")
	}
}

func TestInitializeForcePreservesJSONAndExistingConfig(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	first, err := Initialize(InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Format != configformat.JSON || filepath.Ext(first.ConfigPath) != ".json" {
		t.Fatalf("format = %s", first.Format)
	}
	if _, err := Initialize(InitOptions{}); err == nil {
		t.Fatal("existing config unexpectedly overwritten without force")
	}
	if _, err := SetConfigField(t.Context(), "server.port", "40123"); err != nil {
		t.Fatal(err)
	}
	forced, err := Initialize(InitOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if forced.Format != configformat.JSON || filepath.Ext(forced.ConfigPath) != ".json" {
		t.Fatalf("forced result = %#v", forced)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != 40123 {
		t.Fatalf("init --force reset existing config: %#v", loaded.Server)
	}
	for _, name := range []string{"config.yaml", "config.yml", "config.toml"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("alternate config file exists after init --force: %s err=%v", name, err)
		}
	}
}

func TestUninitializeRemovesManagedRoot(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(InitOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Uninitialize(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("config root still exists: %v", err)
	}
	if err := RemoveConfigRoot(t.TempDir()); err == nil {
		t.Fatal("unmanaged root unexpectedly removed")
	}
}

func TestUninitializePreservesUnrelatedFilesInManagedRoot(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(InitOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Uninitialize(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(unrelated)
	if err != nil || string(data) != "keep" {
		t.Fatalf("unrelated file data=%q err=%v", data, err)
	}
	if configformat.IsManagedRoot(root) {
		t.Fatal("managed root marker remained after uninitialize")
	}
}

func TestSetAuthEnabledRequiresConfiguredToken(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := SetAuthEnabled(t.Context(), "mcp", true); err == nil {
		t.Fatal("MCP auth enabled without token")
	}
	if _, err := SetAuthEnabled(t.Context(), "admin", true); err == nil {
		t.Fatal("admin auth enabled without token")
	}
}

func TestConfigMutationUsesDomainValidationAndPreservesSecrets(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	cfg.Tunnel.APIKey = "runtime-secret"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	want := cfg
	wantErr := config.SetValueValidated(&want, "server.port", "70000")
	if wantErr == nil {
		t.Fatal("domain validation unexpectedly accepted invalid port")
	}
	if _, err := SetConfigField(t.Context(), "server.port", "70000"); err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("application validation err=%v want=%v", err, wantErr)
	}
	result, err := SetConfigField(t.Context(), "server.port", "40123")
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Server.Port != 40123 || result.Config.Auth.MCPTokenHash != "mcp-hash" || result.Config.Auth.AdminTokenHash != "admin-hash" || result.Config.Tunnel.APIKey != "runtime-secret" {
		t.Fatalf("config mutation changed unrelated values: %#v", result.Config)
	}
}

func TestConfigMutationEmitsDeepTraceWithoutSecrets(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-secret-hash"
	cfg.Auth.AdminTokenHash = "admin-secret-hash"
	cfg.Tunnel.APIKey = "runtime-secret-key"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) { events = append(events, event) })
	result, err := SetConfigField(ctx, "server.port", "40123")
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Server.Port != 40123 {
		t.Fatalf("port=%d", result.Config.Server.Port)
	}
	for _, name := range []string{"config.mutation.load.completed", "config.field.validate.completed", "config.persist.completed", "config.runtime.reload.completed", "config.field.mutate.completed"} {
		if !applicationTraceContains(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"mcp-secret-hash", "admin-secret-hash", "runtime-secret-key"} {
		if strings.Contains(text, secret) {
			t.Fatalf("trace leaked secret %q: %s", secret, text)
		}
	}
}

func TestRotateAuthTokenTraceDoesNotLeakCredential(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "old-secret-hash"
	cfg.Auth.AdminTokenHash = "old-admin-secret-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) { events = append(events, event) })
	token, _, err := RotateAuthToken(ctx, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth.kind.normalize.completed", "auth.token.generate.completed", "auth.token.hash.completed", "auth.config.validate.completed", "config.persist.completed", "auth.token.rotate.completed"} {
		if !applicationTraceContains(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{token, loaded.Auth.MCPTokenHash, "old-secret-hash", "old-admin-secret-hash"} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("trace leaked credential: %s", text)
		}
	}
}

func applicationTraceContains(events []tracepkg.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func TestConfigExportImportPreservesSafetyAndState(t *testing.T) {
	defer configformat.SetRootPath("")
	base := t.TempDir()
	root := filepath.Join(base, "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	cfg.Server.Port = 40123
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(base, "backup.cgm")
	if _, err := ExportConfig(bundle, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportConfig(bundle, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("export overwrite safety err=%v", err)
	}
	if _, err := SetConfigField(t.Context(), "server.port", "40234"); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportConfig(context.Background(), bundle, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("import replacement safety err=%v", err)
	}
	if _, err := ImportConfig(context.Background(), bundle, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != 40123 || loaded.Auth.MCPTokenHash != "mcp-hash" || loaded.Auth.AdminTokenHash != "admin-hash" {
		t.Fatalf("import did not restore original config: %#v", loaded)
	}
}

func TestRuntimeRunningWithoutControlState(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	running, err := RuntimeRunning(context.Background())
	if err != nil || running {
		t.Fatalf("running=%t err=%v", running, err)
	}
}
