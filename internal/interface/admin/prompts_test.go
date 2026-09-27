package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestPromptAPIWorkspaceOverrideDoesNotMutateGlobal(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	registered, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(API{Workspaces: manager})
	definition := instructioncontext.PromptDefinition{
		Version: 1, Name: "review", Messages: []instructioncontext.PromptMessage{{
			Role: "user", Content: instructioncontext.PromptTextContent{Type: "text", Text: "Global"},
		}},
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var encoded []byte
		if body != nil {
			var err error
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, path, bytes.NewReader(encoded)))
		return recorder
	}
	global := request(http.MethodPost, "/api/prompts", map[string]any{"scope": "global", "definition": definition})
	if global.Code != http.StatusCreated {
		t.Fatalf("create global=%d %s", global.Code, global.Body)
	}
	workspacePath := "/api/prompts?workspace_id=" + registered.ID
	if got := request(http.MethodGet, workspacePath, nil); got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte("review")) {
		t.Fatalf("workspace list=%d %s", got.Code, got.Body)
	}
	if got := request(http.MethodDelete, "/api/prompts/review?workspace_id="+registered.ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("inherited delete=%d %s", got.Code, got.Body)
	}
	definition.Messages[0].Content.Text = "Local"
	if got := request(http.MethodPost, workspacePath, map[string]any{"scope": "workspace", "workspace_id": registered.ID, "definition": definition}); got.Code != http.StatusCreated {
		t.Fatalf("create override=%d %s", got.Code, got.Body)
	}
	if got := request(http.MethodGet, "/api/prompts/review?workspace_id="+registered.ID, nil); got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte("Local")) {
		t.Fatalf("workspace get=%d %s", got.Code, got.Body)
	}
	if got := request(http.MethodDelete, "/api/prompts/review?workspace_id="+registered.ID, nil); got.Code != http.StatusOK {
		t.Fatalf("delete override=%d %s", got.Code, got.Body)
	}
	if got := request(http.MethodGet, "/api/prompts/review", nil); got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte("Global")) {
		t.Fatalf("global after workspace delete=%d %s", got.Code, got.Body)
	}
}
