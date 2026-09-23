package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type Discovery struct {
	Resource        string
	Issuer          string
	RequestedScopes []string
	ResourceMeta    *oauthex.ProtectedResourceMetadata
	AuthServerMeta  *oauthex.AuthServerMeta
}

type metadataCandidate struct {
	URL      string
	Resource string
}

func (s *Store) ProbeWWWAuthenticate(ctx context.Context, serverURL string) ([]string, error) {
	ctx = s.traceContext(ctx)
	span := tracepkg.Start(ctx, "OAUTH", "oauth.challenge.probe", "Probing MCP OAuth challenge", tracepkg.URL("server_url", serverURL))
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
		"params": map[string]any{"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "chatgpt-mcp", "version": "1.0.0"},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}},
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(body))
	if err != nil {
		span.FailMessage("MCP OAuth challenge probe failed", err)
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", "server/discover")
	response, err := tracepkg.DoHTTP(s.clientForTargets(serverURL), request)
	if err != nil {
		span.FailMessage("MCP OAuth challenge probe failed", err)
		return nil, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	challenges := append([]string(nil), response.Header.Values("WWW-Authenticate")...)
	span.EndMessage("MCP OAuth challenge probed", tracepkg.Int("status", response.StatusCode), tracepkg.Int("challenge_count", len(challenges)))
	return challenges, nil
}

func (s *Store) Discover(ctx context.Context, serverURL, issuerPreference string, challengeHeaders []string) (*Discovery, error) {
	ctx = s.traceContext(ctx)
	span := tracepkg.Start(ctx, "OAUTH", "oauth.discovery", "Discovering OAuth metadata", tracepkg.URL("server_url", serverURL), tracepkg.Bool("issuer_preference", strings.TrimSpace(issuerPreference) != ""), tracepkg.Int("challenge_count", len(challengeHeaders)))
	fail := func(message string, err error, fields ...tracepkg.Field) (*Discovery, error) {
		span.FailMessage(message, err, fields...)
		return nil, err
	}
	challenges, err := oauthex.ParseWWWAuthenticate(challengeHeaders)
	if err != nil {
		wrapped := fmt.Errorf("parse WWW-Authenticate: %w", err)
		return fail("OAuth challenge parsing failed", wrapped)
	}
	candidates, err := protectedResourceCandidates(serverURL, challengeResourceMetadata(challenges))
	if err != nil {
		return fail("OAuth protected-resource candidate resolution failed", err)
	}
	tracepkg.Emit(ctx, "OAUTH", "oauth.discovery.candidates", "OAuth protected-resource metadata candidates resolved", tracepkg.Int("candidate_count", len(candidates)))
	var resourceMeta *oauthex.ProtectedResourceMetadata
	var lastErr error
	for index, candidate := range candidates {
		candidateSpan := tracepkg.Start(ctx, "OAUTH", "oauth.resource-metadata.fetch", "Fetching OAuth protected-resource metadata", tracepkg.Int("candidate_index", index), tracepkg.URL("metadata_url", candidate.URL), tracepkg.URL("resource", candidate.Resource))
		if err := validateOutboundURL(ctx, candidate.URL, serverURL); err != nil {
			lastErr = err
			candidateSpan.FailMessage("OAuth protected-resource metadata candidate denied", errors.New("OAuth metadata candidate denied"))
			continue
		}
		value, fetchErr := oauthex.GetProtectedResourceMetadata(ctx, candidate.URL, candidate.Resource, s.clientForTargets(serverURL))
		if fetchErr != nil {
			lastErr = fetchErr
			candidateSpan.FailMessage("OAuth protected-resource metadata fetch failed", errors.New("OAuth metadata request failed"))
			continue
		}
		if value == nil {
			candidateSpan.EndMessage("OAuth protected-resource metadata not found", tracepkg.Bool("found", false))
			continue
		}
		resourceMeta = value
		candidateSpan.EndMessage("OAuth protected-resource metadata loaded", tracepkg.Bool("found", true), tracepkg.Int("authorization_server_count", len(value.AuthorizationServers)), tracepkg.Int("scope_count", len(value.ScopesSupported)))
		break
	}
	if resourceMeta == nil {
		if lastErr != nil {
			wrapped := fmt.Errorf("protected resource metadata discovery failed: %w", lastErr)
			span.FailMessage("OAuth protected-resource metadata discovery failed", errors.New("OAuth protected-resource metadata discovery failed"), tracepkg.Int("candidate_count", len(candidates)))
			return nil, wrapped
		}
		return fail("OAuth protected-resource metadata not found", errors.New("protected resource metadata not found"), tracepkg.Int("candidate_count", len(candidates)))
	}
	if len(resourceMeta.AuthorizationServers) == 0 {
		return fail("OAuth protected-resource metadata invalid", errors.New("protected resource metadata has no authorization_servers"))
	}
	issuer := resourceMeta.AuthorizationServers[0]
	if strings.TrimSpace(issuerPreference) != "" {
		issuer = strings.TrimSpace(issuerPreference)
		if !slices.Contains(resourceMeta.AuthorizationServers, issuer) {
			err := fmt.Errorf("configured issuer %q is not advertised by the protected resource", issuer)
			return fail("Configured OAuth issuer is not advertised", err, tracepkg.URL("issuer", issuer))
		}
	}
	if err := validateOutboundURL(ctx, issuer, serverURL); err != nil {
		wrapped := fmt.Errorf("authorization server URL denied: %w", err)
		return fail("OAuth authorization server denied by network policy", wrapped, tracepkg.URL("issuer", issuer))
	}
	authSpan := tracepkg.Start(ctx, "OAUTH", "oauth.authorization-metadata.fetch", "Fetching OAuth authorization-server metadata", tracepkg.URL("issuer", issuer))
	authMeta, err := mcpauth.GetAuthServerMetadata(ctx, issuer, s.clientForTargets(serverURL, issuer))
	if err != nil {
		authSpan.FailMessage("OAuth authorization-server metadata fetch failed", errors.New("OAuth authorization metadata request failed"))
		wrapped := fmt.Errorf("authorization server metadata discovery failed: %w", err)
		span.FailMessage("OAuth authorization-server metadata discovery failed", errors.New("OAuth authorization-server metadata discovery failed"), tracepkg.URL("issuer", issuer))
		return nil, wrapped
	}
	if authMeta == nil {
		err := errors.New("authorization server metadata not found")
		authSpan.FailMessage("OAuth authorization-server metadata not found", err)
		return fail("OAuth authorization-server metadata not found", err, tracepkg.URL("issuer", issuer))
	}
	authSpan.EndMessage("OAuth authorization-server metadata loaded", tracepkg.URL("authorization_endpoint", authMeta.AuthorizationEndpoint), tracepkg.URL("token_endpoint", authMeta.TokenEndpoint), tracepkg.Bool("registration_endpoint", authMeta.RegistrationEndpoint != ""), tracepkg.Int("scope_count", len(authMeta.ScopesSupported)))
	if !slices.Contains(authMeta.CodeChallengeMethodsSupported, "S256") {
		return fail("OAuth authorization server lacks PKCE S256", errors.New("authorization server does not advertise PKCE S256"), tracepkg.URL("issuer", issuer))
	}
	for name, endpoint := range map[string]string{
		"authorization": authMeta.AuthorizationEndpoint,
		"token":         authMeta.TokenEndpoint,
		"registration":  authMeta.RegistrationEndpoint,
	} {
		if endpoint == "" {
			continue
		}
		if err := validateOutboundURL(ctx, endpoint, serverURL, issuer); err != nil {
			wrapped := fmt.Errorf("%s endpoint denied: %w", name, err)
			return fail("OAuth endpoint denied by network policy", wrapped, tracepkg.String("endpoint_kind", name), tracepkg.URL("endpoint", endpoint))
		}
	}
	scopes := challengeScopes(challenges)
	scopeSource := "challenge"
	if len(scopes) == 0 {
		scopes = append([]string(nil), resourceMeta.ScopesSupported...)
		scopeSource = "resource_metadata"
	}
	result := &Discovery{
		Resource: resourceMeta.Resource, Issuer: issuer, RequestedScopes: scopes,
		ResourceMeta: resourceMeta, AuthServerMeta: authMeta,
	}
	span.EndMessage("OAuth metadata discovered", tracepkg.URL("resource", result.Resource), tracepkg.URL("issuer", result.Issuer), tracepkg.URL("authorization_endpoint", authMeta.AuthorizationEndpoint), tracepkg.URL("token_endpoint", authMeta.TokenEndpoint), tracepkg.Bool("registration_endpoint", authMeta.RegistrationEndpoint != ""), tracepkg.Any("requested_scopes", append([]string(nil), scopes...)), tracepkg.String("scope_source", scopeSource), tracepkg.Bool("pkce_s256", true))
	return result, nil
}

func protectedResourceCandidates(serverURL, challenged string) ([]metadataCandidate, error) {
	resource, err := url.Parse(serverURL)
	if err != nil || resource.Scheme == "" || resource.Host == "" {
		return nil, fmt.Errorf("invalid MCP server URL: %s", serverURL)
	}
	resource.Fragment = ""
	serverResource := resource.String()
	result := make([]metadataCandidate, 0, 3)
	seen := map[string]bool{}
	add := func(metadataURL, expectedResource string) {
		key := metadataURL + "\x00" + expectedResource
		if metadataURL == "" || seen[key] {
			return
		}
		seen[key] = true
		result = append(result, metadataCandidate{URL: metadataURL, Resource: expectedResource})
	}
	add(challenged, serverResource)
	pathMeta := *resource
	pathMeta.RawQuery = ""
	pathMeta.Path = "/.well-known/oauth-protected-resource/" + strings.TrimLeft(resource.Path, "/")
	add(pathMeta.String(), serverResource)
	rootResource := *resource
	rootResource.Path = ""
	rootResource.RawPath = ""
	rootResource.RawQuery = ""
	rootResource.Fragment = ""
	rootMeta := rootResource
	rootMeta.Path = "/.well-known/oauth-protected-resource"
	add(rootMeta.String(), rootResource.String())
	return result, nil
}

func challengeResourceMetadata(challenges []oauthex.Challenge) string {
	for _, challenge := range challenges {
		if value := challenge.Params["resource_metadata"]; value != "" {
			return value
		}
	}
	return ""
}

func challengeScopes(challenges []oauthex.Challenge) []string {
	for _, challenge := range challenges {
		if challenge.Scheme == "bearer" {
			return strings.Fields(challenge.Params["scope"])
		}
	}
	return nil
}
