package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestWorkspaceListDefaultsToPlainAndSupportsJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	workspaceRoot := t.TempDir()
	registered := executeRequestCommand(t, root, []string{"workspace", "register", workspaceRoot})
	if !strings.Contains(registered, "Workspace registered") {
		t.Fatalf("register=%q", registered)
	}
	jsonOutput := executeRequestCommand(t, root, []string{"workspace", "list", "--json"})
	var items []workspace.Workspace
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &items); err != nil || len(items) != 1 {
		t.Fatalf("json=%q items=%#v err=%v", jsonOutput, items, err)
	}
	registeredInfo, err := os.Stat(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	listedInfo, err := os.Stat(items[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(registeredInfo, listedInfo) {
		t.Fatalf("listed path %q does not identify registered root %q", items[0].Path, workspaceRoot)
	}
	plain := executeRequestCommand(t, root, []string{"workspace", "list"})
	if !strings.Contains(plain, items[0].Path) || !strings.Contains(plain, "Registered workspaces loaded") {
		t.Fatalf("plain=%q", plain)
	}
}

func TestMCPServerListDefaultsToPlainAndSupportsRedactedJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	if _, err := executeRequestCommandError(root, []string{"mcp", "server", "add", "demo", "--transport", "http", "--url", "https://mcp.example.test", "--header", "Authorization=secret-value"}); err != nil {
		t.Fatal(err)
	}
	plain := executeRequestCommand(t, root, []string{"mcp", "server", "list"})
	if !strings.Contains(plain, "demo") || !strings.Contains(plain, "https://mcp.example.test") || strings.Contains(plain, "secret-value") {
		t.Fatalf("plain=%q", plain)
	}
	jsonOutput := executeRequestCommand(t, root, []string{"mcp", "server", "list", "--json"})
	if strings.Contains(jsonOutput, "secret-value") || !strings.Contains(jsonOutput, "redacted") {
		t.Fatalf("json=%q", jsonOutput)
	}
}

func TestTunnelListDefaultsToPlainAndSupportsJSON(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" || r.URL.Query().Get("workspace_id") != "ws_admin" {
			t.Fatalf("request=%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer admin-test" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","description":"First","workspace_ids":["ws_admin"]}]}`))
	}))
	defer server.Close()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tunnel.AdminKey = "admin-test"
	cfg.Tunnel.AdminWorkspaceID = "ws_admin"
	cfg.Tunnel.ControlPlaneBaseURL = server.URL
	if err := config.SaveAs(cfg, configformat.JSON); err != nil {
		t.Fatal(err)
	}
	plain := executeRequestCommand(t, root, []string{"tunnel", "list"})
	if !strings.Contains(plain, "Managed tunnels loaded") || !strings.Contains(plain, "tunnel_one") || strings.HasPrefix(strings.TrimSpace(plain), "[") {
		t.Fatalf("plain=%q", plain)
	}
	jsonOutput := executeRequestCommand(t, root, []string{"tunnel", "list", "--json"})
	var items []tunnel.Metadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &items); err != nil || len(items) != 1 || items[0].Name != "One" {
		t.Fatalf("json=%q items=%#v err=%v", jsonOutput, items, err)
	}
}

func TestWorkspaceShowAndAccessListDefaultToText(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	workspaceRoot := t.TempDir()
	registered := executeRequestCommand(t, root, []string{"workspace", "register", workspaceRoot})
	id := strings.TrimSpace(strings.Split(strings.Split(registered, "id:")[1], "\n")[0])
	showJSON := executeRequestCommand(t, root, []string{"workspace", "show", id, "--json"})
	var item workspace.Workspace
	if err := json.Unmarshal([]byte(strings.TrimSpace(showJSON)), &item); err != nil || item.ID != id {
		t.Fatalf("show json=%q item=%#v err=%v", showJSON, item, err)
	}
	show := executeRequestCommand(t, root, []string{"workspace", "show", id})
	if !strings.Contains(show, "Workspace details") || !strings.Contains(show, item.Path) || strings.HasPrefix(strings.TrimSpace(show), "{") {
		t.Fatalf("show=%q canonical=%q requested=%q", show, item.Path, workspaceRoot)
	}
	access := executeRequestCommand(t, root, []string{"workspace", "access", "list", id})
	if !strings.Contains(access, "Allowed directories loaded") || !strings.Contains(access, "allow dirs: none") || strings.HasPrefix(strings.TrimSpace(access), "[") {
		t.Fatalf("access=%q", access)
	}
	accessJSON := executeRequestCommand(t, root, []string{"workspace", "access", "list", id, "--json"})
	var allowDirs []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(accessJSON)), &allowDirs); err != nil || len(allowDirs) != 0 {
		t.Fatalf("access json=%q allowDirs=%#v err=%v", accessJSON, allowDirs, err)
	}
}
