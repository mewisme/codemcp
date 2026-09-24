package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyAtChecksStructuredTree(t *testing.T) {
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
	write("workspaces.json", `[]`)
	write("workspaces/ws_test/shell.json", `{"workspace_id":"ws_test","cwd":"/tmp"}`)
	write("tunnels/tunnel_test.json", `{"id":"tunnel_test","name":"Test tunnel"}`)

	result, err := verifyAt(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "json" || result.Ext != ".json" || result.Files != 4 {
		t.Fatalf("verify result = %#v", result)
	}
}

func TestVerifyAtRejectsMixedFormat(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"server":{"port":37421,"allow_unauthenticated_loopback":true,"expose":{"mode":"none","interfaces":[]}},"admin":{"enabled":false,"port":37422},"auth":{"mcp_enabled":false,"admin_enabled":false},"tunnel":{"enabled":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspaces.toml"), []byte(`items = []`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyAt(root, false); err == nil {
		t.Fatal("mixed structured format was accepted")
	}
}

func TestVerifyAtRejectsInvalidStructuredFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"server":{"port":37421,"allow_unauthenticated_loopback":true,"expose":{"mode":"none","interfaces":[]}},"admin":{"enabled":false,"port":37422},"auth":{"mcp_enabled":false,"admin_enabled":false},"tunnel":{"enabled":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspaces.json"), []byte(`{invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyAt(root, false); err == nil {
		t.Fatal("invalid structured file was accepted")
	}
}

func TestVerifyRuntimeAtIgnoresInvalidCheckpointState(t *testing.T) {
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
	write("workspaces.json", `[]`)
	write("workspaces/ws_test/checkpoints/index.yaml", "version: 1\ncheckpoints: []\n")
	write("workspaces/ws_test/checkpoints/data/cp_test/manifest.yaml", "version: 1\nfiles:\n  - content: first\n      broken: value\n")
	if _, err := verifyAt(root, false); err == nil {
		t.Fatal("strict verification accepted malformed checkpoint manifest")
	}
	result, err := verifyAt(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "json" || result.Ext != ".json" || result.Files != 4 {
		t.Fatalf("runtime verify result = %#v", result)
	}
}
