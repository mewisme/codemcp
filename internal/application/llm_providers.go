package application

import (
	"context"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
)

type CustomLLMProviderConfig struct {
	Name      string            `json:"name"`
	Protocol  llm.Protocol      `json:"protocol"`
	BaseURL   string            `json:"base_url"`
	Model     string            `json:"model,omitempty"`
	AuthMode  llm.AuthMode      `json:"auth_mode"`
	Discovery llm.DiscoveryMode `json:"discovery"`
	APIKey    *string           `json:"-"`
}

func (s *LLMService) AddCustomProvider(ctx context.Context, rawID string, config CustomLLMProviderConfig) (llm.Provider, error) {
	if s == nil || s.store == nil {
		return llm.Provider{}, fmt.Errorf("LLM service is unavailable")
	}
	provider, err := normalizeCustomLLMProvider(rawID, config)
	if err != nil {
		return llm.Provider{}, err
	}
	updated, err := s.store.UpdateWithSecrets(func(current llm.Catalog) (llm.Catalog, []secretstore.Change, error) {
		for _, existing := range current.Providers {
			if existing.ID == provider.ID {
				return llm.Catalog{}, nil, llm.NewError(llm.ErrorDuplicateID, "id", fmt.Sprintf("provider %q is already registered", provider.ID))
			}
		}
		changes, err := customProviderCredentialChanges(provider.ID, config.APIKey)
		if err != nil {
			return llm.Catalog{}, nil, err
		}
		current.Providers = append(current.Providers, provider)
		return current, changes, nil
	})
	if err != nil {
		return llm.Provider{}, err
	}
	s.invalidateModelCatalog(provider.ID)
	s.clearReadiness(provider.ID)
	return providerFromCatalog(updated, provider.ID)
}

func (s *LLMService) ConfigureCustomProvider(ctx context.Context, rawID string, config CustomLLMProviderConfig) (llm.Provider, error) {
	if s == nil || s.store == nil {
		return llm.Provider{}, fmt.Errorf("LLM service is unavailable")
	}
	id, err := llm.NormalizeProviderID(rawID)
	if err != nil {
		return llm.Provider{}, err
	}
	if llm.IsCoreProvider(id) {
		return llm.Provider{}, llm.NewError(llm.ErrorCoreInvariant, "id", fmt.Sprintf("core provider %q cannot be configured as a custom provider", id))
	}
	updated, err := s.store.UpdateWithSecrets(func(current llm.Catalog) (llm.Catalog, []secretstore.Change, error) {
		providerIndex := -1
		for index := range current.Providers {
			if current.Providers[index].ID == id {
				providerIndex = index
				break
			}
		}
		if providerIndex < 0 {
			return llm.Catalog{}, nil, llm.NewError(llm.ErrorProviderNotFound, "id", fmt.Sprintf("provider %q is not registered", id))
		}
		provider, err := normalizeCustomLLMProvider(string(id), config)
		if err != nil {
			return llm.Catalog{}, nil, err
		}
		provider.Capabilities = cloneProviderCapabilities(current.Providers[providerIndex].Capabilities)
		changes, err := customProviderCredentialChanges(id, config.APIKey)
		if err != nil {
			return llm.Catalog{}, nil, err
		}
		current.Providers[providerIndex] = provider
		return current, changes, nil
	})
	if err != nil {
		return llm.Provider{}, err
	}
	s.invalidateModelCatalog(id)
	s.clearReadiness(id)
	return providerFromCatalog(updated, id)
}

func (s *LLMService) SelectProvider(ctx context.Context, rawID string) (llm.ProviderStatus, error) {
	if s == nil || s.store == nil {
		return llm.ProviderStatus{}, fmt.Errorf("LLM service is unavailable")
	}
	id, err := llm.NormalizeProviderID(rawID)
	if err != nil {
		return llm.ProviderStatus{}, err
	}
	updated, err := s.store.Update(func(current llm.Catalog) (llm.Catalog, error) {
		if _, err := providerFromCatalog(current, id); err != nil {
			return llm.Catalog{}, err
		}
		current.ActiveProvider = id
		return current, nil
	})
	if err != nil {
		return llm.ProviderStatus{}, err
	}
	provider, err := providerFromCatalog(updated, id)
	if err != nil {
		return llm.ProviderStatus{}, err
	}
	return s.selectionStatus(provider), nil
}

func (s *LLMService) ProbeProvider(ctx context.Context, rawID string) error {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return err
	}
	_, err = s.llmClient().Infer(ctx, provider, llm.Request{
		Instructions:    "Return a short acknowledgement.",
		Messages:        []llm.Message{{Role: llm.RoleUser, Content: "Respond with OK."}},
		MaxOutputTokens: 8,
	})
	return err
}

func (s *LLMService) selectionStatus(provider llm.Provider) llm.ProviderStatus {
	status := llm.ProviderStatus{
		ProviderID: provider.ID,
		Selected:   true,
		Readiness:  llm.ReadinessUnknown,
	}
	reasons := make([]string, 0, 2)
	if strings.TrimSpace(provider.Model) == "" {
		reasons = append(reasons, "model is not configured")
	}
	if reason := providerAuthConfigurationReason(provider); reason != "" {
		reasons = append(reasons, reason)
	} else if provider.AuthMode != llm.AuthNone {
		configured, err := llm.CredentialConfigured(s.root, string(provider.ID))
		switch {
		case err != nil:
			reasons = append(reasons, "credential state is unavailable")
		case !configured:
			reasons = append(reasons, "API key is not configured")
		}
	}
	status.Configured = len(reasons) == 0
	if len(reasons) > 0 {
		status.Reason = "selected provider is not ready for inference: " + strings.Join(reasons, "; ")
	}
	return status
}

func providerAuthConfigurationReason(provider llm.Provider) string {
	switch provider.Protocol {
	case llm.ProtocolOpenAI:
		if provider.AuthMode != llm.AuthNone && provider.AuthMode != llm.AuthBearer {
			return fmt.Sprintf("auth mode %q is not supported by the OpenAI-compatible adapter", provider.AuthMode)
		}
	case llm.ProtocolAnthropic:
		if provider.AuthMode != llm.AuthNone && provider.AuthMode != llm.AuthAPIKey {
			return fmt.Sprintf("auth mode %q is not supported by the Anthropic-compatible adapter", provider.AuthMode)
		}
	default:
		return fmt.Sprintf("protocol %q is not supported", provider.Protocol)
	}
	return ""
}

func normalizeCustomLLMProvider(rawID string, config CustomLLMProviderConfig) (llm.Provider, error) {
	return llm.NormalizeCustomProvider(llm.Provider{
		ID:        llm.ProviderID(rawID),
		Name:      config.Name,
		Protocol:  config.Protocol,
		BaseURL:   config.BaseURL,
		Model:     config.Model,
		AuthMode:  config.AuthMode,
		Discovery: config.Discovery,
	})
}

func customProviderCredentialChanges(id llm.ProviderID, value *string) ([]secretstore.Change, error) {
	if value == nil {
		return nil, nil
	}
	change, err := llm.CredentialChange(string(id), *value)
	if err != nil {
		return nil, err
	}
	return []secretstore.Change{change}, nil
}

func cloneProviderCapabilities(value *llm.ProviderCapabilities) *llm.ProviderCapabilities {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
