package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/cftunnel"
)

type CFTunnelAssetManager interface {
	Status() (cftunnel.Status, error)
	Probe(context.Context) (cftunnel.ProbeResult, error)
	Install(context.Context) (cftunnel.InstallResult, error)
	Update(context.Context) (cftunnel.InstallResult, error)
	Remove() (cftunnel.RemoveResult, error)
	ResolvePath() (string, error)
}

type CFTunnelService struct {
	manager    CFTunnelAssetManager
	LoadConfig func() (config.Config, error)
}

func NewCFTunnelService() *CFTunnelService {
	return NewCFTunnelServiceWithManager(cftunnel.New(cftunnel.Options{}))
}

func NewCFTunnelServiceWithManager(manager CFTunnelAssetManager) *CFTunnelService {
	return &CFTunnelService{manager: manager, LoadConfig: config.Load}
}

func (service *CFTunnelService) Status(context.Context) (cftunnel.Status, error) {
	if service == nil || service.manager == nil {
		return cftunnel.Status{}, errors.New("cf-tunnel service is unavailable")
	}
	return service.manager.Status()
}

func (service *CFTunnelService) Probe(ctx context.Context) (cftunnel.ProbeResult, error) {
	if service == nil || service.manager == nil {
		return cftunnel.ProbeResult{}, errors.New("cf-tunnel service is unavailable")
	}
	return service.manager.Probe(ctx)
}

func (service *CFTunnelService) Install(ctx context.Context) (cftunnel.InstallResult, error) {
	if service == nil || service.manager == nil {
		return cftunnel.InstallResult{}, errors.New("cf-tunnel service is unavailable")
	}
	return service.manager.Install(ctx)
}

func (service *CFTunnelService) EnsureAvailable(ctx context.Context, values ...IntegrationEnsureOptions) (IntegrationEnsureResult, error) {
	options := integrationEnsureOptions(values)
	result := IntegrationEnsureResult{Integration: "cf-tunnel", Retry: "cm integration cf install"}
	if service == nil || service.LoadConfig == nil {
		result.State, result.Detail = "failed", "cf-tunnel config loader is unavailable"
		return result, errors.New(result.Detail)
	}
	cfg, err := service.LoadConfig()
	if err != nil {
		result.State, result.Detail = "failed", err.Error()
		return result, err
	}
	if !cfg.Telegram.Enabled || !cfg.Telegram.LogsMiniApp.Enabled {
		result.State, result.Detail = "skipped", "Telegram Logs Mini App is disabled"
		emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "check", "skipped", result.Detail)
		return result, nil
	}
	emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "check", "running", "checking existing executable")
	status, err := service.Status(ctx)
	result.Source = string(status.Source)
	if err != nil {
		result.State, result.Detail = "failed", err.Error()
		emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "check", "failed", result.Detail)
		return result, err
	}
	if status.Verified && status.Path != "" && status.Source != cftunnel.SourceUnavailable {
		result.State = "available"
		emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "check", "success", "existing executable available")
		return result, nil
	}
	if !status.ManagedSupported {
		result.State, result.Detail = "unavailable", "managed cf-tunnel is unsupported on this platform"
		emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "check", "unavailable", result.Detail)
		return result, nil
	}
	if options.SkipManagedInstall {
		result.State, result.Detail = "skipped", "managed installation disabled for this invocation"
		emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "check", "skipped", result.Detail)
		return result, nil
	}
	emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "install", "running", "installing managed asset")
	installed, err := service.Install(ctx)
	if err != nil {
		result.State, result.Detail = "failed", err.Error()
		emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "install", "failed", result.Detail)
		return result, err
	}
	result.Source = string(installed.Status.Source)
	switch {
	case installed.Installed:
		result.State = "installed"
	case installed.AlreadyInstalled:
		result.State = "available"
	default:
		result.State = "unavailable"
	}
	emitIntegrationEnsureEvent(options.Observe, "cf-tunnel", "install", result.State, "managed asset processed")
	return result, nil
}

func (service *CFTunnelService) Update(ctx context.Context) (cftunnel.InstallResult, error) {
	if service == nil || service.manager == nil {
		return cftunnel.InstallResult{}, errors.New("cf-tunnel service is unavailable")
	}
	return service.manager.Update(ctx)
}

func (service *CFTunnelService) Remove(context.Context) (cftunnel.RemoveResult, error) {
	if service == nil || service.manager == nil {
		return cftunnel.RemoveResult{}, errors.New("cf-tunnel service is unavailable")
	}
	return service.manager.Remove()
}

func (service *CFTunnelService) ResolvePath() (string, error) {
	if service == nil || service.manager == nil {
		return "", errors.New("cf-tunnel service is unavailable")
	}
	return service.manager.ResolvePath()
}

func BindCFTunnelOperations(dispatcher *Dispatcher, service *CFTunnelService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		service = NewCFTunnelService()
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.IntegrationCFStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.IntegrationCFProbe, func(ctx context.Context, _ any) (any, error) { return service.Probe(ctx) }},
		{capability.IntegrationCFInstall, func(ctx context.Context, _ any) (any, error) { return service.Install(ctx) }},
		{capability.IntegrationCFUpdate, func(ctx context.Context, _ any) (any, error) { return service.Update(ctx) }},
		{capability.IntegrationCFRemove, func(ctx context.Context, _ any) (any, error) { return service.Remove(ctx) }},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
