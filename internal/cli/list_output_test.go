package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
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
	if !strings.Contains(plain, items[0].Path) || !strings.Contains(plain, "Registered workspaces · 1") {
		t.Fatalf("plain=%q", plain)
	}
}

func TestWorkspaceListAndShowReportTrackedLocalCMState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	workspaceRoot := t.TempDir()
	git := exec.Command("git", "init", "-q")
	git.Dir = workspaceRoot
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	_ = executeRequestCommand(t, root, []string{"workspace", "register", workspaceRoot})
	jsonOutput := executeRequestCommand(t, root, []string{"workspace", "list", "--json"})
	var items []workspace.Workspace
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &items); err != nil || len(items) != 1 {
		t.Fatalf("json=%q items=%#v err=%v", jsonOutput, items, err)
	}
	id := items[0].ID
	tracked := filepath.Join(workspaceRoot, workspace.LocalDirName, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked"), 0600); err != nil {
		t.Fatal(err)
	}
	git = exec.Command("git", "add", "-f", ".cm/tracked.txt")
	git.Dir = workspaceRoot
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	for name, output := range map[string]string{
		"list": executeRequestCommand(t, root, []string{"workspace", "list"}),
		"show": executeRequestCommand(t, root, []string{"workspace", "show", id}),
	} {
		if !strings.Contains(output, "tracked .cm") || !strings.Contains(output, workspace.GitTrackedGuidance) {
			t.Fatalf("%s output missing tracked-state guidance: %q", name, output)
		}
	}
}

func TestUpstreamServerListDefaultsToPlainAndSupportsRedactedJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	if _, err := executeRequestCommandError(root, []string{"upstream", "server", "add", "demo", "--transport", "http", "--url", "https://mcp.example.test", "--header", "Authorization=secret-value"}); err != nil {
		t.Fatal(err)
	}
	plain := executeRequestCommand(t, root, []string{"upstream", "server", "list"})
	if !strings.Contains(plain, "demo") || !strings.Contains(plain, "https://mcp.example.test") || strings.Contains(plain, "secret-value") {
		t.Fatalf("plain=%q", plain)
	}
	jsonOutput := executeRequestCommand(t, root, []string{"upstream", "server", "list", "--json"})
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
	cfg.Tunnel.Admin.Key = "admin-test"
	cfg.Tunnel.Admin.WorkspaceID = "ws_admin"
	cfg.Tunnel.ControlPlaneBaseURL = server.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	plain := executeRequestCommand(t, root, []string{"tunnel", "list"})
	if !strings.Contains(plain, "Loaded managed tunnels · 1") || !strings.Contains(plain, "tunnel_one") || strings.HasPrefix(strings.TrimSpace(plain), "[") {
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
	_ = executeRequestCommand(t, root, []string{"workspace", "register", workspaceRoot})
	listJSON := executeRequestCommand(t, root, []string{"workspace", "list", "--json"})
	var registered []workspace.Workspace
	if err := json.Unmarshal([]byte(strings.TrimSpace(listJSON)), &registered); err != nil || len(registered) != 1 {
		t.Fatalf("list json=%q workspaces=%#v err=%v", listJSON, registered, err)
	}
	id := registered[0].ID
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
	if !strings.Contains(access, "Allowed directories · 0") || !strings.Contains(access, "allow dirs") || !strings.Contains(access, "none") || strings.HasPrefix(strings.TrimSpace(access), "[") {
		t.Fatalf("access=%q", access)
	}
	accessJSON := executeRequestCommand(t, root, []string{"workspace", "access", "list", id, "--json"})
	var allowDirs []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(accessJSON)), &allowDirs); err != nil || len(allowDirs) != 0 {
		t.Fatalf("access json=%q allowDirs=%#v err=%v", accessJSON, allowDirs, err)
	}
}

func TestWorkspaceReadRenderersUseRailHierarchy(t *testing.T) {
	var output bytes.Buffer
	presenter := presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true})
	renderStandalonePresentation(presenter, "Workspace container details", func() {
		renderWorkspaceContainer(presenter, application.WorkspaceContainerView{
			ID: "wsc_demo", Name: "Demo", WorkspaceIDs: []string{"ws_one", "ws_two"},
		})
	})
	text := output.String()
	for _, expected := range []string{"┌  Workspace container details", "│  └─ wsc_demo", "│  │  name — Demo", "│  │  workspaces — ws_one, ws_two", "└  Done"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("workspace container detail missing %q: %q", expected, text)
		}
	}

	output.Reset()
	presenter = presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true})
	renderStandalonePresentation(presenter, "Allowed directories", func() {
		renderWorkspaceAccess(presenter, "ws_demo", []string{"/data/one", "/data/two"})
	})
	access := output.String()
	for _, expected := range []string{"┌  Allowed directories", "│  ▸ Allowed directories · 2", "│  └─ ws_demo", "│  │  allow dirs — /data/one, /data/two", "└  Done"} {
		if !strings.Contains(access, expected) {
			t.Fatalf("workspace access missing %q: %q", expected, access)
		}
	}
}
