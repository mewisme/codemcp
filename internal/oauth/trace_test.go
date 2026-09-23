package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type oauthTraceCollector struct {
	mu     sync.Mutex
	events []tracepkg.Event
}

func (c *oauthTraceCollector) observe(event tracepkg.Event) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

func (c *oauthTraceCollector) snapshot() []tracepkg.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tracepkg.Event(nil), c.events...)
}

func TestOAuthLoginTraceCoversLifecycleWithoutSecretLeak(t *testing.T) {
	const accessSecret = "access-sentinel-4f662f4c"
	const refreshSecret = "refresh-sentinel-b8df31d2"
	const clientSecret = "client-secret-sentinel-a593e741"
	const callbackCode = "code-sentinel-297f1571"
	var generatedState, generatedChallenge, tokenVerifier string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resource := server.URL + "/mcp"
		issuer := server.URL + "/issuer"
		switch request.URL.Path {
		case "/mcp":
			writer.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+server.URL+`/.well-known/oauth-protected-resource/mcp", scope="read"`)
			writer.WriteHeader(http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource/mcp":
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{"resource": resource, "authorization_servers": []string{issuer}, "scopes_supported": []string{"read"}})
		case "/.well-known/oauth-authorization-server/issuer":
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token",
				"registration_endpoint": server.URL + "/register", "response_types_supported": []string{"code"},
				"grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"},
				"code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"read", "offline_access"},
				"authorization_response_iss_parameter_supported": true,
			})
		case "/register":
			writer.Header().Set("Content-Type", "application/json")
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			redirects, _ := body["redirect_uris"].([]any)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"client_id": "trace-client", "client_secret": clientSecret, "token_endpoint_auth_method": "none", "application_type": "native",
				"redirect_uris": redirects, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
			})
		case "/token":
			writer.Header().Set("Content-Type", "application/json")
			_ = request.ParseForm()
			tokenVerifier = request.Form.Get("code_verifier")
			_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": accessSecret, "refresh_token": refreshSecret, "token_type": "Bearer", "expires_in": 3600, "scope": "read offline_access"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	collector := &oauthTraceCollector{}
	store := NewStoreWithClient(filepath.Join(t.TempDir(), "oauth.json"), server.Client()).SetTraceObserver(collector.observe)
	credential, err := store.Login(t.Context(), LoginConfig{ServerID: "alpha", ServerURL: server.URL + "/mcp"}, LoginOptions{OnURL: func(raw string) error {
		authURL, err := url.Parse(raw)
		if err != nil {
			return err
		}
		query := authURL.Query()
		generatedState = query.Get("state")
		generatedChallenge = query.Get("code_challenge")
		callback := query.Get("redirect_uri") + "?code=" + url.QueryEscape(callbackCode) + "&state=" + url.QueryEscape(generatedState) + "&iss=" + url.QueryEscape(server.URL+"/issuer")
		response, err := http.Get(callback)
		if err == nil {
			_ = response.Body.Close()
		}
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != accessSecret || credential.RefreshToken != refreshSecret || tokenVerifier == "" || generatedState == "" || generatedChallenge == "" {
		t.Fatalf("unexpected OAuth result: credential=%#v verifier=%q state=%q challenge=%q", credential, tokenVerifier, generatedState, generatedChallenge)
	}
	if _, err := store.Status("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("alpha"); err != nil {
		t.Fatal(err)
	}

	events := collector.snapshot()
	text := fmt.Sprint(events)
	for _, secret := range []string{accessSecret, refreshSecret, clientSecret, callbackCode, generatedState, generatedChallenge, tokenVerifier} {
		if strings.Contains(text, secret) {
			t.Fatalf("OAuth trace leaked secret %q: %s", secret, text)
		}
	}
	for _, name := range []string{
		"oauth.callback.listen.completed",
		"oauth.challenge.probe.completed",
		"oauth.discovery.completed",
		"oauth.registration.resolve.completed",
		"oauth.callback.received",
		"oauth.token.request.completed",
		"oauth.store.write.completed",
		"oauth.flow.complete.completed",
		"oauth.login.completed",
		"oauth.callback.shutdown.completed",
		"oauth.store.status.completed",
		"oauth.store.delete.completed",
	} {
		if !oauthTraceContains(events, name) {
			t.Fatalf("missing OAuth trace event %s: %#v", name, events)
		}
	}
	for _, event := range events {
		if strings.HasSuffix(event.Name, ".started") && !oauthTraceHasTerminal(events, event.Name) {
			t.Fatalf("OAuth trace span never terminated: %s", event.Name)
		}
	}
}

func oauthTraceContains(events []tracepkg.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func oauthTraceHasTerminal(events []tracepkg.Event, startName string) bool {
	base := strings.TrimSuffix(startName, ".started")
	return oauthTraceContains(events, base+".completed") || oauthTraceContains(events, base+".failed")
}
