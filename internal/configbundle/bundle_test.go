package configbundle

import (
	"bytes"
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

func TestEncodeSealsAndAuthenticatesBundle(t *testing.T) {
	bundle := Bundle{
		Version: Version, CreatedAt: time.Unix(1, 0).UTC(), Source: Platform{OS: "linux", Arch: "amd64", Home: "/home/mew"},
		Files:   []File{{Path: "config.json", Mode: 0600, Data: []byte(`{"secret":"plain-marker"}`)}},
		Secrets: map[string]string{"secret-name": "plain-secret-value"},
	}
	encoded, err := encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range [][]byte{[]byte("plain-marker"), []byte("plain-secret-value")} {
		if bytes.Contains(encoded, plain) {
			t.Fatalf("sealed bundle leaked plaintext %q", plain)
		}
	}
	decoded, err := decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Secrets["secret-name"] != "plain-secret-value" || string(decoded.Files[0].Data) != `{"secret":"plain-marker"}` {
		t.Fatalf("decoded bundle = %#v", decoded)
	}
	tampered := append([]byte(nil), encoded...)
	tampered[len(tampered)-1] ^= 1
	if _, err := decode(tampered); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("tampered bundle error = %v", err)
	}
}

func TestExportIncludesLogicalSecretsAndSkipsRuntimeState(t *testing.T) {
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
	destination := filepath.Join(t.TempDir(), "backup.cgm")
	result, err := Export(root, destination, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Secrets != 1 || result.SkippedFiles < 6 {
		t.Fatalf("result = %#v", result)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk-portable-secret")) {
		t.Fatal("export leaked secret plaintext")
	}
	bundle, err := decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Secrets[secretName] != "sk-portable-secret" {
		t.Fatalf("secrets = %#v", bundle.Secrets)
	}
	for _, file := range bundle.Files {
		if excludedFile(file.Path) {
			t.Fatalf("excluded file was exported: %s", file.Path)
		}
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
	newID := workspace.IDForPath(registry.Workspaces[0].Path)
	if registry.Workspaces[0].ID != newID || !contains(registry.Workspaces[0].LegacyIDs, oldID) {
		t.Fatalf("workspace identity = %#v", registry.Workspaces[0])
	}
	memory, err := os.ReadFile(filepath.Join(stage, "workspaces", newID, "MEMORY.md"))
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
	normalized, _, err := normalizeMainConfig("config.json", data, currentPlatform(), currentPlatform())
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

func TestImportRestoresSecretAndRollsBackInvalidReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	bundleFile := filepath.Join(t.TempDir(), "portable.cgm")
	cfg := validConfig()
	configData, err := configformat.Marshal(configformat.JSON, cfg)
	if err != nil {
		t.Fatal(err)
	}
	secretName := secretstore.Name("tunnel", "runtime-key")
	good := Bundle{
		Version: Version, CreatedAt: time.Now().UTC(), Source: currentPlatform(),
		Files: []File{
			{Path: "config.json", Mode: 0600, Data: configData},
			{Path: "tunnel.json", Mode: 0600, Data: []byte("{\n  \"runtime_key_configured\": true\n}\n")},
		},
		Secrets: map[string]string{secretName: "sk-imported"},
	}
	writeBundleFile(t, bundleFile, good)
	result, err := Import(root, bundleFile, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Secrets != 1 || result.Files < 2 {
		t.Fatalf("result = %#v", result)
	}
	secret, err := secretstore.New(root).Get(secretName)
	if err != nil || secret != "sk-imported" {
		t.Fatalf("secret = %q err=%v", secret, err)
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
	badFile := filepath.Join(t.TempDir(), "bad.cgm")
	writeBundleFile(t, badFile, Bundle{Version: Version, CreatedAt: time.Now().UTC(), Source: currentPlatform(), Files: []File{{Path: "config.json", Mode: 0600, Data: badData}}})
	if _, err := Import(root, badFile, ImportOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "verify imported configuration") {
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
	if err != nil || secret != "sk-imported" {
		t.Fatalf("rolled back secret = %q err=%v", secret, err)
	}
}

func TestImportForceMergesExistingMainConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"server": map[string]any{"port": int64(40100), "existing_only": true},
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
	bundleFile := filepath.Join(t.TempDir(), "merge.cgm")
	writeBundleFile(t, bundleFile, Bundle{Version: Version, CreatedAt: time.Now().UTC(), Source: currentPlatform(), Files: []File{{Path: "config.json", Mode: 0600, Data: importedData}}})
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
	custom := result["custom"].(map[string]any)
	if server["port"] != int64(40200) || server["existing_only"] != true || custom["nested"] != "keep" {
		t.Fatalf("merged import = %#v", result)
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

func writeBundleFile(t *testing.T, path string, bundle Bundle) {
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

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
