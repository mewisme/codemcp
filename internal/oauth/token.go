package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type tokenResponse struct {
	AccessToken  string      `json:"access_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    json.Number `json:"expires_in"`
	RefreshToken string      `json:"refresh_token"`
	Scope        string      `json:"scope"`
	Error        string      `json:"error"`
	Description  string      `json:"error_description"`
}

func (s *Store) AccessToken(ctx context.Context, config RuntimeConfig) (string, error) {
	ctx = s.traceContext(ctx)
	span := tracepkg.Start(ctx, "OAUTH", "oauth.access.resolve", "Resolving OAuth access", tracepkg.String("server", config.ServerID), tracepkg.URL("server_url", config.ServerURL))
	credential, err := s.Get(config.ServerID)
	if err != nil {
		span.FailMessage("OAuth access resolution failed", errors.New("OAuth authorization is not configured"), tracepkg.Bool("configured", false))
		return "", err
	}
	if credential.ServerURL != config.ServerURL {
		err := fmt.Errorf("%w: stored credential is bound to a different MCP server URL", ErrLoginRequired)
		span.FailMessage("OAuth authorization binding mismatch", errors.New("OAuth authorization binding mismatch"), tracepkg.Bool("configured", true), tracepkg.URL("stored_server_url", credential.ServerURL))
		return "", err
	}
	fresh := credential.AccessToken != "" && (credential.ExpiresAt.IsZero() || time.Now().Add(30*time.Second).Before(credential.ExpiresAt))
	if fresh {
		span.EndMessage("OAuth access resolved from cache", tracepkg.Bool("configured", true), tracepkg.Bool("cache_hit", true), tracepkg.Bool("expires", !credential.ExpiresAt.IsZero()), tracepkg.Bool("refresh_available", credential.RefreshToken != ""))
		return credential.AccessToken, nil
	}
	if credential.RefreshToken == "" {
		err := fmt.Errorf("%w: access token expired and no refresh token is available", ErrLoginRequired)
		span.FailMessage("OAuth access refresh unavailable", errors.New("OAuth refresh authorization is unavailable"), tracepkg.Bool("configured", true), tracepkg.Bool("cache_hit", false), tracepkg.Bool("refresh_available", false))
		return "", err
	}
	secret, err := credentialSecret(credential)
	if err != nil {
		span.FailMessage("OAuth client authentication unavailable", errors.New("OAuth client authentication unavailable"), tracepkg.Bool("configured", true), tracepkg.Bool("refresh_available", true))
		return "", err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
		"resource":      {credential.Resource},
	}
	if len(credential.Scopes) > 0 {
		form.Set("scope", strings.Join(credential.Scopes, " "))
	}
	refreshSpan := tracepkg.Start(ctx, "OAUTH", "oauth.access.refresh", "Refreshing OAuth access", tracepkg.String("server", config.ServerID), tracepkg.URL("token_endpoint", credential.TokenEndpoint), tracepkg.String("token_auth_method", credential.TokenAuthMethod), tracepkg.Any("scopes", append([]string(nil), credential.Scopes...)), tracepkg.Int("scope_count", len(credential.Scopes)))
	response, err := s.requestToken(ctx, credential.TokenEndpoint, credential.TokenAuthMethod, credential.ClientID, secret, form, credential.ServerURL, credential.Issuer)
	if err != nil {
		refreshSpan.FailMessage("OAuth access refresh failed", errors.New("OAuth token refresh request failed"))
		span.FailMessage("OAuth access refresh failed", errors.New("OAuth access refresh failed"), tracepkg.Bool("cache_hit", false), tracepkg.Bool("refresh_available", true))
		return "", fmt.Errorf("%w: refresh access token: %v", ErrLoginRequired, err)
	}
	updated, err := applyTokenResponse(credential, response)
	if err != nil {
		refreshSpan.FailMessage("OAuth refresh response validation failed", err)
		span.FailMessage("OAuth access refresh failed", errors.New("OAuth refresh response invalid"))
		return "", err
	}
	if response.RefreshToken == "" {
		updated.RefreshToken = credential.RefreshToken
	}
	if response.Scope == "" {
		updated.Scopes = append([]string(nil), credential.Scopes...)
	}
	if err := s.Put(updated); err != nil {
		refreshSpan.FailMessage("OAuth refreshed authorization persistence failed", errors.New("OAuth refresh persistence failed"))
		span.FailMessage("OAuth access refresh persistence failed", errors.New("OAuth refresh persistence failed"))
		return "", err
	}
	refreshSpan.EndMessage("OAuth access refreshed", tracepkg.Bool("refresh_rotated", response.RefreshToken != ""), tracepkg.Any("scopes", append([]string(nil), updated.Scopes...)), tracepkg.Bool("expires", !updated.ExpiresAt.IsZero()))
	span.EndMessage("OAuth access resolved after refresh", tracepkg.Bool("configured", true), tracepkg.Bool("cache_hit", false), tracepkg.Bool("refresh_available", true), tracepkg.Bool("expires", !updated.ExpiresAt.IsZero()))
	return updated.AccessToken, nil
}

func (s *Store) requestToken(ctx context.Context, endpoint, authMethod, clientID, clientSecret string, form url.Values, trustedOrigins ...string) (tokenResponse, error) {
	ctx = s.traceContext(ctx)
	grantType := form.Get("grant_type")
	scopes := strings.Fields(form.Get("scope"))
	span := tracepkg.Start(ctx, "OAUTH", "oauth.token.request", "Requesting OAuth token", tracepkg.URL("token_endpoint", endpoint), tracepkg.String("grant_type", grantType), tracepkg.String("token_auth_method", normalizeTokenAuthMethod(authMethod, clientSecret)), tracepkg.Any("scopes", scopes), tracepkg.Int("scope_count", len(scopes)), tracepkg.URL("resource", form.Get("resource")))
	if endpoint == "" {
		err := errors.New("token endpoint is required")
		span.FailMessage("OAuth token request validation failed", err)
		return tokenResponse{}, err
	}
	if err := validateOutboundURL(ctx, endpoint, trustedOrigins...); err != nil {
		wrapped := fmt.Errorf("token endpoint denied: %w", err)
		span.FailMessage("OAuth token endpoint denied by network policy", wrapped)
		return tokenResponse{}, wrapped
	}
	requestForm := cloneValues(form)
	method := normalizeTokenAuthMethod(authMethod, clientSecret)
	switch method {
	case "client_secret_basic":
	case "client_secret_post":
		requestForm.Set("client_id", clientID)
		requestForm.Set("client_secret", clientSecret)
	case "none":
		requestForm.Set("client_id", clientID)
	default:
		err := fmt.Errorf("unsupported token auth method: %s", method)
		span.FailMessage("OAuth token authentication method unsupported", err)
		return tokenResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(requestForm.Encode()))
	if err != nil {
		span.FailMessage("OAuth token request construction failed", err)
		return tokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if method == "client_secret_basic" {
		request.SetBasicAuth(clientID, clientSecret)
	}
	response, err := tracepkg.DoHTTP(s.clientForTargets(trustedOrigins...), request)
	if err != nil {
		span.FailMessage("OAuth token HTTP request failed", errors.New("OAuth token HTTP request failed"))
		return tokenResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		span.FailMessage("OAuth token response read failed", errors.New("OAuth token response read failed"), tracepkg.Int("status", response.StatusCode))
		return tokenResponse{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var value tokenResponse
	if err := decoder.Decode(&value); err != nil {
		wrapped := fmt.Errorf("decode token response: %w", err)
		span.FailMessage("OAuth token response decode failed", errors.New("OAuth token response decode failed"), tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(body))))
		return tokenResponse{}, wrapped
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || value.Error != "" {
		message := value.Error
		if message == "" {
			message = response.Status
		}
		if value.Description != "" {
			message += ": " + value.Description
		}
		span.FailMessage("OAuth token endpoint rejected request", errors.New("OAuth token request rejected"), tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(body))))
		return tokenResponse{}, errors.New(message)
	}
	if value.AccessToken == "" {
		err := errors.New("token response is missing access_token")
		span.FailMessage("OAuth token response missing access token", err, tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(body))))
		return tokenResponse{}, err
	}
	expiresIn := int64(0)
	if value.ExpiresIn != "" {
		expiresIn, _ = value.ExpiresIn.Int64()
	}
	responseScopes := strings.Fields(value.Scope)
	span.EndMessage("OAuth token received", tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(body))), tracepkg.String("token_type", value.TokenType), tracepkg.Int64("expires_in_seconds", expiresIn), tracepkg.Any("scopes", responseScopes), tracepkg.Int("scope_count", len(responseScopes)), tracepkg.Bool("has_refresh", value.RefreshToken != ""))
	return value, nil
}

func applyTokenResponse(credential Credential, response tokenResponse) (Credential, error) {
	credential.AccessToken = response.AccessToken
	credential.TokenType = response.TokenType
	credential.RefreshToken = response.RefreshToken
	credential.ExpiresAt = time.Time{}
	if response.ExpiresIn != "" {
		seconds, err := response.ExpiresIn.Int64()
		if err != nil || seconds < 0 {
			return Credential{}, errors.New("token response has invalid expires_in")
		}
		if seconds > 0 {
			credential.ExpiresAt = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
		}
	}
	if response.Scope != "" {
		credential.Scopes = strings.Fields(response.Scope)
	}
	return credential, nil
}

func credentialSecret(credential Credential) (string, error) {
	if credential.ClientSecret != "" {
		return credential.ClientSecret, nil
	}
	if credential.ClientSecretEnvVar == "" {
		return "", nil
	}
	value := strings.TrimSpace(os.Getenv(credential.ClientSecretEnvVar))
	if value == "" {
		return "", fmt.Errorf("%w: missing OAuth client secret environment variable %s", ErrLoginRequired, credential.ClientSecretEnvVar)
	}
	return value, nil
}

func normalizeTokenAuthMethod(method, secret string) string {
	switch method {
	case "client_secret_basic", "client_secret_post", "none":
		return method
	}
	if secret != "" {
		return "client_secret_basic"
	}
	return "none"
}

func cloneValues(value url.Values) url.Values {
	result := make(url.Values, len(value))
	for key, items := range value {
		result[key] = append([]string(nil), items...)
	}
	return result
}
