package admin

import (
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/application"
)

type workspaceContainerRequest struct {
	Name string `json:"name"`
}

type workspaceContainerMembersRequest struct {
	WorkspaceIDs []string `json:"workspace_ids"`
}

type workspaceContainersMembershipRequest struct {
	ContainerIDs []string `json:"container_ids"`
}

func (api API) handleWorkspaceContainers(w http.ResponseWriter, r *http.Request) {
	operations := api.workspaceOperations()
	if operations.Manager() == nil {
		http.Error(w, "workspace registry unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		result, err := operations.ListContainers(r.Context())
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, result.Value)
	case http.MethodPost:
		var request workspaceContainerRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := operations.CreateContainer(r.Context(), request.Name)
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleWorkspaceContainer(w http.ResponseWriter, r *http.Request) {
	operations := api.workspaceOperations()
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspace-containers/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	result, err := operations.GetContainer(r.Context(), id)
	if err != nil {
		writeWorkspaceOperationError(w, err, http.StatusBadRequest)
		return
	}
	value := result.Value
	if len(parts) == 2 && parts[1] == "workspaces" {
		api.handleWorkspaceContainerMembers(w, r, operations, value.ID)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, value)
	case http.MethodPatch:
		var request workspaceContainerRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		updated, err := operations.RenameContainer(r.Context(), value.ID, request.Name)
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, updated.Value)
	case http.MethodDelete:
		if _, err := operations.DeleteContainer(r.Context(), value.ID); err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleWorkspaceContainerMembers(w http.ResponseWriter, r *http.Request, operations *application.WorkspaceService, containerID string) {
	switch r.Method {
	case http.MethodGet:
		result, err := operations.WorkspacesForContainer(r.Context(), containerID)
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Value)
	case http.MethodPost, http.MethodDelete:
		var request workspaceContainerMembersRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result application.Result[application.WorkspaceContainerView]
		var err error
		if r.Method == http.MethodPost {
			result, err = operations.AddWorkspacesToContainer(r.Context(), containerID, request.WorkspaceIDs)
		} else {
			result, err = operations.RemoveWorkspacesFromContainer(r.Context(), containerID, request.WorkspaceIDs)
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

func (api API) handleWorkspaceContainersMembership(w http.ResponseWriter, r *http.Request, operations *application.WorkspaceService, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		result, err := operations.ContainersForWorkspace(r.Context(), workspaceID)
		if err != nil {
			writeWorkspaceOperationError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Value)
	case http.MethodPost, http.MethodDelete:
		var request workspaceContainersMembershipRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result application.Result[[]application.WorkspaceContainerView]
		var err error
		if r.Method == http.MethodPost {
			result, err = operations.AddWorkspaceToContainers(r.Context(), workspaceID, request.ContainerIDs)
		} else {
			result, err = operations.RemoveWorkspaceFromContainers(r.Context(), workspaceID, request.ContainerIDs)
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
