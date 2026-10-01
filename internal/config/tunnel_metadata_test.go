package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestTunnelMetadataRoundTripUsesJSON(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	metadata := tunnel.Metadata{ID: "tunnel_test", Name: "Test tunnel", Description: "Persisted", WorkspaceIDs: []string{"ws_test"}, FetchedAt: time.Now().UTC().Truncate(time.Second)}
	path, err := SaveTunnelMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("path = %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored tunnelMetadataFile
	if err := json.Unmarshal(data, &stored); err != nil || stored.Version != tunnelMetadataVersion {
		t.Fatalf("stored metadata = %#v err=%v", stored, err)
	}
	loaded, err := LoadTunnelMetadata(metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != metadata.ID || loaded.Name != metadata.Name || len(loaded.WorkspaceIDs) != 1 || loaded.WorkspaceIDs[0] != "ws_test" {
		t.Fatalf("metadata = %#v", loaded)
	}
	if err := RemoveTunnelMetadata(metadata.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("metadata file survived removal: %v", err)
	}
}

func TestSyncTunnelMetadataCreatesMissingPersistedJSONFile(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_test" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer runtime-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"id":"tunnel_test","name":"Synced tunnel","description":"Migrated cache","organization_ids":["org_test"]}`))
	}))
	defer server.Close()

	metadata, path, err := SyncTunnelMetadata(context.Background(), tunnel.Config{ID: "tunnel_test", APIKey: "runtime-key", ControlPlaneBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "Synced tunnel" || filepath.Ext(path) != ".json" {
		t.Fatalf("metadata=%#v path=%s", metadata, path)
	}
	loaded, err := LoadTunnelMetadata("tunnel_test")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != metadata.Name || len(loaded.OrganizationIDs) != 1 || loaded.OrganizationIDs[0] != "org_test" {
		t.Fatalf("persisted metadata = %#v", loaded)
	}
}

func TestTunnelMetadataPathRejectsTraversal(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../escape", "nested/id", `nested\id`} {
		if _, err := TunnelMetadataPath(id); err == nil {
			t.Fatalf("accepted unsafe id %q", id)
		}
	}
}

func TestTunnelMetadataRejectsSymlinkDirectoryEscape(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, TunnelMetadataDir()); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := SaveTunnelMetadata(tunnel.Metadata{ID: "tunnel_test", Name: "Outside"}); err == nil {
		t.Fatal("expected symlink tunnel metadata directory to be rejected")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("tunnel metadata escaped config root: %#v", entries)
	}
}
