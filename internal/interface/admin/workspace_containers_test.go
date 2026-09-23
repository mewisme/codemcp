package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestWorkspaceContainerAPICRUDAndMembership(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Workspaces: manager})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspace-containers", strings.NewReader(`{"name":"Backend"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var container workspace.WorkspaceContainer
	if err := json.Unmarshal(recorder.Body.Bytes(), &container); err != nil {
		t.Fatal(err)
	}
	if container.ID == "" || container.Name != "Backend" {
		t.Fatalf("container=%#v", container)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspace-containers/"+container.ID+"/workspaces", strings.NewReader(`{"workspace_ids":["`+first.ID+`","`+second.ID+`"]}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), first.ID) || !strings.Contains(recorder.Body.String(), second.ID) {
		t.Fatalf("add status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+first.ID+"/containers", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), container.ID) {
		t.Fatalf("workspace containers status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+first.ID+"/containers", strings.NewReader(`{"container_ids":["`+container.ID+`"]}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("workspace remove status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/api/workspace-containers/"+container.ID, strings.NewReader(`{"name":"Services"}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"Services"`) {
		t.Fatalf("rename status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspace-containers/"+container.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkspaceContainerAPIIsImmediatelyVisibleToAgentTools(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	writer := workspace.NewManager(store)
	member, err := writer.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtimeManager := workspace.NewManager(store)
	if _, err := runtimeManager.List(); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	tools.RegisterWorkspaceContainerTools(registry, runtimeManager)
	runtime := &tools.Runtime{Registry: registry, Workspaces: runtimeManager, SessionAccess: tools.NewSessionWorkspaceAccessManager()}
	handler := New(API{Workspaces: writer, Tools: runtime})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspace-containers", strings.NewReader(`{"name":"Product"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var container workspace.WorkspaceContainer
	if err := json.Unmarshal(recorder.Body.Bytes(), &container); err != nil {
		t.Fatal(err)
	}
	assertAgentContainerStatus(t, runtime, container.ID, "Product", 0)

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspace-containers/"+container.ID+"/workspaces", strings.NewReader(`{"workspace_ids":["`+member.ID+`"]}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("membership status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertAgentContainerStatus(t, runtime, container.ID, "Product", 1)

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+member.ID+"/containers", strings.NewReader(`{"container_ids":["`+container.ID+`"]}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("workspace membership remove status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertAgentContainerStatus(t, runtime, container.ID, "Product", 0)

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+member.ID+"/containers", strings.NewReader(`{"container_ids":["`+container.ID+`"]}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("workspace membership add status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertAgentContainerStatus(t, runtime, container.ID, "Product", 1)

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/api/workspace-containers/"+container.ID, strings.NewReader(`{"name":"Renamed"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertAgentContainerStatus(t, runtime, container.ID, "Renamed", 1)

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspace-containers/"+container.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	result, err := runtime.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": container.ID})
	if err != nil || !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "workspace container not found") {
		t.Fatalf("agent status after delete = %#v err=%v", result, err)
	}
}

func TestWorkspaceAPIIsImmediatelyVisibleToAgentTools(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	writer := workspace.NewManager(store)
	runtimeManager := workspace.NewManager(store)
	if _, err := runtimeManager.List(); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	tools.RegisterWorkspaceTools(registry, runtimeManager)
	runtime := &tools.Runtime{Registry: registry, Workspaces: runtimeManager, SessionAccess: tools.NewSessionWorkspaceAccessManager()}
	handler := New(API{Workspaces: writer, Tools: runtime})

	root := t.TempDir()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"path":`+strconv.Quote(root)+`}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var item workspace.Workspace
	if err := json.Unmarshal(recorder.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(context.Background(), "workspace_status", map[string]any{"workspace_id": item.ID})
	if err != nil || result.IsError {
		t.Fatalf("agent status after register = %#v err=%v", result, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+item.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("unregister status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	result, err = runtime.Call(context.Background(), "workspace_status", map[string]any{"workspace_id": item.ID})
	if err != nil || !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "workspace not found") {
		t.Fatalf("agent status after unregister = %#v err=%v", result, err)
	}
}

func assertAgentContainerStatus(t *testing.T, runtime *tools.Runtime, containerID, name string, workspaceCount int) {
	t.Helper()
	result, err := runtime.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": containerID})
	if err != nil || result.IsError {
		t.Fatalf("agent status = %#v err=%v", result, err)
	}
	value := result.StructuredContent.(tools.WorkspaceContainerStatusResult)
	if value.Name != name || value.WorkspaceCount != workspaceCount {
		t.Fatalf("agent status = %#v", value)
	}
}
