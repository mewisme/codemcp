package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/capability"
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
	manager CFTunnelAssetManager
}

func NewCFTunnelService() *CFTunnelService {
	return NewCFTunnelServiceWithManager(cftunnel.New(cftunnel.Options{}))
}

func NewCFTunnelServiceWithManager(manager CFTunnelAssetManager) *CFTunnelService {
	return &CFTunnelService{manager: manager}
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
		{capability.TunnelCFStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.TunnelCFProbe, func(ctx context.Context, _ any) (any, error) { return service.Probe(ctx) }},
		{capability.TunnelCFInstall, func(ctx context.Context, _ any) (any, error) { return service.Install(ctx) }},
		{capability.TunnelCFUpdate, func(ctx context.Context, _ any) (any, error) { return service.Update(ctx) }},
		{capability.TunnelCFRemove, func(ctx context.Context, _ any) (any, error) { return service.Remove(ctx) }},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
