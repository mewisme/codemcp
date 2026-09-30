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

type LLMModelCatalogResult struct {
	ProviderID llm.ProviderID `json:"provider_id"`
	Models     []llm.Model    `json:"models"`
	Refreshed  bool           `json:"refreshed"`
}

type LLMProbeResult struct {
	ProviderID llm.ProviderID `json:"provider_id"`
	Model      string         `json:"model,omitempty"`
	Readiness  llm.Readiness  `json:"readiness"`
}

type LLMProviderIDInput struct {
	ID string `json:"id"`
}

type LLMProviderWriteInput struct {
	ID     string                  `json:"id"`
	Config CustomLLMProviderConfig `json:"config"`
}

type LLMProviderModelsInput struct {
	ID      string `json:"id"`
	Refresh bool   `json:"refresh,omitempty"`
}

type LLMProviderCredentialInput struct {
	ID     string `json:"id"`
	APIKey string `json:"-"`
}

type LLMProviderRemoveResult struct {
	ProviderID llm.ProviderID `json:"provider_id"`
	Removed    bool           `json:"removed"`
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

func (s *LLMService) ModelCatalog(ctx context.Context, rawID string, refresh bool) (LLMModelCatalogResult, error) {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return LLMModelCatalogResult{}, err
	}
	var models []llm.Model
	if refresh {
		models, err = s.RefreshProviderModels(ctx, string(provider.ID))
	} else {
		models, err = s.ProviderModels(ctx, string(provider.ID))
	}
	if err != nil {
		return LLMModelCatalogResult{}, err
	}
	return LLMModelCatalogResult{ProviderID: provider.ID, Models: models, Refreshed: refresh}, nil
}

func (s *LLMService) Probe(ctx context.Context, rawID string) (LLMProbeResult, error) {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return LLMProbeResult{}, err
	}
	if err := s.ProbeProvider(ctx, string(provider.ID)); err != nil {
		return LLMProbeResult{ProviderID: provider.ID, Model: provider.Model, Readiness: probeFailureReadiness(err)}, err
	}
	return LLMProbeResult{ProviderID: provider.ID, Model: provider.Model, Readiness: llm.ReadinessReady}, nil
}

func (s *LLMService) providerResult(ctx context.Context, provider llm.Provider, selected bool) (LLMProviderResult, error) {
	credential, err := s.CredentialResult(ctx, string(provider.ID))
	if err != nil {
		return LLMProviderResult{}, err
	}
	status := s.providerConfigurationStatus(provider, selected, credential.Configured)
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
			provider, err := service.ConfigureCustomProvider(ctx, input.ID, input.Config)
			if err != nil {
				return nil, err
			}
			return service.ProviderResult(ctx, string(provider.ID))
		}))},
		{capability.LLMProviderRemove, llmOperationHandler(capability.LLMProviderRemove, typedOperation[LLMProviderIDInput](capability.LLMProviderRemove, func(ctx context.Context, input LLMProviderIDInput) (any, error) {
			id, err := llm.NormalizeProviderID(input.ID)
			if err != nil {
				return nil, err
			}
			if err := service.RemoveProvider(ctx, input.ID); err != nil {
				return nil, err
			}
			return LLMProviderRemoveResult{ProviderID: id, Removed: true}, nil
		}))},
		{capability.LLMProviderSelect, llmOperationHandler(capability.LLMProviderSelect, typedOperation[LLMProviderIDInput](capability.LLMProviderSelect, func(ctx context.Context, input LLMProviderIDInput) (any, error) {
			if _, err := service.SelectProvider(ctx, input.ID); err != nil {
				return nil, err
			}
			return service.ProviderResult(ctx, input.ID)
		}))},
		{capability.LLMProviderModels, llmOperationHandler(capability.LLMProviderModels, typedOperation[LLMProviderModelsInput](capability.LLMProviderModels, func(ctx context.Context, input LLMProviderModelsInput) (any, error) {
			return service.ModelCatalog(ctx, input.ID, input.Refresh)
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
