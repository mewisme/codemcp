package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestUpstreamOAuthAdminFlowDoesNotExposeTokens(t *testing.T) {
	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource := authServer.URL + "/mcp"
		issuer := authServer.URL + "/issuer"
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+authServer.URL+`/.well-known/oauth-protected-resource/mcp", scope="read"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{issuer}, "scopes_supported": []string{"read"}})
		case "/.well-known/oauth-authorization-server/issuer":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": authServer.URL + "/authorize", "token_endpoint": authServer.URL + "/token",
				"registration_endpoint": authServer.URL + "/register", "response_types_supported": []string{"code"},
				"grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"},
				"code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true,
			})
		case "/register":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id": "web-client", "token_endpoint_auth_method": "none", "application_type": "native",
				"redirect_uris": body["redirect_uris"], "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
			})
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "top-secret-access", "refresh_token": "top-secret-refresh", "expires_in": 3600, "scope": "read"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	manager := upstream.NewManagerWithClient(nil, &adminUpstreamClient{})
	if err := manager.Add(upstream.Server{ID: "secure", Name: "Secure", Transport: "http", Enabled: true, URL: authServer.URL + "/mcp", Auth: upstream.AuthConfig{Type: "oauth"}, AllowPrivateNetwork: true}); err != nil {
		t.Fatal(err)
	}
	store := mcpoauth.NewStoreWithClient(filepath.Join(t.TempDir(), "oauth.json"), authServer.Client())
	flows := mcpoauth.NewFlowManager(store)
	api := API{Upstream: manager, OAuth: store, OAuthFlows: flows}
	handler := New(api)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/upstream/secure/auth/login", strings.NewReader(`{"redirect_origin":"http://127.0.0.1:37422"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var session mcpoauth.FlowSession
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	authURL, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	redirectURL, err := url.Parse(authURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	query := redirectURL.Query()
	query.Set("code", "code")
	query.Set("state", authURL.Query().Get("state"))
	query.Set("iss", authServer.URL+"/issuer")
	redirectURL.RawQuery = query.Encode()

	recorder = httptest.NewRecorder()
	api.OAuthCallbackHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, redirectURL.String(), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/upstream/secure/auth/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"configured":true`) || strings.Contains(body, "top-secret-access") || strings.Contains(body, "top-secret-refresh") {
		t.Fatalf("unsafe status response: %s", body)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/upstream/secure/auth/logout", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("logout=%d body=%s", recorder.Code, recorder.Body.String())
	}
	status, err := store.Status("secure")
	if err != nil || status.Configured {
		t.Fatalf("status after logout=%#v err=%v", status, err)
	}
}

func TestOAuthCallbackRejectsUnknownSession(t *testing.T) {
	api := API{OAuth: mcpoauth.NewStore(filepath.Join(t.TempDir(), "oauth.json"))}
	recorder := httptest.NewRecorder()
	api.OAuthCallbackHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/oauth/callback/missing?state=x&code=y", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
