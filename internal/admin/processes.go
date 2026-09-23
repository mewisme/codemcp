package admin

import (
	"errors"
	"net/http"
	"strings"

	shellruntime "go.mewis.me/codemcp/internal/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

func (api API) handleWorkspaceProcesses(w http.ResponseWriter, r *http.Request, item workspace.Workspace, parts []string) {
	if api.Tools == nil || api.Tools.Processes == nil {
		http.Error(w, "process manager unavailable", http.StatusServiceUnavailable)
		return
	}
	if len(parts) == 0 {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		values, err := api.Tools.Processes.Status(item.ID, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, values)
		return
	}
	if len(parts) != 1 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(parts[0])
	switch r.Method {
	case http.MethodGet:
		values, err := api.Tools.Processes.Status(item.ID, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if len(values) != 1 {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, values[0])
	case http.MethodDelete:
		if err := api.Tools.Processes.ClearFinished(item.ID, id); err != nil {
			if errors.Is(err, shellruntime.ErrProcessRunning) {
				http.Error(w, err.Error(), http.StatusConflict)
			} else {
				http.Error(w, err.Error(), http.StatusNotFound)
			}
			return
		}
		writeJSON(w, map[string]bool{"deleted": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
