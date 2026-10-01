package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		query, err := parseLLMModelQuery(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		api.dispatch(w, r, capability.LLMProviderModels, application.LLMProviderModelsInput{ID: id, Query: query})
	case "credential":
		api.handleLLMCredential(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func parseLLMModelQuery(r *http.Request) (application.LLMModelQuery, error) {
	values := r.URL.Query()
	query := application.LLMModelQuery{
		Search: values.Get("search"), ExactIDs: values["id"], Authors: values["author"],
		MinPromptPrice: values.Get("min_prompt_price"), MaxPromptPrice: values.Get("max_prompt_price"),
		MinCompletionPrice: values.Get("min_completion_price"), MaxCompletionPrice: values.Get("max_completion_price"),
		Capabilities: values["capability"], Parameters: values["parameter"], InputModalities: values["input"], OutputModalities: values["output"],
		Ollama: application.LLMOllamaModelQuery{Families: values["family"], Formats: values["format"], Quantizations: values["quantization"]},
		Rank:   values.Get("rank"), RankWindow: values.Get("window"), RecommendFor: values.Get("recommend_for"),
	}
	var err error
	if query.Refresh, err = parseOptionalBool(values.Get("refresh"), false); err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid refresh value")
	}
	if query.CheckAccess, err = parseOptionalBool(values.Get("check_access"), false); err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid check_access value")
	}
	if query.All, err = parseOptionalBool(values.Get("all"), false); err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid all value")
	}
	if query.CountOnly, err = parseOptionalBool(values.Get("count"), false); err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid count value")
	}
	free, freeSet, err := parseOptionalBoolPointer(values.Get("free"))
	if err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid free value")
	}
	paid, paidSet, err := parseOptionalBoolPointer(values.Get("paid"))
	if err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid paid value")
	}
	if freeSet && paidSet && *free && *paid {
		return application.LLMModelQuery{}, fmt.Errorf("free and paid filters are mutually exclusive")
	}
	if freeSet {
		query.Free = free
	}
	if paidSet && *paid {
		value := false
		query.Free = &value
	}
	if query.Offset, err = parseOptionalInt(values.Get("offset")); err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid offset value")
	}
	if query.Limit, err = parseOptionalInt(values.Get("limit")); err != nil {
		return application.LLMModelQuery{}, fmt.Errorf("invalid limit value")
	}
	if raw := strings.TrimSpace(values.Get("range")); raw != "" {
		query.Range, err = application.ParseLLMModelRange(raw)
		if err != nil {
			return application.LLMModelQuery{}, err
		}
	}
	for _, target := range []struct {
		name   string
		assign func(int)
	}{
		{"min_context", func(v int) { query.MinContext = &v }}, {"max_context", func(v int) { query.MaxContext = &v }},
	} {
		if raw := strings.TrimSpace(values.Get(target.name)); raw != "" {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil {
				return application.LLMModelQuery{}, fmt.Errorf("invalid %s value", target.name)
			}
			target.assign(value)
		}
	}
	for _, target := range []struct {
		name   string
		assign func(int64)
	}{
		{"min_parameters", func(v int64) { query.Ollama.MinParameterCount = &v }}, {"max_parameters", func(v int64) { query.Ollama.MaxParameterCount = &v }},
		{"min_size", func(v int64) { query.Ollama.MinSizeBytes = &v }}, {"max_size", func(v int64) { query.Ollama.MaxSizeBytes = &v }},
	} {
		if raw := strings.TrimSpace(values.Get(target.name)); raw != "" {
			value, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil {
				return application.LLMModelQuery{}, fmt.Errorf("invalid %s value", target.name)
			}
			target.assign(value)
		}
	}
	for _, raw := range values["sort"] {
		value, parseErr := application.ParseLLMModelSort(raw)
		if parseErr != nil {
			return application.LLMModelQuery{}, parseErr
		}
		query.Sort = append(query.Sort, value)
	}
	for _, target := range []struct {
		name   string
		assign func(*time.Time)
	}{
		{"created_after", func(v *time.Time) { query.CreatedAfter = v }}, {"created_before", func(v *time.Time) { query.CreatedBefore = v }},
		{"modified_after", func(v *time.Time) { query.ModifiedAfter = v }}, {"modified_before", func(v *time.Time) { query.ModifiedBefore = v }},
	} {
		if raw := strings.TrimSpace(values.Get(target.name)); raw != "" {
			value, parseErr := time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				return application.LLMModelQuery{}, fmt.Errorf("invalid %s value", target.name)
			}
			value = value.UTC()
			target.assign(&value)
		}
	}
	return query, nil
}

func parseOptionalBool(raw string, fallback bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseBool(raw)
}

func parseOptionalBoolPointer(raw string) (*bool, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, nil
	}
	value, err := strconv.ParseBool(raw)
	return &value, true, err
}

func parseOptionalInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.Atoi(raw)
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
