package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/llm"
)

func (s *LLMService) OllamaEndpointClass(ctx context.Context) (llm.OllamaEndpointClass, error) {
	provider, err := s.Provider(ctx, string(llm.OllamaID))
	if err != nil {
		return "", err
	}
	return llm.ClassifyOllamaEndpoint(provider.BaseURL)
}

func (s *LLMService) SetOllamaMode(ctx context.Context, mode llm.OllamaMode) (llm.Provider, error) {
	if s == nil || s.store == nil {
		return llm.Provider{}, errors.New("LLM service is unavailable")
	}
	var updated llm.Provider
	_, err := s.store.Update(func(catalog llm.Catalog) (llm.Catalog, error) {
		for index := range catalog.Providers {
			if catalog.Providers[index].ID != llm.OllamaID {
				continue
			}
			provider, err := llm.ApplyOllamaMode(catalog.Providers[index], mode)
			if err != nil {
				return llm.Catalog{}, err
			}
			catalog.Providers[index] = provider
			updated = provider
			return catalog, nil
		}
		return llm.Catalog{}, llm.NewError(llm.ErrorProviderNotFound, "id", "Ollama core provider is not registered")
	})
	if err != nil {
		return llm.Provider{}, err
	}
	s.invalidateModelCatalog(llm.OllamaID)
	s.clearReadiness(llm.OllamaID)
	return updated, nil
}

func (s *LLMService) OllamaModels(ctx context.Context) ([]llm.Model, error) {
	return s.ProviderModels(ctx, string(llm.OllamaID))
}

func (s *LLMService) ProbeOllama(ctx context.Context) error {
	err := s.ProbeProvider(ctx, string(llm.OllamaID))
	if err == nil {
		return nil
	}
	if llm.IsCategory(err, llm.ErrorTransport) || llm.IsCategory(err, llm.ErrorTimeout) {
		return llm.NewError(llm.ErrorUnavailable, "", "Ollama endpoint is unavailable")
	}
	return err
}
