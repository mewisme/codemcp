package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGenerateRuntimeKeyCreatesScopedServiceAccountKey(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organization/projects":
			writeRuntimeKeyJSON(t, w, map[string]any{"data": []map[string]any{{"id": "proj_default", "name": "Default project", "status": "active"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts":
			writeRuntimeKeyJSON(t, w, map[string]any{"data": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] != runtimeServiceAccountName || body["create_service_account_only"] != true {
				t.Fatalf("service account body=%#v", body)
			}
			writeRuntimeKeyJSON(t, w, map[string]any{"id": "svc_runtime", "name": runtimeServiceAccountName})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organization/projects/proj_default/service_accounts/svc_runtime/api_keys":
			var body struct {
				Name   string   `json:"name"`
				Scopes []string `json:"scopes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != runtimeServiceAccountName || !reflect.DeepEqual(body.Scopes, runtimeKeyScopes) {
				t.Fatalf("api key body=%#v", body)
			}
			writeRuntimeKeyJSON(t, w, map[string]any{"id": "key_runtime", "value": "sk-runtime-generated"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := GenerateRuntimeKey(context.Background(), Config{Admin: AdminConfig{Key: "admin-secret"}, ControlPlaneBaseURL: server.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID != "proj_default" || result.ServiceAccountID != "svc_runtime" || result.KeyID != "key_runtime" || result.Value != "sk-runtime-generated" {
		t.Fatalf("result=%#v", result)
	}
	want := []string{
		"GET /v1/organization/projects",
		"GET /v1/organization/projects/proj_default/service_accounts",
		"POST /v1/organization/projects/proj_default/service_accounts",
		"POST /v1/organization/projects/proj_default/service_accounts/svc_runtime/api_keys",
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want=%#v", requests, want)
	}
}

func TestGenerateRuntimeKeyReusesExistingServiceAccount(t *testing.T) {
	createdServiceAccount := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organization/projects/proj_one/service_accounts":
			writeRuntimeKeyJSON(t, w, map[string]any{"data": []map[string]any{{"id": "svc_existing", "name": runtimeServiceAccountName}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organization/projects/proj_one/service_accounts":
			createdServiceAccount = true
			http.Error(w, "unexpected", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organization/projects/proj_one/service_accounts/svc_existing/api_keys":
			writeRuntimeKeyJSON(t, w, map[string]any{"id": "key_runtime", "value": "sk-runtime-generated"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := GenerateRuntimeKey(context.Background(), Config{Admin: AdminConfig{Key: "admin-secret"}, ControlPlaneBaseURL: server.URL}, "proj_one")
	if err != nil {
		t.Fatal(err)
	}
	if createdServiceAccount || result.ServiceAccountID != "svc_existing" {
		t.Fatalf("created=%t result=%#v", createdServiceAccount, result)
	}
}

func TestSelectRuntimeKeyProjectRequiresExplicitChoiceWhenAmbiguous(t *testing.T) {
	_, err := selectRuntimeKeyProject([]AdminProject{{ID: "proj_one", Name: "One"}, {ID: "proj_two", Name: "Two"}})
	if err == nil {
		t.Fatal("expected ambiguous project error")
	}
	project, err := selectRuntimeKeyProject([]AdminProject{{ID: "proj_one", Name: "One"}, {ID: "proj_default", Name: "Default project"}})
	if err != nil || project != "proj_default" {
		t.Fatalf("project=%q err=%v", project, err)
	}
}

func writeRuntimeKeyJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
