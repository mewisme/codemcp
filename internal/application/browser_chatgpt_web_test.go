package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/browser"
	"go.mewis.me/codemcp/internal/integrations/chatgptweb"
)

type fakeChatGPTBrowserRuntime struct {
	mu       sync.Mutex
	tab      *fakeChatGPTBrowserTab
	snapshot browser.ManagerSnapshot
	closed   bool
	acquired []string
	released []string
}

func (runtime *fakeChatGPTBrowserRuntime) Acquire(_ context.Context, agentID string) (browser.LeaseSnapshot, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return browser.LeaseSnapshot{}, browser.ErrManagerClosed
	}
	if runtime.tab == nil {
		runtime.tab = newFakeChatGPTBrowserTab()
	}
	runtime.snapshot.State = browser.ManagerRunning
	runtime.snapshot.Running = true
	runtime.snapshot.ActiveLeases++
	runtime.acquired = append(runtime.acquired, agentID)
	return browser.LeaseSnapshot{AgentID: agentID, TabID: runtime.tab.ID(), State: browser.LeaseActive}, nil
}

func (runtime *fakeChatGPTBrowserRuntime) Tab(agentID string) (browser.BrowserTab, bool) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed || runtime.snapshot.ActiveLeases == 0 || runtime.tab == nil || agentID == "" {
		return nil, false
	}
	return runtime.tab, true
}

func (runtime *fakeChatGPTBrowserRuntime) Lease(agentID string) (browser.LeaseSnapshot, bool) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed || runtime.snapshot.ActiveLeases == 0 || agentID == "" {
		return browser.LeaseSnapshot{}, false
	}
	return browser.LeaseSnapshot{AgentID: agentID, TabID: runtime.tab.ID(), State: browser.LeaseActive}, true
}

func (runtime *fakeChatGPTBrowserRuntime) Touch(string) error { return nil }

func (runtime *fakeChatGPTBrowserRuntime) Release(_ context.Context, agentID string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.snapshot.ActiveLeases == 0 {
		return browser.ErrLeaseNotFound
	}
	runtime.snapshot.ActiveLeases--
	runtime.released = append(runtime.released, agentID)
	return nil
}

func (runtime *fakeChatGPTBrowserRuntime) Snapshot() browser.ManagerSnapshot {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.snapshot
}

func (runtime *fakeChatGPTBrowserRuntime) Close(context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.closed = true
	runtime.snapshot.State = browser.ManagerClosed
	runtime.snapshot.Running = false
	runtime.snapshot.ActiveLeases = 0
	return nil
}

func (runtime *fakeChatGPTBrowserRuntime) closedSnapshot() bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.closed
}

type fakeChatGPTBrowserTab struct {
	mu          sync.Mutex
	id          string
	navigations []string
	done        chan struct{}
	err         error
}

func newFakeChatGPTBrowserTab() *fakeChatGPTBrowserTab {
	return &fakeChatGPTBrowserTab{id: "tab-auth", done: make(chan struct{})}
}

func (tab *fakeChatGPTBrowserTab) ID() string { return tab.id }

func (tab *fakeChatGPTBrowserTab) Navigate(_ context.Context, url string) error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	tab.navigations = append(tab.navigations, url)
	return nil
}

func (tab *fakeChatGPTBrowserTab) Evaluate(context.Context, string, any) error { return nil }
func (tab *fakeChatGPTBrowserTab) Done() <-chan struct{}                       { return tab.done }

func (tab *fakeChatGPTBrowserTab) Err() error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return tab.err
}

func (tab *fakeChatGPTBrowserTab) Close(context.Context) error { return nil }

func (tab *fakeChatGPTBrowserTab) navigationSnapshot() []string {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return append([]string(nil), tab.navigations...)
}

type sequenceAuthProbe struct {
	mu       sync.Mutex
	evidence []chatgptweb.AuthEvidence
	index    int
}

func (probe *sequenceAuthProbe) Probe(context.Context, browser.BrowserTab) (chatgptweb.AuthEvidence, error) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if len(probe.evidence) == 0 {
		return chatgptweb.AuthEvidence{}, errors.New("no auth evidence")
	}
	index := probe.index
	if index >= len(probe.evidence) {
		index = len(probe.evidence) - 1
	}
	probe.index++
	return probe.evidence[index], nil
}

func TestBrowserIntegrationStatusDistinguishesUnavailableAndRunning(t *testing.T) {
	cfg := config.Default()
	root := t.TempDir()
	service := &BrowserIntegrationService{
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Root:       func() string { return root },
		Detect: func(context.Context, browser.Options) browser.Capability {
			return browser.Capability{State: browser.StateUnavailable, Enabled: true, Reason: "no supported browser"}
		},
	}
	status, err := service.Status(context.Background())
	if err != nil || status.State != BrowserIntegrationUnavailable || status.Running {
		t.Fatalf("unavailable status=%#v err=%v", status, err)
	}

	profile := applicationTestProfile(root)
	lock, ok, err := browser.TryAcquireProfile(profile)
	if err != nil || !ok {
		t.Fatalf("profile lock ok=%t err=%v", ok, err)
	}
	defer lock.Release()
	service.Detect = func(context.Context, browser.Options) browser.Capability {
		return applicationTestCapability(profile)
	}
	status, err = service.Status(context.Background())
	if err != nil || status.State != BrowserIntegrationRunning || !status.Running {
		t.Fatalf("running status=%#v err=%v", status, err)
	}
}

func TestBrowserIntegrationStatusIsPassiveAndDoctorIsActive(t *testing.T) {
	cfg := config.Default()
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	var calls []browser.Options
	service := &BrowserIntegrationService{
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Root:       func() string { return root },
		Detect: func(_ context.Context, options browser.Options) browser.Capability {
			calls = append(calls, options)
			return applicationTestCapability(profile)
		},
	}
	if _, err := service.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !calls[0].Passive {
		t.Fatalf("status detection options=%#v", calls)
	}
	if _, err := service.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1].Passive {
		t.Fatalf("doctor detection options=%#v", calls)
	}
}

func TestChatGPTWebStatusStateMatrix(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	baseCfg := applicationChatGPTWebConfig()
	service := newApplicationChatGPTWebTestService(root, baseCfg, profile)

	tests := []struct {
		name   string
		mutate func(*config.Config)
		setup  func()
		detect browser.Capability
		want   chatgptweb.State
	}{
		{name: "disabled", mutate: func(cfg *config.Config) { cfg.Integrations.ChatGPTWeb.Enabled = false }, want: chatgptweb.StateDisabled},
		{name: "browser unavailable", detect: browser.Capability{State: browser.StateUnavailable, Enabled: true, Reason: "not found"}, want: chatgptweb.StateBrowserUnavailable},
		{name: "needs login", want: chatgptweb.StateNeedsLogin},
		{name: "connector unavailable", mutate: func(cfg *config.Config) { cfg.Tunnel.APIKey = "" }, setup: func() {
			if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
				t.Fatal(err)
			}
		}, want: chatgptweb.StateConnectorUnavailable},
		{name: "ready", setup: func() {
			if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
				t.Fatal(err)
			}
		}, want: chatgptweb.StateReady},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_ = chatgptweb.RemoveAuthMarker(root)
			cfg := baseCfg
			if test.mutate != nil {
				test.mutate(&cfg)
			}
			service.LoadConfig = func() (config.Config, error) { return cfg, nil }
			if test.detect.State != "" {
				service.Detect = func(context.Context, browser.Options) browser.Capability { return test.detect }
			} else {
				service.Detect = func(context.Context, browser.Options) browser.Capability { return applicationTestCapability(profile) }
			}
			if test.setup != nil {
				test.setup()
			}
			status, err := service.Status(context.Background())
			if err != nil || status.State != test.want {
				t.Fatalf("status=%#v err=%v want=%q", status, err, test.want)
			}
		})
	}
}

func TestChatGPTWebStatusIsPassive(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	var calls []browser.Options
	service.Detect = func(_ context.Context, options browser.Options) browser.Capability {
		calls = append(calls, options)
		capability := applicationTestCapability(profile)
		if options.Passive {
			capability.Usable = false
		}
		return capability
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != chatgptweb.StateNeedsLogin {
		t.Fatalf("status=%#v", status)
	}
	if len(calls) != 1 || !calls[0].Passive {
		t.Fatalf("status detection options=%#v", calls)
	}
}

func TestChatGPTWebStatusMalformedMarkerDegradesWithoutLeakingPayload(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	markerPath, _ := chatgptweb.AuthMarkerPath(root)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0700); err != nil {
		t.Fatal(err)
	}
	const secret = "person@example.com bearer-secret"
	if err := os.WriteFile(markerPath, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	status, err := service.Status(context.Background())
	if err != nil || status.State != chatgptweb.StateDegraded {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "person@example.com") {
		t.Fatalf("status leaked malformed marker payload: %s", data)
	}
}

func TestChatGPTWebLoginUsesExactTemporaryChatAndPersistsOnlySafeMarker(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	idleRuntime := &fakeChatGPTBrowserRuntime{snapshot: browser.ManagerSnapshot{State: browser.ManagerRunning, Running: true}}
	verificationRuntime := &fakeChatGPTBrowserRuntime{snapshot: browser.ManagerSnapshot{State: browser.ManagerStopped}}
	probe := &sequenceAuthProbe{evidence: []chatgptweb.AuthEvidence{
		{OriginOK: true, Authenticated: true, Composer: true},
		{OriginOK: true, TemporaryChat: true, Authenticated: true, Composer: true},
	}}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	service.OwnedManager = idleRuntime
	var detectOptions []browser.Options
	service.Detect = func(_ context.Context, options browser.Options) browser.Capability {
		detectOptions = append(detectOptions, options)
		capability := applicationTestCapability(profile)
		capability.Usable = false
		return capability
	}
	plainClosed := false
	var interactiveProfile string
	service.RunInteractive = func(_ context.Context, options browser.InteractiveBrowserOptions) error {
		if !idleRuntime.closedSnapshot() {
			t.Fatal("idle managed runtime was not retired before interactive login")
		}
		probe.mu.Lock()
		probeCalls := probe.index
		probe.mu.Unlock()
		if probeCalls != 0 {
			t.Fatalf("authentication probe ran during interactive login: calls=%d", probeCalls)
		}
		if _, ok, err := chatgptweb.LoadAuthMarker(root); err != nil || ok {
			t.Fatalf("auth marker exists during interactive login: ok=%t err=%v", ok, err)
		}
		if options.URL != chatgptweb.TemporaryChatURL || options.Capability.Profile == nil {
			t.Fatalf("interactive options=%#v", options)
		}
		interactiveProfile = options.Capability.Profile.LocalPath
		plainClosed = true
		return nil
	}
	managerCreated := false
	service.NewManager = func(options browser.ManagerOptions) (chatGPTBrowserRuntime, error) {
		if !plainClosed {
			t.Fatal("managed verification runtime was created before interactive browser closed")
		}
		if _, ok, err := chatgptweb.LoadAuthMarker(root); err != nil || ok {
			t.Fatalf("auth marker exists before managed verification: ok=%t err=%v", ok, err)
		}
		if options.Capability.Profile == nil || options.Capability.Profile.LocalPath != interactiveProfile {
			t.Fatalf("managed profile=%#v interactive profile=%q", options.Capability.Profile, interactiveProfile)
		}
		managerCreated = true
		return verificationRuntime, nil
	}
	service.Probe = probe
	service.PollInterval = time.Millisecond
	service.LoginTimeout = time.Second
	service.DoctorTimeout = time.Second
	fixed := time.Date(2026, 10, 4, 2, 3, 4, 0, time.UTC)
	service.Now = func() time.Time { return fixed }

	status, err := service.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != chatgptweb.StateReady || !status.Authenticated {
		t.Fatalf("status=%#v", status)
	}
	if !managerCreated {
		t.Fatal("managed verification runtime was not created")
	}
	if len(detectOptions) == 0 || !detectOptions[0].Passive {
		t.Fatalf("login detection options=%#v", detectOptions)
	}
	navigations := verificationRuntime.tab.navigationSnapshot()
	if len(navigations) < 1 {
		t.Fatal("managed verification did not navigate browser tab")
	}
	for _, url := range navigations {
		if url != chatgptweb.TemporaryChatURL {
			t.Fatalf("login navigated unexpected URL %q", url)
		}
	}
	marker, ok, err := chatgptweb.LoadAuthMarker(root)
	if err != nil || !ok || !marker.VerifiedAt.Equal(fixed) {
		t.Fatalf("marker=%#v ok=%t err=%v", marker, ok, err)
	}
	if !verificationRuntime.closed {
		t.Fatal("one-shot verification browser manager was not closed")
	}
}

func TestChatGPTWebLoginRejectsActiveAgentBeforeInteractiveLaunch(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	activeRuntime := &fakeChatGPTBrowserRuntime{
		snapshot: browser.ManagerSnapshot{State: browser.ManagerRunning, Running: true, ActiveLeases: 1},
	}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	service.OwnedManager = activeRuntime
	interactiveCalls := 0
	service.RunInteractive = func(context.Context, browser.InteractiveBrowserOptions) error {
		interactiveCalls++
		return nil
	}
	status, err := service.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exclusive browser profile access") {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if interactiveCalls != 0 {
		t.Fatalf("interactive login calls=%d want=0", interactiveCalls)
	}
	if activeRuntime.closedSnapshot() {
		t.Fatal("login closed runtime with active agent leases")
	}
}

func TestChatGPTWebLoginFailedManagedVerificationLeavesMarkerAbsent(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeChatGPTBrowserRuntime{snapshot: browser.ManagerSnapshot{State: browser.ManagerStopped}}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	service.RunInteractive = func(context.Context, browser.InteractiveBrowserOptions) error {
		if _, ok, err := chatgptweb.LoadAuthMarker(root); err != nil || ok {
			t.Fatalf("stale auth marker was not cleared before interactive login: ok=%t err=%v", ok, err)
		}
		return nil
	}
	service.NewManager = func(browser.ManagerOptions) (chatGPTBrowserRuntime, error) { return runtime, nil }
	service.Probe = &sequenceAuthProbe{evidence: []chatgptweb.AuthEvidence{{OriginOK: true, TemporaryChat: true}}}
	service.PollInterval = time.Millisecond
	service.DoctorTimeout = 5 * time.Millisecond

	status, err := service.Login(context.Background())
	if err == nil || status.State != chatgptweb.StateNeedsLogin || status.Authenticated {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if !strings.Contains(status.Reason, "rerun login") {
		t.Fatalf("non-actionable failure reason=%q", status.Reason)
	}
	if _, ok, markerErr := chatgptweb.LoadAuthMarker(root); markerErr != nil || ok {
		t.Fatalf("failed verification persisted auth marker: ok=%t err=%v", ok, markerErr)
	}
	if !runtime.closedSnapshot() {
		t.Fatal("failed one-shot verification runtime was not closed")
	}
}

func TestChatGPTWebLoginManagedHandoffFailureReturnsDegradedWithoutMarker(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	service.RunInteractive = func(context.Context, browser.InteractiveBrowserOptions) error { return nil }
	service.NewManager = func(browser.ManagerOptions) (chatGPTBrowserRuntime, error) {
		return nil, errors.New("managed browser unavailable")
	}

	status, err := service.Login(context.Background())
	if err == nil || status.State != chatgptweb.StateDegraded || status.Authenticated {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if !strings.Contains(status.Reason, "verification could not start") {
		t.Fatalf("handoff reason=%q", status.Reason)
	}
	if _, ok, markerErr := chatgptweb.LoadAuthMarker(root); markerErr != nil || ok {
		t.Fatalf("failed handoff persisted auth marker: ok=%t err=%v", ok, markerErr)
	}
}

func TestChatGPTWebDoctorLiveVerificationReturnsOnlyReadiness(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeChatGPTBrowserRuntime{}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	service.NewManager = func(browser.ManagerOptions) (chatGPTBrowserRuntime, error) { return runtime, nil }
	service.Probe = &sequenceAuthProbe{evidence: []chatgptweb.AuthEvidence{{OriginOK: true, TemporaryChat: true, Authenticated: true, Composer: true}}}
	service.PollInterval = time.Millisecond
	service.DoctorTimeout = time.Second

	result, err := service.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.LiveVerified || result.Status.State != chatgptweb.StateReady {
		t.Fatalf("doctor=%#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"cookie", "bearer", "access_token", "person@example.com"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("doctor leaked forbidden auth material %q: %s", forbidden, encoded)
		}
	}
}

func TestChatGPTWebLogoutDeletesOnlyCodeMCPOwnedProfile(t *testing.T) {
	root := t.TempDir()
	owned := applicationTestProfile(root)
	if err := browser.PrepareProfile(owned); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned.LocalPath, "auth-state"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(root, "google-chrome", "Default")
	if err := os.MkdirAll(ordinary, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ordinary, "marker"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
		t.Fatal(err)
	}

	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), owned)
	service.ResolveOwnedProfiles = func(context.Context, browser.OwnedProfileOptions) ([]browser.ProfileRef, error) {
		return []browser.ProfileRef{owned}, nil
	}
	status, err := service.Logout(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != chatgptweb.StateNeedsLogin {
		t.Fatalf("status=%#v", status)
	}
	if _, err := os.Stat(owned.LocalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned profile still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ordinary, "marker")); err != nil {
		t.Fatalf("ordinary browser profile changed: %v", err)
	}
	if _, ok, err := chatgptweb.LoadAuthMarker(root); err != nil || ok {
		t.Fatalf("auth marker remains ok=%t err=%v", ok, err)
	}
}

func TestChatGPTWebLogoutRejectsActiveOwnedBrowserWithoutForce(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	runtime := &fakeChatGPTBrowserRuntime{snapshot: browser.ManagerSnapshot{State: browser.ManagerRunning, Running: true, ActiveLeases: 1}}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	service.OwnedManager = runtime
	service.ResolveOwnedProfiles = func(context.Context, browser.OwnedProfileOptions) ([]browser.ProfileRef, error) {
		return []browser.ProfileRef{profile}, nil
	}
	if _, err := service.Logout(context.Background(), false); err == nil || !strings.Contains(err.Error(), "active agent tabs") {
		t.Fatalf("logout error=%v", err)
	}
	if runtime.closed {
		t.Fatal("non-force logout closed active browser manager")
	}
}

func TestChatGPTWebLoginAndLogoutRespectExternalProfileOwner(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	lock, ok, err := browser.TryAcquireProfile(profile)
	if err != nil || !ok {
		t.Fatalf("profile lock ok=%t err=%v", ok, err)
	}
	defer lock.Release()

	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	if _, err := service.Login(context.Background()); !errors.Is(err, browser.ErrProfileBusy) {
		t.Fatalf("login error=%v want profile busy", err)
	}
	service.ResolveOwnedProfiles = func(context.Context, browser.OwnedProfileOptions) ([]browser.ProfileRef, error) {
		return []browser.ProfileRef{profile}, nil
	}
	if _, err := service.Logout(context.Background(), true); !errors.Is(err, browser.ErrProfileBusy) {
		t.Fatalf("logout error=%v want profile busy", err)
	}
}

func TestChatGPTWebForceLogoutDropsClosedOwnedManager(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeChatGPTBrowserRuntime{}
	service := newApplicationChatGPTWebTestService(root, applicationChatGPTWebConfig(), profile)
	service.OwnedManager = runtime
	service.ResolveOwnedProfiles = func(context.Context, browser.OwnedProfileOptions) ([]browser.ProfileRef, error) {
		return []browser.ProfileRef{profile}, nil
	}
	if _, err := service.Logout(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if service.OwnedManager != nil || !runtime.closed {
		t.Fatalf("owned manager retained=%t runtime closed=%t", service.OwnedManager != nil, runtime.closed)
	}
}

func TestBrowserAndChatGPTWebOperationsAreBound(t *testing.T) {
	dispatcher := NewDispatcher()
	if err := BindBrowserIntegrationOperations(dispatcher, &BrowserIntegrationService{}); err != nil {
		t.Fatal(err)
	}
	if err := BindChatGPTWebOperations(dispatcher, &ChatGPTWebService{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capability.ID{
		capability.IntegrationBrowserStatus,
		capability.IntegrationBrowserDoctor,
		capability.IntegrationChatGPTWebStatus,
		capability.IntegrationChatGPTWebLogin,
		capability.IntegrationChatGPTWebLogout,
		capability.IntegrationChatGPTWebDoctor,
	} {
		if dispatcher.handlers[id] == nil {
			t.Fatalf("operation %s is not bound", id)
		}
	}
}

func TestChatGPTWebReconcileDefersProfileChangeWhileAgentLeaseActive(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg := applicationChatGPTWebConfig()
	cfg.Integrations.Browser.Path = "/usr/bin/chromium"
	service := NewChatGPTWebService()
	service.Root = func() string { return root }
	service.LoadConfig = func() (config.Config, error) { return cfg, nil }
	service.Detect = func(context.Context, browser.Options) browser.Capability {
		return applicationTestCapabilityWithExecutable(profile, cfg.Integrations.Browser.Path)
	}
	var runtimes []*fakeChatGPTBrowserRuntime
	service.NewManager = func(options browser.ManagerOptions) (chatGPTBrowserRuntime, error) {
		runtime := &fakeChatGPTBrowserRuntime{snapshot: browser.ManagerSnapshot{State: browser.ManagerStopped, MaxTabs: options.MaxTabs}}
		runtimes = append(runtimes, runtime)
		return runtime, nil
	}
	runtime, err := service.AgentBrowserRuntime(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Acquire(context.Background(), "agent-active"); err != nil {
		t.Fatal(err)
	}
	cfg.Integrations.Browser.Path = "/opt/chromium-next"
	if err := service.ReconcileRuntimeConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtimes[0].closedSnapshot() {
		t.Fatal("profile-affecting reload closed browser with active agent lease")
	}
	status, err := service.Status(context.Background())
	if err != nil || !status.RuntimePending {
		t.Fatalf("pending status=%#v err=%v", status, err)
	}
	settings, err := service.AgentBackendSettings(context.Background())
	if err != nil || settings.Available {
		t.Fatalf("pending settings=%#v err=%v", settings, err)
	}
	if err := runtime.Release(context.Background(), "agent-active"); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileRuntimeConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runtimes[0].closedSnapshot() {
		t.Fatal("idle stale browser runtime was not retired")
	}
	if _, err := service.AgentBrowserRuntime(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 2 {
		t.Fatalf("runtime creations=%d want=2", len(runtimes))
	}
}

func TestChatGPTWebReconcileCapacityReductionKeepsExistingAgents(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg := applicationChatGPTWebConfig()
	service := NewChatGPTWebService()
	service.Root = func() string { return root }
	service.LoadConfig = func() (config.Config, error) { return cfg, nil }
	service.Detect = func(context.Context, browser.Options) browser.Capability { return applicationTestCapability(profile) }
	runtime := &fakeChatGPTBrowserRuntime{snapshot: browser.ManagerSnapshot{State: browser.ManagerStopped, MaxTabs: 5}}
	service.NewManager = func(browser.ManagerOptions) (chatGPTBrowserRuntime, error) { return runtime, nil }
	owned, err := service.AgentBrowserRuntime(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := owned.Acquire(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Integrations.ChatGPTWeb.MaxAgents = 2
	if err := service.ReconcileRuntimeConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.closedSnapshot() || runtime.Snapshot().ActiveLeases != 3 {
		t.Fatalf("capacity reduction disrupted live runtime: %#v", runtime.Snapshot())
	}
	settings, err := service.AgentBackendSettings(context.Background())
	if err != nil || !settings.Available || settings.MaxAgents != 2 {
		t.Fatalf("reduced settings=%#v err=%v", settings, err)
	}
}

func TestChatGPTWebDisableBlocksNewSpawnsWithoutKillingExistingAgents(t *testing.T) {
	root := t.TempDir()
	profile := applicationTestProfile(root)
	if err := browser.PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := chatgptweb.WriteAuthMarker(root, time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg := applicationChatGPTWebConfig()
	service := NewChatGPTWebService()
	service.Root = func() string { return root }
	service.LoadConfig = func() (config.Config, error) { return cfg, nil }
	service.Detect = func(context.Context, browser.Options) browser.Capability { return applicationTestCapability(profile) }
	runtime := &fakeChatGPTBrowserRuntime{snapshot: browser.ManagerSnapshot{State: browser.ManagerStopped, MaxTabs: 5}}
	service.NewManager = func(browser.ManagerOptions) (chatGPTBrowserRuntime, error) { return runtime, nil }
	owned, err := service.AgentBrowserRuntime(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owned.Acquire(context.Background(), "existing"); err != nil {
		t.Fatal(err)
	}
	cfg.Integrations.ChatGPTWeb.Enabled = false
	if err := service.ReconcileRuntimeConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.closedSnapshot() || runtime.Snapshot().ActiveLeases != 1 {
		t.Fatalf("disable killed existing agent runtime: %#v", runtime.Snapshot())
	}
	settings, err := service.AgentBackendSettings(context.Background())
	if err != nil || settings.Available {
		t.Fatalf("disabled settings=%#v err=%v", settings, err)
	}
}

func newApplicationChatGPTWebTestService(root string, cfg config.Config, profile browser.ProfileRef) *ChatGPTWebService {
	service := NewChatGPTWebService()
	service.Root = func() string { return root }
	service.LoadConfig = func() (config.Config, error) { return cfg, nil }
	service.Detect = func(context.Context, browser.Options) browser.Capability { return applicationTestCapability(profile) }
	return service
}

func applicationChatGPTWebConfig() config.Config {
	cfg := config.Default()
	cfg.Integrations.Browser.Enabled = true
	cfg.Integrations.ChatGPTWeb.Enabled = true
	cfg.Integrations.ChatGPTWeb.ConnectorName = "CodeMCP"
	cfg.Integrations.ChatGPTWeb.MaxAgents = 5
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.ID = "tunnel_test"
	cfg.Tunnel.APIKey = "runtime-secret-for-test"
	return cfg
}

func applicationTestProfile(root string) browser.ProfileRef {
	return browser.ProfileRef{
		HostPlatform: "linux",
		Transport:    browser.TransportNative,
		Path:         filepath.Join(root, "browser", "chatgpt"),
		LocalPath:    filepath.Join(root, "browser", "chatgpt"),
		LockPath:     filepath.Join(root, "browser", "chatgpt.lock"),
	}
}

func applicationTestOwnedProfileResolver(root string) ownedBrowserProfileResolver {
	return func(context.Context, browser.OwnedProfileOptions) ([]browser.ProfileRef, error) {
		return []browser.ProfileRef{applicationTestProfile(root)}, nil
	}
}

func applicationTestCapability(profile browser.ProfileRef) browser.Capability {
	return applicationTestCapabilityWithExecutable(profile, "/usr/bin/chromium")
}

func applicationTestCapabilityWithExecutable(profile browser.ProfileRef, executable string) browser.Capability {
	candidate := browser.Candidate{
		Family: browser.FamilyChromium, Executable: executable, LocalExecutable: executable,
		HostPlatform: "linux", Transport: browser.TransportNative, Source: browser.SourceConfigured,
	}
	return browser.Capability{
		State: browser.StateAvailable, Enabled: true, Available: true, Launchable: true, Usable: true,
		Family: browser.FamilyChromium, Executable: candidate.Executable, Version: "Chromium Test",
		HostPlatform: "linux", Transport: browser.TransportNative, Graphical: true,
		ProfileHostPlatform: profile.HostPlatform, Profile: &profile, Candidate: &candidate,
	}
}
