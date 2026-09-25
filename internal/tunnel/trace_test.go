package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestTunnelAdminTraceDoesNotLeakAuthorizationMaterial(t *testing.T) {
	const adminKey = "admin-trace-secret"
	const runtimeKey = "runtime-trace-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels" && r.URL.Query().Get("workspace_id") == "ws_admin":
			if r.Header.Get("Authorization") != "Bearer "+adminKey {
				t.Fatalf("admin authorization=%q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","description":"First"},{"id":"tunnel_two","name":"Two","description":"Second"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels/tunnel_runtime":
			if r.Header.Get("Authorization") != "Bearer "+runtimeKey {
				t.Fatalf("runtime authorization=%q", r.Header.Get("Authorization"))
			}
			w.Header().Set("x-request-id", "req_runtime")
			_, _ = w.Write([]byte(`{"id":"tunnel_runtime","name":"Runtime","description":"Runtime tunnel","workspace_ids":["ws_admin"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	access, count, err := VerifyAdminKey(ctx, Config{Admin: AdminConfig{Key: adminKey, WorkspaceID: "ws_admin"}, ControlPlaneBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !access.Read || !access.Manage || count != 2 {
		t.Fatalf("access=%#v count=%d", access, count)
	}
	metadata, err := FetchMetadata(ctx, Config{ID: "tunnel_runtime", APIKey: runtimeKey, ControlPlaneBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ID != "tunnel_runtime" {
		t.Fatalf("metadata=%#v", metadata)
	}
	for _, name := range []string{"tunnel.admin.verify.started", "tunnel.admin.list.started", "tunnel.admin.list.completed", "tunnel.admin.verify.completed", "tunnel.metadata.fetch.started", "tunnel.metadata.fetch.completed"} {
		if !tunnelTraceContains(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	if !tunnelTraceFieldEquals(events, "tunnel.admin.list.started", "scope_type", "workspace") || !tunnelTraceFieldEquals(events, "tunnel.admin.list.started", "scope_id", "ws_admin") {
		t.Fatalf("admin list scope trace=%#v", events)
	}
	if !tunnelTraceHasField(events, "http.request.completed", "status") || !tunnelTraceHasField(events, "http.request.completed", "bytes_read") || !tunnelTraceHasField(events, "http.request.completed", "duration_ms") {
		t.Fatalf("managed tunnel HTTP trace is missing response facts: %#v", events)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{adminKey, runtimeKey} {
		if strings.Contains(text, secret) {
			t.Fatalf("tunnel trace leaked authorization material %q: %s", secret, text)
		}
	}
}

func TestGenerateRuntimeKeyTraceIncludesHTTPFactsWithoutGeneratedKey(t *testing.T) {
	const adminKey = "admin-runtime-key-trace-secret"
	const generatedKey = "generated-runtime-key-trace-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+adminKey {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organization/projects":
			writeRuntimeKeyJSON(t, w, map[string]any{"data": []map[string]any{{"id": "proj_default", "name": "Default project", "status": "active"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts":
			writeRuntimeKeyJSON(t, w, map[string]any{"data": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts":
			writeRuntimeKeyJSON(t, w, map[string]any{"id": "svc_runtime", "name": runtimeServiceAccountName})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts/svc_runtime/api_keys":
			writeRuntimeKeyJSON(t, w, map[string]any{"id": "key_runtime", "value": generatedKey})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	result, err := GenerateRuntimeKey(ctx, Config{Admin: AdminConfig{Key: adminKey}, ControlPlaneBaseURL: server.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != generatedKey || result.ProjectID != "proj_default" || result.KeyID != "key_runtime" {
		t.Fatalf("result=%#v", result)
	}
	for _, name := range []string{"tunnel.runtime-key.generate.started", "tunnel.admin.projects.list.completed", "tunnel.runtime-key.project-selected", "tunnel.runtime-key.service-account.completed", "tunnel.admin-platform.request.completed", "http.request.completed", "tunnel.runtime-key.generate.completed"} {
		if !tunnelTraceContains(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	if !tunnelTraceFieldEquals(events, "tunnel.runtime-key.generate.completed", "generated", true) || !tunnelTraceFieldEquals(events, "tunnel.runtime-key.generate.completed", "project_id", "proj_default") {
		t.Fatalf("runtime-key completion trace=%#v", events)
	}
	if !tunnelTraceHasField(events, "http.request.completed", "status") || !tunnelTraceHasField(events, "http.request.completed", "bytes_read") || !tunnelTraceHasField(events, "http.request.completed", "duration_ms") {
		t.Fatalf("HTTP trace is missing response facts: %#v", events)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{adminKey, generatedKey} {
		if strings.Contains(text, secret) {
			t.Fatalf("runtime-key trace leaked authorization material %q: %s", secret, text)
		}
	}
}

func tunnelTraceContains(events []tracepkg.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func tunnelTraceFieldEquals(events []tracepkg.Event, name, key string, want any) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		for _, field := range event.Fields {
			if field.Key == key && field.Value == want {
				return true
			}
		}
	}
	return false
}

func tunnelTraceHasField(events []tracepkg.Event, name, key string) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		for _, field := range event.Fields {
			if field.Key == key {
				return true
			}
		}
	}
	return false
}
