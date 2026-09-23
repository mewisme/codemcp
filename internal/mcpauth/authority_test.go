package mcpauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthAuthorizationCodePKCEAndRotation(t *testing.T) {
	cfg := Config{Enabled: true, TokenHash: "generation-1"}
	authority, err := New("http://127.0.0.1:37421", "http://127.0.0.1:37421/mcp", func() (Config, error) { return cfg, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := authority.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))

	metadata := httptest.NewRecorder()
	handler.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil))
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), `"resource":"http://127.0.0.1:37421/mcp"`) {
		t.Fatalf("protected resource metadata status=%d body=%s", metadata.Code, metadata.Body.String())
	}

	registration := httptest.NewRecorder()
	registrationBody := `{"client_name":"Cursor","redirect_uris":["http://localhost:8787/callback"],"token_endpoint_auth_method":"none"}`
	handler.ServeHTTP(registration, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(registrationBody)))
	if registration.Code != http.StatusCreated {
		t.Fatalf("registration status=%d body=%s", registration.Code, registration.Body.String())
	}
	var registered registrationResponse
	if err := json.Unmarshal(registration.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.ClientID == "" {
		t.Fatal("registration returned empty client id")
	}

	verifier := strings.Repeat("v", 64)
	state := "cursor-state"
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {registered.ClientID},
		"redirect_uri":          {"http://localhost:8787/callback"},
		"state":                 {state},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"resource":              {"http://127.0.0.1:37421/mcp"},
		"scope":                 {ScopeTools},
	}
	consent := httptest.NewRecorder()
	handler.ServeHTTP(consent, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+params.Encode(), nil))
	if consent.Code != http.StatusOK || !strings.Contains(consent.Body.String(), "Authorize CodeMCP") {
		t.Fatalf("consent status=%d body=%s", consent.Code, consent.Body.String())
	}
	csrf := hiddenInputValue(t, consent.Body.String(), "csrf")
	form := url.Values{}
	for key, values := range params {
		form[key] = append([]string(nil), values...)
	}
	form.Set("csrf", csrf)
	form.Set("decision", "allow")
	approvalRequest := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	approvalRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approval := httptest.NewRecorder()
	handler.ServeHTTP(approval, approvalRequest)
	if approval.Code != http.StatusFound {
		t.Fatalf("approval status=%d body=%s", approval.Code, approval.Body.String())
	}
	redirect, err := url.Parse(approval.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := redirect.Query().Get("code")
	if code == "" || redirect.Query().Get("state") != state || redirect.Query().Get("iss") != "http://127.0.0.1:37421" {
		t.Fatalf("authorization redirect=%s", redirect.String())
	}

	tokenForm := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {registered.ClientID}, "redirect_uri": {"http://localhost:8787/callback"}, "code_verifier": {verifier}, "resource": {"http://127.0.0.1:37421/mcp"}}
	tokenRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokenForm.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResponse := httptest.NewRecorder()
	handler.ServeHTTP(tokenResponse, tokenRequest)
	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("token status=%d body=%s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var tokens map[string]any
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	access, _ := tokens["access_token"].(string)
	if access == "" {
		t.Fatalf("tokens=%#v", tokens)
	}

	protectedRequest := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	protectedRequest.Header.Set("Authorization", "Bearer "+access)
	protectedResponse := httptest.NewRecorder()
	handler.ServeHTTP(protectedResponse, protectedRequest)
	if protectedResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("protected status=%d body=%s called=%t", protectedResponse.Code, protectedResponse.Body.String(), called)
	}

	replayRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokenForm.Encode()))
	replayRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	replay := httptest.NewRecorder()
	handler.ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("authorization code replay status=%d body=%s", replay.Code, replay.Body.String())
	}

	cfg.TokenHash = "generation-2"
	rotatedRequest := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rotatedRequest.Header.Set("Authorization", "Bearer "+access)
	rotated := httptest.NewRecorder()
	handler.ServeHTTP(rotated, rotatedRequest)
	if rotated.Code != http.StatusUnauthorized {
		t.Fatalf("rotated token status=%d body=%s", rotated.Code, rotated.Body.String())
	}
}

func hiddenInputValue(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("hidden input %q missing from %q", name, body)
	}
	start += len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("hidden input %q has no closing quote", name)
	}
	return body[start : start+end]
}

func TestOAuthRejectsWrongPKCEAndSupportsLegacyBearerWhenEnabled(t *testing.T) {
	cfg := Config{Enabled: true, LegacyBearer: true, TokenHash: "legacy-hash"}
	authority, err := New("http://127.0.0.1:37421", "http://127.0.0.1:37421/mcp", func() (Config, error) { return cfg, nil }, func(token, encoded string) bool { return token == "legacy-token" && encoded == "legacy-hash" })
	if err != nil {
		t.Fatal(err)
	}
	handler := authority.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	legacy := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	request.Header.Set("Authorization", "Bearer legacy-token")
	handler.ServeHTTP(legacy, request)
	if legacy.Code != http.StatusNoContent {
		body, _ := io.ReadAll(legacy.Result().Body)
		t.Fatalf("legacy status=%d body=%s", legacy.Code, body)
	}
	cfg.LegacyBearer = false
	rejected := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	request.Header.Set("Authorization", "Bearer legacy-token")
	handler.ServeHTTP(rejected, request)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("disabled legacy bearer status=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestOAuthRejectsUnsafeRedirectAndWrongResource(t *testing.T) {
	authority, err := New("http://127.0.0.1:37421", "http://127.0.0.1:37421/mcp", func() (Config, error) { return Config{Enabled: true, TokenHash: "generation"}, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := authority.Handler(http.NotFoundHandler())
	for _, body := range []string{
		`{"client_name":"bad","redirect_uris":["http://example.com/callback"],"token_endpoint_auth_method":"none"}`,
		`{"client_name":"bad","redirect_uris":["file:///tmp/callback"],"token_endpoint_auth_method":"none"}`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe redirect accepted body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
}
