package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestCanonicalizeStartupPrunesOnlyKnownDeprecatedKeys(t *testing.T) {
	root := t.TempDir()
	defer func() { _ = configformat.SetRootPath("") }()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	tunnelPath := filepath.Join(root, "tunnel.json")
	configData := []byte(`{
		"interactive":true,
		"custom":{"keep":"value"},
		"server":{"enabled":true,"port":37421,"host":"127.0.0.1","legacy_unknown":"keep"},
		"shell":{"path":[],"approval_policy":"strict","unknown_policy":"keep"},
		"tunnel":{"enabled":false,"command":"old","args":["old"],"origin":"old","public_url":"old","unknown":"keep"}
	}`)
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tunnelPath, []byte(`{"api_key":"<secret-file>","custom":"keep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	removed, err := CanonicalizeStartup()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 8 {
		t.Fatalf("removed=%d, want 8", removed)
	}
	mainData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	mainRaw, err := configformat.DecodeGeneric(configformat.JSON, mainData)
	if err != nil {
		t.Fatal(err)
	}
	main := mainRaw.(map[string]any)
	if _, exists := main["interactive"]; exists {
		t.Fatalf("deprecated interactive key retained: %#v", main)
	}
	server := main["server"].(map[string]any)
	if _, exists := server["host"]; exists || server["legacy_unknown"] != "keep" {
		t.Fatalf("server canonicalization=%#v", server)
	}
	shell := main["shell"].(map[string]any)
	if _, exists := shell["approval_policy"]; exists || shell["unknown_policy"] != "keep" {
		t.Fatalf("shell canonicalization=%#v", shell)
	}
	tunnel := main["tunnel"].(map[string]any)
	for _, key := range []string{"command", "args", "origin", "public_url"} {
		if _, exists := tunnel[key]; exists {
			t.Fatalf("deprecated tunnel key %q retained: %#v", key, tunnel)
		}
	}
	if tunnel["unknown"] != "keep" || main["custom"].(map[string]any)["keep"] != "value" {
		t.Fatalf("unknown keys were not preserved: %#v", main)
	}
	tunnelData, err := os.ReadFile(tunnelPath)
	if err != nil {
		t.Fatal(err)
	}
	tunnelRaw, err := configformat.DecodeGeneric(configformat.JSON, tunnelData)
	if err != nil {
		t.Fatal(err)
	}
	tunnelRoot := tunnelRaw.(map[string]any)
	if _, exists := tunnelRoot["api_key"]; exists {
		t.Fatalf("deprecated api_key retained: %#v", tunnelRoot)
	}
	if tunnelRoot["custom"] != "keep" {
		t.Fatalf("unknown tunnel key was removed: %#v", tunnelRoot)
	}
}
