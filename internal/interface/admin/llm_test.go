package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
)

func TestLLMAdminRoutesConvergeWithCanonicalStateAndProtectCredentials(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previousRoot := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previousRoot) })
	restore := secretstore.UseMemoryForTesting()
	t.Cleanup(restore)

	service := application.NewLLMService(root)
	dispatcher := application.NewDispatcher()
	if err := application.BindLLMOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	handler := New(API{Operations: dispatcher})

	added := adminJSON[application.LLMProviderResult](t, handler, http.MethodPost, "/api/llm/providers", `{"id":"browser-fixture","config":{"name":"Browser Fixture","protocol":"openai","base_url":"http://127.0.0.1:65534/v1","model":"fixture-v1","auth_mode":"bearer","discovery":"none"}}`)
	if string(added.ID) != "browser-fixture" || added.Model != "fixture-v1" || added.Core {
		t.Fatalf("added provider=%#v", added)
	}

	selected := adminJSON[application.LLMProviderResult](t, handler, http.MethodPost, "/api/llm/providers/browser-fixture/select", "")
	if !selected.Selected {
		t.Fatalf("selected provider=%#v", selected)
	}

	updated := adminJSON[application.LLMProviderResult](t, handler, http.MethodPut, "/api/llm/providers/browser-fixture", `{"model":"fixture-v2"}`)
	if updated.Model != "fixture-v2" || !updated.Selected {
		t.Fatalf("updated provider=%#v", updated)
	}
	canonical, err := service.ProviderResult(t.Context(), "browser-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated, canonical) {
		t.Fatalf("Admin/browser state diverged from canonical LLMService:\nadmin=%#v\ncanonical=%#v", updated, canonical)
	}

	coreModel := adminJSON[application.LLMProviderResult](t, handler, http.MethodPut, "/api/llm/providers/ollama", `{"model":"ollama/auto"}`)
	if coreModel.Model != "ollama/auto" || !coreModel.Core {
		t.Fatalf("core model update=%#v", coreModel)
	}

	const secret = "sk-browser-admin-secret-must-not-leak"
	recorder := adminRequest(t, handler, http.MethodPut, "/api/llm/providers/browser-fixture/credential", `{"api_key":"`+secret+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("credential status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("credential response leaked raw key: %s", recorder.Body.String())
	}
	var credential application.LLMCredentialResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &credential); err != nil {
		t.Fatal(err)
	}
	if !credential.Configured || credential.Preview == "" || strings.Contains(credential.Preview, secret) {
		t.Fatalf("credential result=%#v", credential)
	}
	if leaked := findAdminSecretOnDisk(t, root, secret); leaked != "" {
		t.Fatalf("raw LLM credential persisted to %s", leaked)
	}

	status := adminJSON[application.LLMStatusResult](t, handler, http.MethodGet, "/api/llm/status", "")
	canonicalStatus, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status, canonicalStatus) {
		t.Fatalf("Admin status diverged from canonical state:\nadmin=%#v\ncanonical=%#v", status, canonicalStatus)
	}

	beforeCoreDelete, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	failedDelete := adminRequest(t, handler, http.MethodDelete, "/api/llm/providers/ollama", "")
	if failedDelete.Code == http.StatusOK {
		t.Fatalf("core provider deletion unexpectedly succeeded: %s", failedDelete.Body.String())
	}
	afterCoreDelete, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeCoreDelete, afterCoreDelete) {
		t.Fatal("failed core removal mutated canonical provider catalog")
	}

	adminJSON[application.LLMProviderResult](t, handler, http.MethodPost, "/api/llm/providers/ollama/select", "")
	removed := adminJSON[application.LLMProviderRemoveResult](t, handler, http.MethodDelete, "/api/llm/providers/browser-fixture", "")
	if !removed.Removed || string(removed.ProviderID) != "browser-fixture" {
		t.Fatalf("remove result=%#v", removed)
	}
	if _, err := service.Provider(t.Context(), "browser-fixture"); err == nil {
		t.Fatal("removed Browser provider still exists in canonical service")
	}
}

func TestLLMAdminHandlerDoesNotOwnLLMStoreOrSecretMutation(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(current), "llm.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{
		"internal/llm",
		"internal/secretstore",
		"AddCustomProvider(",
		"ConfigureCustomProvider(",
		"RemoveProvider(",
		"SetCredential(",
		"ClearCredential(",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Admin LLM handler bypasses canonical operation owner via %q", forbidden)
		}
	}
	for _, required := range []string{"api.dispatch", "capability.LLMProviderAdd", "capability.LLMProviderCredentialSet"} {
		if !strings.Contains(text, required) {
			t.Fatalf("Admin LLM handler missing canonical dispatch marker %q", required)
		}
	}
}

func TestLLMAdminModelQueryUsesCanonicalFilteringSortingAndPagination(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previousRoot := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previousRoot) })
	restore := secretstore.UseMemoryForTesting()
	t.Cleanup(restore)

	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"acme/a","name":"A","context_length":64000,"pricing":{"prompt":"0.000002","completion":"0.000004"}},
			{"id":"acme/b","name":"B","context_length":128000,"pricing":{"prompt":"0.000001","completion":"0.000003"}},
			{"id":"other/c","name":"C","context_length":256000,"pricing":{"prompt":"0","completion":"0"}}
		]}`))
	}))
	defer models.Close()

	service := application.NewLLMService(root)
	if _, err := service.AddCustomProvider(t.Context(), "admin-query", application.NewCustomLLMProviderConfig("Admin Query", "openai", models.URL+"/v1", "acme/b", "none", "openai-models")); err != nil {
		t.Fatal(err)
	}
	dispatcher := application.NewDispatcher()
	if err := application.BindLLMOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	handler := New(API{Operations: dispatcher})

	page := adminJSON[application.LLMModelPage](t, handler, http.MethodGet,
		"/api/llm/providers/admin-query/models?author=acme&max_prompt_price=0.000002&sort=context:desc&range=1:1", "")
	if page.ProviderID != "admin-query" || page.TotalCatalog != 3 || page.Matched != 2 || page.Offset != 0 || page.Limit != 1 || page.Returned != 1 || !page.HasMore || len(page.Models) != 1 || page.Models[0].ID != "acme/b" {
		t.Fatalf("Admin model page=%#v", page)
	}
	count := adminJSON[application.LLMModelPage](t, handler, http.MethodGet, "/api/llm/providers/admin-query/models?author=acme&count=true", "")
	if count.Matched != 2 || count.Returned != 0 || len(count.Models) != 0 {
		t.Fatalf("Admin count page=%#v", count)
	}
	bad := adminRequest(t, handler, http.MethodGet, "/api/llm/providers/admin-query/models?all=true&limit=1", "")
	if bad.Code == http.StatusOK {
		t.Fatalf("invalid Admin pagination was accepted: %s", bad.Body.String())
	}
}

func TestLLMAdminModelQueryParserPreservesRepeatedTypedFilters(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/llm/providers/x/models?author=acme&author=other&capability=tools&capability=reasoning&family=llama&sort=context:desc&sort=name:asc&free=true&min_context=64000&max_size=1234&created_after=2026-09-01T00:00:00Z&check_access=true&refresh=true", nil)
	query, err := parseLLMModelQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(query.Authors, []string{"acme", "other"}) || !reflect.DeepEqual(query.Capabilities, []string{"tools", "reasoning"}) || !reflect.DeepEqual(query.Ollama.Families, []string{"llama"}) || len(query.Sort) != 2 || query.Sort[0].Field != "context" || query.Sort[0].Direction != "desc" || query.Free == nil || !*query.Free || query.MinContext == nil || *query.MinContext != 64000 || query.Ollama.MaxSizeBytes == nil || *query.Ollama.MaxSizeBytes != 1234 || query.CreatedAfter == nil || !query.CheckAccess || !query.Refresh {
		t.Fatalf("parsed query=%#v", query)
	}
}

func adminJSON[T any](t *testing.T, handler http.Handler, method, path, body string) T {
	t.Helper()
	recorder := adminRequest(t, handler, method, path, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, recorder.Code, recorder.Body.String())
	}
	if got, ok := capability.ForAdminRequest(method, strings.SplitN(path, "?", 2)[0]); ok {
		if header := recorder.Header().Get(CanonicalOperationHeader); header != string(got) {
			t.Fatalf("%s %s operation header=%q want=%q", method, path, header, got)
		}
	}
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %s %s: %v body=%s", method, path, err, recorder.Body.String())
	}
	return value
}

func adminRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, path, reader)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func findAdminSecretOnDisk(t *testing.T, root, secret string) string {
	t.Helper()
	var found string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(body, []byte(secret)) {
			found = path
			return filepath.SkipAll
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}
