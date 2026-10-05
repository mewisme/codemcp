package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/browser"
	"go.mewis.me/codemcp/internal/integrations/chatgptweb"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

const (
	chatGPTWebVerificationLease = "__chatgpt_web_login_verify__"
	chatGPTWebDoctorLease       = "__chatgpt_web_doctor__"
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
	RuntimePending     bool                    `json:"runtime_pending"`
	Reason             string                  `json:"reason,omitempty"`
	loginAccount       *chatgptweb.AccountSummary
}

func (status ChatGPTWebStatus) LoginAccount() (chatgptweb.AccountSummary, bool) {
	if status.loginAccount == nil || status.loginAccount.Empty() {
		return chatgptweb.AccountSummary{}, false
	}
	return *status.loginAccount, true
}

type chatGPTBrowserRuntimeIdentity struct {
	Executable   string
	HostPlatform string
	Transport    browser.Transport
	ProfilePath  string
	Minimized    bool
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
	Lease(string) (browser.LeaseSnapshot, bool)
	Tab(string) (browser.BrowserTab, bool)
	Touch(string) error
	Release(context.Context, string) error
	Snapshot() browser.ManagerSnapshot
	Close(context.Context) error
}

func (service *ChatGPTWebService) AgentBackendSettings(ctx context.Context) (chatgptweb.AgentBackendSettings, error) {
	if service == nil {
		return chatgptweb.AgentBackendSettings{}, errors.New("ChatGPT Web integration service is unavailable")
	}
	status, err := service.status(ctx, false)
	if err != nil {
		return chatgptweb.AgentBackendSettings{}, err
	}
	reason := strings.TrimSpace(status.Reason)
	if reason == "" && status.State != chatgptweb.StateReady {
		reason = "ChatGPT Web is " + string(status.State)
	}
	return chatgptweb.AgentBackendSettings{
		Available:     status.State == chatgptweb.StateReady && !status.RuntimePending,
		Reason:        reason,
		MaxAgents:     status.MaxAgents,
		ConnectorName: status.ConnectorName,
	}, nil
}

func (service *ChatGPTWebService) AgentBrowserRuntime(ctx context.Context, maxAgents int) (chatgptweb.AgentBackendRuntime, error) {
	if service == nil {
		return nil, errors.New("ChatGPT Web integration service is unavailable")
	}
	if maxAgents < 1 || maxAgents > chatgptweb.DefaultMaxAgents {
		return nil, fmt.Errorf("ChatGPT Web max agents must be between 1 and %d", chatgptweb.DefaultMaxAgents)
	}
	status, err := service.status(ctx, false)
	if err != nil {
		return nil, err
	}
	if status.State != chatgptweb.StateReady {
		reason := strings.TrimSpace(status.Reason)
		if reason == "" {
			reason = "state is " + string(status.State)
		}
		return nil, fmt.Errorf("ChatGPT Web backend unavailable: %s", boundedIntegrationReason(reason))
	}
	cfg, capability, err := service.capability(ctx)
	if err != nil {
		return nil, err
	}
	service.browserMu.Lock()
	defer service.browserMu.Unlock()
	desiredIdentity := browserRuntimeIdentity(capability, cfg.Integrations.Browser.Minimized)
	if service.OwnedManager != nil {
		snapshot := service.OwnedManager.Snapshot()
		if snapshot.State == browser.ManagerClosed {
			service.OwnedManager = nil
			service.ownedRuntimeIdentity = chatGPTBrowserRuntimeIdentity{}
			service.runtimePendingReason = ""
		} else {
			profileChanged := service.ownedRuntimeIdentity != (chatGPTBrowserRuntimeIdentity{}) && service.ownedRuntimeIdentity != desiredIdentity
			capacityIncrease := snapshot.MaxTabs > 0 && maxAgents > snapshot.MaxTabs
			if profileChanged || capacityIncrease {
				if snapshot.ActiveLeases > 0 {
					service.runtimePendingReason = pendingBrowserRuntimeReason(profileChanged, capacityIncrease)
					return nil, errors.New(service.runtimePendingReason)
				}
				if err := service.OwnedManager.Close(applicationContext(ctx)); err != nil {
					return nil, err
				}
				service.OwnedManager = nil
				service.ownedRuntimeIdentity = chatGPTBrowserRuntimeIdentity{}
				service.runtimePendingReason = ""
			} else {
				service.runtimePendingReason = ""
				return service.OwnedManager, nil
			}
		}
	}
	if service.NewManager == nil {
		return nil, errors.New("ChatGPT Web browser manager factory is unavailable")
	}
	runtime, err := service.NewManager(browser.ManagerOptions{Capability: capability, MaxTabs: maxAgents, Minimized: cfg.Integrations.Browser.Minimized})
	if err != nil {
		return nil, err
	}
	service.OwnedManager = runtime
	service.ownedRuntimeIdentity = desiredIdentity
	service.runtimePendingReason = ""
	return runtime, nil
}

// ReconcileRuntimeConfig preserves active browser-backed agents while applying
// configuration changes that are safe without replacing their live profile.
func (service *ChatGPTWebService) ReconcileRuntimeConfig(ctx context.Context) error {
	if service == nil {
		return nil
	}
	cfg, capability, err := service.capability(ctx)
	if err != nil {
		return err
	}
	service.browserMu.Lock()
	defer service.browserMu.Unlock()
	if service.OwnedManager == nil {
		service.runtimePendingReason = ""
		return nil
	}
	snapshot := service.OwnedManager.Snapshot()
	if snapshot.State == browser.ManagerClosed {
		service.OwnedManager = nil
		service.ownedRuntimeIdentity = chatGPTBrowserRuntimeIdentity{}
		service.runtimePendingReason = ""
		return nil
	}
	desiredIdentity := browserRuntimeIdentity(capability, cfg.Integrations.Browser.Minimized)
	profileChanged := capability.State == browser.StateAvailable && service.ownedRuntimeIdentity != (chatGPTBrowserRuntimeIdentity{}) && service.ownedRuntimeIdentity != desiredIdentity
	capacityIncrease := snapshot.MaxTabs > 0 && cfg.Integrations.ChatGPTWeb.MaxAgents > snapshot.MaxTabs
	shouldRetire := !cfg.Integrations.Browser.Enabled || !cfg.Integrations.ChatGPTWeb.Enabled || profileChanged || capacityIncrease
	if !shouldRetire {
		service.runtimePendingReason = ""
		return nil
	}
	if snapshot.ActiveLeases > 0 {
		if profileChanged || capacityIncrease {
			service.runtimePendingReason = pendingBrowserRuntimeReason(profileChanged, capacityIncrease)
		}
		return nil
	}
	if err := service.OwnedManager.Close(applicationContext(ctx)); err != nil {
		return err
	}
	service.OwnedManager = nil
	service.ownedRuntimeIdentity = chatGPTBrowserRuntimeIdentity{}
	service.runtimePendingReason = ""
	return nil
}

func RegisterChatGPTWebAgentBackend(manager *managedagent.Manager, service *ChatGPTWebService) error {
	if manager == nil {
		return errors.New("managed agent manager is unavailable")
	}
	if service == nil {
		return errors.New("ChatGPT Web integration service is unavailable")
	}
	backend, err := chatgptweb.NewAgentBackend(chatgptweb.AgentBackendOptions{
		Manager:  manager,
		Settings: service.AgentBackendSettings,
		Runtime:  service.AgentBrowserRuntime,
	})
	if err != nil {
		return err
	}
	return manager.RegisterBackend(backend)
}

type ChatGPTWebService struct {
	browserMu sync.Mutex

	LoadConfig     func() (config.Config, error)
	Root           func() string
	Detect         func(context.Context, browser.Options) browser.Capability
	NewManager     func(browser.ManagerOptions) (chatGPTBrowserRuntime, error)
	RunInteractive func(context.Context, browser.InteractiveBrowserOptions) error
	Probe          chatgptweb.AuthProbe
	Now            func() time.Time

	LoginTimeout         time.Duration
	DoctorTimeout        time.Duration
	PollInterval         time.Duration
	OwnedManager         chatGPTBrowserRuntime
	ownedRuntimeIdentity chatGPTBrowserRuntimeIdentity
	runtimePendingReason string

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
		RunInteractive:       browser.RunInteractiveBrowser,
		Probe:                chatgptweb.DOMAuthProbe{},
		Now:                  time.Now,
		LoginTimeout:         10 * time.Minute,
		DoctorTimeout:        10 * time.Second,
		PollInterval:         500 * time.Millisecond,
		ResolveOwnedProfiles: browser.ResolveOwnedProfiles,
	}
}

func (service *ChatGPTWebService) Status(ctx context.Context) (ChatGPTWebStatus, error) {
	return service.status(ctx, false)
}

func (service *ChatGPTWebService) status(ctx context.Context, browserLocked bool) (ChatGPTWebStatus, error) {
	cfg, capability, err := service.capability(ctx)
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	return service.statusFromCapability(browserLocked, cfg, capability)
}

func (service *ChatGPTWebService) statusFromCapability(browserLocked bool, cfg config.Config, capability browser.Capability) (ChatGPTWebStatus, error) {
	status := service.baseStatus(cfg, capability)
	status = service.withRuntimePending(status, capability, cfg.Integrations.Browser.Minimized, browserLocked)
	if !cfg.Integrations.ChatGPTWeb.Enabled {
		status.State = chatgptweb.StateDisabled
		return status, nil
	}
	if capability.State != browser.StateAvailable {
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

func (service *ChatGPTWebService) withRuntimePending(status ChatGPTWebStatus, capability browser.Capability, minimized, browserLocked bool) ChatGPTWebStatus {
	if service == nil {
		return status
	}
	if !browserLocked {
		service.browserMu.Lock()
		defer service.browserMu.Unlock()
	}
	if service.OwnedManager == nil {
		return status
	}
	snapshot := service.OwnedManager.Snapshot()
	if snapshot.State == browser.ManagerClosed || snapshot.ActiveLeases == 0 {
		return status
	}
	desiredIdentity := browserRuntimeIdentity(capability, minimized)
	profileChanged := capability.State == browser.StateAvailable && service.ownedRuntimeIdentity != (chatGPTBrowserRuntimeIdentity{}) && service.ownedRuntimeIdentity != desiredIdentity
	capacityIncrease := snapshot.MaxTabs > 0 && status.MaxAgents > snapshot.MaxTabs
	reason := strings.TrimSpace(service.runtimePendingReason)
	if reason == "" && (profileChanged || capacityIncrease) {
		reason = pendingBrowserRuntimeReason(profileChanged, capacityIncrease)
	}
	if reason != "" {
		status.RuntimePending = true
		status.Reason = boundedIntegrationReason(reason)
	}
	return status
}

func (service *ChatGPTWebService) Login(ctx context.Context) (ChatGPTWebStatus, error) {
	if service == nil || service.Probe == nil || service.Now == nil || service.RunInteractive == nil {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web authentication service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	service.browserMu.Lock()
	defer service.browserMu.Unlock()
	cfg, capability, err := service.loginCapability(ctx)
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	if !cfg.Integrations.ChatGPTWeb.Enabled {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web integration is disabled")
	}
	if capability.State != browser.StateAvailable || !capability.Available || !capability.Launchable {
		return ChatGPTWebStatus{}, fmt.Errorf("ChatGPT Web browser is unavailable: %s", boundedIntegrationReason(capability.Reason))
	}
	if capability.Profile == nil {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web browser profile is unresolved")
	}
	profileSpan := tracepkg.Start(ctx, "CHATGPT", "chatgpt.auth.profile.prepare", "Preparing ChatGPT browser profile")
	if err := service.prepareInteractiveLoginLocked(ctx); err != nil {
		profileSpan.Fail(err)
		return ChatGPTWebStatus{}, err
	}
	if err := chatgptweb.RemoveAuthMarker(service.Root()); err != nil {
		profileSpan.Fail(err)
		return ChatGPTWebStatus{}, err
	}
	profileSpan.End()
	loginCtx, cancelLogin := context.WithTimeout(ctx, service.loginTimeout())
	interactiveSpan := tracepkg.Start(ctx, "CHATGPT", "chatgpt.auth.interactive", "Waiting for ChatGPT sign-in")
	err = service.RunInteractive(loginCtx, browser.InteractiveBrowserOptions{
		Capability: capability,
		URL:        chatgptweb.TemporaryChatURL,
	})
	cancelLogin()
	if err != nil {
		interactiveSpan.Fail(err)
		status, _ := service.loginFailureStatus(capability, chatgptweb.StateNeedsLogin,
			"interactive ChatGPT sign-in did not complete; complete sign-in and close the CodeMCP browser window")
		return status, fmt.Errorf("interactive ChatGPT sign-in did not complete: %w", err)
	}
	interactiveSpan.End()
	verificationRuntimeSpan := tracepkg.Start(ctx, "CHATGPT", "chatgpt.auth.verification.runtime", "Starting authentication verification")
	runtime, oneShot, err := service.browserRuntime(capability, cfg.Integrations.Browser.Minimized)
	if err != nil {
		verificationRuntimeSpan.Fail(err)
		status, _ := service.loginFailureStatus(capability, chatgptweb.StateDegraded,
			"managed browser verification could not start after interactive sign-in")
		return status, fmt.Errorf("start managed ChatGPT verification browser: %w", err)
	}
	runtimeClosed := false
	defer func() {
		if oneShot && !runtimeClosed {
			_ = runtime.Close(context.Background())
		}
	}()
	lease, err := runtime.Acquire(ctx, chatGPTWebVerificationLease)
	if err != nil {
		verificationRuntimeSpan.Fail(err)
		status, _ := service.loginFailureStatus(capability, chatgptweb.StateDegraded,
			"managed browser verification could not acquire the isolated profile after interactive sign-in")
		return status, fmt.Errorf("acquire managed ChatGPT verification tab: %w", err)
	}
	leaseActive := true
	defer func() {
		if leaseActive {
			_ = runtime.Release(context.Background(), lease.AgentID)
		}
	}()
	tab, ok := runtime.Tab(lease.AgentID)
	if !ok {
		verificationRuntimeSpan.Fail(errors.New("ChatGPT Web verification tab is unavailable"))
		status, _ := service.loginFailureStatus(capability, chatgptweb.StateDegraded,
			"managed browser verification tab is unavailable after interactive sign-in")
		return status, errors.New("ChatGPT Web verification tab is unavailable")
	}
	if err := tab.Navigate(ctx, chatgptweb.TemporaryChatURL); err != nil {
		verificationRuntimeSpan.Fail(err)
		status, _ := service.loginFailureStatus(capability, chatgptweb.StateDegraded,
			"managed browser could not open ChatGPT Temporary Chat after interactive sign-in")
		return status, fmt.Errorf("open ChatGPT Temporary Chat for verification: %w", err)
	}
	verificationRuntimeSpan.End()
	verificationPollSpan := tracepkg.Start(ctx, "CHATGPT", "chatgpt.auth.verification.poll", "Verifying ChatGPT authentication")
	evidence, err := service.waitForAuth(ctx, tab, service.doctorTimeout())
	if err != nil {
		verificationPollSpan.Fail(err)
		status, _ := service.loginFailureStatus(capability, chatgptweb.StateNeedsLogin,
			"ChatGPT authentication could not be verified after interactive sign-in; rerun login and complete any browser verification before closing the window")
		return status, fmt.Errorf("verify ChatGPT authentication after interactive sign-in: %w; rerun login and complete any browser verification before closing the window", err)
	}
	verificationPollSpan.End()
	markerSpan := tracepkg.Start(ctx, "CHATGPT", "chatgpt.auth.marker.persist", "Saving verified authentication")
	if err := chatgptweb.WriteAuthMarker(service.Root(), service.Now()); err != nil {
		markerSpan.Fail(err)
		return ChatGPTWebStatus{}, err
	}
	markerSpan.End()
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
	status, err := service.status(ctx, true)
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	return withLoginAccount(status, evidence), nil
}

func (service *ChatGPTWebService) prepareInteractiveLoginLocked(ctx context.Context) error {
	if service.OwnedManager == nil {
		return nil
	}
	snapshot := service.OwnedManager.Snapshot()
	if snapshot.State != browser.ManagerClosed && snapshot.ActiveLeases > 0 {
		return errors.New("ChatGPT Web login requires exclusive browser profile access while no agent tabs are active")
	}
	if snapshot.State != browser.ManagerClosed {
		if err := service.OwnedManager.Close(applicationContext(ctx)); err != nil {
			return fmt.Errorf("retire idle ChatGPT Web browser before login: %w", err)
		}
	}
	service.OwnedManager = nil
	service.ownedRuntimeIdentity = chatGPTBrowserRuntimeIdentity{}
	service.runtimePendingReason = ""
	return nil
}

func withLoginAccount(status ChatGPTWebStatus, evidence chatgptweb.AuthEvidence) ChatGPTWebStatus {
	if evidence.Account.Empty() {
		return status
	}
	account := evidence.Account
	status.loginAccount = &account
	return status
}

func (service *ChatGPTWebService) loginFailureStatus(capability browser.Capability, state chatgptweb.State, reason string) (ChatGPTWebStatus, error) {
	if service == nil || service.LoadConfig == nil {
		return ChatGPTWebStatus{}, errors.New("ChatGPT Web integration service is unavailable")
	}
	cfg, err := service.LoadConfig()
	if err != nil {
		return ChatGPTWebStatus{}, err
	}
	status := service.baseStatus(cfg, capability)
	status.Authenticated = false
	status.State = state
	status.Reason = boundedIntegrationReason(reason)
	return status, nil
}

func (service *ChatGPTWebService) Doctor(ctx context.Context) (ChatGPTWebDoctorResult, error) {
	if service == nil {
		return ChatGPTWebDoctorResult{}, errors.New("ChatGPT Web integration service is unavailable")
	}
	service.browserMu.Lock()
	defer service.browserMu.Unlock()
	cfg, capability, err := service.capability(ctx)
	if err != nil {
		return ChatGPTWebDoctorResult{}, err
	}
	status, err := service.statusFromCapability(true, cfg, capability)
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
	if status.BrowserState == BrowserIntegrationRunning && service.OwnedManager == nil {
		result.Checks = append(result.Checks, ChatGPTWebDoctorCheck{ID: "live_auth", OK: false, Message: "live authentication probe deferred because another CodeMCP process owns the browser profile"})
		return result, nil
	}
	runtime, oneShot, err := service.browserRuntime(capability, cfg.Integrations.Browser.Minimized)
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
	return service.status(ctx, true)
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
		StateRoot: service.Root(), Passive: true,
	})
	return cfg, capability, nil
}

func (service *ChatGPTWebService) loginCapability(ctx context.Context) (config.Config, browser.Capability, error) {
	return service.capability(ctx)
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

func browserRuntimeIdentity(capability browser.Capability, minimized bool) chatGPTBrowserRuntimeIdentity {
	identity := chatGPTBrowserRuntimeIdentity{
		Executable: strings.TrimSpace(capability.Executable), HostPlatform: strings.TrimSpace(capability.HostPlatform), Transport: capability.Transport,
		Minimized: minimized,
	}
	if capability.Profile != nil {
		identity.ProfilePath = strings.TrimSpace(capability.Profile.LocalPath)
	}
	return identity
}

func pendingBrowserRuntimeReason(profileChanged, capacityIncrease bool) string {
	switch {
	case profileChanged && capacityIncrease:
		return "ChatGPT Web browser runtime configuration and capacity changes are pending until active agent tabs finish"
	case profileChanged:
		return "ChatGPT Web browser runtime configuration change is pending until active agent tabs finish"
	default:
		return "ChatGPT Web browser capacity increase is pending until active agent tabs finish"
	}
}

func (service *ChatGPTWebService) browserRuntime(capability browser.Capability, minimized bool) (chatGPTBrowserRuntime, bool, error) {
	if service.OwnedManager != nil {
		return service.OwnedManager, false, nil
	}
	if service.NewManager == nil {
		return nil, false, errors.New("ChatGPT Web browser manager factory is unavailable")
	}
	runtime, err := service.NewManager(browser.ManagerOptions{Capability: capability, MaxTabs: 1, Minimized: minimized})
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

func applicationContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
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
