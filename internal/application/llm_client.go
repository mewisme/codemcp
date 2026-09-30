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
	return facade.service.llmClient().Infer(ctx, provider, request)
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
