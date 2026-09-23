package tunnel

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

type fakeBackend struct {
	mu        sync.Mutex
	started   bool
	stopped   bool
	startErr  error
	autoReady bool
	ready     chan struct{}
	done      chan os.Signal
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{autoReady: true, ready: make(chan struct{}), done: make(chan os.Signal)}
}

func (b *fakeBackend) Start(context.Context) error {
	b.mu.Lock()
	b.started = true
	err := b.startErr
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if b.autoReady {
		select {
		case <-b.ready:
		default:
			close(b.ready)
		}
	}
	return nil
}

func (b *fakeBackend) Stop(context.Context) error {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	return nil
}

func (b *fakeBackend) WaitUntilReady(ctx context.Context) error {
	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *fakeBackend) Done() <-chan os.Signal { return b.done }

func TestValidateConfigRequiresHTTPSControlPlane(t *testing.T) {
	base := Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}
	if err := ValidateConfig(base); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret", ControlPlaneBaseURL: "https://api.openai.com"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret", ControlPlaneBaseURL: "http://127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret", ControlPlaneBaseURL: "http://localhost:8080"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret", ControlPlaneBaseURL: "http://api.openai.com"}); err == nil {
		t.Fatal("expected non-loopback http control plane to fail")
	}
}

func TestDisabledTunnelDoesNotConstructBackend(t *testing.T) {
	called := false
	client := newConfigured(Config{Enabled: false}, &tools.Runtime{Registry: tools.NewRegistry()}, func(Config, sdkmcp.Transport) (backend, error) {
		called = true
		return newFakeBackend(), nil
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if called || client.Status().Running {
		t.Fatalf("disabled tunnel constructed backend: called=%v status=%+v", called, client.Status())
	}
}

func TestBuiltinOpenAITunnelLifecycle(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	fake := newFakeBackend()
	var seen Config
	client := newConfigured(Config{
		Enabled: true, ID: "tunnel_0123456789abcdef0123456789abcdef", APIKey: "secret",
		ControlPlaneBaseURL: "https://api.openai.com", OrganizationID: "org_test",
	}, runtime, func(cfg Config, transport sdkmcp.Transport) (backend, error) {
		if transport == nil {
			t.Fatal("expected in-memory MCP transport")
		}
		seen = cfg
		return fake, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.StartContext(ctx); err != nil {
		t.Fatal(err)
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), time.Second)
	defer readyCancel()
	if err := client.WaitUntilReady(readyCtx); err != nil {
		t.Fatal(err)
	}
	status := client.Status()
	if status.Provider != ProviderOpenAI || !status.Running || !status.Ready || status.ID != seen.ID {
		t.Fatalf("status = %+v", status)
	}
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
	if status = client.Status(); status.Running || status.Ready {
		t.Fatalf("expected stopped tunnel: %+v", status)
	}
	fake.mu.Lock()
	stopped := fake.stopped
	fake.mu.Unlock()
	if !stopped {
		t.Fatal("embedded OpenAI backend was not stopped")
	}
}

func TestTunnelLifecycleObserverReportsConnectingReadyAndStopped(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	fake := newFakeBackend()
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) { return fake, nil })
	events := make(chan LifecycleEvent, 8)
	client.SetLifecycleObserver(func(event LifecycleEvent) { events <- event })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	waitLifecycleState(t, events, LifecycleConnecting)
	readyCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitUntilReady(readyCtx); err != nil {
		t.Fatal(err)
	}
	waitLifecycleState(t, events, LifecycleReady)
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
	waitLifecycleState(t, events, LifecycleStopped)
}

func TestTunnelBackendShutdownReconnects(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	created := make(chan *fakeBackend, 4)
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) {
		fake := newFakeBackend()
		created <- fake
		return fake, nil
	})
	client.restartDelay = func(int) time.Duration { return time.Millisecond }
	events := make(chan LifecycleEvent, 16)
	client.SetLifecycleObserver(func(event LifecycleEvent) { events <- event })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	first := <-created
	readyCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitUntilReady(readyCtx); err != nil {
		t.Fatal(err)
	}
	waitLifecycleState(t, events, LifecycleReady)
	select {
	case first.done <- os.Interrupt:
	case <-time.After(time.Second):
		t.Fatal("backend watcher did not receive shutdown signal")
	}
	degraded := waitLifecycleState(t, events, LifecycleDegraded)
	if degraded.Message == "" {
		t.Fatal("degraded lifecycle event did not include a reason")
	}
	var reconnecting LifecycleEvent
	select {
	case reconnecting = <-events:
		if reconnecting.State != LifecycleReconnecting {
			t.Fatalf("event after degraded = %+v, want reconnecting without duplicate degraded", reconnecting)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnecting lifecycle event")
	}
	if reconnecting.Attempt != 1 || reconnecting.RetryIn != time.Millisecond {
		t.Fatalf("reconnecting event = %+v", reconnecting)
	}
	second := <-created
	if second == first {
		t.Fatal("restart reused the previous backend")
	}
	waitLifecycleState(t, events, LifecycleReady)
	status := client.Status()
	if !status.Running || !status.Ready || status.Restarting || status.LastError != "" {
		t.Fatalf("tunnel did not recover: %+v", status)
	}
	first.mu.Lock()
	firstStopped := first.stopped
	first.mu.Unlock()
	if !firstStopped {
		t.Fatal("failed backend was not stopped before reconnect")
	}
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
	waitLifecycleState(t, events, LifecycleStopped)
}

func TestTunnelStopDuringReconnectPreventsRestart(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	created := make(chan *fakeBackend, 4)
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) {
		fake := newFakeBackend()
		created <- fake
		return fake, nil
	})
	client.restartDelay = func(int) time.Duration { return time.Hour }
	events := make(chan LifecycleEvent, 16)
	client.SetLifecycleObserver(func(event LifecycleEvent) { events <- event })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	first := <-created
	readyCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitUntilReady(readyCtx); err != nil {
		t.Fatal(err)
	}
	waitLifecycleState(t, events, LifecycleReady)
	first.done <- os.Interrupt
	waitLifecycleState(t, events, LifecycleDegraded)
	waitLifecycleState(t, events, LifecycleReconnecting)
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
	waitLifecycleState(t, events, LifecycleStopped)
	time.Sleep(20 * time.Millisecond)
	select {
	case extra := <-created:
		t.Fatalf("tunnel restarted after explicit stop: %#v", extra)
	default:
	}
	status := client.Status()
	if status.Running || status.Ready || status.Restarting {
		t.Fatalf("tunnel remained active after explicit stop: %+v", status)
	}
}

func TestWaitUntilReadySurvivesReconnectBeforeInitialReady(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	created := make(chan *fakeBackend, 4)
	count := 0
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) {
		count++
		fake := newFakeBackend()
		if count == 1 {
			fake.autoReady = false
		}
		created <- fake
		return fake, nil
	})
	client.restartDelay = func(int) time.Duration { return time.Millisecond }
	events := make(chan LifecycleEvent, 16)
	client.SetLifecycleObserver(func(event LifecycleEvent) { events <- event })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	first := <-created
	waitLifecycleState(t, events, LifecycleConnecting)
	readyCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	readyErr := make(chan error, 1)
	go func() { readyErr <- client.WaitUntilReady(readyCtx) }()
	first.done <- os.Interrupt
	waitLifecycleState(t, events, LifecycleDegraded)
	waitLifecycleState(t, events, LifecycleReconnecting)
	<-created
	waitLifecycleState(t, events, LifecycleReady)
	if err := <-readyErr; err != nil {
		t.Fatalf("WaitUntilReady returned during recoverable restart: %v", err)
	}
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultRestartDelayIsBoundedExponential(t *testing.T) {
	tests := map[int]time.Duration{0: time.Second, 1: time.Second, 2: 2 * time.Second, 5: 16 * time.Second, 6: 30 * time.Second, 20: 30 * time.Second}
	for attempt, want := range tests {
		if got := defaultRestartDelay(attempt); got != want {
			t.Fatalf("attempt %d delay = %s, want %s", attempt, got, want)
		}
	}
}

func TestTunnelIdleReconnectRepeats(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	created := make(chan *fakeBackend, 8)
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) {
		fake := newFakeBackend()
		created <- fake
		return fake, nil
	})
	client.idleInterval = 25 * time.Millisecond
	client.restartDelay = func(int) time.Duration { return time.Millisecond }
	events := make(chan LifecycleEvent, 32)
	client.SetLifecycleObserver(func(event LifecycleEvent) { events <- event })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	first := waitBackendCreated(t, created)
	waitLifecycleState(t, events, LifecycleReady)
	second := waitBackendCreated(t, created)
	if second == first {
		t.Fatal("idle reconnect reused the previous backend")
	}
	waitLifecycleState(t, events, LifecycleReady)
	third := waitBackendCreated(t, created)
	if third == second {
		t.Fatal("repeated idle reconnect reused the previous backend")
	}
	waitLifecycleState(t, events, LifecycleReady)
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestTunnelMCPActivityResetsIdleDeadline(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	created := make(chan *fakeBackend, 4)
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) {
		fake := newFakeBackend()
		created <- fake
		return fake, nil
	})
	client.idleInterval = 100 * time.Millisecond
	client.restartDelay = func(int) time.Duration { return time.Millisecond }
	events := make(chan LifecycleEvent, 16)
	client.SetLifecycleObserver(func(event LifecycleEvent) { events <- event })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	waitBackendCreated(t, created)
	waitLifecycleState(t, events, LifecycleReady)
	client.mu.RLock()
	session, before := client.session, client.lastActivity
	client.mu.RUnlock()
	time.Sleep(60 * time.Millisecond)
	client.markMCPActivity(session)
	client.mu.RLock()
	after := client.lastActivity
	client.mu.RUnlock()
	if !after.After(before) {
		t.Fatalf("last activity did not advance: before=%s after=%s", before, after)
	}
	select {
	case <-created:
		t.Fatal("MCP activity did not postpone idle reconnect")
	case <-time.After(60 * time.Millisecond):
	}
	waitBackendCreated(t, created)
	waitLifecycleState(t, events, LifecycleReady)
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
}

func waitBackendCreated(t *testing.T, created <-chan *fakeBackend) *fakeBackend {
	t.Helper()
	select {
	case backend := <-created:
		return backend
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tunnel backend")
		return nil
	}
}

func waitLifecycleState(t *testing.T, events <-chan LifecycleEvent, state LifecycleState) LifecycleEvent {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.State == state {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for lifecycle state %s", state)
		}
	}
}

func TestTunnelContextCancellationStopsEmbeddedBackend(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	fake := newFakeBackend()
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) { return fake, nil })
	ctx, cancel := context.WithCancel(context.Background())
	if err := client.StartContext(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for client.Status().Running {
		if time.Now().After(deadline) {
			t.Fatalf("tunnel did not stop after context cancellation: %+v", client.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	fake.mu.Lock()
	stopped := fake.stopped
	fake.mu.Unlock()
	if !stopped {
		t.Fatal("embedded backend was not stopped after context cancellation")
	}
}

func TestEnabledTunnelRequiresRuntimeIDAndAPIKey(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		runtime *tools.Runtime
	}{
		{name: "runtime", cfg: Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}},
		{name: "id", cfg: Config{Enabled: true, APIKey: "secret"}, runtime: &tools.Runtime{Registry: tools.NewRegistry()}},
		{name: "api-key", cfg: Config{Enabled: true, ID: "tunnel_test"}, runtime: &tools.Runtime{Registry: tools.NewRegistry()}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client := newConfigured(test.cfg, test.runtime, func(Config, sdkmcp.Transport) (backend, error) { return newFakeBackend(), nil })
			if err := client.Start(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestConfigureRejectsRunningTunnel(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	client := newConfigured(Config{Enabled: true, ID: "tunnel_test", APIKey: "secret"}, runtime, func(Config, sdkmcp.Transport) (backend, error) { return newFakeBackend(), nil })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if err := client.Configure(Config{}); err == nil {
		t.Fatal("expected running configure rejection")
	}
}

func TestReconfigureRollsBackRunningTunnelWhenPersistenceFails(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	oldConfig := Config{Enabled: true, ID: "tunnel_old", APIKey: "old-secret"}
	newConfig := Config{Enabled: true, ID: "tunnel_new", APIKey: "new-secret"}
	created := map[string][]*fakeBackend{}
	client := newConfigured(oldConfig, runtime, func(cfg Config, _ sdkmcp.Transport) (backend, error) {
		fake := newFakeBackend()
		created[cfg.ID] = append(created[cfg.ID], fake)
		return fake, nil
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := client.Reconfigure(newConfig, func() error { return errors.New("persist failed") }); err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := client.Config(); got != oldConfig {
		t.Fatalf("config = %#v, want %#v", got, oldConfig)
	}
	if !client.Status().Running {
		t.Fatal("old tunnel was not restarted")
	}
	if len(created[oldConfig.ID]) != 2 || len(created[newConfig.ID]) != 1 {
		t.Fatalf("backend generations = old:%d new:%d", len(created[oldConfig.ID]), len(created[newConfig.ID]))
	}
	created[newConfig.ID][0].mu.Lock()
	newStopped := created[newConfig.ID][0].stopped
	created[newConfig.ID][0].mu.Unlock()
	if !newStopped {
		t.Fatal("candidate tunnel was not stopped during rollback")
	}
	defer client.Stop()
}

func TestReconfigureRollsBackWhenCandidateStartFails(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	oldConfig := Config{Enabled: true, ID: "tunnel_old", APIKey: "old-secret"}
	newConfig := Config{Enabled: true, ID: "tunnel_new", APIKey: "new-secret"}
	oldStarts := 0
	client := newConfigured(oldConfig, runtime, func(cfg Config, _ sdkmcp.Transport) (backend, error) {
		fake := newFakeBackend()
		if cfg.ID == newConfig.ID {
			fake.startErr = errors.New("candidate start failed")
		} else {
			oldStarts++
		}
		return fake, nil
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	persisted := false
	if err := client.Reconfigure(newConfig, func() error { persisted = true; return nil }); err == nil {
		t.Fatal("expected candidate start failure")
	}
	if persisted {
		t.Fatal("persistence ran after candidate start failure")
	}
	if got := client.Config(); got != oldConfig || !client.Status().Running || oldStarts != 2 {
		t.Fatalf("rollback failed: config=%#v status=%+v oldStarts=%d", got, client.Status(), oldStarts)
	}
	defer client.Stop()
}

func TestReconfigureRejectsInvalidCandidateBeforeStoppingCurrentTunnel(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	oldConfig := Config{Enabled: true, ID: "tunnel_old", APIKey: "old-secret"}
	fake := newFakeBackend()
	client := newConfigured(oldConfig, runtime, func(Config, sdkmcp.Transport) (backend, error) { return fake, nil })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := client.Reconfigure(Config{Enabled: true}, func() error { return nil }); err == nil {
		t.Fatal("expected invalid candidate rejection")
	}
	if !client.Status().Running || client.Config() != oldConfig {
		t.Fatalf("current tunnel changed after invalid candidate: config=%#v status=%+v", client.Config(), client.Status())
	}
	fake.mu.Lock()
	stopped := fake.stopped
	fake.mu.Unlock()
	if stopped {
		t.Fatal("current backend was stopped before candidate validation")
	}
	defer client.Stop()
}

func TestSyncManagementConfigDoesNotRestartRunningTunnel(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	fake := newFakeBackend()
	cfg := Config{Enabled: true, ID: "tunnel_test", APIKey: "runtime-key"}
	client := newConfigured(cfg, runtime, func(Config, sdkmcp.Transport) (backend, error) { return fake, nil })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	next := cfg
	next.AdminKey = "admin-key"
	next.AdminWorkspaceID = "ws_admin"
	if err := client.SyncManagementConfig(next); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	stopped := fake.stopped
	fake.mu.Unlock()
	if stopped || !client.Status().Running || client.Config().AdminKey != "admin-key" || !client.Status().AdminKeyConfigured {
		t.Fatalf("management sync restarted or failed to update tunnel: stopped=%t status=%+v config=%#v", stopped, client.Status(), client.Config())
	}
}
