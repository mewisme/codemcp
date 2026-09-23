package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/upstream"
)

type upstreamToolsResponse struct {
	ServerID     string          `json:"server_id"`
	Tools        []upstream.Tool `json:"tools"`
	ProxiedTools []string        `json:"proxied_tools"`
}

func (api API) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	manager := api.upstreamManager()
	if manager == nil {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		values := manager.List()
		public := make([]upstream.Server, len(values))
		for index, value := range values {
			public[index] = publicUpstream(value)
		}
		writeJSON(w, public)
	case http.MethodPost:
		var server upstream.Server
		if err := decodeJSONBody(w, r, &server); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		normalized, err := upstream.NormalizeServer(server)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, exists := manager.Get(normalized.ID); exists {
			http.Error(w, "upstream server already exists; use PUT to update", http.StatusConflict)
			return
		}
		if err := manager.Add(normalized); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := api.refreshUpstreamProxies(); err != nil {
			http.Error(w, "upstream configuration saved but proxy refresh failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, publicUpstream(normalized))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleUpstream(w http.ResponseWriter, r *http.Request) {
	manager := api.upstreamManager()
	if manager == nil {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/upstream/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	server, exists := manager.Get(id)
	if !exists {
		http.Error(w, "unknown upstream server: "+id, http.StatusNotFound)
		return
	}
	if len(parts) == 1 {
		api.handleUpstreamServer(w, r, manager, server)
		return
	}
	if len(parts) == 3 && parts[1] == "auth" {
		api.handleUpstreamOAuth(w, r, manager, server, parts[2])
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "status":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		writeJSON(w, manager.CheckHealth(ctx, id, queryBool(r, "refresh", true)))
	case "tools":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		values, err := manager.Tools(ctx, id, queryBool(r, "refresh", false))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, upstreamToolsResponse{ServerID: id, Tools: values, ProxiedTools: manager.ProxiedToolNames(server, values)})
	default:
		http.NotFound(w, r)
	}
}

func (api API) handleUpstreamServer(w http.ResponseWriter, r *http.Request, manager *upstream.Manager, server upstream.Server) {
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
		normalized, err := upstream.NormalizeServer(next)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := manager.Add(normalized); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := api.refreshUpstreamProxies(); err != nil {
			http.Error(w, "upstream configuration saved but proxy refresh failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, publicUpstream(normalized))
	case http.MethodDelete:
		if err := manager.Remove(server.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := api.refreshUpstreamProxies(); err != nil {
			http.Error(w, "upstream configuration saved but proxy refresh failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) refreshUpstreamProxies() error {
	manager := api.upstreamManager()
	if api.Tools == nil || manager == nil || api.Tools.Upstream != manager {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return tools.RefreshUpstreamProxies(ctx, api.Tools.Registry, manager, false)
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
