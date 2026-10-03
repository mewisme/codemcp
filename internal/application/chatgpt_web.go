package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/browser"
	"go.mewis.me/codemcp/internal/integrations/chatgptweb"
	"go.mewis.me/codemcp/internal/tunnel"
)

const (
	chatGPTWebLoginLease  = "__chatgpt_web_login__"
	chatGPTWebDoctorLease = "__chatgpt_web_doctor__"
)

type ChatGPTWebStatus struct {
	Enabled            bool                    `json:"enabled"`
	State              chatgptweb.State        `json:"state"`
	BrowserState       BrowserIntegrationState `json:"browser_state"`
	BrowserFamily      browser.Family          `json:"browser_family,omitempty"`
	BrowserTransport   browser.Transport       `json:"browser_transport,omitempty"`
	Authenticated      bool                    `json:"authenticated"`
	ConnectorName      string                  `json:"connector_name"`
	ConnectorAvailable bool                    `json:"connector_available"`
	MaxAgents          int                     `json:"max_agents"`
	Reason             string                  `json:"reason,omitempty"`
}

type ChatGPTWebDoctorCheck struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type ChatGPTWebDoctorResult struct {
	Status       ChatGPTWebStatus        `json:"status"`
	Checks       []ChatGPTWebDoctorCheck `json:"checks"`
	LiveVerified bool                    `json:"live_verified"`
}

type ChatGPTWebLogoutInput struct {
	Force bool `json:"force"`
}

type chatGPTBrowserRuntime interface {
	Acquire(context.Context, string) (browser.LeaseSnapshot, error)
	Tab(string) (browser.BrowserTab, bool)
	Release(context.Context, string) error
	Snapshot() browser.ManagerSnapshot
	Close(context.Context) error
}

type ChatGPTWebService struct {
	browserMu sync.Mutex

	LoadConfig func() (config.Config, error)
	Root       func() string
	Detect     func(context.Context, browser.Options) browser.Capability
	NewManager func(browser.ManagerOptions) (chatGPTBrowserRuntime, error)
	Probe      chatgptweb.AuthProbe
	Now        func() time.Time

	LoginTimeout  time.Duration
	DoctorTimeout time.Duration
	PollInterval  time.Duration
	OwnedManager  chatGPTBrowserRuntime

	ResolveOwnedProfiles func(context.Context, browser.OwnedProfileOptions) ([]browser.ProfileRef, error)
}

func NewChatGPTWebService() *ChatGPTWebService {
	return &ChatGPTWebService{
		LoadConfig: config.Load,
		Root:       config.RootPath,
		Detect:     browser.Detect,
		NewManager: func(options browser.ManagerOptions) (chatGPTBrowserRuntime, error) {
			return browser.NewManager(options)
		},
		Probe:                chatgptweb.DOMAuthProbe{},
		Now:                  time.Now,
		LoginTimeout:         10 * time.Minute,
		DoctorTimeout:        10 * time.Second,
		PollInterval:         500 * time.Millisecond,
		ResolveOwnedProfiles: browser.ResolveOwnedProfiles,
	}
}

func (service *ChatGPTWebService) Status(ctx context.Context) (ChatGPTWebStatus, error) {
	cfg, capability, err := service.capability(ctx)
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	status := service.baseStatus(cfg, capability)
	if !cfg.Integrations.ChatGPTWeb.Enabled {
		status.State = chatgptweb.StateDisabled
		return status, nil
	}
	if capability.State != browser.StateAvailable || !capability.Usable {
		status.State = chatgptweb.StateBrowserUnavailable
		status.Reason = boundedIntegrationReason(capability.Reason)
		return status, nil
	}
	_, authenticated, markerErr := chatgptweb.LoadAuthMarker(service.Root())
	if markerErr != nil {
		status.State = chatgptweb.StateDegraded
		status.Reason = boundedIntegrationReason(markerErr.Error())
		return status, nil
	}
	status.Authenticated = authenticated
	if !authenticated {
		status.State = chatgptweb.StateNeedsLogin
		return status, nil
	}
	if capability.Profile == nil {
		status.State = chatgptweb.StateDegraded
		status.Reason = "authenticated marker exists but the CodeMCP browser profile is unresolved"
		return status, nil
	}
	if _, err := os.Stat(capability.Profile.LocalPath); err != nil {
		status.State = chatgptweb.StateDegraded
		if errors.Is(err, os.ErrNotExist) {
			status.Reason = "authenticated marker exists but the CodeMCP browser profile is missing"
		} else {
			status.Reason = "CodeMCP browser profile cannot be inspected"
		}
		return status, nil
	}
	if !status.ConnectorAvailable {
		status.State = chatgptweb.StateConnectorUnavailable
		status.Reason = "configured CodeMCP Secure MCP connector route is unavailable"
		return status, nil
	}
	status.State = chatgptweb.StateReady
	return status, nil
}

func (service *ChatGPTWebService) Login(ctx context.Context) (ChatGPTWebStatus, error) {
	if service == nil || service.Probe == nil || service.Now == nil {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web authentication service is unavailable")
	}
	service.browserMu.Lock()
	defer service.browserMu.Unlock()
	cfg, capability, err := service.capability(ctx)
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	if !cfg.Integrations.ChatGPTWeb.Enabled {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web integration is disabled")
	}
	if capability.State != browser.StateAvailable || !capability.Usable {
		return ChatGPTWebStatus{}, fmt.Errorf("ChatGPT Web browser is unavailable: %s", boundedIntegrationReason(capability.Reason))
	}
	runtime, oneShot, err := service.browserRuntime(capability)
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	runtimeClosed := false
	if oneShot {
		defer func() {
			if !runtimeClosed {
				_ = runtime.Close(context.Background())
			}
		}()
	}
	if runtime.Snapshot().ActiveLeases > 0 {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web login requires exclusive browser profile access while no agent tabs are active")
	}
	lease, err := runtime.Acquire(ctx, chatGPTWebLoginLease)
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	leaseActive := true
	defer func() {
		if leaseActive {
			_ = runtime.Release(context.Background(), lease.AgentID)
		}
	}()
	tab, ok := runtime.Tab(lease.AgentID)
	if !ok {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web login tab is unavailable")
	}
	if err := tab.Navigate(ctx, chatgptweb.TemporaryChatURL); err != nil {
		return ChatGPTWebStatus{}, fmt.Errorf("open ChatGPT Temporary Chat: %w", err)
	}
	if _, err := service.waitForAuth(ctx, tab, service.loginTimeout()); err != nil {
		return ChatGPTWebStatus{}, err
	}
	if err := chatgptweb.WriteAuthMarker(service.Root(), service.Now()); err != nil {
		return ChatGPTWebStatus{}, err
	}
	if err := runtime.Release(context.Background(), lease.AgentID); err != nil {
		return ChatGPTWebStatus{}, err
	}
	leaseActive = false
	if oneShot {
		if err := runtime.Close(context.Background()); err != nil {
			return ChatGPTWebStatus{}, err
		}
		runtimeClosed = true
	}
	return service.Status(ctx)
}

func (service *ChatGPTWebService) Doctor(ctx context.Context) (ChatGPTWebDoctorResult, error) {
	if service == nil {
		return ChatGPTWebDoctorResult{}, errors.New("ChatGPT Web integration service is unavailable")
	}
	service.browserMu.Lock()
	defer service.browserMu.Unlock()
	status, err := service.Status(ctx)
	if err != nil {
		return ChatGPTWebDoctorResult{}, err
	}
	result := ChatGPTWebDoctorResult{Status: status}
	result.Checks = append(result.Checks,
		ChatGPTWebDoctorCheck{ID: "browser", OK: status.BrowserState == BrowserIntegrationAvailable || status.BrowserState == BrowserIntegrationRunning, Message: "supported isolated browser route is available"},
		ChatGPTWebDoctorCheck{ID: "authentication_marker", OK: status.Authenticated, Message: "last verified ChatGPT authentication marker is present"},
		ChatGPTWebDoctorCheck{ID: "connector_route", OK: status.ConnectorAvailable, Message: "CodeMCP Secure MCP connector route is configured"},
	)
	if !status.Enabled || !status.Authenticated || status.BrowserState == BrowserIntegrationUnavailable || status.BrowserState == BrowserIntegrationDisabled {
		return result, nil
	}
	_, capability, err := service.capability(ctx)
	if err != nil {
		return ChatGPTWebDoctorResult{}, err
	}
	if status.BrowserState == BrowserIntegrationRunning && service.OwnedManager == nil {
		result.Checks = append(result.Checks, ChatGPTWebDoctorCheck{ID: "live_auth", OK: false, Message: "live authentication probe deferred because another CodeMCP process owns the browser profile"})
		return result, nil
	}
	runtime, oneShot, err := service.browserRuntime(capability)
	if err != nil {
		return ChatGPTWebDoctorResult{}, err
	}
	if oneShot {
		defer runtime.Close(context.Background())
	}
	if runtime.Snapshot().ActiveLeases > 0 {
		result.Checks = append(result.Checks, ChatGPTWebDoctorCheck{ID: "live_auth", OK: false, Message: "live authentication probe deferred while agent tabs are active"})
		return result, nil
	}
	lease, err := runtime.Acquire(ctx, chatGPTWebDoctorLease)
	if err != nil {
		return ChatGPTWebDoctorResult{}, err
	}
	defer runtime.Release(context.Background(), lease.AgentID)
	tab, ok := runtime.Tab(lease.AgentID)
	if !ok {
		return ChatGPTWebDoctorResult{}, errors.New("ChatGPT Web doctor tab is unavailable")
	}
	if err := tab.Navigate(ctx, chatgptweb.TemporaryChatURL); err != nil {
		return ChatGPTWebDoctorResult{}, err
	}
	evidence, probeErr := service.waitForAuth(ctx, tab, service.doctorTimeout())
	result.LiveVerified = probeErr == nil && evidence.Ready()
	result.Checks = append(result.Checks, ChatGPTWebDoctorCheck{ID: "live_auth", OK: result.LiveVerified, Message: "server-authenticated Temporary Chat composer is live"})
	if !result.LiveVerified {
		result.Status.Authenticated = false
		result.Status.State = chatgptweb.StateNeedsLogin
		result.Status.Reason = "live ChatGPT authentication could not be verified"
		return result, nil
	}
	result.Status.Authenticated = true
	if result.Status.ConnectorAvailable {
		result.Status.State = chatgptweb.StateReady
	} else {
		result.Status.State = chatgptweb.StateConnectorUnavailable
	}
	return result, nil
}

func (service *ChatGPTWebService) Logout(ctx context.Context, force bool) (ChatGPTWebStatus, error) {
	if service == nil || service.Root == nil || service.ResolveOwnedProfiles == nil {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web logout service is unavailable")
	}
	service.browserMu.Lock()
	defer service.browserMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if service.OwnedManager != nil {
		snapshot := service.OwnedManager.Snapshot()
		if snapshot.ActiveLeases > 0 && !force {
			return ChatGPTWebStatus{}, errors.New("ChatGPT Web has active agent tabs; retry with force after acknowledging cancellation")
		}
		if err := service.OwnedManager.Close(ctx); err != nil {
			return ChatGPTWebStatus{}, err
		}
		service.OwnedManager = nil
	}
	profiles, err := service.ResolveOwnedProfiles(ctx, browser.OwnedProfileOptions{StateRoot: service.Root()})
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	locks := make([]*browser.ProfileLock, 0, len(profiles))
	defer func() {
		for _, lock := range locks {
			_ = lock.Release()
		}
	}()
	for _, profile := range profiles {
		lock, ok, lockErr := browser.TryAcquireProfile(profile)
		if lockErr != nil {
			return ChatGPTWebStatus{}, lockErr
		}
		if !ok {
			return ChatGPTWebStatus{}, browser.ErrProfileBusy
		}
		locks = append(locks, lock)
	}
	for _, profile := range profiles {
		if err := browser.RemoveProfile(profile); err != nil {
			return ChatGPTWebStatus{}, err
		}
	}
	if err := chatgptweb.RemoveAuthMarker(service.Root()); err != nil {
		return ChatGPTWebStatus{}, err
	}
	return service.Status(ctx)
}

func (service *ChatGPTWebService) capability(ctx context.Context) (config.Config, browser.Capability, error) {
	if service == nil || service.LoadConfig == nil || service.Root == nil || service.Detect == nil {
		return config.Config{}, browser.Capability{}, errors.New("ChatGPT Web integration service is unavailable")
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

func (service *ChatGPTWebService) baseStatus(cfg config.Config, capability browser.Capability) ChatGPTWebStatus {
	browserState := BrowserIntegrationUnavailable
	switch capability.State {
	case browser.StateDisabled:
		browserState = BrowserIntegrationDisabled
	case browser.StateAvailable:
		browserState = BrowserIntegrationAvailable
		if capability.Profile != nil {
			if busy, err := browser.ProfileInUse(*capability.Profile); err == nil && busy {
				browserState = BrowserIntegrationRunning
			}
		}
	}
	connectorAvailable := cfg.Tunnel.Enabled && tunnel.Configured(cfg.Tunnel)
	return ChatGPTWebStatus{
		Enabled:      cfg.Integrations.ChatGPTWeb.Enabled,
		BrowserState: browserState, BrowserFamily: capability.Family, BrowserTransport: capability.Transport,
		ConnectorName:      strings.TrimSpace(cfg.Integrations.ChatGPTWeb.ConnectorName),
		ConnectorAvailable: connectorAvailable, MaxAgents: cfg.Integrations.ChatGPTWeb.MaxAgents,
	}
}

func (service *ChatGPTWebService) browserRuntime(capability browser.Capability) (chatGPTBrowserRuntime, bool, error) {
	if service.OwnedManager != nil {
		return service.OwnedManager, false, nil
	}
	if service.NewManager == nil {
		return nil, false, errors.New("ChatGPT Web browser manager factory is unavailable")
	}
	runtime, err := service.NewManager(browser.ManagerOptions{Capability: capability, MaxTabs: 1})
	if err != nil {
		return nil, false, err
	}
	return runtime, true, nil
}

func (service *ChatGPTWebService) waitForAuth(ctx context.Context, tab browser.BrowserTab, timeout time.Duration) (chatgptweb.AuthEvidence, error) {
	if service == nil || service.Probe == nil {
		return chatgptweb.AuthEvidence{}, errors.New("ChatGPT Web authentication probe is unavailable")
	}
	if timeout <= 0 {
		return chatgptweb.AuthEvidence{}, errors.New("ChatGPT Web authentication timeout must be positive")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	interval := service.PollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last chatgptweb.AuthEvidence
	for {
		evidence, err := service.Probe.Probe(probeCtx, tab)
		if err == nil {
			last = evidence
			if evidence.Ready() {
				return evidence, nil
			}
			if evidence.OriginOK && evidence.Authenticated && !evidence.TemporaryChat {
				if navErr := tab.Navigate(probeCtx, chatgptweb.TemporaryChatURL); navErr != nil {
					return last, navErr
				}
			}
		}
		select {
		case <-probeCtx.Done():
			if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
				return last, errors.New("ChatGPT Web server-authenticated Temporary Chat composer could not be verified")
			}
			return last, probeCtx.Err()
		case <-tab.Done():
			if tab.Err() != nil {
				return last, tab.Err()
			}
			return last, errors.New("ChatGPT Web browser tab closed before authentication completed")
		case <-ticker.C:
		}
	}
}

func (service *ChatGPTWebService) loginTimeout() time.Duration {
	if service.LoginTimeout > 0 {
		return service.LoginTimeout
	}
	return 10 * time.Minute
}

func (service *ChatGPTWebService) doctorTimeout() time.Duration {
	if service.DoctorTimeout > 0 {
		return service.DoctorTimeout
	}
	return 10 * time.Second
}

func boundedIntegrationReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= browser.ReasonLimit {
		return value
	}
	return value[:browser.ReasonLimit]
}

func BindChatGPTWebOperations(dispatcher *Dispatcher, service *ChatGPTWebService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("ChatGPT Web service is nil")
	}
	for _, binding := range []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.IntegrationChatGPTWebStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.IntegrationChatGPTWebLogin, func(ctx context.Context, _ any) (any, error) { return service.Login(ctx) }},
		{capability.IntegrationChatGPTWebLogout, func(ctx context.Context, input any) (any, error) {
			value, _ := input.(ChatGPTWebLogoutInput)
			return service.Logout(ctx, value.Force)
		}},
		{capability.IntegrationChatGPTWebDoctor, func(ctx context.Context, _ any) (any, error) { return service.Doctor(ctx) }},
	} {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
