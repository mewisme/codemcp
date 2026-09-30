package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/upstream"
)

type upstreamToolsResponse struct {
	ServerID     string          `json:"server_id"`
	Tools        []upstream.Tool `json:"tools"`
	ProxiedTools []string        `json:"proxied_tools"`
}

func (api API) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	service := api.upstreamService()
	if service == nil {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		result, err := service.List(r.Context())
		if err != nil {
			writeUpstreamOperationError(w, err, http.StatusInternalServerError)
			return
		}
		public := make([]upstream.Server, len(result.Value))
		for index, value := range result.Value {
			public[index] = publicUpstream(value)
		}
		writeJSON(w, public)
	case http.MethodPost:
		var server upstream.Server
		if err := decodeJSONBody(w, r, &server); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := service.Create(r.Context(), server)
		if err != nil {
			writeUpstreamOperationError(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, publicUpstream(result.Value))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleUpstream(w http.ResponseWriter, r *http.Request) {
	service := api.upstreamService()
	if service == nil {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/upstream/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	result, err := service.Get(r.Context(), id)
	if err != nil {
		writeUpstreamOperationError(w, err, http.StatusNotFound)
		return
	}
	server := result.Value
	if len(parts) == 1 {
		api.handleUpstreamServer(w, r, service, server)
		return
	}
	if len(parts) == 3 && parts[1] == "auth" {
		api.handleUpstreamOAuth(w, r, service.Manager(), server, parts[2])
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "enable", "disable":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var updated application.Result[upstream.Server]
		if parts[1] == "enable" {
			updated, err = service.Enable(r.Context(), id)
		} else {
			updated, err = service.Disable(r.Context(), id)
		}
		if err != nil {
			writeUpstreamOperationError(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, publicUpstream(updated.Value))
	case "status":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		status, err := service.Status(ctx, id, queryBool(r, "refresh", true))
		if err != nil {
			writeUpstreamOperationError(w, err, http.StatusBadGateway)
			return
		}
		writeJSON(w, status.Value)
	case "tools":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		values, err := service.Tools(ctx, id, queryBool(r, "refresh", false))
		if err != nil {
			writeUpstreamOperationError(w, err, http.StatusBadGateway)
			return
		}
		writeJSON(w, upstreamToolsResponse{ServerID: id, Tools: values.Value.Tools, ProxiedTools: values.Value.ProxiedTools})
	default:
		http.NotFound(w, r)
	}
}

func (api API) handleUpstreamServer(w http.ResponseWriter, r *http.Request, service *application.UpstreamService, server upstream.Server) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, publicUpstream(server))
	case http.MethodPut:
		var next upstream.Server
		if err := decodeJSONBody(w, r, &next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(next.ID) != "" && next.ID != server.ID {
			http.Error(w, "upstream id cannot be changed", http.StatusBadRequest)
			return
		}
		next.ID = server.ID
		next.Headers = restoreRedactedMap(server.Headers, next.Headers)
		next.Env = restoreRedactedMap(server.Env, next.Env)
		result, err := service.Update(r.Context(), server.ID, next)
		if err != nil {
			writeUpstreamOperationError(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, publicUpstream(result.Value))
	case http.MethodDelete:
		if _, err := service.Remove(r.Context(), server.ID); err != nil {
			writeUpstreamOperationError(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) upstreamService() *application.UpstreamService {
	manager := api.upstreamManager()
	if manager == nil {
		return nil
	}
	return application.NewUpstreamService(manager, func(ctx context.Context) error {
		if api.Tools == nil || api.Tools.Upstream != manager {
			return nil
		}
		if ctx == nil {
			ctx = context.Background()
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := tools.RefreshUpstreamProxies(refreshCtx, api.Tools.Registry, manager, false); err != nil {
			return fmt.Errorf("proxy refresh failed: %w", err)
		}
		return nil
	})
}

func writeUpstreamOperationError(w http.ResponseWriter, err error, fallback int) {
	status := fallback
	switch application.ErrorCodeOf(err) {
	case application.ErrorInvalidArgument:
		status = http.StatusBadRequest
	case application.ErrorNotFound:
		status = http.StatusNotFound
	case application.ErrorConflict:
		status = http.StatusConflict
	case application.ErrorUnavailable:
		status = http.StatusBadGateway
	}
	http.Error(w, err.Error(), status)
}

func publicUpstream(server upstream.Server) upstream.Server {
	value := server
	value.Headers = redactMap(server.Headers)
	value.Env = redactMap(server.Env)
	return value
}

func restoreRedactedMap(current, next map[string]string) map[string]string {
	if next == nil {
		result := make(map[string]string, len(current))
		for key, value := range current {
			result[key] = value
		}
		return result
	}
	result := make(map[string]string, len(next))
	for key, value := range next {
		if value == "<redacted>" {
			if original, ok := current[key]; ok {
				result[key] = original
				continue
			}
		}
		result[key] = value
	}
	return result
}

func redactMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		if upstream.SensitiveConfigKey(key) {
			result[key] = "<redacted>"
		} else {
			result[key] = value
		}
	}
	return result
}

func queryBool(r *http.Request, key string, fallback bool) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}
