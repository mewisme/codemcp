package application

import (
	"context"
	"errors"
	"net/http"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
)

type CodeGraphService struct {
	LoadConfig  func() (config.Config, error)
	ManagedRoot string
	HTTPClient  *http.Client
}

func NewCodeGraphService() *CodeGraphService {
	return &CodeGraphService{LoadConfig: config.Load}
}

func (s *CodeGraphService) Status(context.Context) (codegraph.Status, error) {
	runtime, err := s.runtime()
	if err != nil {
		return codegraph.Status{}, err
	}
	return runtime.Status()
}

func (s *CodeGraphService) Probe(ctx context.Context) (codegraph.ProbeResult, error) {
	runtime, err := s.runtime()
	if err != nil {
		return codegraph.ProbeResult{}, err
	}
	return runtime.Probe(ctx)
}

func (s *CodeGraphService) Install(ctx context.Context) (codegraph.InstallResult, error) {
	runtime, err := s.runtime()
	if err != nil {
		return codegraph.InstallResult{}, err
	}
	return runtime.Install(ctx)
}

func (s *CodeGraphService) runtime() (*codegraph.Runtime, error) {
	if s == nil || s.LoadConfig == nil {
		return nil, errors.New("CodeGraph config loader is unavailable")
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}
	return codegraph.New(codegraph.Options{
		Enabled:        cfg.Integrations.CodeGraph.Enabled,
		ConfiguredPath: cfg.Integrations.CodeGraph.Path,
		ManagedRoot:    s.ManagedRoot,
		HTTPClient:     s.HTTPClient,
	}), nil
}

func BindCodeGraphOperations(dispatcher *Dispatcher, service *CodeGraphService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("CodeGraph service is nil")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.IntegrationCodeGraphStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.IntegrationCodeGraphProbe, func(ctx context.Context, _ any) (any, error) { return service.Probe(ctx) }},
		{capability.IntegrationCodeGraphInstall, func(ctx context.Context, _ any) (any, error) { return service.Install(ctx) }},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
