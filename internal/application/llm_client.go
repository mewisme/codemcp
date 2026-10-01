package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
)

type LLMProviderBackend interface {
	llm.InferenceClient
	llm.ModelDiscoverer
}

type LLMInferenceFacade interface {
	Infer(context.Context, llm.Request) (llm.Result, error)
}

type llmInferenceFacade struct {
	service *LLMService
}

func (s *LLMService) InferenceFacade() LLMInferenceFacade {
	return llmInferenceFacade{service: s}
}

func (facade llmInferenceFacade) Infer(ctx context.Context, request llm.Request) (llm.Result, error) {
	if facade.service == nil {
		return llm.Result{}, llm.NewError(llm.ErrorUnavailable, "", "LLM service is unavailable")
	}
	provider, err := facade.service.ActiveProvider(ctx)
	if err != nil {
		return llm.Result{}, err
	}
	result, err := facade.service.infer(ctx, provider, request)
	if err != nil {
		readiness := probeFailureReadiness(err)
		facade.service.observeReadiness(provider.ID, readiness, llmReadinessReason(err))
		return llm.Result{}, err
	}
	facade.service.observeReadiness(provider.ID, llm.ReadinessReady, "")
	return result, nil
}

func (s *LLMService) infer(ctx context.Context, provider llm.Provider, request llm.Request) (llm.Result, error) {
	if provider.ID != llm.OllamaID || !strings.EqualFold(strings.TrimSpace(provider.Model), llm.OllamaAutoModel) {
		return s.llmClient().Infer(ctx, provider, request)
	}
	models, err := s.ProviderModels(ctx, string(provider.ID))
	if err != nil {
		return llm.Result{}, err
	}
	candidates := s.ollamaAutoCandidates(provider, models)
	if len(candidates) == 0 {
		return llm.Result{}, llm.NewError(llm.ErrorUnavailable, "model", "Ollama auto model selection found no available models")
	}
	var lastErr error
	for _, model := range candidates {
		selected := provider
		selected.Model = model
		result, inferErr := s.llmClient().Infer(ctx, selected, request)
		if inferErr == nil {
			s.rememberAutoModel(provider.ID, model)
			s.observeModelAccess(provider, model, LLMModelAccessAvailable, nil)
			return result, nil
		}
		if llm.IsCategory(inferErr, llm.ErrorProvider) {
			s.observeModelAccess(provider, model, LLMModelAccessUnavailable, inferErr)
		}
		lastErr = inferErr
		if !ollamaAutoCanFailOver(inferErr) || ctx != nil && ctx.Err() != nil {
			return llm.Result{}, inferErr
		}
	}
	if lastErr != nil {
		return llm.Result{}, lastErr
	}
	return llm.Result{}, llm.NewError(llm.ErrorUnavailable, "model", "Ollama auto model selection could not complete inference")
}

func (s *LLMService) ollamaAutoCandidates(provider llm.Provider, models []llm.Model) []string {
	preferred := s.autoModel(provider.ID)
	access := map[string]LLMModelAccessResult(nil)
	if s != nil && s.accessCache != nil {
		access = s.accessCache.snapshot(provider, time.Now().UTC())
	}
	result := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	appendModel := func(raw string, allowedStates ...LLMModelAccessState) {
		model := strings.TrimSpace(raw)
		if model == "" || strings.EqualFold(model, llm.OllamaAutoModel) {
			return
		}
		if observation, ok := access[model]; ok && len(allowedStates) > 0 {
			allowed := false
			for _, state := range allowedStates {
				if observation.State == state {
					allowed = true
					break
				}
			}
			if !allowed {
				return
			}
		}
		if _, ok := seen[model]; ok {
			return
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	if observation, ok := access[preferred]; !ok || observation.State != LLMModelAccessUnavailable {
		appendModel(preferred)
	}
	for _, model := range models {
		appendModel(model.ID, LLMModelAccessAvailable)
	}
	for _, model := range models {
		observation, ok := access[model.ID]
		if !ok || observation.State == LLMModelAccessUnknown {
			appendModel(model.ID)
		}
	}
	return result
}

func ollamaAutoCanFailOver(err error) bool {
	for _, category := range []llm.ErrorCategory{
		llm.ErrorInvalidRequest,
		llm.ErrorInvalidResponse,
		llm.ErrorProvider,
		llm.ErrorUnavailable,
		llm.ErrorTimeout,
	} {
		if llm.IsCategory(err, category) {
			return true
		}
	}
	return false
}

func (s *LLMService) autoModel(id llm.ProviderID) string {
	if s == nil {
		return ""
	}
	s.autoModelMu.RLock()
	defer s.autoModelMu.RUnlock()
	return s.autoModels[id]
}

func (s *LLMService) rememberAutoModel(id llm.ProviderID, model string) {
	if s == nil {
		return
	}
	s.autoModelMu.Lock()
	s.autoModels[id] = strings.TrimSpace(model)
	s.autoModelMu.Unlock()
}

func (s *LLMService) clearAutoModel(id llm.ProviderID) {
	if s == nil {
		return
	}
	s.autoModelMu.Lock()
	delete(s.autoModels, id)
	s.autoModelMu.Unlock()
}

func llmReadinessReason(err error) string {
	value, ok := llm.AsError(err)
	if !ok {
		return "LLM provider request failed"
	}
	switch value.Category {
	case llm.ErrorMisconfigured:
		return "LLM provider configuration is incomplete"
	case llm.ErrorUnauthorized:
		return "LLM provider authentication failed"
	case llm.ErrorRateLimited:
		return "LLM provider is rate limited"
	case llm.ErrorTimeout:
		return "LLM provider request timed out"
	case llm.ErrorCancelled:
		return "LLM provider request was cancelled"
	case llm.ErrorUnsupported:
		return "LLM provider does not support the requested operation"
	case llm.ErrorInvalidRequest, llm.ErrorInvalidResponse:
		return "LLM provider returned an invalid request or response"
	default:
		return "LLM provider is unavailable"
	}
}

func (s *LLMService) llmClient() LLMProviderBackend {
	if s == nil || s.backend == nil {
		return newDefaultLLMBackend("")
	}
	return s.backend
}

func newDefaultLLMBackend(root string) LLMProviderBackend {
	return llm.NewClient(llm.ClientOptions{Credential: func(_ context.Context, providerID llm.ProviderID) (string, error) {
		credential, err := llm.LoadCredential(root, string(providerID))
		if errors.Is(err, secretstore.ErrNotFound) {
			return "", nil
		}
		return credential, err
	}})
}
