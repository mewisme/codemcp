package semantic

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	MaxProviderConcurrency = 4
	MaxQueuedEvaluations   = 16
	MaxCacheEntries        = 256
	CacheTTL               = 2 * time.Minute
	cacheSchemaVersion     = 1
)

type ReuseDiagnostics struct {
	CacheEntries     int
	CacheCapacity    int
	InFlight         int
	ConcurrencyLimit int
	Queued           int
	QueueCapacity    int
}

type managedProvider struct {
	name       string
	model      string
	provider   Provider
	classifier RiskClassifier
	options    ManagerOptions
	gate       chan struct{}

	queueMu sync.Mutex
	queued  int

	cacheMu    sync.Mutex
	cache      map[string]*list.Element
	lru        *list.List
	generation uint64
}

type cacheEntry struct {
	key        string
	result     Result
	expiresAt  time.Time
	generation uint64
}

type cacheIdentity struct {
	Version  int     `json:"version"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Request  Request `json:"request"`
}

func newManagedProvider(name, model string, registration ProviderRegistration, options ManagerOptions) *managedProvider {
	return &managedProvider{
		name: name, model: model, provider: registration.Provider, classifier: registration.RiskClassifier,
		options: options, gate: make(chan struct{}, MaxProviderConcurrency),
		cache: make(map[string]*list.Element, MaxCacheEntries), lru: list.New(),
	}
}

func (p *managedProvider) evaluate(ctx context.Context, request Request) (Result, error) {
	if p == nil || p.provider == nil {
		return Result{}, NewError(ErrorUnavailable, "")
	}
	if err := ValidateRequest(request); err != nil {
		return Result{}, err
	}
	key, err := requestDigest(p.name, p.model, request)
	if err != nil {
		return Result{}, NewError(ErrorInvalidRequest, "semantic request cannot be normalized")
	}
	if result, ok := p.cacheGet(key); ok {
		result.Runtime.CacheHit = true
		result.Runtime.Attempts = 0
		return result, nil
	}
	if err := p.acquire(ctx); err != nil {
		return Result{}, err
	}
	defer p.release()
	if result, ok := p.cacheGet(key); ok {
		result.Runtime.CacheHit = true
		result.Runtime.Attempts = 0
		return result, nil
	}

	generation := p.currentGeneration()
	result, attempts, err := p.withRetry(ctx, func(attemptCtx context.Context) (Result, error) {
		return p.provider.Evaluate(attemptCtx, request)
	})
	if err != nil {
		return Result{}, err
	}
	if err := ValidateResult(request, result); err != nil {
		if IsCategory(err, ErrorInvalidResponse) {
			return Result{}, err
		}
		return Result{}, NewError(ErrorInvalidResponse, "semantic provider returned an invalid normalized result")
	}
	result.Runtime = RuntimeMetadata{Attempts: attempts}
	p.cacheSetIfCurrent(key, result, generation)
	return cloneResult(result), nil
}

func (p *managedProvider) classifyRisk(ctx context.Context, input RiskInput, minConfidence float64) (RiskAssessment, error) {
	if p == nil || p.classifier == nil {
		return RiskAssessment{}, NewError(ErrorUnavailable, "")
	}
	if err := ValidateRiskInput(input); err != nil {
		return RiskAssessment{}, err
	}
	if err := p.acquire(ctx); err != nil {
		return RiskAssessment{}, err
	}
	defer p.release()
	assessment, _, err := retryValue(ctx, p.options, func(attemptCtx context.Context) (RiskAssessment, error) {
		return p.classifier.ClassifyRisk(attemptCtx, input)
	})
	if err != nil {
		return RiskAssessment{}, err
	}
	if err := ValidateRiskAssessment(input, assessment, minConfidence); err != nil {
		return RiskAssessment{}, err
	}
	return assessment, nil
}

func (p *managedProvider) withRetry(ctx context.Context, fn func(context.Context) (Result, error)) (Result, int, error) {
	return retryValue(ctx, p.options, fn)
}

func retryValue[T any](ctx context.Context, options ManagerOptions, fn func(context.Context) (T, error)) (T, int, error) {
	var zero T
	operationCtx, cancel := boundedContext(ctx, options.Timeout)
	defer cancel()
	for attempt := 1; attempt <= options.MaxAttempts; attempt++ {
		if err := operationCtx.Err(); err != nil {
			return zero, attempt - 1, contextSemanticError(operationCtx)
		}
		value, err := fn(operationCtx)
		if err == nil {
			return value, attempt, nil
		}
		err = normalizeProviderError(operationCtx, err)
		if !retryableSemanticError(err) || attempt == options.MaxAttempts {
			return zero, attempt, err
		}
		delay := options.RetryBaseDelay << (attempt - 1)
		if err := options.Sleep(operationCtx, delay); err != nil {
			return zero, attempt, normalizeProviderError(operationCtx, err)
		}
	}
	return zero, options.MaxAttempts, NewError(ErrorProvider, "")
}

func boundedContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func normalizeProviderError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return contextSemanticError(ctx)
	}
	if _, ok := AsError(err); ok {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewError(ErrorTimeout, "")
	}
	if errors.Is(err, context.Canceled) {
		return NewError(ErrorCancelled, "")
	}
	return NewError(ErrorProvider, "")
}

func retryableSemanticError(err error) bool {
	value, ok := AsError(err)
	if !ok {
		return false
	}
	switch value.Category {
	case ErrorRateLimited, ErrorOverloaded, ErrorTransport, ErrorTimeout:
		return true
	default:
		return false
	}
}

func (p *managedProvider) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return contextSemanticError(ctx)
	default:
	}
	select {
	case p.gate <- struct{}{}:
		return nil
	default:
	}
	p.queueMu.Lock()
	if p.queued >= MaxQueuedEvaluations {
		p.queueMu.Unlock()
		return NewError(ErrorOverloaded, "")
	}
	p.queued++
	p.queueMu.Unlock()
	defer func() {
		p.queueMu.Lock()
		p.queued--
		p.queueMu.Unlock()
	}()
	select {
	case p.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return contextSemanticError(ctx)
	}
}

func (p *managedProvider) release() { <-p.gate }

func (p *managedProvider) cacheGet(key string) (Result, bool) {
	now := p.options.Now()
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	element, ok := p.cache[key]
	if !ok {
		return Result{}, false
	}
	entry := element.Value.(*cacheEntry)
	if !now.Before(entry.expiresAt) || entry.generation != p.generation {
		p.removeElement(element)
		return Result{}, false
	}
	p.lru.MoveToFront(element)
	return cloneResult(entry.result), true
}

func (p *managedProvider) cacheSetIfCurrent(key string, result Result, generation uint64) {
	now := p.options.Now()
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if generation != p.generation {
		return
	}
	result.Runtime.CacheHit = false
	if element, ok := p.cache[key]; ok {
		entry := element.Value.(*cacheEntry)
		entry.result = cloneResult(result)
		entry.expiresAt = now.Add(CacheTTL)
		entry.generation = generation
		p.lru.MoveToFront(element)
		return
	}
	entry := &cacheEntry{key: key, result: cloneResult(result), expiresAt: now.Add(CacheTTL), generation: generation}
	p.cache[key] = p.lru.PushFront(entry)
	for p.lru.Len() > MaxCacheEntries {
		p.removeElement(p.lru.Back())
	}
}

func (p *managedProvider) currentGeneration() uint64 {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	return p.generation
}

func (p *managedProvider) invalidate() {
	if p == nil {
		return
	}
	p.cacheMu.Lock()
	p.generation++
	p.cache = make(map[string]*list.Element, MaxCacheEntries)
	p.lru.Init()
	p.cacheMu.Unlock()
}

func (p *managedProvider) diagnostics() ReuseDiagnostics {
	if p == nil {
		return ReuseDiagnostics{CacheCapacity: MaxCacheEntries, ConcurrencyLimit: MaxProviderConcurrency, QueueCapacity: MaxQueuedEvaluations}
	}
	p.cacheMu.Lock()
	entries := len(p.cache)
	p.cacheMu.Unlock()
	p.queueMu.Lock()
	queued := p.queued
	p.queueMu.Unlock()
	return ReuseDiagnostics{
		CacheEntries: entries, CacheCapacity: MaxCacheEntries,
		InFlight: len(p.gate), ConcurrencyLimit: MaxProviderConcurrency,
		Queued: queued, QueueCapacity: MaxQueuedEvaluations,
	}
}

func (p *managedProvider) removeElement(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*cacheEntry)
	delete(p.cache, entry.key)
	p.lru.Remove(element)
}

func requestDigest(provider, model string, request Request) (string, error) {
	payload, err := json.Marshal(cacheIdentity{Version: cacheSchemaVersion, Provider: provider, Model: model, Request: request})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func contextSemanticError(ctx context.Context) error {
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return NewError(ErrorTimeout, "")
	}
	return NewError(ErrorCancelled, "")
}

func cloneResult(source Result) Result {
	result := source
	if source.Usage != nil {
		usage := *source.Usage
		result.Usage = &usage
	}
	result.Answers = make(map[string]Answer, len(source.Answers))
	for key, answer := range source.Answers {
		cloned := answer
		if answer.Noul != nil {
			value := *answer.Noul
			cloned.Noul = &value
		}
		if answer.Choice != nil {
			value := *answer.Choice
			value.Probabilities = cloneFloatMap(answer.Choice.Probabilities)
			cloned.Choice = &value
		}
		if answer.Score != nil {
			value := *answer.Score
			value.Probabilities = cloneFloatMap(answer.Score.Probabilities)
			cloned.Score = &value
		}
		result.Answers[key] = cloned
	}
	return result
}

func cloneFloatMap(source map[string]float64) map[string]float64 {
	if source == nil {
		return nil
	}
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
