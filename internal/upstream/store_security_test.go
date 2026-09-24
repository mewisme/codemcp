package upstream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreKeepsSensitiveHeaderAndEnvInSecretFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstreams.json")
	store := NewStore(path)
	server := Server{ID: "alpha", Name: "Alpha", Transport: "http", URL: "https://example.test/mcp", Headers: map[string]string{"Authorization": "Bearer header-private-value", "X-Test": "ok"}, Env: map[string]string{"API_TOKEN": "env-private-value", "MODE": "test"}}
	if err := store.Save([]Server{server}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "header-private-value") || strings.Contains(text, "env-private-value") || !strings.Contains(text, `"version": 1`) || !strings.Contains(text, "secret-file") || !strings.Contains(text, `"X-Test": "ok"`) || !strings.Contains(text, `"MODE": "test"`) {
		t.Fatalf("upstream file persistence = %s", data)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Headers["Authorization"] != "Bearer header-private-value" || loaded[0].Env["API_TOKEN"] != "env-private-value" {
		t.Fatalf("loaded=%#v", loaded)
	}
}

func TestUpstreamSecretsMigrateToSecretFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstreams.json")
	stored := diskStore{Upstreams: []Server{{ID: "alpha", Name: "Alpha", Transport: "http", URL: "https://example.test/mcp", Headers: map[string]string{"Authorization": "Bearer legacy-header-value"}, Env: map[string]string{"API_TOKEN": "legacy-env-value"}}}}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].Headers["Authorization"] != "Bearer legacy-header-value" || loaded[0].Env["API_TOKEN"] != "legacy-env-value" {
		t.Fatalf("loaded=%#v", loaded)
	}
	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migrated), "legacy-header-value") || strings.Contains(string(migrated), "legacy-env-value") || !strings.Contains(string(migrated), `"version": 1`) || strings.Count(string(migrated), "secret-file") < 2 {
		t.Fatalf("upstream file was not migrated: %s", migrated)
	}
}

func TestStoreRejectsSymlinkConfigFile(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	want := []byte(`{"upstreams":[]}`)
	if err := os.WriteFile(outside, want, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "upstreams.json")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := NewStore(path)
	if _, err := store.Load(); err == nil {
		t.Fatal("expected symlink upstream config to be rejected")
	}
	if err := store.Save(nil); err == nil {
		t.Fatal("expected save through symlink upstream config to be rejected")
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("outside upstream config changed: %s", data)
	}
}
