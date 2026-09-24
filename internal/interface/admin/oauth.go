package admin

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/upstream"
)

type oauthLoginRequest struct {
	RedirectOrigin     string `json:"redirect_origin"`
	Issuer             string `json:"issuer,omitempty"`
	ClientID           string `json:"client_id,omitempty"`
	ClientSecretEnvVar string `json:"client_secret_env_var,omitempty"`
	ClientMetadataURL  string `json:"client_metadata_url,omitempty"`
	Scope              string `json:"scope,omitempty"`
}

func (api API) handleUpstreamOAuth(w http.ResponseWriter, r *http.Request, manager *upstream.Manager, server upstream.Server, action string) {
	api = api.withOAuth()
	switch action {
	case "status":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status, err := api.OAuth.Status(server.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, status)
	case "login":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if server.Transport != "http" {
			http.Error(w, "OAuth login requires an HTTP upstream server", http.StatusBadRequest)
			return
		}
		if server.Auth.Type == "none" {
			http.Error(w, "OAuth is disabled for this upstream server", http.StatusBadRequest)
			return
		}
		if hasStaticAuthorization(server) {
			http.Error(w, "server uses static Authorization; remove the static credential before managed OAuth login", http.StatusBadRequest)
			return
		}
		var request oauthLoginRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		origin, err := mcpoauth.ValidateRedirectOrigin(request.RedirectOrigin)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		session, err := api.OAuthFlows.Begin(ctx, mcpoauth.LoginConfig{
			ServerID: server.ID, ServerURL: server.URL, Scope: server.Auth.Scope, Issuer: request.Issuer,
			ClientID: request.ClientID, ClientSecretEnvVar: request.ClientSecretEnvVar, ClientMetadataURL: request.ClientMetadataURL,
		}, strings.TrimRight(origin, "/")+"/oauth/callback", request.Scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, session)
	case "logout":
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := api.OAuth.Delete(server.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if manager != nil {
			_ = manager.Disconnect(server.ID)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (api API) OAuthCallbackHandler() http.Handler {
	api = api.withOAuth()
	return withCanonicalOperation(http.HandlerFunc(api.handleOAuthCallback))
}

func (api API) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	credential, err := api.OAuthFlows.Complete(ctx, strings.TrimPrefix(r.URL.Path, "/oauth/callback/"), query.Get("state"), query.Get("code"), query.Get("iss"), query.Get("error"), query.Get("error_description"))
	if err != nil {
		writeOAuthCallbackPage(w, false, err.Error())
		return
	}
	if manager := api.upstreamManager(); manager != nil {
		_ = manager.Disconnect(credential.ServerID)
	}
	writeOAuthCallbackPage(w, true, "Authorization completed for "+credential.ServerID+".")
}

func (api API) withOAuth() API {
	if api.OAuth == nil {
		api.OAuth = mcpoauth.NewStore(mcpoauth.Path())
	}
	if api.OAuthFlows == nil {
		api.OAuthFlows = mcpoauth.NewFlowManager(api.OAuth)
	}
	return api
}

func hasStaticAuthorization(server upstream.Server) bool {
	if strings.TrimSpace(server.BearerTokenEnvVar) != "" {
		return true
	}
	for key := range server.Headers {
		if strings.EqualFold(key, "Authorization") {
			return true
		}
	}
	return false
}

func writeOAuthCallbackPage(w http.ResponseWriter, ok bool, message string) {
	status := http.StatusOK
	title := "OAuth complete"
	if !ok {
		status = http.StatusBadRequest
		title = "OAuth failed"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head><body><main><h1>%s</h1><p>%s</p></main><script>setTimeout(function(){window.close()},900)</script></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}
