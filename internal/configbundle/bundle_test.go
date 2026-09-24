package configbundle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	memorypkg "go.mewis.me/codemcp/internal/memory"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestEncodeWritesVersionedJSONEnvelopeWithoutSecrets(t *testing.T) {
	envelope := Envelope{
		Version: Version, CreatedAt: time.Unix(1, 0).UTC(), Source: Platform{OS: "linux", Arch: "amd64", Home: "/home/mew"}, SecretPolicy: SecretPolicyExcluded,
		Files: []File{{Path: "config.json", Mode: 0600, Data: []byte(`{"server":{}}`)}},
	}
	encoded, err := encode(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("export is not JSON: %q", encoded)
	}
	if bytes.Contains(encoded, []byte(`"secrets"`)) || !bytes.Contains(encoded, []byte(`"secret_policy": "excluded"`)) {
		t.Fatalf("secret policy envelope = %s", encoded)
	}
	decoded, err := decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SecretPolicy != SecretPolicyExcluded || string(decoded.Files[0].Data) != `{"server":{}}` {
		t.Fatalf("decoded envelope = %#v", decoded)
	}
	withSecrets := []byte(`{"version":1,"created_at":"2026-09-24T00:00:00Z","source":{"os":"linux","arch":"amd64"},"secret_policy":"excluded","files":[],"secrets":{"x":"plain"}}`)
	if _, err := decode(withSecrets); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("secret-bearing envelope error = %v", err)
	}
}

func TestExportExcludesManagedSecretsAndRuntimeState(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, validConfig())
	if err := os.WriteFile(filepath.Join(root, "tunnel.json"), []byte("{\n  \"runtime_key_configured\": true\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secretName := secretstore.Name("tunnel", "runtime-key")
	if err := secretstore.New(root).Set(secretName, "sk-portable-secret"); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		".runtime-control.json":                     `{"pid":1}`,
		"logs/runtime.jsonl":                        "runtime log\n",
		"runtime/environment.json":                  `{"version":1}`,
		"state/instance.json":                       `{"version":1}`,
		"oauth.json":                                `{"version":1,"credentials":{"server":{"server_id":"server","client_secret":"oauth-client-secret","access_token":"oauth-access-token","refresh_token":"oauth-refresh-token"}}}`,
		"upstreams.json":                            `{"version":1,"upstreams":[{"id":"server","headers":{"Authorization":"upstream-header-secret","X-Test":"ok"},"env":{"API_TOKEN":"upstream-env-secret","MODE":"test"}}]}`,
		"workspaces/ws_test/checkpoints/index.json": `{"version":1}`,
		"workspaces/ws_test/shell.json":             `{"workspace_id":"ws_test"}`,
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "backup.json")
	result, err := Export(root, destination, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkippedFiles < 6 {
		t.Fatalf("result = %#v", result)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || bytes.Contains(raw, []byte("sk-portable-secret")) || bytes.Contains(raw, []byte(`"secrets"`)) {
		t.Fatal("export leaked secret plaintext")
	}
	bundle, err := decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SecretPolicy != SecretPolicyExcluded {
		t.Fatalf("secret policy = %q", bundle.SecretPolicy)
	}
	for _, file := range bundle.Files {
		text := string(file.Data)
		for _, secret := range []string{"mcp-hash", "admin-hash", "oauth-client-secret", "oauth-access-token", "oauth-refresh-token", "upstream-header-secret", "upstream-env-secret"} {
			if strings.Contains(text, secret) {
				t.Fatalf("presentation-safe export leaked %q in %s", secret, file.Path)
			}
		}
		if file.Path == "config.json" && (strings.Contains(text, "mcp_token_hash") || strings.Contains(text, "admin_token_hash")) {
			t.Fatalf("sensitive credential verifier remained in config export: %s", text)
		}
	}
	for _, file := range bundle.Files {
		if excludedFile(file.Path) {
			t.Fatalf("excluded file was exported: %s", file.Path)
		}
	}
}

func TestExportRequiresCanonicalConfigJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[server]\nport = 37421\n"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Export(root, destination, ExportOptions{}); err == nil || !strings.Contains(err.Error(), "configuration is not initialized") {
		t.Fatalf("legacy-only config export error = %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("legacy-only config unexpectedly exported: %v", err)
	}
}

func TestLegacyUpstreamFileIsRejectedWithoutLeakingSecrets(t *testing.T) {
	secret := "legacy-upstream-secret"
	data := []byte(`{"version":1,"servers":[{"id":"legacy","headers":{"Authorization":"` + secret + `"}}]}`)
	if _, err := presentationSafeFile("upstream.json", data); err == nil {
		t.Fatal("legacy upstream file was accepted for export")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("legacy upstream export error leaked secret: %q", err)
	}
	sensitive, err := containsSensitiveState("upstream.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if !sensitive {
		t.Fatal("legacy upstream secret was not detected")
	}
}

func TestMaterializeRejectsLegacyUpstreamFilename(t *testing.T) {
	configData, err := configformat.Marshal(configformat.JSON, validConfig())
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{
		Version: Version,
		Source:  currentPlatform(),
		Files: []File{
			{Path: "config.json", Mode: 0600, Data: configData},
			{Path: "upstream.json", Mode: 0600, Data: []byte(`{"version":1,"servers":[]}`)},
		},
	}
	root := t.TempDir()
	if _, err := materialize(root, bundle, currentPlatform()); err == nil || !strings.Contains(err.Error(), "migrated to upstreams.json") {
		t.Fatalf("legacy upstream materialize error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "upstream.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy upstream file was materialized: %v", err)
	}
}

func TestMaterializeMapsHomePathsAndWorkspaceStateAcrossPlatforms(t *testing.T) {
	targetHome := t.TempDir()
	for _, relative := range []string{"allowed", "bin", "projects/app"} {
		if err := os.MkdirAll(filepath.Join(targetHome, filepath.FromSlash(relative)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	source := foreignPlatform(targetHome)
	sourceAllowed := sourcePath(source, "allowed")
	sourceBin := sourcePath(source, "bin")
	sourceWorkspace := sourcePath(source, "projects/app")
	sourceOutside := foreignOutsidePath(source)
	cfg := validConfig()
	cfg.Permissions.AllowDirs = []string{sourceAllowed, sourceOutside}
	cfg.Shell.Path = []string{sourceBin, sourceOutside}
	configData, err := configformat.Marshal(configformat.JSON, cfg)
	if err != nil {
		t.Fatal(err)
	}
	oldID := "ws_source"
	registryData, err := configformat.Marshal(configformat.JSON, workspaceRegistry{Version: 3, Workspaces: []workspace.Workspace{{ID: oldID, Path: sourceWorkspace, AllowDirs: []string{sourceAllowed}}}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalMemory := "## tooling\n\n### package-manager\n- use pnpm\n\n## tui\n\n### theme\n- use Charm defaults\n"
	bundle := Bundle{Version: Version, Source: source, Files: []File{
		{Path: "config.json", Mode: 0600, Data: configData},
		{Path: "workspaces.json", Mode: 0600, Data: registryData},
		{Path: "workspaces/" + oldID + "/MEMORY.md", Mode: 0600, Data: []byte(canonicalMemory)},
	}}
	target := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Home: targetHome}
	stage := filepath.Join(t.TempDir(), "stage")
	result, err := materialize(stage, bundle, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.skippedPaths != 2 {
		t.Fatalf("skipped paths = %d, want 2", result.skippedPaths)
	}
	var importedConfig config.Config
	data, err := os.ReadFile(filepath.Join(stage, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := configformat.Unmarshal(configformat.JSON, data, &importedConfig); err != nil {
		t.Fatal(err)
	}
	if len(importedConfig.Permissions.AllowDirs) != 1 || importedConfig.Permissions.AllowDirs[0] != filepath.Join(targetHome, "allowed") {
		t.Fatalf("allow dirs = %#v", importedConfig.Permissions.AllowDirs)
	}
	if len(importedConfig.Shell.Path) != 1 || importedConfig.Shell.Path[0] != filepath.Join(targetHome, "bin") {
		t.Fatalf("shell path = %#v", importedConfig.Shell.Path)
	}
	var registry workspaceRegistry
	data, err = os.ReadFile(filepath.Join(stage, "workspaces.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := configformat.Unmarshal(configformat.JSON, data, &registry); err != nil {
		t.Fatal(err)
	}
	if len(registry.Workspaces) != 1 || registry.Workspaces[0].Path != filepath.Join(targetHome, "projects", "app") {
		t.Fatalf("workspaces = %#v", registry.Workspaces)
	}
	if registry.Workspaces[0].ID != oldID {
		t.Fatalf("workspace identity = %#v", registry.Workspaces[0])
	}
	memory, err := os.ReadFile(filepath.Join(stage, "workspaces", oldID, "MEMORY.md"))
	if err != nil || string(memory) != canonicalMemory {
		t.Fatalf("memory = %q err=%v", memory, err)
	}
	document := memorypkg.Parse(string(memory))
	if len(document.Entries) != 2 || document.Entries[0].Scope != "tooling" || document.Entries[0].Key != "package-manager" || document.Entries[1].Scope != "tui" || document.Entries[1].Key != "theme" {
		t.Fatalf("portable memory document = %#v", document)
	}
}

func TestMaterializeCanonicalizesFilePermissions(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows does not expose Unix permission bits consistently")
	}
	cfg := validConfig()
	data, err := configformat.Marshal(configformat.JSON, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(t.TempDir(), "stage")
	_, err = materialize(stage, Bundle{Version: Version, Source: currentPlatform(), Files: []File{{Path: "config.json", Mode: 0777, Data: data}}}, currentPlatform())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(stage, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("imported config mode = %#o", info.Mode().Perm())
	}
}

func TestNormalizeMainConfigPreservesUnknownKeys(t *testing.T) {
	raw := map[string]any{
		"server": map[string]any{"port": int64(37421), "legacy_flag": true},
		"custom": map[string]any{"nested": "keep"},
	}
	data, err := configformat.EncodeGeneric(configformat.JSON, raw)
	if err != nil {
		t.Fatal(err)
	}
	normalized, _, err := normalizeMainConfig(data, currentPlatform(), currentPlatform())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := configformat.DecodeGeneric(configformat.JSON, normalized)
	if err != nil {
		t.Fatal(err)
	}
	root := decoded.(map[string]any)
	server := root["server"].(map[string]any)
	custom := root["custom"].(map[string]any)
	if server["legacy_flag"] != true || custom["nested"] != "keep" {
		t.Fatalf("normalized config lost unknown keys: %#v", root)
	}
}

func TestImportPreservesExistingSecretsAndRejectsInvalidEnvelopeBeforeMutation(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "config")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, root, validConfig())
	if err := configformat.MarkRoot(root); err != nil {
		t.Fatal(err)
	}
	secretName := secretstore.Name("tunnel", "runtime-key")
	if err := secretstore.New(root).Set(secretName, "sk-existing"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tunnel.json"), []byte("{\n  \"version\": 1,\n  \"runtime_key_configured\": true\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.Server.Port = 40200
	configData, err := configformat.Marshal(configformat.JSON, cfg)
	if err != nil {
		t.Fatal(err)
	}
	configData, err = presentationSafeConfig(configData)
	if err != nil {
		t.Fatal(err)
	}
	envelopeFile := filepath.Join(t.TempDir(), "portable.json")
	good := Envelope{
		Version: Version, CreatedAt: time.Now().UTC(), Source: currentPlatform(), SecretPolicy: SecretPolicyExcluded,
		Files: []File{{Path: "config.json", Mode: 0600, Data: configData}},
	}
	writeEnvelopeFile(t, envelopeFile, good)
	result, err := Import(root, envelopeFile, ImportOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.BackupPath == "" {
		t.Fatal("forced import did not retain a backup")
	}
	if err := os.RemoveAll(result.BackupPath); err != nil {
		t.Fatal(err)
	}
	secret, err := secretstore.New(root).Get(secretName)
	if err != nil || secret != "sk-existing" {
		t.Fatalf("secret = %q err=%v", secret, err)
	}
	tunnelData, err := os.ReadFile(filepath.Join(root, "tunnel.json"))
	if err != nil || !strings.Contains(string(tunnelData), `"runtime_key_configured": true`) {
		t.Fatalf("tunnel secret metadata = %s err=%v", tunnelData, err)
	}
	if _, err := config.VerifyAt(root); err != nil {
		t.Fatal(err)
	}

	original, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	badConfig := validConfig()
	badConfig.Server.Port = 0
	badData, err := configformat.Marshal(configformat.JSON, badConfig)
	if err != nil {
		t.Fatal(err)
	}
	badData, err = presentationSafeConfig(badData)
	if err != nil {
		t.Fatal(err)
	}
	badFile := filepath.Join(t.TempDir(), "bad.json")
	writeEnvelopeFile(t, badFile, Envelope{Version: Version, CreatedAt: time.Now().UTC(), Source: currentPlatform(), SecretPolicy: SecretPolicyExcluded, Files: []File{{Path: "config.json", Mode: 0600, Data: badData}}})
	if _, err := Import(root, badFile, ImportOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "before activation") {
		t.Fatalf("invalid import error = %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatal("failed import did not restore previous config root")
	}
	secret, err = secretstore.New(root).Get(secretName)
	if err != nil || secret != "sk-existing" {
		t.Fatalf("rolled back secret = %q err=%v", secret, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config-backup-") || strings.HasPrefix(entry.Name(), ".config-failed-import-") || strings.HasPrefix(entry.Name(), ".config-import-") {
			t.Fatalf("invalid import left transactional residue: %s", entry.Name())
		}
	}
}

func TestImportForceMergesExistingMainConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"server": map[string]any{"port": int64(40100), "existing_only": true},
		"auth":   map[string]any{"mcp_token_hash": "target-mcp-hash", "admin_token_hash": "target-admin-hash"},
		"custom": map[string]any{"nested": "keep"},
	}
	existingData, err := configformat.EncodeGeneric(configformat.JSON, existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), existingData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := configformat.MarkRoot(root); err != nil {
		t.Fatal(err)
	}
	imported := validConfig()
	imported.Server.Port = 40200
	importedData, err := configformat.Marshal(configformat.JSON, imported)
	if err != nil {
		t.Fatal(err)
	}
	importedData, err = presentationSafeConfig(importedData)
	if err != nil {
		t.Fatal(err)
	}
	bundleFile := filepath.Join(t.TempDir(), "merge.json")
	writeEnvelopeFile(t, bundleFile, Envelope{Version: Version, CreatedAt: time.Now().UTC(), Source: currentPlatform(), SecretPolicy: SecretPolicyExcluded, Files: []File{{Path: "config.json", Mode: 0600, Data: importedData}}})
	if _, err := Import(root, bundleFile, ImportOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := configformat.DecodeGeneric(configformat.JSON, saved)
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(map[string]any)
	server := result["server"].(map[string]any)
	auth := result["auth"].(map[string]any)
	custom := result["custom"].(map[string]any)
	if server["port"] != int64(40200) || server["existing_only"] != true || auth["mcp_token_hash"] != "target-mcp-hash" || auth["admin_token_hash"] != "target-admin-hash" || custom["nested"] != "keep" {
		t.Fatalf("merged import = %#v", result)
	}
}

func TestValidateEnvelopeRejectsSensitiveState(t *testing.T) {
	data, err := configformat.Marshal(configformat.JSON, validConfig())
	if err != nil {
		t.Fatal(err)
	}
	envelope := Envelope{Version: Version, CreatedAt: time.Now().UTC(), Source: currentPlatform(), SecretPolicy: SecretPolicyExcluded, Files: []File{{Path: "config.json", Data: data}}}
	if err := validateEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "sensitive state") {
		t.Fatalf("sensitive envelope error = %v", err)
	}
}

func validConfig() config.Config {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	return cfg
}

func writeConfigFile(t *testing.T, root string, cfg config.Config) {
	t.Helper()
	data, err := configformat.Marshal(configformat.JSON, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeEnvelopeFile(t *testing.T, path string, bundle Bundle) {
	t.Helper()
	data, err := encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func foreignPlatform(targetHome string) Platform {
	if runtime.GOOS == "windows" {
		return Platform{OS: "linux", Arch: "amd64", Home: "/home/mew"}
	}
	return Platform{OS: "windows", Arch: "amd64", Home: `C:\Users\Mew`}
}

func sourcePath(source Platform, relative string) string {
	if source.OS == "windows" {
		return strings.TrimRight(source.Home, `\/`) + `\` + strings.ReplaceAll(relative, "/", `\`)
	}
	return strings.TrimRight(source.Home, "/") + "/" + relative
}

func foreignOutsidePath(source Platform) string {
	if source.OS == "windows" {
		return `D:\External\bin`
	}
	return "/opt/external/bin"
}
