package llm

import (
	"context"
	"net/http"
	"strings"
)

const maxOpenRouterSupportedParameters = 128

type openRouterModelListResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int    `json:"context_length"`
		Pricing       struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
		SupportedParameters []string `json:"supported_parameters"`
	} `json:"data"`
}

func (c *Client) discoverOpenRouterModels(ctx context.Context, provider Provider) ([]Model, error) {
	raw, err := c.doJSON(ctx, http.MethodGet, provider.BaseURL+"/models", nil, nil)
	if err != nil {
		return nil, err
	}
	var response openRouterModelListResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return nil, err
	}
	if len(response.Data) > MaxDiscoveredModels {
		return nil, NewError(ErrorInvalidResponse, "models", "provider returned too many models")
	}
	models := make([]Model, 0, len(response.Data))
	seen := make(map[string]struct{}, len(response.Data))
	for _, item := range response.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || len(id) > MaxModelIDBytes {
			return nil, NewError(ErrorInvalidResponse, "models", "provider returned an invalid model id")
		}
		name := strings.TrimSpace(item.Name)
		if len(name) > MaxModelNameBytes {
			return nil, NewError(ErrorInvalidResponse, "models", "provider returned an invalid model name")
		}
		if item.ContextLength < 0 {
			return nil, NewError(ErrorInvalidResponse, "models", "provider returned an invalid context length")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		if name == "" {
			name = id
		}
		parameters := normalizeOpenRouterSupportedParameters(item.SupportedParameters)
		models = append(models, Model{
			ID:                       id,
			Name:                     name,
			ContextLength:            item.ContextLength,
			PromptPrice:              strings.TrimSpace(item.Pricing.Prompt),
			CompletionPrice:          strings.TrimSpace(item.Pricing.Completion),
			Free:                     isOpenRouterZeroPrice(item.Pricing.Prompt) && isOpenRouterZeroPrice(item.Pricing.Completion),
			SupportedParameters:      parameters,
			SupportsStructuredOutput: containsOpenRouterParameter(parameters, "structured_outputs"),
		})
	}
	return models, nil
}

func normalizeOpenRouterSupportedParameters(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, min(len(values), maxOpenRouterSupportedParameters))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == maxOpenRouterSupportedParameters {
			break
		}
	}
	return result
}

func containsOpenRouterParameter(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func isOpenRouterZeroPrice(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, char := range value {
		if char != '0' && char != '.' {
			return false
		}
	}
	return true
}
