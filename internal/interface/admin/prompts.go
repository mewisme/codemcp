package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/instructioncontext"
)

func (api API) promptService() *application.PromptService {
	return &application.PromptService{Workspaces: api.workspaceManager(), AllowGlobalMutation: func(context.Context) bool { return true }}
}

func promptAPIError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}

func decodePromptBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("prompt body must contain a single JSON value")
		}
		return err
	}
	return nil
}

func (api API) handlePrompts(w http.ResponseWriter, r *http.Request) {
	service := api.promptService()
	switch r.Method {
	case http.MethodGet:
		values, err := service.List(r.URL.Query().Get("workspace_id"))
		if err != nil {
			promptAPIError(w, err)
			return
		}
		writeJSON(w, values)
	case http.MethodPost:
		var request application.PromptWriteRequest
		if err := decodePromptBody(w, r, &request); err != nil {
			promptAPIError(w, err)
			return
		}
		request.Mode = "create"
		value, err := service.Write(r.Context(), request)
		if err != nil {
			promptAPIError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handlePrompt(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	workspaceID := r.URL.Query().Get("workspace_id")
	service := api.promptService()
	scope := instructioncontext.PromptScopeGlobal
	if workspaceID != "" {
		scope = instructioncontext.PromptScopeWorkspace
	}
	switch r.Method {
	case http.MethodGet:
		value, err := service.Definition(workspaceID, name)
		if err != nil {
			promptAPIError(w, err)
			return
		}
		writeJSON(w, value)
	case http.MethodPut:
		var definition instructioncontext.PromptDefinition
		if err := decodePromptBody(w, r, &definition); err != nil {
			promptAPIError(w, err)
			return
		}
		if definition.Name != name {
			promptAPIError(w, errors.New("prompt name cannot change during update"))
			return
		}
		value, err := service.Write(r.Context(), application.PromptWriteRequest{Scope: scope, WorkspaceID: workspaceID, Mode: "update", Definition: definition})
		if err != nil {
			promptAPIError(w, err)
			return
		}
		writeJSON(w, value)
	case http.MethodDelete:
		if strings.TrimSpace(name) == "" {
			promptAPIError(w, errors.New("prompt name is required"))
			return
		}
		if err := service.Delete(r.Context(), application.PromptDeleteRequest{Scope: scope, WorkspaceID: workspaceID, Name: name}); err != nil {
			promptAPIError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true, "name": name, "scope": scope})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
