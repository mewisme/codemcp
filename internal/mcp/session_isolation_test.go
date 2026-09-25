package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

type isolationHTTPFixture struct {
	runtime *HTTPRuntime
	first   workspace.Workspace
	second  workspace.Workspace
}

func newIsolationHTTPFixture(t *testing.T) isolationHTTPFixture {
	t.Helper()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoints"))
	registry := tools.NewRegistry()
	tools.RegisterCore(registry, manager, checkpoints)
	tools.RegisterWorkspaceContainerTools(registry, manager)
	toolRuntime := &tools.Runtime{Registry: registry, Workspaces: manager, Checkpoints: checkpoints, SessionAccess: tools.NewSessionWorkspaceAccessManager()}
	return isolationHTTPFixture{runtime: NewHTTPRuntimeWithTools(toolRuntime), first: first, second: second}
}

func callWorkspaceToolHTTP(t *testing.T, runtime *HTTPRuntime, sessionID, name string, args map[string]any, id int) tools.Result {
	t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req := modernRequest("tools/call", string(body))
	req.Header.Set(NameHeader, name)
	req.Header.Set(SessionIDHeader, sessionID)
	res := httptestResponse(runtime, req)
	if res.Code != 200 {
		t.Fatalf("%s status = %d: %s", name, res.Code, res.Body.String())
	}
	response := decodeResponse(t, res)
	if response.Error != nil {
		t.Fatalf("%s protocol error = %#v", name, response.Error)
	}
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result tools.Result
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func httptestResponse(runtime *HTTPRuntime, req *http.Request) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	return res
}

func TestHTTPModernTransportMetadataCannotChangeWorkspaceIsolation(t *testing.T) {
	fixture := newIsolationHTTPFixture(t)
	firstPath := filepath.Join(fixture.first.Path, "session-a.txt")
	secondPath := filepath.Join(fixture.second.Path, "session-b.txt")
	if result := callWorkspaceToolHTTP(t, fixture.runtime, "session-a", "write_file", map[string]any{"workspace_id": fixture.first.ID, "path": "session-a.txt", "content": "a"}, 1); result.IsError {
		t.Fatalf("session A first workspace failed: %#v", result)
	}
	if result := callWorkspaceToolHTTP(t, fixture.runtime, "session-b", "write_file", map[string]any{"workspace_id": fixture.second.ID, "path": "session-b.txt", "content": "b"}, 2); result.IsError {
		t.Fatalf("session B second workspace failed: %#v", result)
	}
	if data, err := os.ReadFile(firstPath); err != nil || string(data) != "a" {
		t.Fatalf("first workspace content = %q err=%v", data, err)
	}
	if data, err := os.ReadFile(secondPath); err != nil || string(data) != "b" {
		t.Fatalf("second workspace content = %q err=%v", data, err)
	}
	result := callWorkspaceToolHTTP(t, fixture.runtime, "session-a", "write_file", map[string]any{"workspace_id": fixture.second.ID, "path": "session-a-second.txt", "content": "a2"}, 3)
	if result.IsError {
		t.Fatalf("session A second workspace failed: %#v", result)
	}
	if data, err := os.ReadFile(filepath.Join(fixture.second.Path, "session-a-second.txt")); err != nil || string(data) != "a2" {
		t.Fatalf("second workspace content = %q err=%v", data, err)
	}
	escape := filepath.Join(fixture.second.Path, "escape.txt")
	result = callWorkspaceToolHTTP(t, fixture.runtime, "session-a", "write_file", map[string]any{"workspace_id": fixture.first.ID, "path": escape, "content": "wrong"}, 4)
	if !result.IsError {
		t.Fatalf("workspace path escape was allowed: %#v", result)
	}
	if got := fixture.runtime.Server.Tools.SessionAccess.Count(); got != 0 {
		t.Fatalf("modern session metadata created legacy workspace access state: %d", got)
	}
}

func TestHTTPModernTransportMetadataDoesNotPartitionWorkspaceAccess(t *testing.T) {
	fixture := newIsolationHTTPFixture(t)
	for i, sessionID := range []string{"session-a", "session-b"} {
		name := fmt.Sprintf("shared-%d.txt", i)
		result := callWorkspaceToolHTTP(t, fixture.runtime, sessionID, "write_file", map[string]any{"workspace_id": fixture.first.ID, "path": name, "content": sessionID}, 10+i)
		if result.IsError {
			t.Fatalf("%s sharing workspace failed: %#v", sessionID, result)
		}
		data, err := os.ReadFile(filepath.Join(fixture.first.Path, name))
		if err != nil || string(data) != sessionID {
			t.Fatalf("%s content = %q err=%v", sessionID, data, err)
		}
	}
	if got := fixture.runtime.Server.Tools.SessionAccess.Count(); got != 0 {
		t.Fatalf("modern session metadata created legacy workspace access state: %d", got)
	}
}

func TestHTTPModernProjectContextUsesExplicitWorkspaceNotTransportSession(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	fixture := newIsolationHTTPFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.first.Path, "AGENTS.md"), []byte("first workspace"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.second.Path, "AGENTS.md"), []byte("second workspace"), 0644); err != nil {
		t.Fatal(err)
	}
	first := callWorkspaceToolHTTP(t, fixture.runtime, "project-session", "project_context", map[string]any{"workspace_id": fixture.first.ID, "include_git": false}, 20)
	if first.IsError || len(first.Content) == 0 || !strings.Contains(first.Content[0].Text, "first workspace") {
		t.Fatalf("first project_context failed: %#v", first)
	}
	second := callWorkspaceToolHTTP(t, fixture.runtime, "project-session", "project_context", map[string]any{"workspace_id": fixture.second.ID, "include_git": false}, 21)
	if second.IsError || len(second.Content) == 0 || !strings.Contains(second.Content[0].Text, "second workspace") || strings.Contains(second.Content[0].Text, "first workspace") {
		t.Fatalf("second project_context was not isolated: %#v", second)
	}
	if got := fixture.runtime.Server.Tools.SessionAccess.Count(); got != 0 {
		t.Fatalf("project_context created legacy session access state: %d", got)
	}
}

func TestHTTPModernContainerContextDoesNotCreateTransportSessionAuthority(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	fixture := newIsolationHTTPFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.first.Path, "AGENTS.md"), []byte("container first sentinel"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.second.Path, "AGENTS.md"), []byte("container second sentinel"), 0644); err != nil {
		t.Fatal(err)
	}
	container, err := fixture.runtime.Server.Tools.Workspaces.CreateContainer("Product")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.Server.Tools.Workspaces.AddWorkspacesToContainer(container.ID, []string{fixture.first.ID, fixture.second.ID}); err != nil {
		t.Fatal(err)
	}
	sessionID := "container-session"
	containerResult := callWorkspaceToolHTTP(t, fixture.runtime, sessionID, "workspace_container_context", map[string]any{"container_id": container.ID}, 30)
	if containerResult.IsError || len(containerResult.Content) == 0 {
		t.Fatalf("container context failed: %#v", containerResult)
	}
	text := containerResult.Content[0].Text
	for _, expected := range []string{container.ID, fixture.first.ID, fixture.second.ID, "orchestration scope"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("container context missing %q: %s", expected, text)
		}
	}
	for _, forbidden := range []string{"container first sentinel", "container second sentinel"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("container context merged member project content %q: %s", forbidden, text)
		}
	}
	if _, ok := fixture.runtime.Server.Tools.SessionAccess.Lookup(sessionID); ok {
		t.Fatal("container context granted fake workspace session access")
	}

	first := callWorkspaceToolHTTP(t, fixture.runtime, sessionID, "project_context", map[string]any{"workspace_id": fixture.first.ID, "include_git": false}, 31)
	if first.IsError || len(first.Content) == 0 || !strings.Contains(first.Content[0].Text, "container first sentinel") || strings.Contains(first.Content[0].Text, "container second sentinel") {
		t.Fatalf("first member context was not isolated: %#v", first)
	}
	if got := fixture.runtime.Server.Tools.SessionAccess.Count(); got != 0 {
		t.Fatalf("first member created legacy session access state: %d", got)
	}

	second := callWorkspaceToolHTTP(t, fixture.runtime, sessionID, "project_context", map[string]any{"workspace_id": fixture.second.ID, "include_git": false}, 32)
	if second.IsError || len(second.Content) == 0 || !strings.Contains(second.Content[0].Text, "container second sentinel") || strings.Contains(second.Content[0].Text, "container first sentinel") {
		t.Fatalf("second member context was not isolated: %#v", second)
	}
	if got := fixture.runtime.Server.Tools.SessionAccess.Count(); got != 0 {
		t.Fatalf("member access created legacy session state: %d", got)
	}
}
