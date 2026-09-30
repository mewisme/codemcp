package application

import (
	"context"

	"go.mewis.me/codemcp/internal/llm"
)

func (s *LLMService) ProbeOpenRouter(ctx context.Context) error {
	return s.ProbeProvider(ctx, string(llm.OpenRouterID))
}
