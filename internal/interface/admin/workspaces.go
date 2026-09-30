package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/workspace"
)

type workspaceRequest struct {
	Path string `json:"path"`
}

type workspaceRelocateRequest struct {
	Path       string                         `json:"path"`
	Resolution workspace.RelocationResolution `json:"resolution,omitempty"`
}

type workspacePurgeRequest struct {
	Confirm bool `json:"confirm"`
}

func (api API) workspaceOperations() *application.WorkspaceService {
	manager := api.workspaceManager()
	return application.NewWorkspaceService(manager, func(context.Context) error {
		if api.Tools == nil || api.Tools.Workspaces == nil {
			return nil
		}
		return api.Tools.ReloadWorkspaces()
	})
}

func (api API) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	operations := api.workspaceOperations()
	if operations.Manager() == nil {
		http.Error(w, "workspace registry unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		result, err := operations.List(r.Context())
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, result.Value)
	case http.MethodPost:
		var request workspaceRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := operations.Register(r.Context(), request.Path)
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	operations := api.workspaceOperations()
	manager := operations.Manager()
	if manager == nil {
		http.Error(w, "workspace registry unavailable", http.StatusServiceUnavailable)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspaces/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	result, err := operations.Get(r.Context(), id)
	if err != nil {
		writeWorkspaceOperationError(w, err, http.StatusInternalServerError)
		return
	}
	value := result.Value
	if len(parts) == 2 && parts[1] == "context" {
		api.handleWorkspaceContext(w, r, manager, value.ID)
		return
	}
	if len(parts) == 2 && parts[1] == "relocate" {
		api.handleWorkspaceRelocate(w, r, operations, value.ID)
		return
	}
	if len(parts) == 2 && parts[1] == "access" {
		api.handleWorkspaceAccess(w, r, operations, value.ID)
		return
	}
	if len(parts) == 2 && parts[1] == "purge" {
		api.handleWorkspacePurge(w, r, operations, value.ID)
		return
	}
	if len(parts) == 2 && parts[1] == "containers" {
		api.handleWorkspaceContainersMembership(w, r, operations, value.ID)
		return
	}
	if len(parts) >= 2 && parts[1] == "executions" {
		api.handleWorkspaceExecutions(w, r, value.ID, parts[2:])
		return
	}
	if len(parts) >= 2 && parts[1] == "processes" {
		api.handleWorkspaceProcesses(w, r, value.ID, parts[2:])
		return
	}
	if len(parts) >= 3 && parts[1] == "integrations" && parts[2] == "codegraph" {
		api.handleCodeGraphWorkspace(w, r, value.ID, parts[1:])
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, value)
	case http.MethodDelete:
		if _, err := operations.Unregister(r.Context(), value.ID); err != nil {
			writeWorkspaceOperationError(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleWorkspaceRelocate(w http.ResponseWriter, r *http.Request, operations *application.WorkspaceService, workspaceID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request workspaceRelocateRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := operations.Relocate(r.Context(), application.WorkspaceRelocateRequest{
		ID:         workspaceID,
		Path:       request.Path,
		Resolution: request.Resolution,
	})
	if err != nil {
		if conflict, ok := application.WorkspaceRelocationConflictOf(err); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, conflict)
			return
		}
		writeWorkspaceOperationError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, result.Value.After)
}

func (api API) handleWorkspaceAccess(w http.ResponseWriter, r *http.Request, operations *application.WorkspaceService, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		result, err := operations.AccessList(r.Context(), workspaceID)
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Value)
	case http.MethodPost, http.MethodDelete:
		var request workspaceRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var (
			result application.Result[application.WorkspaceView]
			err    error
		)
		if r.Method == http.MethodPost {
			result, err = operations.AddAllowDir(r.Context(), workspaceID, request.Path)
		} else {
			result, err = operations.RemoveAllowDir(r.Context(), workspaceID, request.Path)
		}
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleWorkspacePurge(w http.ResponseWriter, r *http.Request, operations *application.WorkspaceService, workspaceID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request workspacePurgeRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := operations.Purge(r.Context(), workspaceID, request.Confirm)
	if err != nil {
		writeWorkspaceOperationError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, result.Value)
}

func (api API) handleWorkspaceContext(w http.ResponseWriter, r *http.Request, manager *workspace.Manager, workspaceID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	service := application.NewProjectContextService(r.Context(), manager)
	defaults := projectcontext.DefaultOptions()
	result, err := service.Build(r.Context(), workspaceID, projectcontext.Options{
		Path:                strings.TrimSpace(r.URL.Query().Get("path")),
		MemoryQuery:         strings.TrimSpace(r.URL.Query().Get("memory_query")),
		MaxMemoryEntries:    queryInt(r, "max_memory_entries", defaults.MaxMemoryEntries, projectcontext.MinMemoryEntries, projectcontext.MaxMemoryEntries),
		MaxMemoryBytes:      queryInt(r, "max_memory_bytes", defaults.MaxMemoryBytes, projectcontext.MinMemoryBytes, projectcontext.MaxMemoryBytes),
		MaxInstructionBytes: queryInt(r, "max_instruction_bytes", defaults.MaxInstructionBytes, projectcontext.MinInstructionBytes, projectcontext.MaxInstructionBytes),
		MaxSectionBytes:     queryInt(r, "max_section_bytes", defaults.MaxSectionBytes, projectcontext.MinSectionBytes, projectcontext.MaxSectionBytes),
		MaxLinesPerSection:  queryInt(r, "max_lines_per_section", defaults.MaxLinesPerSection, projectcontext.MinLinesPerSection, projectcontext.MaxLinesPerSection),
		IncludeGit:          queryBool(r, "include_git", defaults.IncludeGit),
		IncludeMemory:       queryBool(r, "include_memory", defaults.IncludeMemory),
		IncludeSkills:       queryBool(r, "include_skills", defaults.IncludeSkills),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, result)
}

func writeWorkspaceOperationError(w http.ResponseWriter, err error, fallback int) {
	status := fallback
	switch application.ErrorCodeOf(err) {
	case application.ErrorInvalidArgument:
		status = http.StatusBadRequest
	case application.ErrorNotFound:
		status = http.StatusNotFound
	case application.ErrorConflict:
		status = http.StatusConflict
	case application.ErrorUnavailable:
		status = http.StatusInternalServerError
	}
	http.Error(w, err.Error(), status)
}

func queryInt(r *http.Request, key string, fallback, min, max int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return fallback
	}
	return value
}
