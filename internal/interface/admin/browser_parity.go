package admin

import (
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

func (api API) handleAuth(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/auth"), "/")
	if path == "" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		api.dispatch(w, r, capability.AuthStatus, nil)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	var operation capability.ID
	switch parts[0] + "/" + parts[1] {
	case "mcp/rotate":
		operation = capability.AuthMCPRotate
	case "mcp/enable":
		operation = capability.AuthMCPEnable
	case "mcp/disable":
		operation = capability.AuthMCPDisable
	case "admin/rotate":
		operation = capability.AuthAdminRotate
	case "admin/enable":
		operation = capability.AuthAdminEnable
	case "admin/disable":
		operation = capability.AuthAdminDisable
	default:
		http.NotFound(w, r)
		return
	}
	api.dispatch(w, r, operation, nil)
}

func (api API) handleSettings(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/settings"), "/")
	if path == "" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		api.dispatch(w, r, capability.ConfigList, application.ConfigListInput{
			Prefix: strings.TrimSpace(r.URL.Query().Get("prefix")),
			Query:  strings.TrimSpace(r.URL.Query().Get("query")),
		})
		return
	}
	if path == "export" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		api.dispatch(w, r, capability.ConfigExport, nil)
		return
	}
	if strings.Contains(path, "/") {
		http.NotFound(w, r)
		return
	}
	key := strings.TrimSpace(path)
	switch r.Method {
	case http.MethodGet:
		api.dispatch(w, r, capability.ConfigGet, application.ConfigGetInput{
			Key: key, Reveal: false, Why: queryBool(r, "why", false),
		})
	case http.MethodPut:
		var input application.ConfigSetInput
		if err := decodeJSONBody(w, r, &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		input.Key = key
		if strings.TrimSpace(input.Action) == "" {
			input.Action = "set"
		}
		if strings.TrimSpace(input.SecretSource) == "" {
			input.SecretSource = "browser-protected-input"
		}
		api.dispatch(w, r, capability.ConfigSet, input)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleTelegramSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input application.TelegramSetupInput
	if err := decodeJSONBody(w, r, &input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.dispatch(w, r, capability.TelegramSetup, input)
}

func (api API) handleTunnelSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	api.dispatch(w, r, capability.TunnelSync, nil)
}

func (api API) handleLogsFollow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	api.handleLogs(w, r)
}

func (api API) handleCompletionDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	health, err := application.CompletionHealth(r.Context(), strings.TrimSpace(r.URL.Query().Get("workspace_id")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, health)
}
