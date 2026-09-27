package application

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
)

type TypeSafeProviderState string

const (
	TypeSafeDisabled      TypeSafeProviderState = "disabled"
	TypeSafeMisconfigured TypeSafeProviderState = "misconfigured"
	TypeSafeReady         TypeSafeProviderState = "ready"
	TypeSafeDegraded      TypeSafeProviderState = "degraded"
)

type TypeSafeStatus struct {
	Enabled          bool                  `json:"enabled"`
	APIKeyConfigured bool                  `json:"api_key_configured"`
	State            TypeSafeProviderState `json:"state"`
	Model            string                `json:"model"`
	TimeoutMS        int                   `json:"timeout_ms"`
}

type TypeSafeProbeResult struct {
	Status        TypeSafeStatus                         `json:"status"`
	Provider      typesafeintegration.ProbeResult        `json:"provider"`
	ErrorCategory typesafeintegration.ProbeErrorCategory `json:"error_category,omitempty"`
}

type TypeSafeService struct {
	LoadConfig func() (config.Config, error)
	SetField   func(context.Context, string, string) (SettingResult, error)
	Root       func() string
	HTTPClient *http.Client
	BaseURL    string
}

func NewTypeSafeService() *TypeSafeService {
	service := NewSettingService()
	return &TypeSafeService{
		LoadConfig: config.Load,
		SetField:   service.Set,
		Root:       config.RootPath,
	}
}

func (s *TypeSafeService) Status(context.Context) (TypeSafeStatus, error) {
	if s == nil || s.LoadConfig == nil || s.Root == nil {
		return TypeSafeStatus{}, errors.New("typesafe integration status is unavailable")
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return TypeSafeStatus{}, err
	}
	credential, err := typesafeintegration.Credential(s.Root())
	if err != nil {
		return TypeSafeStatus{}, err
	}
	status := TypeSafeStatus{
		Enabled: cfg.Integrations.TypeSafe.Enabled, APIKeyConfigured: credential.Configured,
		Model: cfg.Integrations.TypeSafe.Model, TimeoutMS: cfg.Integrations.TypeSafe.TimeoutMS,
	}
	switch {
	case !status.Enabled:
		status.State = TypeSafeDisabled
	case !status.APIKeyConfigured:
		status.State = TypeSafeMisconfigured
	default:
		status.State = TypeSafeReady
	}
	return status, nil
}

func (s *TypeSafeService) Enable(ctx context.Context) (TypeSafeStatus, error) {
	return s.setEnabled(ctx, true)
}

func (s *TypeSafeService) Disable(ctx context.Context) (TypeSafeStatus, error) {
	return s.setEnabled(ctx, false)
}

func (s *TypeSafeService) setEnabled(ctx context.Context, enabled bool) (TypeSafeStatus, error) {
	if s == nil || s.SetField == nil {
		return TypeSafeStatus{}, errors.New("typesafe integration mutation is unavailable")
	}
	if _, err := s.SetField(ctx, "integrations.typesafe.enabled", strconv.FormatBool(enabled)); err != nil {
		return TypeSafeStatus{}, err
	}
	return s.Status(ctx)
}

func (s *TypeSafeService) Probe(ctx context.Context) (TypeSafeProbeResult, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return TypeSafeProbeResult{}, err
	}
	result := TypeSafeProbeResult{Status: status}
	if !status.Enabled {
		return result, errors.New("typesafe integration is disabled")
	}
	if !status.APIKeyConfigured {
		return result, errors.New("typesafe API key is not configured")
	}
	apiKey, err := typesafeintegration.LoadAPIKey(s.Root())
	if err != nil {
		return result, err
	}
	provider, err := typesafeintegration.Probe(
		ctx, apiKey, status.Model, time.Duration(status.TimeoutMS)*time.Millisecond,
		typesafeintegration.ProbeOptions{Client: s.HTTPClient, BaseURL: s.BaseURL},
	)
	result.Provider = provider
	if err == nil {
		return result, nil
	}
	var providerErr *typesafeintegration.ProbeError
	if errors.As(err, &providerErr) {
		result.ErrorCategory = providerErr.Category
		result.Status.State = TypeSafeDegraded
	}
	return result, err
}

func BindTypeSafeOperations(dispatcher *Dispatcher, service *TypeSafeService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("TypeSafe service is nil")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.IntegrationTypeSafeStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.IntegrationTypeSafeEnable, func(ctx context.Context, _ any) (any, error) { return service.Enable(ctx) }},
		{capability.IntegrationTypeSafeDisable, func(ctx context.Context, _ any) (any, error) { return service.Disable(ctx) }},
		{capability.IntegrationTypeSafeProbe, func(ctx context.Context, _ any) (any, error) { return service.Probe(ctx) }},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
