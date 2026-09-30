package application

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/llm"
)

const maxLLMModelCatalogCacheEntries = 32

type llmModelCatalogCacheEntry struct {
	fingerprint string
	models      []llm.Model
	access      uint64
}

type llmModelCatalogCache struct {
	mu      sync.Mutex
	entries map[llm.ProviderID]llmModelCatalogCacheEntry
	clock   uint64
}

func newLLMModelCatalogCache() *llmModelCatalogCache {
	return &llmModelCatalogCache{entries: make(map[llm.ProviderID]llmModelCatalogCacheEntry)}
}

func (s *LLMService) ProviderModels(ctx context.Context, rawID string) ([]llm.Model, error) {
	return s.providerModels(ctx, rawID, false)
}

func (s *LLMService) RefreshProviderModels(ctx context.Context, rawID string) ([]llm.Model, error) {
	return s.providerModels(ctx, rawID, true)
}

func (s *LLMService) providerModels(ctx context.Context, rawID string, refresh bool) ([]llm.Model, error) {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return nil, err
	}
	if !refresh && s != nil && s.modelCache != nil {
		if models, ok := s.modelCache.get(provider); ok {
			return models, nil
		}
	}
	models, err := s.llmClient().DiscoverModels(ctx, provider)
	if err != nil {
		return nil, err
	}
	if s != nil && s.modelCache != nil {
		s.modelCache.put(provider, models)
	}
	return cloneLLMModels(models), nil
}

func (s *LLMService) invalidateModelCatalog(id llm.ProviderID) {
	if s == nil || s.modelCache == nil {
		return
	}
	s.modelCache.delete(id)
}

func (s *LLMService) invalidateAllModelCatalogs() {
	if s == nil || s.modelCache == nil {
		return
	}
	s.modelCache.clear()
}

func (c *llmModelCatalogCache) get(provider llm.Provider) ([]llm.Model, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[provider.ID]
	if !ok {
		return nil, false
	}
	if entry.fingerprint != llmModelCatalogFingerprint(provider) {
		delete(c.entries, provider.ID)
		return nil, false
	}
	c.clock++
	entry.access = c.clock
	c.entries[provider.ID] = entry
	return cloneLLMModels(entry.models), true
}

func (c *llmModelCatalogCache) put(provider llm.Provider, models []llm.Model) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[provider.ID]; !exists && len(c.entries) >= maxLLMModelCatalogCacheEntries {
		var oldestID llm.ProviderID
		var oldestAccess uint64
		first := true
		for id, entry := range c.entries {
			if first || entry.access < oldestAccess {
				oldestID = id
				oldestAccess = entry.access
				first = false
			}
		}
		delete(c.entries, oldestID)
	}
	c.clock++
	c.entries[provider.ID] = llmModelCatalogCacheEntry{
		fingerprint: llmModelCatalogFingerprint(provider),
		models:      cloneLLMModels(models),
		access:      c.clock,
	}
}

func (c *llmModelCatalogCache) delete(id llm.ProviderID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}

func (c *llmModelCatalogCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

func llmModelCatalogFingerprint(provider llm.Provider) string {
	structured := false
	if provider.Capabilities != nil {
		structured = provider.Capabilities.StructuredOutput
	}
	return strings.Join([]string{
		string(provider.Protocol),
		provider.BaseURL,
		string(provider.AuthMode),
		string(provider.Discovery),
		string(provider.CoreKind),
		strconv.FormatBool(structured),
	}, "\x00")
}

func cloneLLMModels(models []llm.Model) []llm.Model {
	if models == nil {
		return nil
	}
	result := make([]llm.Model, len(models))
	for index, model := range models {
		result[index] = model
		result[index].SupportedParameters = append([]string(nil), model.SupportedParameters...)
	}
	return result
}
