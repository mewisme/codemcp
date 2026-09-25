package admin

import (
	"fmt"
	"sort"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestPublicAdminOperationsHaveCanonicalIDs(t *testing.T) {
	expected := []capability.AdminBinding{
		{Method: "GET", Path: "/api/health"},
		{Method: "GET", Path: "/api/network/interfaces"},
		{Method: "GET", Path: "/api/config"},
		{Method: "PUT", Path: "/api/config"},
		{Method: "GET", Path: "/api/instructions/global"},
		{Method: "PUT", Path: "/api/instructions/global"},
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
