package application

import (
	"context"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/llm"
)

const (
	defaultLLMModelReadLimit = 50
	maxLLMModelReadLimit     = 200
	maxLLMModelSearchBytes   = 128
)

type LLMModelQuery struct {
	Search   string `json:"search,omitempty"`
	FreeOnly bool   `json:"free_only,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type LLMModelPage struct {
	ProviderID llm.ProviderID `json:"provider_id"`
	Models     []llm.Model    `json:"models"`
	Total      int            `json:"total"`
	Truncated  bool           `json:"truncated"`
}

func (s *LLMService) OpenRouterModels(ctx context.Context, query LLMModelQuery) (LLMModelPage, error) {
	provider, err := s.Provider(ctx, string(llm.OpenRouterID))
	if err != nil {
		return LLMModelPage{}, err
	}
	query, err = normalizeLLMModelQuery(query)
	if err != nil {
		return LLMModelPage{}, err
	}
	models, err := s.ProviderModels(ctx, string(provider.ID))
	if err != nil {
		return LLMModelPage{}, err
	}
	filtered := make([]llm.Model, 0, min(len(models), query.Limit))
	search := strings.ToLower(query.Search)
	total := 0
	for _, model := range models {
		if query.FreeOnly && !model.Free {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(model.ID), search) && !strings.Contains(strings.ToLower(model.Name), search) {
			continue
		}
		total++
		if len(filtered) < query.Limit {
			filtered = append(filtered, model)
		}
	}
	return LLMModelPage{
		ProviderID: provider.ID,
		Models:     filtered,
		Total:      total,
		Truncated:  total > len(filtered),
	}, nil
}

func (s *LLMService) ProbeOpenRouter(ctx context.Context) error {
	return s.ProbeProvider(ctx, string(llm.OpenRouterID))
}

func normalizeLLMModelQuery(query LLMModelQuery) (LLMModelQuery, error) {
	query.Search = strings.TrimSpace(query.Search)
	if len(query.Search) > maxLLMModelSearchBytes {
		return LLMModelQuery{}, fmt.Errorf("LLM model search must be at most %d bytes", maxLLMModelSearchBytes)
	}
	if query.Limit < 0 || query.Limit > maxLLMModelReadLimit {
		return LLMModelQuery{}, fmt.Errorf("LLM model limit must be between 0 and %d", maxLLMModelReadLimit)
	}
	if query.Limit == 0 {
		query.Limit = defaultLLMModelReadLimit
	}
	return query, nil
}
