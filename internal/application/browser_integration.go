package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/browser"
)

type BrowserIntegrationState string

const (
	BrowserIntegrationDisabled    BrowserIntegrationState = "disabled"
	BrowserIntegrationUnavailable BrowserIntegrationState = "unavailable"
	BrowserIntegrationAvailable   BrowserIntegrationState = "available"
	BrowserIntegrationRunning     BrowserIntegrationState = "running"
)

type BrowserIntegrationStatus struct {
	Enabled      bool                    `json:"enabled"`
	State        BrowserIntegrationState `json:"state"`
	Family       browser.Family          `json:"family,omitempty"`
	Version      string                  `json:"version,omitempty"`
	HostPlatform string                  `json:"host_platform,omitempty"`
	Transport    browser.Transport       `json:"transport,omitempty"`
	Graphical    bool                    `json:"graphical"`
	Running      bool                    `json:"running"`
	Reason       string                  `json:"reason,omitempty"`
}

type BrowserDoctorCheck struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type BrowserDoctorResult struct {
	Status BrowserIntegrationStatus `json:"status"`
	Checks []BrowserDoctorCheck     `json:"checks"`
}

type BrowserIntegrationService struct {
	LoadConfig func() (config.Config, error)
	Root       func() string
	Detect     func(context.Context, browser.Options) browser.Capability
}

func NewBrowserIntegrationService() *BrowserIntegrationService {
	return &BrowserIntegrationService{
		LoadConfig: config.Load,
		Root:       config.RootPath,
		Detect:     browser.Detect,
	}
}

func (service *BrowserIntegrationService) Status(ctx context.Context) (BrowserIntegrationStatus, error) {
	_, capability, err := service.capability(ctx)
	if err != nil {
		return BrowserIntegrationStatus{}, err
	}
	status := BrowserIntegrationStatus{
		Enabled: capability.Enabled, Family: capability.Family, Version: capability.Version,
		HostPlatform: capability.HostPlatform, Transport: capability.Transport,
		Graphical: capability.Graphical, Reason: capability.Reason,
	}
	switch capability.State {
	case browser.StateDisabled:
		status.State = BrowserIntegrationDisabled
		return status, nil
	case browser.StateUnavailable:
		status.State = BrowserIntegrationUnavailable
		return status, nil
	case browser.StateAvailable:
		status.State = BrowserIntegrationAvailable
	default:
		return BrowserIntegrationStatus{}, errors.New("browser capability returned an unknown state")
	}
	if capability.Profile == nil {
		return BrowserIntegrationStatus{}, errors.New("available browser capability has no owned profile")
	}
	busy, err := browser.ProfileInUse(*capability.Profile)
	if err != nil {
		return BrowserIntegrationStatus{}, err
	}
	if busy {
		status.State = BrowserIntegrationRunning
		status.Running = true
	}
	return status, nil
}

func (service *BrowserIntegrationService) Doctor(ctx context.Context) (BrowserDoctorResult, error) {
	status, err := service.Status(ctx)
	if err != nil {
		return BrowserDoctorResult{}, err
	}
	result := BrowserDoctorResult{Status: status}
	result.Checks = append(result.Checks,
		BrowserDoctorCheck{
			ID: "enabled", OK: status.Enabled,
			Message: "browser capability detection is enabled",
		},
		BrowserDoctorCheck{
			ID: "usable", OK: status.State == BrowserIntegrationAvailable || status.State == BrowserIntegrationRunning,
			Message: "a supported Chrome, Chromium, or Edge route is usable",
		},
		BrowserDoctorCheck{
			ID: "graphical", OK: status.Graphical,
			Message: "a graphical browser route is available",
		},
	)
	return result, nil
}

func (service *BrowserIntegrationService) capability(ctx context.Context) (config.Config, browser.Capability, error) {
	if service == nil || service.LoadConfig == nil || service.Root == nil || service.Detect == nil {
		return config.Config{}, browser.Capability{}, errors.New("browser integration service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := service.LoadConfig()
	if err != nil {
		return config.Config{}, browser.Capability{}, err
	}
	capability := service.Detect(ctx, browser.Options{
		Enabled: cfg.Integrations.Browser.Enabled, ConfiguredPath: cfg.Integrations.Browser.Path,
		StateRoot: service.Root(),
	})
	return cfg, capability, nil
}

func BindBrowserIntegrationOperations(dispatcher *Dispatcher, service *BrowserIntegrationService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("browser integration service is nil")
	}
	for _, binding := range []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.IntegrationBrowserStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.IntegrationBrowserDoctor, func(ctx context.Context, _ any) (any, error) { return service.Doctor(ctx) }},
	} {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
