package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/llm"
)

const (
	llmModelAccessTTL         = 5 * time.Minute
	llmModelAccessConcurrency = 4
	llmModelAccessTimeout     = 12 * time.Second
)

type llmModelAccessCacheEntry struct {
	fingerprint string
	checkedAt   time.Time
	expiresAt   time.Time
	models      map[string]LLMModelAccessResult
}

type llmModelAccessCache struct {
	mu      sync.RWMutex
	entries map[llm.ProviderID]llmModelAccessCacheEntry
}

func newLLMModelAccessCache() *llmModelAccessCache {
	return &llmModelAccessCache{entries: map[llm.ProviderID]llmModelAccessCacheEntry{}}
}

func (cache *llmModelAccessCache) get(provider llm.Provider, models []llm.Model, now time.Time) (map[string]LLMModelAccessResult, time.Time, bool) {
	if cache == nil {
		return nil, time.Time{}, false
	}
	cache.mu.RLock()
	entry, ok := cache.entries[provider.ID]
	cache.mu.RUnlock()
	if !ok || entry.fingerprint != llmModelAccessFingerprint(provider) || !now.Before(entry.expiresAt) || !llmModelAccessCovers(entry.models, models) {
		return nil, time.Time{}, false
	}
	return cloneLLMModelAccess(entry.models), entry.checkedAt, true
}

func (cache *llmModelAccessCache) put(provider llm.Provider, models map[string]LLMModelAccessResult, checkedAt time.Time) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	cache.entries[provider.ID] = llmModelAccessCacheEntry{
		fingerprint: llmModelAccessFingerprint(provider),
		checkedAt:   checkedAt,
		expiresAt:   checkedAt.Add(llmModelAccessTTL),
		models:      cloneLLMModelAccess(models),
	}
	cache.mu.Unlock()
}

func (cache *llmModelAccessCache) observe(provider llm.Provider, model string, observation LLMModelAccessResult) {
	if cache == nil || strings.TrimSpace(model) == "" {
		return
	}
	now := observation.CheckedAt
	if now.IsZero() {
		now = time.Now().UTC()
		observation.CheckedAt = now
	}
	cache.mu.Lock()
	entry, ok := cache.entries[provider.ID]
	fingerprint := llmModelAccessFingerprint(provider)
	if !ok || entry.fingerprint != fingerprint || !now.Before(entry.expiresAt) {
		entry = llmModelAccessCacheEntry{
			fingerprint: fingerprint,
			checkedAt:   now,
			expiresAt:   now.Add(llmModelAccessTTL),
			models:      map[string]LLMModelAccessResult{},
		}
	}
	if entry.models == nil {
		entry.models = map[string]LLMModelAccessResult{}
	}
	entry.models[strings.TrimSpace(model)] = observation
	cache.entries[provider.ID] = entry
	cache.mu.Unlock()
}

func (cache *llmModelAccessCache) snapshot(provider llm.Provider, now time.Time) map[string]LLMModelAccessResult {
	if cache == nil {
		return nil
	}
	cache.mu.RLock()
	entry, ok := cache.entries[provider.ID]
	cache.mu.RUnlock()
	if !ok || entry.fingerprint != llmModelAccessFingerprint(provider) || !now.Before(entry.expiresAt) {
		return nil
	}
	return cloneLLMModelAccess(entry.models)
}

func (cache *llmModelAccessCache) delete(id llm.ProviderID) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	delete(cache.entries, id)
	cache.mu.Unlock()
}

func (s *LLMService) modelAccess(ctx context.Context, provider llm.Provider, models []llm.Model, force bool) (map[string]LLMModelAccessResult, time.Time, error) {
	now := time.Now().UTC()
	if !force && s != nil && s.accessCache != nil {
		if cached, checkedAt, ok := s.accessCache.get(provider, models, now); ok {
			return cached, checkedAt, nil
		}
	}
	if len(models) == 0 {
		return map[string]LLMModelAccessResult{}, now, nil
	}

	type result struct {
		model string
		value LLMModelAccessResult
		err   error
		fatal bool
	}
	workers := min(llmModelAccessConcurrency, len(models))
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan string)
	results := make(chan result, len(models))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for model := range jobs {
				value, err, fatal := s.probeModelAccess(probeCtx, provider, model)
				results <- result{model: model, value: value, err: err, fatal: fatal}
				if fatal {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, model := range models {
			select {
			case jobs <- model.ID:
			case <-probeCtx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	observations := make(map[string]LLMModelAccessResult, len(models))
	var scanErr error
	for item := range results {
		observations[item.model] = item.value
		if item.fatal && (scanErr == nil || llm.IsCategory(scanErr, llm.ErrorCancelled) && !llm.IsCategory(item.err, llm.ErrorCancelled)) {
			scanErr = item.err
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return observations, now, ctx.Err()
	}
	if scanErr != nil {
		return observations, now, scanErr
	}
	if s != nil && s.accessCache != nil {
		s.accessCache.put(provider, observations, now)
	}
	return observations, now, nil
}

func (s *LLMService) probeModelAccess(ctx context.Context, provider llm.Provider, model string) (LLMModelAccessResult, error, bool) {
	checkedAt := time.Now().UTC()
	selected := provider
	selected.Model = strings.TrimSpace(model)
	probeCtx, cancel := context.WithTimeout(ctx, llmModelAccessTimeout)
	defer cancel()
	_, err := s.llmClient().Infer(probeCtx, selected, llm.Request{
		Messages:        []llm.Message{{Role: llm.RoleUser, Content: "Reply OK."}},
		MaxOutputTokens: 1,
	})
	if err == nil {
		return LLMModelAccessResult{State: LLMModelAccessAvailable, CheckedAt: checkedAt}, nil, false
	}
	value, ok := llm.AsError(err)
	if !ok {
		return LLMModelAccessResult{State: LLMModelAccessUnknown, Reason: "model access check failed", CheckedAt: checkedAt}, err, false
	}
	result := LLMModelAccessResult{State: LLMModelAccessUnknown, ErrorCategory: value.Category, Reason: value.Detail, CheckedAt: checkedAt}
	switch value.Category {
	case llm.ErrorProvider, llm.ErrorInvalidRequest:
		result.State = LLMModelAccessUnavailable
		return result, nil, false
	case llm.ErrorUnauthorized, llm.ErrorRateLimited, llm.ErrorMisconfigured, llm.ErrorCancelled:
		return result, err, true
	default:
		if strings.TrimSpace(result.Reason) == "" {
			result.Reason = llmReadinessReason(err)
		}
		return result, nil, false
	}
}

func (s *LLMService) observeModelAccess(provider llm.Provider, model string, state LLMModelAccessState, err error) {
	if s == nil || s.accessCache == nil {
		return
	}
	observation := LLMModelAccessResult{State: state, CheckedAt: time.Now().UTC()}
	if value, ok := llm.AsError(err); ok {
		observation.ErrorCategory = value.Category
		observation.Reason = value.Detail
	}
	s.accessCache.observe(provider, model, observation)
}

func llmModelAccessFingerprint(provider llm.Provider) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s", provider.ID, provider.Protocol, provider.BaseURL, provider.AuthMode, provider.Discovery)
}

func llmModelAccessCovers(access map[string]LLMModelAccessResult, models []llm.Model) bool {
	if len(access) != len(models) {
		return false
	}
	for _, model := range models {
		if _, ok := access[model.ID]; !ok {
			return false
		}
	}
	return true
}

func cloneLLMModelAccess(input map[string]LLMModelAccessResult) map[string]LLMModelAccessResult {
	if input == nil {
		return nil
	}
	result := make(map[string]LLMModelAccessResult, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
