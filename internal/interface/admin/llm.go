package admin

import (
	"net/http"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

type llmProviderCreateRequest struct {
	ID     string                              `json:"id"`
	Config application.CustomLLMProviderConfig `json:"config"`
}

type llmProviderUpdateRequest struct {
	Config *application.CustomLLMProviderConfig `json:"config,omitempty"`
	Model  *string                              `json:"model,omitempty"`
}

type llmCredentialRequest struct {
	APIKey string `json:"api_key"`
}

func (api API) handleLLMStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	api.dispatch(w, r, capability.LLMStatus, nil)
}

func (api API) handleLLMProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.dispatch(w, r, capability.LLMProviderList, nil)
	case http.MethodPost:
		var input llmProviderCreateRequest
		if err := decodeJSONBody(w, r, &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		api.dispatch(w, r, capability.LLMProviderAdd, application.LLMProviderWriteInput{ID: input.ID, Config: input.Config})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleLLMProvider(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/llm/providers/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			api.dispatch(w, r, capability.LLMProviderGet, application.LLMProviderIDInput{ID: id})
		case http.MethodPut:
			api.handleLLMProviderUpdate(w, r, id)
		case http.MethodDelete:
			api.dispatch(w, r, capability.LLMProviderRemove, application.LLMProviderIDInput{ID: id})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "select":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		api.dispatch(w, r, capability.LLMProviderSelect, application.LLMProviderIDInput{ID: id})
	case "probe":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		api.dispatch(w, r, capability.LLMProviderProbe, application.LLMProviderIDInput{ID: id})
	case "models":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		refresh := false
		if raw := strings.TrimSpace(r.URL.Query().Get("refresh")); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				http.Error(w, "invalid refresh value", http.StatusBadRequest)
				return
			}
			refresh = value
		}
		api.dispatch(w, r, capability.LLMProviderModels, application.LLMProviderModelsInput{ID: id, Refresh: refresh})
	case "credential":
		api.handleLLMCredential(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (api API) handleLLMProviderUpdate(w http.ResponseWriter, r *http.Request, id string) {
	var input llmProviderUpdateRequest
	if err := decodeJSONBody(w, r, &input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if (input.Config == nil) == (input.Model == nil) {
		http.Error(w, "exactly one of config or model is required", http.StatusBadRequest)
		return
	}
	operationInput := application.LLMProviderWriteInput{ID: id, Model: input.Model}
	if input.Config != nil {
		operationInput.Config = *input.Config
	}
	api.dispatch(w, r, capability.LLMProviderConfigure, operationInput)
}

func (api API) handleLLMCredential(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPut:
		var input llmCredentialRequest
		if err := decodeJSONBody(w, r, &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		api.dispatch(w, r, capability.LLMProviderCredentialSet, application.LLMProviderCredentialInput{ID: id, APIKey: input.APIKey})
		input.APIKey = ""
	case http.MethodDelete:
		api.dispatch(w, r, capability.LLMProviderCredentialClear, application.LLMProviderIDInput{ID: id})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
