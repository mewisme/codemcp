package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	mcpoauth "go.mewis.me/codemcp/internal/oauth"
)

func TestNativeClientUsesStoredOAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		var body rpcRequest
		_ = json.NewDecoder(request.Body).Decode(&body)
		_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": body.ID, "result": map[string]any{"capabilities": map[string]any{}}})
	}))
	defer server.Close()
	store := mcpoauth.NewStore(filepath.Join(t.TempDir(), "oauth.json"))
	if err := store.Put(mcpoauth.Credential{ServerID: "secure", ServerURL: server.URL, AccessToken: "access", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	client := NewNativeClientWithOAuthStore(store)
	if err := client.Connect(context.Background(), Server{ID: "secure", Enabled: true, Transport: "http", URL: server.URL, Auth: AuthConfig{Type: "oauth"}, AllowPrivateNetwork: true}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeClientRequiresOAuthLogin(t *testing.T) {
	store := mcpoauth.NewStore(filepath.Join(t.TempDir(), "oauth.json"))
	client := NewNativeClientWithOAuthStore(store)
	err := client.Connect(context.Background(), Server{ID: "secure", Enabled: true, Transport: "http", URL: "https://example.invalid/mcp", Auth: AuthConfig{Type: "oauth"}})
	var loginErr *OAuthLoginRequiredError
	if !errors.As(err, &loginErr) {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestNativeClientAutoAuthTurns401IntoLoginInstruction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("WWW-Authenticate", `Bearer scope="read"`)
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	store := mcpoauth.NewStore(filepath.Join(t.TempDir(), "oauth.json"))
	client := NewNativeClientWithOAuthStore(store)
	err := client.Connect(context.Background(), Server{ID: "secure", Enabled: true, Transport: "http", URL: server.URL, Auth: AuthConfig{Type: "auto"}, AllowPrivateNetwork: true})
	var loginErr *OAuthLoginRequiredError
	if !errors.As(err, &loginErr) {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestNativeClientClearsStoredOAuthCredential(t *testing.T) {
	store := mcpoauth.NewStore(filepath.Join(t.TempDir(), "oauth.json"))
	if err := store.Put(mcpoauth.Credential{ServerID: "secure", ServerURL: "https://example.test/mcp", AccessToken: "secret"}); err != nil {
		t.Fatal(err)
	}
	client := NewNativeClientWithOAuthStore(store)
	if err := client.ClearOAuthCredential("secure"); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status("secure")
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured {
		t.Fatalf("credential still configured: %#v", status)
	}
}
