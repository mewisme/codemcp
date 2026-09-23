package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const ScopeTools = "mcp:tools"

const (
	codeTTL         = 5 * time.Minute
	consentTTL      = 5 * time.Minute
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
	maxClients      = 256
)

type Config struct {
	Enabled      bool
	LegacyBearer bool
	TokenHash    string
}

type ConfigProvider func() (Config, error)
type LegacyVerifier func(token, encoded string) bool

type Authority struct {
	mu           sync.Mutex
	issuer       string
	resource     string
	provider     ConfigProvider
	legacyVerify LegacyVerifier
	clients      map[string]client
	consents     map[string]time.Time
	codes        map[string]authorizationCode
	access       map[string]tokenGrant
	refresh      map[string]tokenGrant
}

type client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

type authorizationCode struct {
	ClientID    string
	RedirectURI string
	Challenge   string
	Resource    string
	Scope       string
	Generation  string
	ExpiresAt   time.Time
}

type tokenGrant struct {
	ClientID   string
	Resource   string
	Scope      string
	Generation string
	ExpiresAt  time.Time
}

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

func New(issuer, resource string, provider ConfigProvider, legacyVerify LegacyVerifier) (*Authority, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	resource = strings.TrimRight(strings.TrimSpace(resource), "/")
	if err := validateAbsoluteHTTPURL(issuer); err != nil {
		return nil, fmt.Errorf("invalid OAuth issuer: %w", err)
	}
	if err := validateAbsoluteHTTPURL(resource); err != nil {
		return nil, fmt.Errorf("invalid MCP OAuth resource: %w", err)
	}
	if provider == nil {
		return nil, errors.New("MCP OAuth config provider is required")
	}
	return &Authority{issuer: issuer, resource: resource, provider: provider, legacyVerify: legacyVerify, clients: map[string]client{}, consents: map[string]time.Time{}, codes: map[string]authorizationCode{}, access: map[string]tokenGrant{}, refresh: map[string]tokenGrant{}}, nil
}

func (a *Authority) Handler(mcpHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	protected := sdkauth.RequireBearerToken(a.verifyToken, &sdkauth.RequireBearerTokenOptions{ResourceMetadataURL: a.issuer + "/.well-known/oauth-protected-resource/mcp", Scopes: []string{ScopeTools}})(mcpHandler)
	mcpAuth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := a.provider()
		if err != nil {
			http.Error(w, "authentication configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		if !cfg.Enabled {
			mcpHandler.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
	mux.Handle("/mcp", mcpAuth)
	mux.Handle("/mcp/", mcpAuth)
	mux.HandleFunc("/.well-known/oauth-protected-resource", a.serveProtectedResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", a.serveProtectedResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server", a.serveAuthorizationServerMetadata)
	mux.HandleFunc("/oauth/register", a.serveRegister)
	mux.HandleFunc("/oauth/authorize", a.serveAuthorize)
	mux.HandleFunc("/oauth/token", a.serveToken)
	return mux
}

func (a *Authority) serveProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"resource": a.resource, "authorization_servers": []string{a.issuer}, "scopes_supported": []string{ScopeTools}, "bearer_methods_supported": []string{"header"}, "resource_name": "CodeMCP"})
}

func (a *Authority) serveAuthorizationServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"issuer": a.issuer, "authorization_endpoint": a.issuer + "/oauth/authorize", "token_endpoint": a.issuer + "/oauth/token", "registration_endpoint": a.issuer + "/oauth/register", "scopes_supported": []string{ScopeTools}, "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true})
}

func (a *Authority) serveRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request registrationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid registration request")
		return
	}
	if request.TokenEndpointAuthMethod != "" && request.TokenEndpointAuthMethod != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients are supported")
		return
	}
	if len(request.RedirectURIs) == 0 || len(request.RedirectURIs) > 8 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect URI is required")
		return
	}
	redirects := make([]string, 0, len(request.RedirectURIs))
	seen := map[string]bool{}
	for _, raw := range request.RedirectURIs {
		redirect, err := validateRedirectURI(raw)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
		if !seen[redirect] {
			seen[redirect] = true
			redirects = append(redirects, redirect)
		}
	}
	id, err := randomToken("mcp_client_", 18)
	if err != nil {
		http.Error(w, "client registration failed", http.StatusInternalServerError)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.clients) >= maxClients {
		writeOAuthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "client registration limit reached")
		return
	}
	value := client{ID: id, Name: strings.TrimSpace(request.ClientName), RedirectURIs: redirects}
	a.clients[id] = value
	writeJSON(w, http.StatusCreated, registrationResponse{ClientID: id, ClientName: value.Name, RedirectURIs: redirects, TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}})
}

func (a *Authority) serveAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.serveAuthorizeForm(w, r)
		return
	}
	if r.Method == http.MethodPost {
		a.completeAuthorization(w, r)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (a *Authority) serveAuthorizeForm(w http.ResponseWriter, r *http.Request) {
	request, registered, err := a.validateAuthorizationRequest(r.URL.Query())
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	csrf, err := randomToken("", 24)
	if err != nil {
		http.Error(w, "authorization unavailable", http.StatusInternalServerError)
		return
	}
	a.mu.Lock()
	a.cleanupLocked(time.Now())
	a.consents[csrf] = time.Now().Add(consentTTL)
	a.mu.Unlock()
	data := struct {
		ClientName string
		ClientID   string
		Resource   string
		Scope      string
		CSRF       string
		Fields     map[string]string
	}{ClientName: registered.Name, ClientID: registered.ID, Resource: request.Get("resource"), Scope: normalizeScope(request.Get("scope")), CSRF: csrf, Fields: map[string]string{}}
	for _, key := range []string{"response_type", "client_id", "redirect_uri", "state", "code_challenge", "code_challenge_method", "resource", "scope"} {
		data.Fields[key] = request.Get(key)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = consentTemplate.Execute(w, data)
}

func (a *Authority) completeAuthorization(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid authorization form")
		return
	}
	csrf := strings.TrimSpace(r.Form.Get("csrf"))
	a.mu.Lock()
	a.cleanupLocked(time.Now())
	expiresAt, ok := a.consents[csrf]
	if ok {
		delete(a.consents, csrf)
	}
	a.mu.Unlock()
	if csrf == "" || !ok || !time.Now().Before(expiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "authorization form expired or invalid")
		return
	}
	request, _, err := a.validateAuthorizationRequest(r.Form)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	redirect, _ := url.Parse(request.Get("redirect_uri"))
	query := redirect.Query()
	if request.Get("state") != "" {
		query.Set("state", request.Get("state"))
	}
	query.Set("iss", a.issuer)
	if r.Form.Get("decision") != "allow" {
		query.Set("error", "access_denied")
		redirect.RawQuery = query.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
		return
	}
	cfg, err := a.provider()
	if err != nil || !cfg.Enabled || cfg.TokenHash == "" {
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "MCP authentication is unavailable")
		return
	}
	code, err := randomToken("mcp_code_", 32)
	if err != nil {
		http.Error(w, "authorization unavailable", http.StatusInternalServerError)
		return
	}
	a.mu.Lock()
	a.cleanupLocked(time.Now())
	a.codes[code] = authorizationCode{ClientID: request.Get("client_id"), RedirectURI: request.Get("redirect_uri"), Challenge: request.Get("code_challenge"), Resource: a.resource, Scope: normalizeScope(request.Get("scope")), Generation: cfg.TokenHash, ExpiresAt: time.Now().Add(codeTTL)}
	a.mu.Unlock()
	query.Set("code", code)
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (a *Authority) validateAuthorizationRequest(values url.Values) (url.Values, client, error) {
	if values.Get("response_type") != "code" {
		return nil, client{}, errors.New("response_type must be code")
	}
	if values.Get("code_challenge_method") != "S256" || strings.TrimSpace(values.Get("code_challenge")) == "" {
		return nil, client{}, errors.New("PKCE S256 is required")
	}
	if resource := strings.TrimRight(values.Get("resource"), "/"); resource != a.resource {
		return nil, client{}, errors.New("resource does not match this MCP server")
	}
	if scope := normalizeScope(values.Get("scope")); scope != ScopeTools {
		return nil, client{}, errors.New("unsupported OAuth scope")
	}
	a.mu.Lock()
	registered, ok := a.clients[values.Get("client_id")]
	a.mu.Unlock()
	if !ok {
		return nil, client{}, errors.New("unknown OAuth client")
	}
	redirect, err := validateRedirectURI(values.Get("redirect_uri"))
	if err != nil || !contains(registered.RedirectURIs, redirect) {
		return nil, client{}, errors.New("redirect_uri does not match registered client")
	}
	copyValues := url.Values{}
	for key, items := range values {
		copyValues[key] = append([]string(nil), items...)
	}
	return copyValues, registered, nil
}

func (a *Authority) serveToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid token request")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		a.exchangeCode(w, r)
	case "refresh_token":
		a.exchangeRefresh(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant type")
	}
}

func (a *Authority) exchangeCode(w http.ResponseWriter, r *http.Request) {
	codeValue := r.Form.Get("code")
	a.mu.Lock()
	a.cleanupLocked(time.Now())
	code, ok := a.codes[codeValue]
	if ok {
		delete(a.codes, codeValue)
	}
	a.mu.Unlock()
	if !ok || time.Now().After(code.ExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if code.ClientID != r.Form.Get("client_id") || code.RedirectURI != r.Form.Get("redirect_uri") || code.Resource != strings.TrimRight(r.Form.Get("resource"), "/") {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code binding mismatch")
		return
	}
	challenge := pkceChallenge(r.Form.Get("code_verifier"))
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(code.Challenge)) != 1 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verifier mismatch")
		return
	}
	cfg, err := a.provider()
	if err != nil || !cfg.Enabled || cfg.TokenHash == "" || cfg.TokenHash != code.Generation {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "MCP authentication changed during authorization")
		return
	}
	a.issueTokens(w, code.ClientID, code.Resource, code.Scope, code.Generation)
}

func (a *Authority) exchangeRefresh(w http.ResponseWriter, r *http.Request) {
	refreshValue := r.Form.Get("refresh_token")
	a.mu.Lock()
	a.cleanupLocked(time.Now())
	grant, ok := a.refresh[refreshValue]
	if ok {
		delete(a.refresh, refreshValue)
	}
	a.mu.Unlock()
	cfg, err := a.provider()
	if !ok || err != nil || !cfg.Enabled || cfg.TokenHash == "" || cfg.TokenHash != grant.Generation || grant.ClientID != r.Form.Get("client_id") || grant.Resource != strings.TrimRight(r.Form.Get("resource"), "/") {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	a.issueTokens(w, grant.ClientID, grant.Resource, grant.Scope, grant.Generation)
}

func (a *Authority) issueTokens(w http.ResponseWriter, clientID, resource, scope, generation string) {
	access, err := randomToken("mcp_at_", 32)
	if err != nil {
		http.Error(w, "token issuance failed", http.StatusInternalServerError)
		return
	}
	refresh, err := randomToken("mcp_rt_", 32)
	if err != nil {
		http.Error(w, "token issuance failed", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	a.mu.Lock()
	a.access[access] = tokenGrant{ClientID: clientID, Resource: resource, Scope: scope, Generation: generation, ExpiresAt: now.Add(accessTokenTTL)}
	a.refresh[refresh] = tokenGrant{ClientID: clientID, Resource: resource, Scope: scope, Generation: generation, ExpiresAt: now.Add(refreshTokenTTL)}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(accessTokenTTL.Seconds()), "refresh_token": refresh, "scope": scope})
}

func (a *Authority) verifyToken(_ context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	cfg, err := a.provider()
	if err != nil || !cfg.Enabled || cfg.TokenHash == "" {
		return nil, sdkauth.ErrInvalidToken
	}
	if cfg.LegacyBearer && a.legacyVerify != nil && a.legacyVerify(token, cfg.TokenHash) {
		return &sdkauth.TokenInfo{Scopes: []string{ScopeTools}, Expiration: time.Now().Add(time.Minute), UserID: "legacy-mcp-token", Extra: map[string]any{"auth_mode": "legacy_bearer"}}, nil
	}
	a.mu.Lock()
	a.cleanupLocked(time.Now())
	grant, ok := a.access[token]
	a.mu.Unlock()
	if !ok || grant.Generation != cfg.TokenHash || grant.Resource != a.resource || time.Now().After(grant.ExpiresAt) {
		return nil, sdkauth.ErrInvalidToken
	}
	return &sdkauth.TokenInfo{Scopes: strings.Fields(grant.Scope), Expiration: grant.ExpiresAt, UserID: "oauth:" + grant.ClientID, Extra: map[string]any{"auth_mode": "oauth", "resource": grant.Resource}}, nil
}

func (a *Authority) cleanupLocked(now time.Time) {
	for nonce, expiresAt := range a.consents {
		if !now.Before(expiresAt) {
			delete(a.consents, nonce)
		}
	}
	for code, value := range a.codes {
		if !now.Before(value.ExpiresAt) {
			delete(a.codes, code)
		}
	}
	for token, value := range a.access {
		if !now.Before(value.ExpiresAt) {
			delete(a.access, token)
		}
	}
	for token, value := range a.refresh {
		if !now.Before(value.ExpiresAt) {
			delete(a.refresh, token)
		}
	}
}

func validateRedirectURI(raw string) (string, error) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Scheme == "" || value.Host == "" || value.Fragment != "" || value.User != nil {
		return "", errors.New("redirect URI must be an absolute URL without fragment or user info")
	}
	if value.Scheme == "http" {
		host := value.Hostname()
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("HTTP redirect URI must use a loopback host")
		}
	} else if value.Scheme != "https" {
		return "", errors.New("redirect URI must use HTTPS or loopback HTTP")
	}
	return value.String(), nil
}

func validateAbsoluteHTTPURL(raw string) error {
	value, err := url.Parse(raw)
	if err != nil || value.Host == "" || (value.Scheme != "http" && value.Scheme != "https") || value.User != nil || value.Fragment != "" {
		return errors.New("URL must be absolute HTTP or HTTPS without user info or fragment")
	}
	return nil
}

func normalizeScope(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ScopeTools
	}
	if len(fields) == 1 && fields[0] == ScopeTools {
		return ScopeTools
	}
	return strings.Join(fields, " ")
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func randomToken(prefix string, size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]any{"error": code, "error_description": description})
}

var consentTemplate = template.Must(template.New("consent").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Authorize CodeMCP</title><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><main><h1>Authorize CodeMCP</h1><p><strong>{{if .ClientName}}{{.ClientName}}{{else}}{{.ClientID}}{{end}}</strong> wants access to <code>{{.Resource}}</code>.</p><p>Scope: <code>{{.Scope}}</code></p><form method="post">{{range $key,$value := .Fields}}<input type="hidden" name="{{$key}}" value="{{$value}}">{{end}}<input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" name="decision" value="allow">Allow</button><button type="submit" name="decision" value="deny">Deny</button></form></main></body></html>`))
