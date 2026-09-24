package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestCanonicalOperationMiddlewareAnnotatesConcreteAdminRequest(t *testing.T) {
	var seen capability.ID
	handler := withCanonicalOperation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		seen, ok = CanonicalOperation(r)
		if !ok {
			t.Fatal("canonical operation missing from request context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws_123", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d", recorder.Code)
	}
	if seen != capability.WorkspaceShow {
		t.Fatalf("operation=%q want=%q", seen, capability.WorkspaceShow)
	}
	if got := recorder.Header().Get(CanonicalOperationHeader); got != string(capability.WorkspaceShow) {
		t.Fatalf("response operation header=%q", got)
	}
}

func TestCanonicalOperationMiddlewareRejectsBrowserMismatch(t *testing.T) {
	called := false
	handler := withCanonicalOperation(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	request.Header.Set(CanonicalOperationHeader, string(capability.WorkspaceList))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if called {
		t.Fatal("mismatched browser operation reached Admin handler")
	}
}

func TestCanonicalOperationMiddlewareDoesNotAuthorizeFromBrowserHeader(t *testing.T) {
	called := false
	handler := withCanonicalOperation(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/not-an-admin-route", nil)
	request.Header.Set(CanonicalOperationHeader, string(capability.WorkspaceRegister))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if called {
		t.Fatal("browser-declared operation created a route mapping")
	}
}

func TestCanonicalBrowserMetadataPreservesWorkspaceSuccessAndFailureSemantics(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	handler := New(API{Workspaces: manager})

	success := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"path":`+jsonString(t.TempDir())+`}`))
	success.Header.Set(CanonicalOperationHeader, string(capability.WorkspaceRegister))
	successRecorder := httptest.NewRecorder()
	handler.ServeHTTP(successRecorder, success)
	if successRecorder.Code != http.StatusOK {
		t.Fatalf("success status=%d body=%s", successRecorder.Code, successRecorder.Body.String())
	}
	if got := successRecorder.Header().Get(CanonicalOperationHeader); got != string(capability.WorkspaceRegister) {
		t.Fatalf("success response operation=%q", got)
	}

	failure := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"path":""}`))
	failure.Header.Set(CanonicalOperationHeader, string(capability.WorkspaceRegister))
	failureRecorder := httptest.NewRecorder()
	handler.ServeHTTP(failureRecorder, failure)
	if failureRecorder.Code != http.StatusBadRequest {
		t.Fatalf("failure status=%d body=%s", failureRecorder.Code, failureRecorder.Body.String())
	}
	if !strings.Contains(failureRecorder.Body.String(), "path is required") {
		t.Fatalf("failure body=%q", failureRecorder.Body.String())
	}
}
