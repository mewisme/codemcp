package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestConvertFormatAtConvertsStructuredTree(t *testing.T) {
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"server":{"port":37421},"admin":{"enabled":true}}`)
	write("tunnel.json", `{"api_key":"secret"}`)
	write("upstream.json", `[{"id":"alpha","name":"Alpha","transport":"http","enabled":true}]`)
	write("workspaces/ws_test/shell.json", `{"workspace_id":"ws_test","cwd":"/tmp"}`)
	write("workspaces/ws_test/checkpoints/index.json", `{"version":1,"checkpoints":[]}`)
	write("workspaces/ws_test/checkpoints/data/cp_test/manifest.json", `{"version":1,"id":"cp_test"}`)
	write("tunnels/tunnel_test.json", `{"id":"tunnel_test","name":"Test tunnel"}`)
	write("workspaces/ws_test/activity.jsonl", "{}\n")
	memoryContent := "## tooling\n\n### package-manager\n- use pnpm\n"
	write("workspaces/ws_test/MEMORY.md", memoryContent)

	converted, err := convertFormatAt(root, configformat.TOML)
	if err != nil {
		t.Fatal(err)
	}
	if converted != 7 {
		t.Fatalf("converted = %d, want 7", converted)
	}
	for _, relative := range []string{
		"config.toml", "tunnel.toml", "upstream.toml", "workspaces/ws_test/shell.toml",
		"workspaces/ws_test/checkpoints/index.toml", "workspaces/ws_test/checkpoints/data/cp_test/manifest.toml", "tunnels/tunnel_test.toml",
	} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("missing converted %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("old config survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "workspaces/ws_test/activity.jsonl")); err != nil {
		t.Fatalf("jsonl log should not be converted: %v", err)
	}
	memoryData, err := os.ReadFile(filepath.Join(root, "workspaces/ws_test/MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(memoryData) != memoryContent {
		t.Fatalf("memory should not be converted: %q", memoryData)
	}
	upstreamData, err := os.ReadFile(filepath.Join(root, "upstream.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var upstream struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := configformat.Unmarshal(configformat.TOML, upstreamData, &upstream); err != nil {
		t.Fatal(err)
	}
	if len(upstream.Servers) != 1 || upstream.Servers[0]["id"] != "alpha" {
		t.Fatalf("upstream = %#v", upstream.Servers)
	}
}

func TestConvertFormatAtPreservesLegacyInteractiveKey(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "config.json")
	if err := os.WriteFile(source, []byte(`{"interactive":true,"server":{"port":37421}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := convertFormatAt(root, configformat.TOML); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := configformat.DecodeGeneric(configformat.TOML, data)
	if err != nil {
		t.Fatal(err)
	}
	configValue, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("converted config = %#v", value)
	}
	if value, exists := configValue["interactive"]; !exists || value != true {
		t.Fatalf("legacy interactive key was removed during conversion: %s", data)
	}
}

func TestConvertFormatAtRepairsMixedStructuredFormats(t *testing.T) {
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"server":{"port":37421,"allow_unauthenticated_loopback":true,"expose":{"mode":"none","interfaces":[]}},"admin":{"enabled":false,"port":37422},"auth":{"mcp_enabled":false,"admin_enabled":false},"tunnel":{"enabled":false}}`)
	write("tunnel.toml", `api_key = 'secret'`)
	write("workspaces.yaml", "[]\n")

	converted, err := convertFormatAt(root, configformat.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if converted != 2 {
		t.Fatalf("converted = %d, want 2", converted)
	}
	for _, relative := range []string{"config.json", "tunnel.json", "workspaces.json"} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("missing %s: %v", relative, err)
		}
	}
	for _, relative := range []string{"tunnel.toml", "workspaces.yaml"} {
		if _, err := os.Stat(filepath.Join(root, relative)); !os.IsNotExist(err) {
			t.Fatalf("old mixed-format file survived %s: %v", relative, err)
		}
	}
}

func TestConvertFormatAtRejectsAmbiguousTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tunnel.json"), []byte(`{"api_key":"one"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tunnel.toml"), []byte(`api_key = 'two'`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := convertFormatAt(root, configformat.YAML); err == nil {
		t.Fatal("ambiguous structured files were accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "tunnel.yaml")); !os.IsNotExist(err) {
		t.Fatalf("conversion mutated before ambiguity failure: %v", err)
	}
}

func TestConvertFormatAtPreflightsExistingTargetFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tunnel.toml"), []byte(`api_key = 'secret'`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := convertFormatAt(root, configformat.JSON); err == nil {
		t.Fatal("invalid target-format file was skipped during preflight")
	}
	if _, err := os.Stat(filepath.Join(root, "tunnel.json")); !os.IsNotExist(err) {
		t.Fatalf("conversion mutated before target preflight failed: %v", err)
	}
}
