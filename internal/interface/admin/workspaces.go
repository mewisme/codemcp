package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/workspace"
)

type workspaceRequest struct {
	Path string `json:"path"`
}

type workspaceRelocateRequest struct {
	Path string `json:"path"`
}

func (api API) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	manager := api.workspaceManager()
	if manager == nil {
		http.Error(w, "workspace registry unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		values, err := manager.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, values)
	case http.MethodPost:
		var request workspaceRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(request.Path) == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}
		value, err := manager.Register(request.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := api.syncWorkspaceRuntime(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	manager := api.workspaceManager()
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
	value, err := manager.Get(id)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "context" {
		api.handleWorkspaceContext(w, r, manager, value)
		return
	}
	if len(parts) == 2 && parts[1] == "relocate" {
		api.handleWorkspaceRelocate(w, r, manager, value)
		return
	}
	if len(parts) == 2 && parts[1] == "containers" {
		api.handleWorkspaceContainersMembership(w, r, manager, value)
		return
	}
	if len(parts) >= 2 && parts[1] == "executions" {
		api.handleWorkspaceExecutions(w, r, value, parts[2:])
		return
	}
	if len(parts) >= 2 && parts[1] == "processes" {
		api.handleWorkspaceProcesses(w, r, value, parts[2:])
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
		if err := manager.Unregister(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := api.syncWorkspaceRuntime(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleWorkspaceRelocate(w http.ResponseWriter, r *http.Request, manager *workspace.Manager, item workspace.Workspace) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request workspaceRelocateRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.Path) == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	value, err := manager.Relocate(item.ID, request.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := api.syncWorkspaceRuntime(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, value)
}

func (api API) handleWorkspaceContext(w http.ResponseWriter, r *http.Request, manager *workspace.Manager, item workspace.Workspace) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	toolProfile := func() instructioncontext.ToolProfile {
		count := 0
		if api.Tools != nil {
			count = len(api.Tools.List())
		}
		return instructioncontext.ToolProfile{Name: "full", Count: count}
	}
	service := projectcontext.New(manager, toolProfile)
	if api.Config != nil {
		service.Environment = func() (bool, int) {
			cfg := api.Config.Snapshot()
			return cfg.Admin.Enabled, cfg.Admin.Port
		}
	}
	defaults := projectcontext.DefaultOptions()
	result, err := service.Build(r.Context(), item.ID, projectcontext.Options{
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
