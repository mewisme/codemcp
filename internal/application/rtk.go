package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/integrations/rtk"
)

type RTKService struct {
	LoadConfig        func() (config.Config, error)
	SetField          func(context.Context, string, string) (ConfigMutationResult, error)
	ManagedRoot       string
	HTTPClient        *http.Client
	SignatureVerifier rtk.SignatureVerifier
}

func NewRTKService() *RTKService {
	return &RTKService{
		LoadConfig: config.Load,
		SetField:   SetConfigField,
	}
}

func (s *RTKService) Status(context.Context) (rtk.Status, error) {
	manager, err := s.manager()
	if err != nil {
		return rtk.Status{}, err
	}
	return manager.Status()
}

func (s *RTKService) Enable(ctx context.Context) (rtk.Status, error) {
	return s.setEnabled(ctx, true)
}

func (s *RTKService) Disable(ctx context.Context) (rtk.Status, error) {
	return s.setEnabled(ctx, false)
}

func (s *RTKService) Probe(ctx context.Context) (rtk.ProbeResult, error) {
	manager, err := s.manager()
	if err != nil {
		return rtk.ProbeResult{}, err
	}
	return manager.Probe(ctx)
}

func (s *RTKService) Install(ctx context.Context) (rtk.InstallResult, error) {
	manager, err := s.manager()
	if err != nil {
		return rtk.InstallResult{}, err
	}
	return manager.Install(ctx)
}

func (s *RTKService) setEnabled(ctx context.Context, enabled bool) (rtk.Status, error) {
	if s == nil || s.SetField == nil {
		return rtk.Status{}, errors.New("RTK config mutator is unavailable")
	}
	if _, err := s.SetField(ctx, "integrations.rtk.enabled", fmt.Sprintf("%t", enabled)); err != nil {
		return rtk.Status{}, err
	}
	return s.Status(ctx)
}

func (s *RTKService) manager() (*rtk.Manager, error) {
	if s == nil || s.LoadConfig == nil {
		return nil, errors.New("RTK config loader is unavailable")
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}
	return rtk.New(rtk.Options{
		Enabled:           cfg.Integrations.RTK.Enabled,
		ConfiguredPath:    cfg.Integrations.RTK.Path,
		ManagedRoot:       s.ManagedRoot,
		HTTPClient:        s.HTTPClient,
		SignatureVerifier: s.SignatureVerifier,
	}), nil
}

func RTKStatusForConfig(cfg config.Config) (rtk.Status, error) {
	return rtk.New(rtk.Options{
		Enabled:        cfg.Integrations.RTK.Enabled,
		ConfiguredPath: cfg.Integrations.RTK.Path,
	}).Status()
}

func BindRTKOperations(dispatcher *Dispatcher, service *RTKService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("RTK service is nil")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.IntegrationRTKStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.IntegrationRTKEnable, func(ctx context.Context, _ any) (any, error) { return service.Enable(ctx) }},
		{capability.IntegrationRTKDisable, func(ctx context.Context, _ any) (any, error) { return service.Disable(ctx) }},
		{capability.IntegrationRTKProbe, func(ctx context.Context, _ any) (any, error) { return service.Probe(ctx) }},
		{capability.IntegrationRTKInstall, func(ctx context.Context, _ any) (any, error) { return service.Install(ctx) }},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}

func RTKProjectContextInstruction(context.Context, string, string) ([]instructioncontext.IntegrationInstruction, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	status, statusErr := RTKStatusForConfig(cfg)
	content := "RTK command rewriting is disabled."
	if cfg.Integrations.RTK.Enabled {
		source := string(status.Source)
		if source == "" {
			source = string(rtk.SourceUnavailable)
		}
		content = fmt.Sprintf("RTK command rewriting is enabled; source=%s; version=%s. Shell security and approval classification use the requested command before any RTK rewrite.", source, rtk.Version)
		if statusErr != nil {
			content = "RTK command rewriting is enabled but its configured executable state is unavailable. Shell security and approval classification still use the requested command."
		}
	}
	return []instructioncontext.IntegrationInstruction{{
		ID:      "rtk",
		Source:  "CodeMCP",
		Content: content,
	}}, nil
}
