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

	coreModel := adminJSON[application.LLMProviderResult](t, handler, http.MethodPut, "/api/llm/providers/openrouter", `{"model":"openrouter/auto"}`)
	if coreModel.Model != "openrouter/auto" || !coreModel.Core {
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
	failedDelete := adminRequest(t, handler, http.MethodDelete, "/api/llm/providers/openrouter", "")
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

	adminJSON[application.LLMProviderResult](t, handler, http.MethodPost, "/api/llm/providers/openrouter/select", "")
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
