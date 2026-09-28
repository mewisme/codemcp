package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestPublicAdminOperationsHaveCanonicalIDs(t *testing.T) {
	expected := []capability.AdminBinding{
		{Method: "GET", Path: "/api/health"},
		{Method: "GET", Path: "/api/status"},
		{Method: "GET", Path: "/api/doctor"},
		{Method: "GET", Path: "/api/about"},
		{Method: "POST", Path: "/api/runtime/up"},
		{Method: "POST", Path: "/api/runtime/down"},
		{Method: "POST", Path: "/api/runtime/restart"},
		{Method: "GET", Path: "/api/logs"},
		{Method: "GET", Path: "/api/logs/info"},
		{Method: "DELETE", Path: "/api/logs"},
		{Method: "POST", Path: "/api/install"},
		{Method: "GET", Path: "/api/update"},
		{Method: "POST", Path: "/api/update"},
		{Method: "GET", Path: "/api/telemetry"},
		{Method: "GET", Path: "/api/telemetry/show"},
		{Method: "POST", Path: "/api/telemetry/enable"},
		{Method: "POST", Path: "/api/telemetry/disable"},
		{Method: "GET", Path: "/api/network/interfaces"},
		{Method: "GET", Path: "/api/config"},
		{Method: "PUT", Path: "/api/config"},
		{Method: "GET", Path: "/api/config/path"},
		{Method: "GET", Path: "/api/config/verify"},
		{Method: "GET", Path: "/api/instructions/global"},
		{Method: "PUT", Path: "/api/instructions/global"},
		{Method: "GET", Path: "/api/prompts"},
		{Method: "POST", Path: "/api/prompts"},
		{Method: "GET", Path: "/api/prompts/{name}"},
		{Method: "PUT", Path: "/api/prompts/{name}"},
		{Method: "DELETE", Path: "/api/prompts/{name}"},
		{Method: "GET", Path: "/api/workspaces"},
		{Method: "POST", Path: "/api/workspaces"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}"},
		{Method: "DELETE", Path: "/api/workspaces/{workspace_id}"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/context"},
		{Method: "POST", Path: "/api/workspaces/{workspace_id}/relocate"},
		{Method: "POST", Path: "/api/workspaces/{workspace_id}/purge"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/containers"},
		{Method: "POST", Path: "/api/workspaces/{workspace_id}/containers"},
		{Method: "DELETE", Path: "/api/workspaces/{workspace_id}/containers"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/stream"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/{execution_id}"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/{execution_id}/stream"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/processes"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/processes/{process_id}"},
		{Method: "DELETE", Path: "/api/workspaces/{workspace_id}/processes/{process_id}"},
		{Method: "GET", Path: "/api/workspace-containers"},
		{Method: "POST", Path: "/api/workspace-containers"},
		{Method: "GET", Path: "/api/workspace-containers/{container_id}"},
		{Method: "PATCH", Path: "/api/workspace-containers/{container_id}"},
		{Method: "DELETE", Path: "/api/workspace-containers/{container_id}"},
		{Method: "GET", Path: "/api/workspace-containers/{container_id}/workspaces"},
		{Method: "POST", Path: "/api/workspace-containers/{container_id}/workspaces"},
		{Method: "DELETE", Path: "/api/workspace-containers/{container_id}/workspaces"},
		{Method: "GET", Path: "/api/tools"},
		{Method: "GET", Path: "/api/requests"},
		{Method: "GET", Path: "/api/requests/stream"},
		{Method: "GET", Path: "/api/requests/{request_id}"},
		{Method: "POST", Path: "/api/requests/{request_id}/approve"},
		{Method: "POST", Path: "/api/requests/{request_id}/deny"},
		{Method: "GET", Path: "/api/requests/grants"},
		{Method: "POST", Path: "/api/requests/grants/{request_id}/revoke"},
		{Method: "GET", Path: "/api/completions"},
		{Method: "GET", Path: "/api/completions/current"},
		{Method: "GET", Path: "/api/completions/stream"},
		{Method: "GET", Path: "/api/completions/view/{completion_id}"},
		{Method: "GET", Path: "/api/notifications"},
		{Method: "GET", Path: "/api/integrations/typesafe"},
		{Method: "GET", Path: "/api/integrations/typesafe/doctor"},
		{Method: "POST", Path: "/api/integrations/typesafe/probe"},
		{Method: "POST", Path: "/api/integrations/typesafe/enable"},
		{Method: "POST", Path: "/api/integrations/typesafe/disable"},
		{Method: "GET", Path: "/api/integrations/rtk"},
		{Method: "POST", Path: "/api/integrations/rtk/enable"},
		{Method: "POST", Path: "/api/integrations/rtk/disable"},
		{Method: "POST", Path: "/api/integrations/rtk/probe"},
		{Method: "POST", Path: "/api/integrations/rtk/install"},
		{Method: "GET", Path: "/api/integrations/rtk/global"},
		{Method: "GET", Path: "/api/integrations/codegraph"},
		{Method: "POST", Path: "/api/integrations/codegraph/probe"},
		{Method: "POST", Path: "/api/integrations/codegraph/install"},
		{Method: "GET", Path: "/api/integrations/codegraph/global"},
		{Method: "GET", Path: "/api/integrations/cf"},
		{Method: "POST", Path: "/api/integrations/cf/probe"},
		{Method: "POST", Path: "/api/integrations/cf/install"},
		{Method: "POST", Path: "/api/integrations/cf/update"},
		{Method: "DELETE", Path: "/api/integrations/cf"},
		{Method: "GET", Path: "/api/workspaces/{workspace_id}/integrations/codegraph"},
		{Method: "POST", Path: "/api/workspaces/{workspace_id}/integrations/codegraph/init"},
		{Method: "POST", Path: "/api/workspaces/{workspace_id}/integrations/codegraph/sync"},
		{Method: "GET", Path: "/api/upstream"},
		{Method: "POST", Path: "/api/upstream"},
		{Method: "GET", Path: "/api/upstream/{server_id}"},
		{Method: "PUT", Path: "/api/upstream/{server_id}"},
		{Method: "DELETE", Path: "/api/upstream/{server_id}"},
		{Method: "GET", Path: "/api/upstream/{server_id}/status"},
		{Method: "GET", Path: "/api/upstream/{server_id}/tools"},
		{Method: "GET", Path: "/api/upstream/{server_id}/auth/status"},
		{Method: "POST", Path: "/api/upstream/{server_id}/auth/login"},
		{Method: "DELETE", Path: "/api/upstream/{server_id}/auth/logout"},
		{Method: "GET", Path: "/api/tunnel/config"},
		{Method: "GET", Path: "/api/tunnel"},
		{Method: "POST", Path: "/api/tunnel"},
		{Method: "DELETE", Path: "/api/tunnel"},
		{Method: "PUT", Path: "/api/tunnel"},
		{Method: "GET", Path: "/api/tunnel/admin/key"},
		{Method: "PUT", Path: "/api/tunnel/admin/key"},
		{Method: "POST", Path: "/api/tunnel/admin/key"},
		{Method: "DELETE", Path: "/api/tunnel/admin/key"},
		{Method: "GET", Path: "/api/tunnel/managed"},
		{Method: "POST", Path: "/api/tunnel/managed"},
		{Method: "POST", Path: "/api/tunnel/managed/use"},
		{Method: "GET", Path: "/api/tunnel/managed/{tunnel_id}"},
		{Method: "PUT", Path: "/api/tunnel/managed/{tunnel_id}"},
		{Method: "DELETE", Path: "/api/tunnel/managed/{tunnel_id}"},
		{Method: "GET", Path: "/api/activity/stream"},
		{Method: "GET", Path: "/api/activity/{call_id}"},
		{Method: "GET", Path: "/oauth/callback/{server_id}"},
	}

	actual := map[string]capability.ID{}
	for _, spec := range capability.All() {
		for _, binding := range spec.Admin {
			key := adminBindingKey(binding)
			if previous, exists := actual[key]; exists {
				t.Fatalf("Admin operation %s is owned by both %s and %s", key, previous, spec.ID)
			}
			actual[key] = spec.ID
		}
	}
	want := map[string]bool{}
	for _, binding := range expected {
		key := adminBindingKey(binding)
		want[key] = true
		id, ok := capability.ForAdmin(binding.Method, binding.Path)
		if !ok {
			t.Errorf("public Admin operation has no canonical ID: %s", key)
			continue
		}
		if _, exists := actual[key]; !exists {
			t.Errorf("public Admin operation %s resolved to %s but is absent from registry inventory", key, id)
		}
	}
	var stale []string
	for key, id := range actual {
		if !want[key] {
			stale = append(stale, fmt.Sprintf("%s (%s)", key, id))
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("registry contains Admin operations not present in public route inventory:\n  %v", stale)
	}
}

func adminBindingKey(binding capability.AdminBinding) string {
	return binding.Method + " " + binding.Path
}

func TestRestoredAdminRoutesAreRegistered(t *testing.T) {
	handler := New(API{})
	paths := []string{
		"/api/status", "/api/doctor", "/api/about",
		"/api/runtime/up", "/api/runtime/down", "/api/runtime/restart",
		"/api/logs", "/api/logs/info", "/api/install", "/api/update",
		"/api/telemetry", "/api/telemetry/show", "/api/telemetry/enable", "/api/telemetry/disable",
		"/api/config/path", "/api/config/verify",
		"/api/integrations/rtk", "/api/integrations/rtk/enable", "/api/integrations/rtk/disable", "/api/integrations/rtk/probe", "/api/integrations/rtk/install", "/api/integrations/rtk/global",
		"/api/integrations/codegraph", "/api/integrations/codegraph/probe", "/api/integrations/codegraph/install", "/api/integrations/codegraph/global",
		"/api/integrations/cf", "/api/integrations/cf/probe", "/api/integrations/cf/install", "/api/integrations/cf/update",
		"/api/integrations/typesafe/enable", "/api/integrations/typesafe/disable",
		"/api/workspaces/ws_contract/integrations/codegraph", "/api/workspaces/ws_contract/integrations/codegraph/init", "/api/workspaces/ws_contract/integrations/codegraph/sync",
	}
	for _, path := range paths {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, path, nil))
		if recorder.Code == http.StatusNotFound {
			t.Errorf("restored Admin route is not registered: %s", path)
		}
	}
}
