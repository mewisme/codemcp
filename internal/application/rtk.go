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
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type RTKService struct {
	LoadConfig        func() (config.Config, error)
	SetField          func(context.Context, string, string) (ConfigMutationResult, error)
	ManagedRoot       string
	HTTPClient        *http.Client
	SignatureVerifier rtk.SignatureVerifier
	ensureManager     func() (rtkEnsureManager, error)
}

type rtkEnsureManager interface {
	Status() (rtk.Status, error)
	Install(context.Context) (rtk.InstallResult, error)
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
	span := tracepkg.Start(ctx, "INTEGRATION", "integration.rtk.probe", "Probing RTK")
	manager, err := s.manager()
	if err != nil {
		span.Fail(err)
		return rtk.ProbeResult{}, err
	}
	result, err := manager.Probe(ctx)
	span.Finish(err)
	return result, err
}

func (s *RTKService) Install(ctx context.Context) (rtk.InstallResult, error) {
	span := tracepkg.Start(ctx, "INTEGRATION", "integration.rtk.install", "Installing RTK")
	manager, err := s.manager()
	if err != nil {
		span.Fail(err)
		return rtk.InstallResult{}, err
	}
	result, err := manager.Install(ctx)
	span.Finish(err)
	return result, err
}

func (s *RTKService) EnsureAvailable(ctx context.Context, values ...IntegrationEnsureOptions) (IntegrationEnsureResult, error) {
	options := integrationEnsureOptions(values)
	options.Observe = traceIntegrationEnsureObserver(ctx, options.Observe)
	emitIntegrationEnsureEvent(options.Observe, "rtk", "check", "running", "checking existing executable")
	manager, err := s.ensureAvailabilityManager()
	if err != nil {
		result := IntegrationEnsureResult{Integration: "rtk", State: "failed", Detail: err.Error(), Retry: "cm integration rtk install"}
		emitIntegrationEnsureEvent(options.Observe, "rtk", "check", "failed", result.Detail)
		return result, err
	}
	status, err := manager.Status()
	result := IntegrationEnsureResult{Integration: "rtk", Source: string(status.Source), Retry: "cm integration rtk install"}
	if err != nil {
		result.State, result.Detail = "failed", err.Error()
		emitIntegrationEnsureEvent(options.Observe, "rtk", "check", "failed", result.Detail)
		return result, err
	}
	if !status.Enabled || status.Source == rtk.SourceDisabled {
		result.State, result.Detail = "skipped", "disabled by configuration"
		emitIntegrationEnsureEvent(options.Observe, "rtk", "check", "skipped", result.Detail)
		return result, nil
	}
	if status.Verified && status.Path != "" && status.Source != rtk.SourceUnavailable {
		result.State = "available"
		emitIntegrationEnsureEvent(options.Observe, "rtk", "check", "success", "existing executable available")
		return result, nil
	}
	if !status.ManagedSupported {
		result.State, result.Detail = "unavailable", "managed RTK is unsupported on this platform"
		emitIntegrationEnsureEvent(options.Observe, "rtk", "check", "unavailable", result.Detail)
		return result, nil
	}
	if options.SkipManagedInstall {
		result.State, result.Detail = "skipped", "managed installation disabled for this invocation"
		emitIntegrationEnsureEvent(options.Observe, "rtk", "check", "skipped", result.Detail)
		return result, nil
	}
	emitIntegrationEnsureEvent(options.Observe, "rtk", "install", "running", "installing managed asset")
	installed, err := manager.Install(ctx)
	if err != nil {
		result.State, result.Detail = "failed", err.Error()
		emitIntegrationEnsureEvent(options.Observe, "rtk", "install", "failed", result.Detail)
		return result, err
	}
	result.Source = string(installed.Status.Source)
	if installed.Installed {
		result.State = "installed"
	} else if installed.AlreadyInstalled {
		result.State = "available"
	} else {
		result.State = "unavailable"
	}
	emitIntegrationEnsureEvent(options.Observe, "rtk", "install", result.State, "managed asset processed")
	return result, nil
}

func (s *RTKService) ensureAvailabilityManager() (rtkEnsureManager, error) {
	if s != nil && s.ensureManager != nil {
		return s.ensureManager()
	}
	return s.manager()
}

func (s *RTKService) ResolveGlobal(context.Context) (rtk.GlobalResolutionResult, error) {
	manager, err := s.manager()
	if err != nil {
		return rtk.GlobalResolutionResult{}, err
	}
	return manager.ResolveGlobal()
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
		{capability.IntegrationRTKInstallGlobal, func(ctx context.Context, _ any) (any, error) { return service.ResolveGlobal(ctx) }},
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
