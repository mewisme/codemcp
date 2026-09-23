package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchMetadataWithRuntimeKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_test" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-runtime" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("x-request-id", "req_test")
		_, _ = w.Write([]byte(`{"id":"tunnel_test","name":"Mew WSL","description":"Local development tunnel","creator":"user_test","workspace_ids":["ws_openai"],"organization_ids":["org_test"],"tenant_ids":["tenant_test"]}`))
	}))
	defer server.Close()

	metadata, err := FetchMetadata(context.Background(), Config{ID: "tunnel_test", APIKey: "sk-runtime", ControlPlaneBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ID != "tunnel_test" || metadata.Name != "Mew WSL" || metadata.Description != "Local development tunnel" || metadata.Creator != "user_test" || metadata.RequestID != "req_test" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if len(metadata.WorkspaceIDs) != 1 || metadata.WorkspaceIDs[0] != "ws_openai" || len(metadata.OrganizationIDs) != 1 || metadata.OrganizationIDs[0] != "org_test" {
		t.Fatalf("metadata scopes = %#v", metadata)
	}
	if metadata.FetchedAt.IsZero() {
		t.Fatal("fetched_at is zero")
	}
}

func TestCreateWithAdminAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tunnels" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-admin" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "Mew Tunnel" || body["description"] != "Created from CodeMCP" {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("x-request-id", "req_create")
		_, _ = w.Write([]byte(`{"id":"tunnel_created","name":"Mew Tunnel","description":"Created from CodeMCP","workspace_ids":["ws_openai"],"organization_ids":["org_test"]}`))
	}))
	defer server.Close()

	metadata, err := CreateManaged(context.Background(), Config{AdminKey: "sk-admin", ControlPlaneBaseURL: server.URL}, CreateRequest{Name: "Mew Tunnel", Description: "Created from CodeMCP", WorkspaceIDs: []string{"ws_openai"}, OrganizationIDs: []string{"org_test"}})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ID != "tunnel_created" || metadata.Name != "Mew Tunnel" || metadata.RequestID != "req_create" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestCreateRequiresAdminKeyAndScope(t *testing.T) {
	if _, err := CreateManaged(context.Background(), Config{}, CreateRequest{Name: "n", Description: "d", WorkspaceIDs: []string{"ws"}}); err == nil {
		t.Fatal("expected missing admin key error")
	}
	if _, err := CreateManaged(context.Background(), Config{AdminKey: "sk-admin"}, CreateRequest{Name: "n", Description: "d"}); err == nil {
		t.Fatal("expected missing scope error")
	}
}

func TestRefreshMetadataCachesSuccessfulLookup(t *testing.T) {
	client := NewConfigured(Config{ID: "tunnel_test", APIKey: "sk-runtime"}, nil)
	var calls atomic.Int32
	client.metadataFetch = func(context.Context, Config) (Metadata, error) {
		calls.Add(1)
		return Metadata{ID: "tunnel_test", Name: "Cached tunnel", FetchedAt: time.Now().UTC()}, nil
	}
	first, err := client.RefreshMetadata(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.RefreshMetadata(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || first.Name != "Cached tunnel" || second.Name != "Cached tunnel" || client.Status().Metadata == nil {
		t.Fatalf("calls=%d first=%#v second=%#v status=%#v", calls.Load(), first, second, client.Status())
	}
}

func TestVerifyAdminKeyUsesManagedListScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" || r.URL.Query().Get("workspace_id") != "ws_admin" {
			t.Fatalf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer sk-admin" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","description":"First"},{"id":"tunnel_two","name":"Two","description":"Second"}]}`))
	}))
	defer server.Close()

	cfg := Config{AdminKey: "sk-admin", AdminWorkspaceID: "ws_admin", ControlPlaneBaseURL: server.URL}
	access, count, err := VerifyAdminKey(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || !access.Read || !access.Manage || !AdminConfigured(cfg) {
		t.Fatalf("access=%#v count=%d configured=%t", access, count, AdminConfigured(cfg))
	}
}

func TestStatusRedactsAdminKeyAndExposesVerifiedScope(t *testing.T) {
	client := NewConfigured(Config{AdminKey: "sk-admin", AdminWorkspaceID: "ws_admin"}, nil)
	status := client.Status()
	if !status.AdminKeyConfigured || status.AdminScope == nil || status.AdminScope.WorkspaceID != "ws_admin" {
		t.Fatalf("status = %#v", status)
	}
}

func TestSeedMetadataPopulatesStatusWithoutFetch(t *testing.T) {
	client := NewConfigured(Config{ID: "tunnel_test", APIKey: "runtime-key"}, nil)
	client.metadataFetch = func(context.Context, Config) (Metadata, error) {
		t.Fatal("seeded metadata should not fetch")
		return Metadata{}, nil
	}
	metadata := Metadata{ID: "tunnel_test", Name: "Persisted tunnel", FetchedAt: time.Now().UTC().Add(-time.Hour)}
	if err := client.SeedMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	status := client.Status()
	if status.Metadata == nil || status.Metadata.Name != "Persisted tunnel" {
		t.Fatalf("status = %#v", status)
	}
}

func TestVerifyAdminKeyDetectsReadOnlyAccessWithoutMutation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-read" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tunnels":
			http.Error(w, `{"error":{"message":"missing manage permission"}}`, http.StatusForbidden)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tunnels/tunnel_000000"):
			http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	cfg := Config{AdminKey: "sk-read", AdminWorkspaceID: "ws_admin", ControlPlaneBaseURL: server.URL}
	access, count, err := VerifyAdminKey(context.Background(), cfg)
	if err != nil || !access.Read || access.Manage || count != 0 {
		t.Fatalf("access=%#v count=%d err=%v", access, count, err)
	}
}
