package application

import (
	"context"
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/llm"
)

type LLMCredentialResult struct {
	ProviderID llm.ProviderID `json:"provider_id"`
	Configured bool           `json:"configured"`
	Preview    string         `json:"preview"`
}

type LLMProviderResult struct {
	ID         llm.ProviderID      `json:"id"`
	Name       string              `json:"name"`
	Protocol   llm.Protocol        `json:"protocol"`
	BaseURL    string              `json:"base_url"`
	Model      string              `json:"model,omitempty"`
	AuthMode   llm.AuthMode        `json:"auth_mode"`
	Discovery  llm.DiscoveryMode   `json:"discovery"`
	CoreKind   llm.CoreKind        `json:"core_kind,omitempty"`
	Core       bool                `json:"core"`
	Selected   bool                `json:"selected"`
	Configured bool                `json:"configured"`
	Readiness  llm.Readiness       `json:"readiness"`
	Reason     string              `json:"reason,omitempty"`
	Credential LLMCredentialResult `json:"credential"`
}

type LLMStatusResult struct {
	ActiveProvider llm.ProviderID      `json:"active_provider"`
	Active         LLMProviderResult   `json:"active"`
	Providers      []LLMProviderResult `json:"providers"`
}

type LLMModelCatalogResult = LLMModelPage

type LLMProbeResult struct {
	ProviderID llm.ProviderID `json:"provider_id"`
	Model      string         `json:"model,omitempty"`
	Readiness  llm.Readiness  `json:"readiness"`
}

type LLMProviderIDInput struct {
	ID string `json:"id"`
}

type LLMProviderWriteInput struct {
	ID         string                  `json:"id"`
	Config     CustomLLMProviderConfig `json:"config"`
	Model      *string                 `json:"model,omitempty"`
	OllamaMode *string                 `json:"ollama_mode,omitempty"`
}

type LLMProviderModelsInput struct {
	ID    string        `json:"id"`
	Query LLMModelQuery `json:"query,omitempty"`
}

type LLMProviderCredentialInput struct {
	ID     string `json:"id"`
	APIKey string `json:"-"`
}

type LLMProviderRemoveResult struct {
	ProviderID llm.ProviderID `json:"provider_id"`
	Removed    bool           `json:"removed"`
}

func (s *LLMService) RemoveProviderResult(ctx context.Context, rawID string) (LLMProviderRemoveResult, error) {
	id, err := llm.NormalizeProviderID(rawID)
	if err != nil {
		return LLMProviderRemoveResult{}, err
	}
	if err := s.RemoveProvider(ctx, rawID); err != nil {
		return LLMProviderRemoveResult{}, err
	}
	return LLMProviderRemoveResult{ProviderID: id, Removed: true}, nil
}

func (s *LLMService) SetProviderModel(ctx context.Context, rawID, model string) (LLMProviderResult, error) {
	if s == nil || s.store == nil {
		return LLMProviderResult{}, errors.New("LLM service is unavailable")
	}
	id, err := llm.NormalizeProviderID(rawID)
	if err != nil {
		return LLMProviderResult{}, err
	}
	_, err = s.store.Update(func(catalog llm.Catalog) (llm.Catalog, error) {
		for index := range catalog.Providers {
			if catalog.Providers[index].ID != id {
				continue
			}
			catalog.Providers[index].Model = strings.TrimSpace(model)
			return catalog, nil
		}
		return llm.Catalog{}, llm.NewError(llm.ErrorProviderNotFound, "id", "LLM provider is not registered")
	})
	if err != nil {
		return LLMProviderResult{}, err
	}
	s.invalidateModelCatalog(id)
	s.clearReadiness(id)
	return s.ProviderResult(ctx, string(id))
}

func (s *LLMService) SetOllamaModeValue(ctx context.Context, raw string) (LLMProviderResult, error) {
	mode := llm.OllamaMode(strings.ToLower(strings.TrimSpace(raw)))
	provider, err := s.SetOllamaMode(ctx, mode)
	if err != nil {
		return LLMProviderResult{}, err
	}
	return s.ProviderResult(ctx, string(provider.ID))
}

func LLMFailureCategory(err error) (string, bool) {
	value, ok := llm.AsError(err)
	if !ok {
		return "", false
	}
	return string(value.Category), true
}

func (s *LLMService) Status(ctx context.Context) (LLMStatusResult, error) {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return LLMStatusResult{}, err
	}
	providers := make([]LLMProviderResult, 0, len(catalog.Providers))
	var active LLMProviderResult
	for _, provider := range catalog.Providers {
		result, err := s.providerResult(ctx, provider, provider.ID == catalog.ActiveProvider)
		if err != nil {
			return LLMStatusResult{}, err
		}
		providers = append(providers, result)
		if result.Selected {
			active = result
		}
	}
	return LLMStatusResult{ActiveProvider: catalog.ActiveProvider, Active: active, Providers: providers}, nil
}

func (s *LLMService) Providers(ctx context.Context) ([]LLMProviderResult, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	return append([]LLMProviderResult(nil), status.Providers...), nil
}

func (s *LLMService) ProviderResult(ctx context.Context, rawID string) (LLMProviderResult, error) {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return LLMProviderResult{}, err
	}
	id, err := llm.NormalizeProviderID(rawID)
	if err != nil {
		return LLMProviderResult{}, err
	}
	provider, err := providerFromCatalog(catalog, id)
	if err != nil {
		return LLMProviderResult{}, err
	}
	return s.providerResult(ctx, provider, provider.ID == catalog.ActiveProvider)
}

func (s *LLMService) CredentialResult(ctx context.Context, rawID string) (LLMCredentialResult, error) {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return LLMCredentialResult{}, err
	}
	preview, configured, err := s.CredentialPreview(ctx, string(provider.ID))
	if err != nil {
		return LLMCredentialResult{}, err
	}
	return LLMCredentialResult{ProviderID: provider.ID, Configured: configured, Preview: preview}, nil
}

func (s *LLMService) Probe(ctx context.Context, rawID string) (LLMProbeResult, error) {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return LLMProbeResult{}, err
	}
	if err := s.ProbeProvider(ctx, string(provider.ID)); err != nil {
		readiness := probeFailureReadiness(err)
		s.observeReadiness(provider.ID, readiness, llmReadinessReason(err))
		return LLMProbeResult{ProviderID: provider.ID, Model: provider.Model, Readiness: readiness}, err
	}
	s.observeReadiness(provider.ID, llm.ReadinessReady, "")
	return LLMProbeResult{ProviderID: provider.ID, Model: provider.Model, Readiness: llm.ReadinessReady}, nil
}

func (s *LLMService) providerResult(ctx context.Context, provider llm.Provider, selected bool) (LLMProviderResult, error) {
	credential, err := s.CredentialResult(ctx, string(provider.ID))
	if err != nil {
		return LLMProviderResult{}, err
	}
	status := s.providerConfigurationStatus(provider, selected, credential.Configured)
	if status.Configured {
		if observation, ok := s.readinessObservation(provider.ID); ok {
			status.Readiness = observation.Readiness
			status.Reason = observation.Reason
		}
	}
	return LLMProviderResult{
		ID: provider.ID, Name: provider.Name, Protocol: provider.Protocol, BaseURL: provider.BaseURL,
		Model: provider.Model, AuthMode: provider.AuthMode, Discovery: provider.Discovery, CoreKind: provider.CoreKind,
		Core: llm.IsCoreProvider(provider.ID), Selected: selected, Configured: status.Configured,
		Readiness: status.Readiness, Reason: status.Reason, Credential: credential,
	}, nil
}

func (s *LLMService) providerConfigurationStatus(provider llm.Provider, selected, credentialConfigured bool) llm.ProviderStatus {
	status := llm.ProviderStatus{ProviderID: provider.ID, Selected: selected, Readiness: llm.ReadinessUnknown}
	reasons := make([]string, 0, 3)
	if strings.TrimSpace(provider.Model) == "" {
		reasons = append(reasons, "model is not configured")
	}
	if reason := providerAuthConfigurationReason(provider); reason != "" {
		reasons = append(reasons, reason)
	} else if provider.AuthMode != llm.AuthNone && !credentialConfigured {
		reasons = append(reasons, "API key is not configured")
	}
	status.Configured = len(reasons) == 0
	if !status.Configured {
		status.Readiness = llm.ReadinessDegraded
		status.Reason = strings.Join(reasons, "; ")
	}
	return status
}

func probeFailureReadiness(err error) llm.Readiness {
	value, ok := llm.AsError(err)
	if !ok {
		return llm.ReadinessUnavailable
	}
	switch value.Category {
	case llm.ErrorMisconfigured, llm.ErrorUnauthorized, llm.ErrorInvalidRequest, llm.ErrorUnsupported:
		return llm.ReadinessDegraded
	default:
		return llm.ReadinessUnavailable
	}
}

func BindLLMOperations(dispatcher *Dispatcher, service *LLMService) error {
	if dispatcher == nil || service == nil {
		return errors.New("LLM operation dependencies are unavailable")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.LLMStatus, llmOperationHandler(capability.LLMStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) })},
		{capability.LLMProviderList, llmOperationHandler(capability.LLMProviderList, func(ctx context.Context, _ any) (any, error) { return service.Providers(ctx) })},
		{capability.LLMProviderGet, llmOperationHandler(capability.LLMProviderGet, typedOperation[LLMProviderIDInput](capability.LLMProviderGet, func(ctx context.Context, input LLMProviderIDInput) (any, error) {
			return service.ProviderResult(ctx, input.ID)
		}))},
		{capability.LLMProviderAdd, llmOperationHandler(capability.LLMProviderAdd, typedOperation[LLMProviderWriteInput](capability.LLMProviderAdd, func(ctx context.Context, input LLMProviderWriteInput) (any, error) {
			provider, err := service.AddCustomProvider(ctx, input.ID, input.Config)
			if err != nil {
				return nil, err
			}
			return service.ProviderResult(ctx, string(provider.ID))
		}))},
		{capability.LLMProviderConfigure, llmOperationHandler(capability.LLMProviderConfigure, typedOperation[LLMProviderWriteInput](capability.LLMProviderConfigure, func(ctx context.Context, input LLMProviderWriteInput) (any, error) {
			if input.OllamaMode != nil {
				if strings.TrimSpace(input.ID) != string(llm.OllamaID) {
					return nil, llm.NewError(llm.ErrorInvalidRequest, "id", "Ollama mode can only configure the Ollama provider")
				}
				return service.SetOllamaModeValue(ctx, string(*input.OllamaMode))
			}
			if input.Model != nil {
				return service.SetProviderModel(ctx, input.ID, *input.Model)
			}
			provider, err := service.ConfigureCustomProvider(ctx, input.ID, input.Config)
			if err != nil {
				return nil, err
			}
			return service.ProviderResult(ctx, string(provider.ID))
		}))},
		{capability.LLMProviderRemove, llmOperationHandler(capability.LLMProviderRemove, typedOperation[LLMProviderIDInput](capability.LLMProviderRemove, func(ctx context.Context, input LLMProviderIDInput) (any, error) {
			return service.RemoveProviderResult(ctx, input.ID)
		}))},
		{capability.LLMProviderSelect, llmOperationHandler(capability.LLMProviderSelect, typedOperation[LLMProviderIDInput](capability.LLMProviderSelect, func(ctx context.Context, input LLMProviderIDInput) (any, error) {
			if _, err := service.SelectProvider(ctx, input.ID); err != nil {
				return nil, err
			}
			return service.ProviderResult(ctx, input.ID)
		}))},
		{capability.LLMProviderModels, llmOperationHandler(capability.LLMProviderModels, typedOperation[LLMProviderModelsInput](capability.LLMProviderModels, func(ctx context.Context, input LLMProviderModelsInput) (any, error) {
			return service.ModelCatalog(ctx, input.ID, input.Query)
		}))},
		{capability.LLMProviderProbe, llmOperationHandler(capability.LLMProviderProbe, typedOperation[LLMProviderIDInput](capability.LLMProviderProbe, func(ctx context.Context, input LLMProviderIDInput) (any, error) { return service.Probe(ctx, input.ID) }))},
		{capability.LLMProviderCredentialSet, llmOperationHandler(capability.LLMProviderCredentialSet, typedOperation[LLMProviderCredentialInput](capability.LLMProviderCredentialSet, func(ctx context.Context, input LLMProviderCredentialInput) (any, error) {
			if err := service.SetCredential(ctx, input.ID, input.APIKey); err != nil {
				return nil, err
			}
			return service.CredentialResult(ctx, input.ID)
		}))},
		{capability.LLMProviderCredentialClear, llmOperationHandler(capability.LLMProviderCredentialClear, typedOperation[LLMProviderIDInput](capability.LLMProviderCredentialClear, func(ctx context.Context, input LLMProviderIDInput) (any, error) {
			if err := service.ClearCredential(ctx, input.ID); err != nil {
				return nil, err
			}
			return service.CredentialResult(ctx, input.ID)
		}))},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}

func llmOperationHandler(id capability.ID, handler OperationHandler) OperationHandler {
	return func(ctx context.Context, input any) (any, error) {
		value, err := handler(ctx, input)
		if err == nil {
			return value, nil
		}
		return nil, llmApplicationError(id, err)
	}
}

func llmApplicationError(id capability.ID, err error) error {
	var operationErr *OperationError
	if errors.As(err, &operationErr) {
		return operationErr
	}
	value, ok := llm.AsError(err)
	if !ok {
		return err
	}
	code := ErrorInternal
	retryable := false
	switch value.Category {
	case llm.ErrorProviderNotFound:
		code = ErrorNotFound
	case llm.ErrorDuplicateID, llm.ErrorActiveRemoval:
		code = ErrorConflict
	case llm.ErrorUnsupported:
		code = ErrorUnsupported
	case llm.ErrorUnavailable, llm.ErrorRateLimited, llm.ErrorTimeout, llm.ErrorTransport, llm.ErrorProvider:
		code, retryable = ErrorUnavailable, true
	case llm.ErrorCancelled:
		code = ErrorUnavailable
	case llm.ErrorUnauthorized:
		code = ErrorUnavailable
	case llm.ErrorInvalidID, llm.ErrorReservedID, llm.ErrorInvalidProvider, llm.ErrorInvalidProtocol,
		llm.ErrorInvalidAuth, llm.ErrorInvalidDiscovery, llm.ErrorInvalidEndpoint, llm.ErrorCoreInvariant,
		llm.ErrorMissingCore, llm.ErrorInvalidActive, llm.ErrorMisconfigured, llm.ErrorInvalidRequest, llm.ErrorInvalidResponse:
		code = ErrorInvalidArgument
	}
	if retryable {
		return retryableOperationError(id, code, err)
	}
	return operationError(id, code, err)
}
