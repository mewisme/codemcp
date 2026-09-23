package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	defaultFlowTTL        = 10 * time.Minute
	defaultFlowMaxPending = 128
)

type FlowSession struct {
	ID               string    `json:"session_id"`
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type FlowManager struct {
	mu         sync.Mutex
	store      *Store
	sessions   map[string]pendingLogin
	ttl        time.Duration
	maxPending int
	inflight   int
	now        func() time.Time
}

type pendingLogin struct {
	Config       LoginConfig
	Discovery    *Discovery
	Registration registration
	Scopes       []string
	State        string
	Verifier     string
	RedirectURL  string
	ExpiresAt    time.Time
}

type registration struct {
	Kind               string
	ClientID           string
	ClientSecret       string
	ClientSecretEnvVar string
	ClientMetadataURL  string
	TokenAuthMethod    string
}

func NewFlowManager(store *Store) *FlowManager {
	if store == nil {
		store = NewStore(Path())
	}
	return &FlowManager{store: store, sessions: map[string]pendingLogin{}, ttl: defaultFlowTTL, maxPending: defaultFlowMaxPending, now: time.Now}
}

func (m *FlowManager) Begin(ctx context.Context, config LoginConfig, redirectBase, extraScope string) (FlowSession, error) {
	ctx = m.store.traceContext(ctx)
	config.ServerID = strings.TrimSpace(config.ServerID)
	config.ServerURL = strings.TrimSpace(config.ServerURL)
	span := tracepkg.Start(ctx, "OAUTH", "oauth.flow.begin", "Preparing OAuth login flow", tracepkg.String("server", config.ServerID), tracepkg.URL("server_url", config.ServerURL), tracepkg.Bool("issuer_preference", strings.TrimSpace(config.Issuer) != ""), tracepkg.Bool("preregistered_client", strings.TrimSpace(config.ClientID) != ""), tracepkg.Bool("client_metadata_document", strings.TrimSpace(config.ClientMetadataURL) != ""))
	fail := func(message string, err error, fields ...tracepkg.Field) (FlowSession, error) {
		span.FailMessage(message, err, fields...)
		return FlowSession{}, err
	}
	if config.ServerID == "" || config.ServerURL == "" {
		return fail("OAuth login flow validation failed", errors.New("OAuth login requires server id and URL"))
	}
	if err := validateRedirectURL(redirectBase); err != nil {
		return fail("OAuth redirect URL validation failed", err)
	}
	if err := m.reserveFlowSlot(); err != nil {
		return fail("OAuth login flow capacity exhausted", err)
	}
	reserved := true
	defer func() {
		if reserved {
			m.releaseFlowSlot()
		}
	}()
	challengeHeaders, err := m.store.ProbeWWWAuthenticate(ctx, config.ServerURL)
	if err != nil {
		wrapped := fmt.Errorf("probe MCP authorization challenge: %w", err)
		return fail("OAuth challenge probe failed", wrapped)
	}
	discovery, err := m.store.Discover(ctx, config.ServerURL, config.Issuer, challengeHeaders)
	if err != nil {
		span.FailMessage("OAuth metadata discovery failed", errors.New("OAuth metadata discovery failed"))
		return FlowSession{}, err
	}
	previous, _ := m.store.Get(config.ServerID)
	scopes := unionScopes(discovery.RequestedScopes, strings.Fields(config.Scope), strings.Fields(extraScope))
	previousScopesReused := previous.ServerURL == config.ServerURL && previous.Issuer == discovery.Issuer
	if previousScopesReused {
		scopes = unionScopes(scopes, previous.Scopes)
	}
	if slices.Contains(discovery.AuthServerMeta.ScopesSupported, "offline_access") && !slices.Contains(scopes, "offline_access") {
		scopes = append(scopes, "offline_access")
	}
	tracepkg.Emit(ctx, "OAUTH", "oauth.flow.scopes", "OAuth scopes resolved", tracepkg.Any("scopes", append([]string(nil), scopes...)), tracepkg.Int("scope_count", len(scopes)), tracepkg.Bool("previous_scopes_reused", previousScopesReused))
	id, err := randomToken(24)
	if err != nil {
		return fail("OAuth login session allocation failed", err)
	}
	redirectURL, err := flowRedirectURL(redirectBase, id)
	if err != nil {
		return fail("OAuth callback URL construction failed", err)
	}
	registration, err := m.store.resolveRegistration(ctx, config, discovery, redirectURL, scopes)
	if err != nil {
		span.FailMessage("OAuth client registration resolution failed", errors.New("OAuth client registration failed"))
		return FlowSession{}, err
	}
	state, err := randomToken(32)
	if err != nil {
		return fail("OAuth state allocation failed", err)
	}
	verifier, err := randomToken(48)
	if err != nil {
		return fail("OAuth PKCE verifier allocation failed", err)
	}
	authorizationURL, err := buildAuthorizationURL(discovery.AuthServerMeta.AuthorizationEndpoint, registration.ClientID, redirectURL, discovery.Resource, state, pkceChallenge(verifier), scopes)
	if err != nil {
		return fail("OAuth authorization URL construction failed", err)
	}
	expiresAt := m.clock()().UTC().Add(m.ttl)
	pending := pendingLogin{
		Config: config, Discovery: discovery, Registration: registration, Scopes: scopes, State: state, Verifier: verifier, RedirectURL: redirectURL, ExpiresAt: expiresAt,
	}
	if err := m.commitFlowSlot(id, pending); err != nil {
		return fail("OAuth login flow capacity exhausted", err)
	}
	reserved = false
	span.EndMessage("OAuth login flow prepared", tracepkg.URL("authorization_endpoint", discovery.AuthServerMeta.AuthorizationEndpoint), tracepkg.URL("token_endpoint", discovery.AuthServerMeta.TokenEndpoint), tracepkg.String("registration", registration.Kind), tracepkg.String("token_auth_method", registration.TokenAuthMethod), tracepkg.Any("scopes", append([]string(nil), scopes...)), tracepkg.Int("scope_count", len(scopes)), tracepkg.String("callback_origin", redirectBase), tracepkg.Int64("flow_ttl_ms", m.ttl.Milliseconds()))
	return FlowSession{ID: id, AuthorizationURL: authorizationURL, ExpiresAt: expiresAt}, nil
}

func (m *FlowManager) Complete(ctx context.Context, id, state, code, issuer, oauthError, errorDescription string) (Credential, error) {
	ctx = m.store.traceContext(ctx)
	span := tracepkg.Start(ctx, "OAUTH", "oauth.flow.complete", "Completing OAuth login flow", tracepkg.Bool("session_present", strings.TrimSpace(id) != ""), tracepkg.Bool("state_present", state != ""), tracepkg.Bool("code_present", code != ""), tracepkg.Bool("issuer_present", issuer != ""), tracepkg.Bool("oauth_error_present", oauthError != ""))
	tracepkg.Emit(ctx, "OAUTH", "oauth.callback.received", "OAuth callback received", tracepkg.Bool("state_present", state != ""), tracepkg.Bool("code_present", code != ""), tracepkg.Bool("issuer_present", issuer != ""), tracepkg.Bool("oauth_error_present", oauthError != ""))
	m.mu.Lock()
	m.cleanupLocked(m.clock()())
	pending, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		err := errors.New("OAuth login session not found or expired")
		span.FailMessage("OAuth callback session was not found", err)
		return Credential{}, err
	}
	if state != pending.State {
		m.mu.Unlock()
		err := errors.New("OAuth state mismatch")
		span.FailMessage("OAuth callback state validation failed", err, tracepkg.String("server", pending.Config.ServerID))
		return Credential{}, err
	}
	delete(m.sessions, id)
	m.mu.Unlock()

	if oauthError != "" {
		message := oauthError
		if errorDescription != "" {
			message += ": " + errorDescription
		}
		span.FailMessage("OAuth authorization server returned an error", errors.New("OAuth authorization failed"), tracepkg.String("server", pending.Config.ServerID))
		return Credential{}, errors.New(message)
	}
	if code == "" {
		err := errors.New("OAuth callback is missing authorization code")
		span.FailMessage("OAuth callback authorization code missing", err, tracepkg.String("server", pending.Config.ServerID))
		return Credential{}, err
	}
	if err := ValidateIssuerResponse(issuer, pending.Discovery.Issuer, pending.Discovery.AuthServerMeta.AuthorizationResponseIssParameterSupported); err != nil {
		span.FailMessage("OAuth callback issuer validation failed", err, tracepkg.String("server", pending.Config.ServerID), tracepkg.URL("issuer", pending.Discovery.Issuer))
		return Credential{}, err
	}
	secret, err := registrationSecret(pending.Registration)
	if err != nil {
		span.FailMessage("OAuth client secret resolution failed", errors.New("OAuth client secret unavailable"), tracepkg.String("server", pending.Config.ServerID))
		return Credential{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {pending.RedirectURL},
		"code_verifier": {pending.Verifier},
		"resource":      {pending.Discovery.Resource},
	}
	token, err := m.store.requestToken(ctx, pending.Discovery.AuthServerMeta.TokenEndpoint, pending.Registration.TokenAuthMethod, pending.Registration.ClientID, secret, form, pending.Config.ServerURL, pending.Discovery.Issuer)
	if err != nil {
		span.FailMessage("OAuth authorization code exchange failed", errors.New("OAuth token exchange failed"), tracepkg.String("server", pending.Config.ServerID), tracepkg.URL("token_endpoint", pending.Discovery.AuthServerMeta.TokenEndpoint))
		return Credential{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	credential := Credential{
		ServerID: pending.Config.ServerID, ServerURL: pending.Config.ServerURL, Resource: pending.Discovery.Resource, Issuer: pending.Discovery.Issuer,
		Registration: pending.Registration.Kind, ClientID: pending.Registration.ClientID, ClientSecret: pending.Registration.ClientSecret,
		ClientSecretEnvVar: pending.Registration.ClientSecretEnvVar, ClientMetadataURL: pending.Registration.ClientMetadataURL,
		AuthorizationURL: pending.Discovery.AuthServerMeta.AuthorizationEndpoint, TokenEndpoint: pending.Discovery.AuthServerMeta.TokenEndpoint,
		TokenAuthMethod: pending.Registration.TokenAuthMethod, Scopes: append([]string(nil), pending.Scopes...),
	}
	credential, err = applyTokenResponse(credential, token)
	if err != nil {
		span.FailMessage("OAuth token response validation failed", err, tracepkg.String("server", pending.Config.ServerID))
		return Credential{}, err
	}
	if token.Scope == "" {
		credential.Scopes = append([]string(nil), pending.Scopes...)
	}
	if err := m.store.Put(credential); err != nil {
		span.FailMessage("OAuth credential persistence failed", errors.New("OAuth authorization persistence failed"), tracepkg.String("server", pending.Config.ServerID))
		return Credential{}, err
	}
	span.EndMessage("OAuth login flow completed", tracepkg.String("server", pending.Config.ServerID), tracepkg.URL("issuer", credential.Issuer), tracepkg.String("registration", credential.Registration), tracepkg.Any("scopes", append([]string(nil), credential.Scopes...)), tracepkg.Int("scope_count", len(credential.Scopes)), tracepkg.Bool("has_refresh", credential.RefreshToken != ""), tracepkg.Bool("expires", !credential.ExpiresAt.IsZero()))
	return credential, nil
}

func (m *FlowManager) Cancel(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *FlowManager) reserveFlowSlot() error {
	if m == nil {
		return errors.New("OAuth flow manager is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(m.clock()())
	if m.maxPending > 0 && len(m.sessions)+m.inflight >= m.maxPending {
		return fmt.Errorf("too many pending OAuth login flows (maximum %d)", m.maxPending)
	}
	m.inflight++
	return nil
}

func (m *FlowManager) releaseFlowSlot() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.inflight > 0 {
		m.inflight--
	}
	m.mu.Unlock()
}

func (m *FlowManager) commitFlowSlot(id string, pending pendingLogin) error {
	if m == nil {
		return errors.New("OAuth flow manager is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(m.clock()())
	if m.maxPending > 0 && len(m.sessions) >= m.maxPending {
		return fmt.Errorf("too many pending OAuth login flows (maximum %d)", m.maxPending)
	}
	if m.inflight <= 0 {
		return errors.New("OAuth flow reservation is missing")
	}
	m.inflight--
	m.sessions[id] = pending
	return nil
}

func (m *FlowManager) clock() func() time.Time {
	if m != nil && m.now != nil {
		return m.now
	}
	return time.Now
}

func (m *FlowManager) cleanupLocked(now time.Time) {
	for id, session := range m.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(m.sessions, id)
		}
	}
}

func (s *Store) resolveRegistration(ctx context.Context, config LoginConfig, discovery *Discovery, redirectURL string, scopes []string) (registration, error) {
	ctx = s.traceContext(ctx)
	span := tracepkg.Start(ctx, "OAUTH", "oauth.registration.resolve", "Resolving OAuth client registration", tracepkg.String("server", config.ServerID), tracepkg.Bool("preregistered_client", strings.TrimSpace(config.ClientID) != ""), tracepkg.Bool("client_metadata_document", strings.TrimSpace(config.ClientMetadataURL) != ""), tracepkg.Bool("dcr_available", discovery.AuthServerMeta.RegistrationEndpoint != ""), tracepkg.Int("scope_count", len(scopes)))
	metadataURL := strings.TrimSpace(config.ClientMetadataURL)
	if metadataURL != "" && discovery.AuthServerMeta.ClientIDMetadataDocumentSupported {
		if err := validateClientMetadataURL(metadataURL); err != nil {
			span.FailMessage("OAuth Client ID Metadata Document validation failed", err)
			return registration{}, err
		}
		result := registration{Kind: "cimd", ClientID: metadataURL, ClientMetadataURL: metadataURL, TokenAuthMethod: "none"}
		span.EndMessage("OAuth client registration resolved", tracepkg.String("registration", result.Kind), tracepkg.URL("client_metadata_url", metadataURL), tracepkg.String("token_auth_method", result.TokenAuthMethod))
		return result, nil
	}
	clientID := strings.TrimSpace(config.ClientID)
	if clientID != "" {
		secret := ""
		if env := strings.TrimSpace(config.ClientSecretEnvVar); env != "" {
			secret = strings.TrimSpace(os.Getenv(env))
			if secret == "" {
				err := fmt.Errorf("missing OAuth client secret environment variable: %s", env)
				span.FailMessage("OAuth pre-registered client secret unavailable", errors.New("OAuth client secret environment variable is empty"), tracepkg.Bool("client_auth_env_present", true))
				return registration{}, err
			}
		}
		result := registration{
			Kind: "preregistered", ClientID: clientID, ClientSecretEnvVar: strings.TrimSpace(config.ClientSecretEnvVar),
			TokenAuthMethod: selectTokenAuthMethod(discovery.AuthServerMeta.TokenEndpointAuthMethodsSupported, secret),
		}
		span.EndMessage("OAuth client registration resolved", tracepkg.String("registration", result.Kind), tracepkg.String("client_id", clientID), tracepkg.Bool("client_auth_env_present", result.ClientSecretEnvVar != ""), tracepkg.String("token_auth_method", result.TokenAuthMethod))
		return result, nil
	}
	if discovery.AuthServerMeta.RegistrationEndpoint == "" {
		var err error
		if metadataURL != "" {
			err = errors.New("authorization server does not support configured Client ID Metadata Document and has no DCR endpoint")
		} else {
			err = errors.New("OAuth client registration is required: configure a client ID or Client ID Metadata Document")
		}
		span.FailMessage("OAuth client registration unavailable", err)
		return registration{}, err
	}
	grantTypes := []string{"authorization_code"}
	if slices.Contains(discovery.AuthServerMeta.GrantTypesSupported, "refresh_token") || slices.Contains(discovery.AuthServerMeta.ScopesSupported, "offline_access") {
		grantTypes = append(grantTypes, "refresh_token")
	}
	if err := validateOutboundURL(ctx, discovery.AuthServerMeta.RegistrationEndpoint, config.ServerURL, discovery.Issuer); err != nil {
		wrapped := fmt.Errorf("dynamic client registration endpoint denied: %w", err)
		span.FailMessage("OAuth dynamic registration endpoint denied", wrapped, tracepkg.URL("registration_endpoint", discovery.AuthServerMeta.RegistrationEndpoint))
		return registration{}, wrapped
	}
	dcrSpan := tracepkg.Start(ctx, "OAUTH", "oauth.registration.dynamic", "Registering OAuth client dynamically", tracepkg.URL("registration_endpoint", discovery.AuthServerMeta.RegistrationEndpoint), tracepkg.Any("grant_types", append([]string(nil), grantTypes...)), tracepkg.Int("scope_count", len(scopes)))
	response, err := oauthex.RegisterClient(ctx, discovery.AuthServerMeta.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{redirectURL}, TokenEndpointAuthMethod: "none", GrantTypes: grantTypes,
		ResponseTypes: []string{"code"}, ClientName: "codemcp", Scope: strings.Join(scopes, " "), ApplicationType: "native",
	}, s.clientForTargets(config.ServerURL, discovery.Issuer))
	if err != nil {
		dcrSpan.FailMessage("OAuth dynamic client registration failed", errors.New("OAuth dynamic registration request failed"))
		span.FailMessage("OAuth client registration failed", errors.New("OAuth dynamic registration failed"))
		return registration{}, fmt.Errorf("dynamic client registration: %w", err)
	}
	method := response.TokenEndpointAuthMethod
	if method == "" {
		method = normalizeTokenAuthMethod("", response.ClientSecret)
	}
	result := registration{Kind: "dcr", ClientID: response.ClientID, ClientSecret: response.ClientSecret, TokenAuthMethod: method}
	dcrSpan.EndMessage("OAuth client registered dynamically", tracepkg.String("client_id", response.ClientID), tracepkg.Bool("client_auth_material_present", response.ClientSecret != ""), tracepkg.String("token_auth_method", method))
	span.EndMessage("OAuth client registration resolved", tracepkg.String("registration", result.Kind), tracepkg.String("client_id", result.ClientID), tracepkg.Bool("client_auth_material_present", result.ClientSecret != ""), tracepkg.String("token_auth_method", result.TokenAuthMethod))
	return result, nil
}

func ValidateIssuerResponse(iss, expected string, advertised bool) error {
	if advertised && iss == "" {
		return errors.New("authorization response is missing required iss parameter")
	}
	if iss != "" && iss != expected {
		return fmt.Errorf("authorization response issuer %q does not match expected issuer %q", iss, expected)
	}
	return nil
}

func ValidateRedirectOrigin(raw string) (string, error) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Host == "" || (value.Scheme != "http" && value.Scheme != "https") {
		return "", errors.New("redirect origin must be an absolute HTTP or HTTPS origin")
	}
	if value.User != nil || value.RawQuery != "" || value.Fragment != "" || (value.Path != "" && value.Path != "/") {
		return "", errors.New("redirect origin must not contain path, query, fragment, or user info")
	}
	if value.Scheme == "http" && !isLoopbackHost(value.Hostname()) {
		return "", errors.New("HTTP OAuth callback origins must use a loopback host; use HTTPS for remote admin access")
	}
	return value.Scheme + "://" + value.Host, nil
}

func validateRedirectURL(raw string) error {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Host == "" || (value.Scheme != "http" && value.Scheme != "https") {
		return errors.New("OAuth redirect URL must be absolute HTTP or HTTPS")
	}
	if value.Scheme == "http" && !isLoopbackHost(value.Hostname()) {
		return errors.New("HTTP OAuth redirect URLs must use a loopback host")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func flowRedirectURL(base, id string) (string, error) {
	value, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	value.Path = strings.TrimRight(value.Path, "/") + "/" + url.PathEscape(id)
	value.RawQuery = ""
	value.Fragment = ""
	return value.String(), nil
}

func buildAuthorizationURL(endpoint, clientID, redirectURL, resource, state, challenge string, scopes []string) (string, error) {
	value, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := value.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURL)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("resource", resource)
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	value.RawQuery = query.Encode()
	return value.String(), nil
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func unionScopes(groups ...[]string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, group := range groups {
		for _, scope := range group {
			scope = strings.TrimSpace(scope)
			if scope == "" || seen[scope] {
				continue
			}
			seen[scope] = true
			result = append(result, scope)
		}
	}
	return result
}

func validateClientMetadataURL(raw string) error {
	value, err := url.Parse(raw)
	if err != nil || value.Scheme != "https" || value.Host == "" || value.Path == "" || value.Path == "/" {
		return errors.New("client ID metadata document URL must be a non-root HTTPS URL")
	}
	return nil
}

func registrationSecret(value registration) (string, error) {
	if value.ClientSecret != "" {
		return value.ClientSecret, nil
	}
	if value.ClientSecretEnvVar == "" {
		return "", nil
	}
	secret := strings.TrimSpace(os.Getenv(value.ClientSecretEnvVar))
	if secret == "" {
		return "", fmt.Errorf("missing OAuth client secret environment variable: %s", value.ClientSecretEnvVar)
	}
	return secret, nil
}

func selectTokenAuthMethod(supported []string, secret string) string {
	if secret == "" {
		return "none"
	}
	if slices.Contains(supported, "client_secret_post") {
		return "client_secret_post"
	}
	if slices.Contains(supported, "client_secret_basic") || len(supported) == 0 {
		return "client_secret_basic"
	}
	return "client_secret_basic"
}
