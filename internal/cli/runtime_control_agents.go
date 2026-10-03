package cli

import (
	"net/http"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

func registerRuntimeManagedAgentRoutes(mux *http.ServeMux, token string, operations application.OperationDispatcher) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/agents/spawn", authenticatedControl(token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		var input application.ManagedAgentSpawnInput
		if err := decodeControlJSON(r, &input); err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		value, err := dispatchRuntimeOperation(r.Context(), operations, capability.ManagedAgentSpawn, input)
		writeControlJSON(w, value, err)
	}))
	mux.HandleFunc("/agents", authenticatedControl(token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		value, err := dispatchRuntimeOperation(r.Context(), operations, capability.ManagedAgentList, application.ManagedAgentListInput{
			WorkspaceID: query.Get("workspace"),
			Backend:     query.Get("backend"),
			State:       query.Get("state"),
		})
		writeControlJSON(w, value, err)
	}))
	mux.HandleFunc("/agents/get", authenticatedControl(token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		value, err := dispatchRuntimeOperation(r.Context(), operations, capability.ManagedAgentGet, application.ManagedAgentIDInput{AgentID: r.URL.Query().Get("id")})
		writeControlJSON(w, value, err)
	}))
	mux.HandleFunc("/agents/wait", authenticatedControl(token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		var input application.ManagedAgentWaitInput
		if err := decodeControlJSON(r, &input); err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		value, err := dispatchRuntimeOperation(r.Context(), operations, capability.ManagedAgentWait, input)
		writeControlJSON(w, value, err)
	}))
	mux.HandleFunc("/agents/send", authenticatedControl(token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		var input application.ManagedAgentSendInput
		if err := decodeControlJSON(r, &input); err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		value, err := dispatchRuntimeOperation(r.Context(), operations, capability.ManagedAgentSend, input)
		writeControlJSON(w, value, err)
	}))
	mux.HandleFunc("/agents/cancel", authenticatedControl(token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		var input application.ManagedAgentIDInput
		if err := decodeControlJSON(r, &input); err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		value, err := dispatchRuntimeOperation(r.Context(), operations, capability.ManagedAgentCancel, input)
		writeControlJSON(w, value, err)
	}))
}
