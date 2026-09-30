package application

import (
	"context"
	"errors"

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
	result, err := facade.service.llmClient().Infer(ctx, provider, request)
	if err != nil {
		readiness := probeFailureReadiness(err)
		facade.service.observeReadiness(provider.ID, readiness, llmReadinessReason(err))
		return llm.Result{}, err
	}
	facade.service.observeReadiness(provider.ID, llm.ReadinessReady, "")
	return result, nil
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
