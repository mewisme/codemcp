package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type LLMService struct {
	root  string
	store *llm.Store
}

func NewLLMService(root string) *LLMService {
	return &LLMService{root: strings.TrimSpace(root), store: llm.NewStore(root)}
}

func (s *LLMService) Catalog(context.Context) (llm.Catalog, error) {
	if s == nil || s.store == nil {
		return llm.Catalog{}, errors.New("LLM service is unavailable")
	}
	return s.store.Load()
}

func (s *LLMService) Provider(ctx context.Context, rawID string) (llm.Provider, error) {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return llm.Provider{}, err
	}
	id, err := llm.NormalizeProviderID(rawID)
	if err != nil {
		return llm.Provider{}, err
	}
	for _, provider := range catalog.Providers {
		if provider.ID == id {
			return provider, nil
		}
	}
	return llm.Provider{}, llm.NewError(llm.ErrorProviderNotFound, "id", fmt.Sprintf("provider %q is not registered", id))
}

func (s *LLMService) ActiveProvider(ctx context.Context) (llm.Provider, error) {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return llm.Provider{}, err
	}
	return providerFromCatalog(catalog, catalog.ActiveProvider)
}

func (s *LLMService) CredentialConfigured(ctx context.Context, rawID string) (bool, error) {
	if _, err := s.Provider(ctx, rawID); err != nil {
		return false, err
	}
	return llm.CredentialConfigured(s.root, rawID)
}

func (s *LLMService) CredentialPreview(ctx context.Context, rawID string) (string, bool, error) {
	configured, err := s.CredentialConfigured(ctx, rawID)
	if err != nil {
		return "", false, err
	}
	if !configured {
		return "not configured", false, nil
	}
	value, err := llm.LoadCredential(s.root, rawID)
	if err != nil {
		return "", false, err
	}
	return tracepkg.MaskSecret(value, true), true, nil
}

func (s *LLMService) SetCredential(ctx context.Context, rawID, value string) error {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("LLM API key must not be empty; unset it to clear the credential")
	}
	change, err := llm.CredentialChange(string(provider.ID), value)
	if err != nil {
		return err
	}
	return secretstore.New(s.root).Apply([]secretstore.Change{change})
}

func (s *LLMService) ClearCredential(ctx context.Context, rawID string) error {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return err
	}
	change, err := llm.CredentialChange(string(provider.ID), "")
	if err != nil {
		return err
	}
	return secretstore.New(s.root).Apply([]secretstore.Change{change})
}

func (s *LLMService) RemoveProvider(ctx context.Context, rawID string) error {
	if s == nil {
		return errors.New("LLM service is unavailable")
	}
	if _, err := s.Provider(ctx, rawID); err != nil {
		return err
	}
	return llm.RemoveProvider(s.root, rawID)
}

func (s *SettingService) llmService() *LLMService {
	if s != nil && s.llm != nil {
		return s.llm
	}
	return NewLLMService(config.RootPath())
}

func (s *SettingService) readLLMStaticSetting(ctx context.Context, spec config.FieldSpec) (string, *bool, error) {
	service := s.llmService()
	catalog, err := service.Catalog(ctx)
	if err != nil {
		return "", nil, err
	}
	provider, err := providerFromCatalog(catalog, catalog.ActiveProvider)
	if err != nil {
		return "", nil, err
	}
	switch spec.Key {
	case "llm.provider":
		return string(catalog.ActiveProvider), nil, nil
	case "llm.base_url":
		return provider.BaseURL, nil, nil
	case "llm.model":
		return provider.Model, nil, nil
	case "llm.api_key_configured":
		configured, err := service.CredentialConfigured(ctx, string(provider.ID))
		return strconv.FormatBool(configured), boolPointer(configured), err
	default:
		return "", nil, fmt.Errorf("unsupported LLM setting: %s", spec.Key)
	}
}

func (s *SettingService) readLLMDynamicSetting(ctx context.Context, match config.FieldSelectorMatch) (string, *bool, error) {
	service := s.llmService()
	provider, err := service.Provider(ctx, match.ResourceID)
	if err != nil {
		return "", nil, err
	}
	switch dynamicSettingSuffix(match.Spec.Key) {
	case "name":
		return provider.Name, nil, nil
	case "protocol":
		return string(provider.Protocol), nil, nil
	case "base_url":
		return provider.BaseURL, nil, nil
	case "model":
		return provider.Model, nil, nil
	case "api_key_configured":
		configured, err := service.CredentialConfigured(ctx, string(provider.ID))
		return strconv.FormatBool(configured), boolPointer(configured), err
	case "auth_mode":
		return string(provider.AuthMode), nil, nil
	case "discovery":
		return string(provider.Discovery), nil, nil
	case "core":
		return strconv.FormatBool(llm.IsCoreProvider(provider.ID)), nil, nil
	default:
		return "", nil, fmt.Errorf("unsupported LLM provider setting: %s", dynamicSettingSuffix(match.Spec.Key))
	}
}

func (s *SettingService) presentLLMSecret(ctx context.Context, spec config.FieldSpec, selector *config.FieldSelectorMatch) (SettingResult, error) {
	service := s.llmService()
	id := ""
	if selector != nil {
		provider, err := service.Provider(ctx, selector.ResourceID)
		if err != nil {
			return SettingResult{}, err
		}
		id = string(provider.ID)
	} else {
		provider, err := service.ActiveProvider(ctx)
		if err != nil {
			return SettingResult{}, err
		}
		id = string(provider.ID)
	}
	value, configured, err := service.CredentialPreview(ctx, id)
	if err != nil {
		return SettingResult{}, err
	}
	return SettingResult{Spec: presentationSpec(spec, selector), Value: value, Configured: boolPointer(configured)}, nil
}

func (s *SettingService) validateLLMSettingChanges(ctx context.Context, items []resolvedSettingChange) error {
	service := s.llmService()
	catalog, err := service.Catalog(ctx)
	if err != nil {
		return err
	}
	_, _, err = stageLLMSettingChanges(catalog, items)
	return err
}

func (s *SettingService) applyLLMSettingChanges(ctx context.Context, items []resolvedSettingChange) (SettingApplyResult, error) {
	service := s.llmService()
	_, err := service.store.UpdateWithSecrets(func(current llm.Catalog) (llm.Catalog, []secretstore.Change, error) {
		return stageLLMSettingChanges(current, items)
	})
	if err != nil {
		return SettingApplyResult{}, err
	}
	results := make([]SettingResult, 0, len(items))
	for _, item := range items {
		key := item.change.Key
		var result SettingResult
		if item.spec.Secret {
			result, err = s.Present(ctx, key)
		} else {
			result, err = s.Read(ctx, key)
		}
		if err != nil {
			return SettingApplyResult{}, err
		}
		results = append(results, result)
	}
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return SettingApplyResult{}, err
	}
	return SettingApplyResult{Results: results, Config: cfg}, nil
}

func stageLLMSettingChanges(current llm.Catalog, items []resolvedSettingChange) (llm.Catalog, []secretstore.Change, error) {
	if _, err := llm.NormalizeCatalog(current); err != nil {
		return llm.Catalog{}, nil, err
	}
	for _, item := range items {
		if item.selector == nil && item.spec.Key == "llm.provider" {
			if item.change.Unset {
				return llm.Catalog{}, nil, errors.New("llm.provider cannot be cleared")
			}
			id, err := llm.NormalizeProviderID(item.change.Value)
			if err != nil {
				return llm.Catalog{}, nil, err
			}
			if _, err := providerFromCatalog(current, id); err != nil {
				return llm.Catalog{}, nil, err
			}
			current.ActiveProvider = id
		}
	}

	changes := make([]secretstore.Change, 0)
	semanticTargets := map[string]struct{}{}
	for _, item := range items {
		if item.selector == nil && item.spec.Key == "llm.provider" {
			continue
		}
		id := current.ActiveProvider
		suffix := strings.TrimPrefix(item.spec.Key, "llm.")
		if item.selector != nil {
			var err error
			id, err = llm.NormalizeProviderID(item.selector.ResourceID)
			if err != nil {
				return llm.Catalog{}, nil, err
			}
			suffix = dynamicSettingSuffix(item.spec.Key)
		}
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
		target := string(id) + "\x00" + suffix
		if _, exists := semanticTargets[target]; exists {
			return llm.Catalog{}, nil, fmt.Errorf("duplicate LLM provider setting mutation for %s.%s", id, suffix)
		}
		semanticTargets[target] = struct{}{}

		raw := item.change.Value
		if item.change.Unset {
			raw = ""
		}
		if suffix == "api_key" {
			if !item.change.Unset && strings.TrimSpace(raw) == "" {
				return llm.Catalog{}, nil, errors.New("LLM API key must not be empty; unset it to clear the credential")
			}
			change, err := llm.CredentialChange(string(id), raw)
			if err != nil {
				return llm.Catalog{}, nil, err
			}
			changes = append(changes, change)
			continue
		}
		if err := applyLLMProviderSetting(&current.Providers[providerIndex], suffix, raw); err != nil {
			return llm.Catalog{}, nil, err
		}
	}
	normalized, err := llm.NormalizeCatalog(current)
	if err != nil {
		return llm.Catalog{}, nil, err
	}
	return normalized, changes, nil
}

func applyLLMProviderSetting(provider *llm.Provider, suffix, raw string) error {
	if provider == nil {
		return errors.New("LLM provider is required")
	}
	raw = strings.TrimSpace(raw)
	switch suffix {
	case "base_url":
		provider.BaseURL = raw
	case "model":
		provider.Model = raw
	case "name":
		provider.Name = raw
	case "protocol":
		provider.Protocol = llm.Protocol(strings.ToLower(raw))
	case "auth_mode":
		provider.AuthMode = llm.AuthMode(strings.ToLower(raw))
	case "discovery":
		provider.Discovery = llm.DiscoveryMode(strings.ToLower(raw))
	default:
		return fmt.Errorf("unsupported LLM provider setting: %s", suffix)
	}
	return nil
}

func providerFromCatalog(catalog llm.Catalog, id llm.ProviderID) (llm.Provider, error) {
	for _, provider := range catalog.Providers {
		if provider.ID == id {
			return provider, nil
		}
	}
	return llm.Provider{}, llm.NewError(llm.ErrorProviderNotFound, "id", fmt.Sprintf("provider %q is not registered", id))
}

func llmSettingChanges(items []resolvedSettingChange) (bool, error) {
	count := 0
	for _, item := range items {
		if item.spec.ApplicationOwner == "llm.providers" {
			count++
		}
	}
	if count == 0 {
		return false, nil
	}
	if count != len(items) {
		return false, errors.New("LLM settings cannot be combined with another setting owner in one transaction")
	}
	return true, nil
}
