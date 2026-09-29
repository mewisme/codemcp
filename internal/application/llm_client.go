package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
)

func (s *LLMService) llmClient() *llm.Client {
	root := ""
	if s != nil {
		root = s.root
	}
	return llm.NewClient(llm.ClientOptions{Credential: func(_ context.Context, providerID llm.ProviderID) (string, error) {
		credential, err := llm.LoadCredential(root, string(providerID))
		if errors.Is(err, secretstore.ErrNotFound) {
			return "", nil
		}
		return credential, err
	}})
}
