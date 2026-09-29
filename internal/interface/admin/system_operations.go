package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/logger"
	managed "go.mewis.me/codemcp/internal/service"
)

func (api API) dispatch(w http.ResponseWriter, r *http.Request, operation capability.ID, input any) {
	if api.Operations == nil {
		http.Error(w, "application operation dispatcher unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := api.Operations.Dispatch(r.Context(), application.DispatchRequest{Operation: operation, Input: input})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, result.Value)
}

func (api API) handleStatus(w http.ResponseWriter, r *http.Request) {
	api.dispatch(w, r, capability.StatusOverview, nil)
}
func (api API) handleDoctor(w http.ResponseWriter, r *http.Request) {
	api.dispatch(w, r, capability.DoctorRead, nil)
}

func (api API) handleAbout(w http.ResponseWriter, r *http.Request) {
	value, err := application.LoadAbout(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, value)
}

func (api API) handleConfigPath(w http.ResponseWriter, r *http.Request) {
	value, err := application.ConfigSource(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, value)
}

func (api API) handleConfigVerify(w http.ResponseWriter, r *http.Request) {
	value, err := application.VerifyConfigContext(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, value)
}

func (api API) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Scope managed.Scope `json:"scope,omitempty"`
	}
	if err := decodeJSONBody(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/runtime/")
	value, err := application.ManagedRuntimeAction(r.Context(), action, request.Scope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, value)
}

func (api API) handleLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		tail, err := strconv.Atoi(defaultString(query.Get("tail"), "200"))
		if err != nil || tail < 0 {
			http.Error(w, "tail must be zero or greater", http.StatusBadRequest)
			return
		}
		value, err := application.LoadLogsContext(r.Context(), application.LogsQueryOptions{Tail: tail, All: strings.EqualFold(query.Get("all"), "true"), Since: query.Get("since"), Until: query.Get("until"), Session: query.Get("session"), Level: query.Get("level"), Components: query.Get("components"), Workspace: query.Get("workspace"), Tool: query.Get("tool"), Status: query.Get("status"), Source: query.Get("source"), Event: query.Get("event"), Grep: query.Get("grep")}, logger.VisibilityDefault, 2000, time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, value)
	case http.MethodDelete:
		if err := application.ClearLogs(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]bool{"cleared": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleLogsInfo(w http.ResponseWriter, r *http.Request) {
	value, err := application.LoadLogsInfoContext(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, value)
}

func (api API) handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request application.InstallCurrentOptions
	if err := decodeJSONBody(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value, err := application.InstallCurrentContext(r.Context(), request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, value)
}

func (api API) handleUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		value, err := application.CheckForUpdate(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, value)
	case http.MethodPost:
		var request application.UpdateApplyOptions
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		value, err := application.ApplyUpdate(r.Context(), request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/telemetry"), "/")
	switch {
	case r.Method == http.MethodGet && path == "":
		api.dispatch(w, r, capability.TelemetryStatus, nil)
	case r.Method == http.MethodGet && path == "show":
		api.dispatch(w, r, capability.TelemetryShow, nil)
	case r.Method == http.MethodPost && path == "enable":
		api.dispatch(w, r, capability.TelemetryEnable, nil)
	case r.Method == http.MethodPost && path == "disable":
		api.dispatch(w, r, capability.TelemetryDisable, nil)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
