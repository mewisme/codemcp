package application

import (
	"context"
	"strconv"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

type TelemetryStatus struct {
	PersistedEnabled    bool                          `json:"persisted_enabled"`
	EffectiveEnabled    bool                          `json:"effective_enabled"`
	Source              config.TelemetryEnabledSource `json:"source"`
	EnvironmentOverride bool                          `json:"environment_override"`
	EndpointAvailable   bool                          `json:"endpoint_available"`
	EndpointHost        string                        `json:"endpoint_host,omitempty"`
	Product             string                        `json:"product,omitempty"`
	IdentityPresent     bool                          `json:"identity_present"`
}

type TelemetryBootstrapResult struct {
	Enabled           bool `json:"enabled"`
	EndpointAvailable bool `json:"endpoint_available"`
	IdentityPresent   bool `json:"identity_present"`
	IdentityCreated   bool `json:"identity_created"`
}

type telemetryEnableReconciler interface {
	SetEnabled(bool)
}

type TelemetryService struct {
	Settings   *SettingService
	Client     telemetryEnableReconciler
	LoadConfig func(context.Context) (config.Config, error)
	Source     func() (configformat.Source, error)
	Endpoint   func() string
	Identity   *producttelemetry.IdentityStore
}

func NewTelemetryService() *TelemetryService {
	return &TelemetryService{
		Settings:   NewSettingService(),
		LoadConfig: LoadConfig,
		Source:     config.Source,
		Endpoint:   func() string { return producttelemetry.Endpoint },
		Identity:   producttelemetry.NewIdentityStore(),
	}
}

func (service *TelemetryService) Status(ctx context.Context) (TelemetryStatus, error) {
	if service == nil {
		service = NewTelemetryService()
	}
	load := service.LoadConfig
	if load == nil {
		load = LoadConfig
	}
	cfg, err := load(ctx)
	if err != nil {
		return TelemetryStatus{}, err
	}
	sourceFn := service.Source
	if sourceFn == nil {
		sourceFn = config.Source
	}
	source, err := sourceFn()
	if err != nil {
		return TelemetryStatus{}, err
	}
	effective := config.ResolveTelemetryEnabled(cfg, source.Exists)
	endpointMeta, err := producttelemetry.BuildEndpointMetadata()
	if service.Endpoint != nil {
		endpointMeta, err = producttelemetry.ParseEndpoint(service.Endpoint())
	}
	if err != nil {
		return TelemetryStatus{}, err
	}
	identityStore := service.Identity
	if identityStore == nil {
		identityStore = producttelemetry.NewIdentityStore()
	}
	_, identityPresent := identityStore.Read()
	return TelemetryStatus{
		PersistedEnabled:    cfg.Telemetry.Enabled,
		EffectiveEnabled:    effective.Enabled,
		Source:              effective.Source,
		EnvironmentOverride: effective.Source == config.TelemetrySourceEnv,
		EndpointAvailable:   endpointMeta.Available,
		EndpointHost:        endpointMeta.Host,
		Product:             endpointMeta.Product,
		IdentityPresent:     identityPresent,
	}, nil
}

func (service *TelemetryService) Show(ctx context.Context) (TelemetryStatus, error) {
	return service.Status(ctx)
}

func (service *TelemetryService) Enable(ctx context.Context) (TelemetryStatus, error) {
	return service.setEnabled(ctx, true)
}

func (service *TelemetryService) Disable(ctx context.Context) (TelemetryStatus, error) {
	return service.setEnabled(ctx, false)
}

func (service *TelemetryService) Bootstrap(ctx context.Context) (TelemetryBootstrapResult, error) {
	status, err := service.Status(ctx)
	if err != nil {
		return TelemetryBootstrapResult{}, err
	}
	result := TelemetryBootstrapResult{
		Enabled:           status.EffectiveEnabled,
		EndpointAvailable: status.EndpointAvailable,
		IdentityPresent:   status.IdentityPresent,
	}
	if !status.EffectiveEnabled || !status.EndpointAvailable {
		return result, nil
	}
	endpoint := producttelemetry.Endpoint
	if service.Endpoint != nil {
		endpoint = service.Endpoint()
	}
	store := service.Identity
	if store == nil {
		store = producttelemetry.NewIdentityStore()
	}
	_, created, err := store.Ensure(true, endpoint)
	if err != nil {
		return TelemetryBootstrapResult{}, err
	}
	result.IdentityPresent = true
	result.IdentityCreated = created
	return result, nil
}

func (service *TelemetryService) setEnabled(ctx context.Context, enabled bool) (TelemetryStatus, error) {
	settings := service.Settings
	if settings == nil {
		settings = NewSettingService()
	}
	if _, err := settings.Set(ctx, "telemetry.enabled", strconv.FormatBool(enabled)); err != nil {
		return TelemetryStatus{}, err
	}
	status, err := service.Status(ctx)
	if err != nil {
		return TelemetryStatus{}, err
	}
	if service.Client != nil {
		service.Client.SetEnabled(status.EffectiveEnabled)
	}
	return status, nil
}

func BindTelemetryOperations(dispatcher *Dispatcher, service *TelemetryService) error {
	if dispatcher == nil {
		return nil
	}
	if service == nil {
		service = NewTelemetryService()
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.TelemetryStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.TelemetryEnable, func(ctx context.Context, _ any) (any, error) { return service.Enable(ctx) }},
		{capability.TelemetryDisable, func(ctx context.Context, _ any) (any, error) { return service.Disable(ctx) }},
		{capability.TelemetryShow, func(ctx context.Context, _ any) (any, error) { return service.Show(ctx) }},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
