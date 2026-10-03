package chatgptweb

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/integrations/browser"
)

type backendTestRuntime struct {
	mu             sync.Mutex
	tabs           map[string]*backendTestTab
	leases         map[string]browser.LeaseSnapshot
	acquired       []string
	released       []string
	touched        []string
	acquireStarted chan struct{}
	acquireGate    <-chan struct{}
}

func newBackendTestRuntime() *backendTestRuntime {
	return &backendTestRuntime{tabs: map[string]*backendTestTab{}, leases: map[string]browser.LeaseSnapshot{}}
}

func (runtime *backendTestRuntime) Acquire(ctx context.Context, agentID string) (browser.LeaseSnapshot, error) {
	if runtime.acquireStarted != nil {
		select {
		case runtime.acquireStarted <- struct{}{}:
		default:
		}
	}
	if runtime.acquireGate != nil {
		select {
		case <-ctx.Done():
			return browser.LeaseSnapshot{}, ctx.Err()
		case <-runtime.acquireGate:
		}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.tabs[agentID] != nil {
		return browser.LeaseSnapshot{}, browser.ErrLeaseExists
	}
	tab := &backendTestTab{id: agentID, done: make(chan struct{})}
	runtime.tabs[agentID] = tab
	runtime.acquired = append(runtime.acquired, agentID)
	lease := browser.LeaseSnapshot{AgentID: agentID, TabID: tab.id, State: browser.LeaseActive}
	runtime.leases[agentID] = lease
	return lease, nil
}

func (runtime *backendTestRuntime) Tab(agentID string) (browser.BrowserTab, bool) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	tab, ok := runtime.tabs[agentID]
	return tab, ok
}

func (runtime *backendTestRuntime) Lease(agentID string) (browser.LeaseSnapshot, bool) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	lease, ok := runtime.leases[agentID]
	return lease, ok
}

func (runtime *backendTestRuntime) Touch(agentID string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	lease, ok := runtime.leases[agentID]
	if runtime.tabs[agentID] == nil || !ok {
		return browser.ErrLeaseNotFound
	}
	if lease.State != browser.LeaseActive {
		return errors.New(lease.Failure)
	}
	runtime.touched = append(runtime.touched, agentID)
	return nil
}

func (runtime *backendTestRuntime) Release(_ context.Context, agentID string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.tabs[agentID] == nil {
		return browser.ErrLeaseNotFound
	}
	delete(runtime.tabs, agentID)
	delete(runtime.leases, agentID)
	runtime.released = append(runtime.released, agentID)
	return nil
}

func (runtime *backendTestRuntime) counts() (int, int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return len(runtime.acquired), len(runtime.released)
}

func (runtime *backendTestRuntime) touchedCount() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return len(runtime.touched)
}

func (runtime *backendTestRuntime) setLeaseState(agentID string, state browser.LeaseState, failure string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	lease := runtime.leases[agentID]
	lease.State = state
	lease.Failure = failure
	runtime.leases[agentID] = lease
}

type backendTestTab struct {
	id   string
	done chan struct{}
}

func (tab *backendTestTab) ID() string                              { return tab.id }
func (*backendTestTab) Navigate(context.Context, string) error      { return nil }
func (*backendTestTab) Evaluate(context.Context, string, any) error { return nil }
func (tab *backendTestTab) Done() <-chan struct{}                   { return tab.done }
func (*backendTestTab) Err() error                                  { return nil }
func (*backendTestTab) Close(context.Context) error                 { return nil }

type backendTestDriver struct {
	mu             sync.Mutex
	startRequest   TurnRequest
	startCalled    chan struct{}
	startGate      chan struct{}
	startResult    TurnResult
	startErr       error
	followUps      []string
	followUpResult TurnResult
	followUpErr    error
	cancels        int
	state          TurnState
}

func (driver *backendTestDriver) Start(ctx context.Context, request TurnRequest) (TurnResult, error) {
	driver.mu.Lock()
	driver.startRequest = request
	driver.state = TurnGenerating
	called, gate, result, err := driver.startCalled, driver.startGate, driver.startResult, driver.startErr
	driver.mu.Unlock()
	if called != nil {
		select {
		case called <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-ctx.Done():
			return TurnResult{}, ctx.Err()
		case <-gate:
		}
	}
	driver.mu.Lock()
	if err == nil {
		driver.state = TurnFinal
	}
	driver.mu.Unlock()
	return result, err
}

func (driver *backendTestDriver) FollowUp(_ context.Context, message string) (TurnResult, error) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	driver.followUps = append(driver.followUps, message)
	if driver.followUpErr == nil {
		driver.state = TurnFinal
	}
	return driver.followUpResult, driver.followUpErr
}

func (driver *backendTestDriver) Cancel(context.Context) error {
	driver.mu.Lock()
	driver.cancels++
	driver.state = TurnCancelled
	driver.mu.Unlock()
	return nil
}

func (driver *backendTestDriver) State() TurnState {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.state
}
func (driver *backendTestDriver) request() TurnRequest {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.startRequest
}
func (driver *backendTestDriver) followUpSnapshot() []string {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]string(nil), driver.followUps...)
}

func newBackendTestManager(t *testing.T, maxParallel int) *managedagent.Manager {
	t.Helper()
	manager, err := managedagent.NewManager(managedagent.ManagerOptions{
		DefaultBackend: AgentBackendID, GlobalCapacity: managedagent.Capacity{MaxParallel: maxParallel}, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func registerBackendTestAdapter(t *testing.T, manager *managedagent.Manager, runtime *backendTestRuntime, maxAgents int, factory func(browser.BrowserTab) (AgentTurnDriver, error)) {
	t.Helper()
	backend, err := NewAgentBackend(AgentBackendOptions{
		Manager: manager,
		Settings: func(context.Context) (AgentBackendSettings, error) {
			return AgentBackendSettings{Available: true, MaxAgents: maxAgents, ConnectorName: "CodeMCP"}, nil
		},
		Runtime: func(context.Context, int) (AgentBackendRuntime, error) { return runtime, nil },
		Driver:  factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
}

func TestAgentBackendClaimCompletionFinalAndRelease(t *testing.T) {
	manager := newBackendTestManager(t, 2)
	runtime := newBackendTestRuntime()
	startCalled := make(chan struct{}, 1)
	startGate := make(chan struct{})
	var driversMu sync.Mutex
	drivers := map[string]*backendTestDriver{}
	registerBackendTestAdapter(t, manager, runtime, 2, func(tab browser.BrowserTab) (AgentTurnDriver, error) {
		driver := &backendTestDriver{startCalled: startCalled, startGate: startGate, startResult: TurnResult{State: TurnFinal, Text: "final child answer"}}
		driversMu.Lock()
		drivers[tab.ID()] = driver
		driversMu.Unlock()
		return driver, nil
	})
	owner, _ := managedagent.NewMCPController("parent-session")
	spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: "ws_test", Prompt: "inspect the delegated issue", Model: "GPT-5.6 Sol", ReasoningEffort: "high",
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-startCalled:
	case <-time.After(time.Second):
		t.Fatal("ChatGPT Web driver did not start")
	}
	driversMu.Lock()
	driver := drivers[string(spawned.ID)]
	driversMu.Unlock()
	if driver == nil {
		t.Fatal("driver was not associated with managed agent")
	}
	request := driver.request()
	if request.Prompt != "inspect the delegated issue" || request.Model != "GPT-5.6 Sol" || request.ReasoningEffort != "high" || request.ConnectorName != "CodeMCP" || !request.RequireConnector {
		t.Fatalf("driver request=%#v", request)
	}
	for _, required := range []string{string(spawned.ID), "Workspace ID: ws_test", "agent_claim", "project_context", "memory enabled", "no parent transcript", "agent_complete"} {
		if !strings.Contains(request.Bootstrap, required) {
			t.Fatalf("bootstrap missing %q: %q", required, request.Bootstrap)
		}
	}
	tokenMatch := regexp.MustCompile(`one-time token: ([A-Za-z0-9_-]+)\.`).FindStringSubmatch(request.Bootstrap)
	if len(tokenMatch) != 2 {
		t.Fatalf("claim token missing from private bootstrap: %q", request.Bootstrap)
	}
	if _, bound := manager.SessionBinding("child-session"); bound {
		t.Fatal("child session was bound before agent_claim")
	}
	binding, err := manager.ConsumeClaim(spawned.ID, tokenMatch[1], "child-session")
	if err != nil {
		t.Fatal(err)
	}
	if binding.AgentID != spawned.ID || binding.WorkspaceID != "ws_test" || binding.Backend != AgentBackendID || !binding.Active {
		t.Fatalf("claim binding=%#v", binding)
	}
	if err := manager.ObserveAcceptedCompletion(managedagent.CompletionEvent{AgentID: spawned.ID, WorkspaceID: "ws_test", Status: managedagent.StateCompleted}); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.Get(context.Background(), owner, spawned.ID)
	if err != nil || pending.State != managedagent.StateCompletionPending {
		t.Fatalf("pending snapshot=%#v err=%v", pending, err)
	}
	if _, released := runtime.counts(); released != 0 {
		t.Fatal("tab released before same browser turn produced final answer")
	}
	close(startGate)
	terminal := waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateCompleted)
	if terminal.Result != "final child answer" {
		t.Fatalf("terminal result=%#v", terminal)
	}
	acquired, released := runtime.counts()
	if acquired != 1 || released != 1 {
		t.Fatalf("browser leases acquired=%d released=%d", acquired, released)
	}
	if _, err := manager.Send(context.Background(), owner, spawned.ID, managedagent.Message{Content: "must reject"}); err == nil {
		t.Fatal("terminal managed agent accepted follow-up")
	}
	if active, ok := manager.SessionBinding("child-session"); !ok || active.Active {
		t.Fatalf("terminal claim binding=%#v ok=%t", active, ok)
	}
}

func TestAgentBackendIdleFollowUpReusesTabAndCapacity(t *testing.T) {
	manager := newBackendTestManager(t, 3)
	runtime := newBackendTestRuntime()
	var driversMu sync.Mutex
	drivers := map[string]*backendTestDriver{}
	registerBackendTestAdapter(t, manager, runtime, 1, func(tab browser.BrowserTab) (AgentTurnDriver, error) {
		driver := &backendTestDriver{startResult: TurnResult{State: TurnFinal, Text: "idle first"}, followUpResult: TurnResult{State: TurnFinal, Text: "idle second"}}
		driversMu.Lock()
		drivers[tab.ID()] = driver
		driversMu.Unlock()
		return driver, nil
	})
	owner, _ := managedagent.NewMCPController("owner")
	spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{WorkspaceID: "ws_test", Prompt: "first"}})
	if err != nil {
		t.Fatal(err)
	}
	idle := waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateIdle)
	if idle.Result != "idle first" {
		t.Fatalf("idle snapshot=%#v", idle)
	}
	if _, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{WorkspaceID: "ws_test", Prompt: "must exceed backend capacity"}}); !errors.Is(err, managedagent.ErrCapacityReached) {
		t.Fatalf("second spawn error=%v", err)
	}
	if _, err := manager.Send(context.Background(), owner, spawned.ID, managedagent.Message{Content: "continue same chat"}); err != nil {
		t.Fatal(err)
	}
	second := waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateIdle)
	if second.Result != "idle second" || second.Turn != 2 {
		t.Fatalf("second idle=%#v", second)
	}
	driversMu.Lock()
	driver := drivers[string(spawned.ID)]
	driversMu.Unlock()
	if got := driver.followUpSnapshot(); len(got) != 1 || got[0] != "continue same chat" {
		t.Fatalf("follow-ups=%v", got)
	}
	acquired, released := runtime.counts()
	if acquired != 1 || released != 0 {
		t.Fatalf("follow-up changed tab lease acquired=%d released=%d", acquired, released)
	}
	if _, err := manager.Cancel(context.Background(), owner, spawned.ID); err != nil {
		t.Fatal(err)
	}
	_, released = runtime.counts()
	if released != 1 {
		t.Fatalf("cancel did not release tab: %d", released)
	}
}

func TestAgentBackendReadinessFailsClosed(t *testing.T) {
	manager := newBackendTestManager(t, 5)
	backend, err := NewAgentBackend(AgentBackendOptions{
		Manager: manager,
		Settings: func(context.Context) (AgentBackendSettings, error) {
			return AgentBackendSettings{Available: false, Reason: "needs_login", MaxAgents: 5, ConnectorName: "CodeMCP"}, nil
		},
		Runtime: func(context.Context, int) (AgentBackendRuntime, error) { return newBackendTestRuntime(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
	owner, _ := managedagent.NewMCPController("owner")
	if _, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{WorkspaceID: "ws_test", Prompt: "must not silently fall back"}}); !errors.Is(err, managedagent.ErrBackendUnavailable) || !strings.Contains(err.Error(), "needs_login") {
		t.Fatalf("spawn readiness error=%v", err)
	}
}

func TestAgentBackendBoundedWaitPollingKeepsWorkingTurnAlive(t *testing.T) {
	manager := newBackendTestManager(t, 2)
	runtime := newBackendTestRuntime()
	startCalled := make(chan struct{}, 1)
	startGate := make(chan struct{})
	registerBackendTestAdapter(t, manager, runtime, 2, func(browser.BrowserTab) (AgentTurnDriver, error) {
		return &backendTestDriver{
			startCalled: startCalled,
			startGate:   startGate,
			startResult: TurnResult{State: TurnFinal, Text: "finished after tool rounds"},
		}, nil
	})
	owner, _ := managedagent.NewMCPController("owner-bounded-wait")
	spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: "ws_test", Prompt: "perform several tool rounds",
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-startCalled:
	case <-time.After(time.Second):
		t.Fatal("ChatGPT Web turn did not start")
	}

	started := time.Now()
	for range 3 {
		snapshot, err := manager.Wait(context.Background(), owner, spawned.ID, spawned.Revision, 5*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.State != managedagent.StateWorking {
			t.Fatalf("bounded wait snapshot=%#v", snapshot)
		}
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded ChatGPT waits held parent too long: %s", elapsed)
	}
	if touches := runtime.touchedCount(); touches < 3 {
		t.Fatalf("working lease touches=%d want>=3", touches)
	}

	close(startGate)
	idle := waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateIdle)
	if idle.Result != "finished after tool rounds" {
		t.Fatalf("idle after tool rounds=%#v", idle)
	}
}

func TestAgentBackendCancelDuringAcquireReleasesLateLease(t *testing.T) {
	manager := newBackendTestManager(t, 2)
	runtime := newBackendTestRuntime()
	started := make(chan struct{}, 1)
	gate := make(chan struct{})
	runtime.acquireStarted = started
	runtime.acquireGate = gate
	registerBackendTestAdapter(t, manager, runtime, 2, func(browser.BrowserTab) (AgentTurnDriver, error) {
		return &backendTestDriver{startResult: TurnResult{State: TurnFinal, Text: "unused"}}, nil
	})
	owner, _ := managedagent.NewMCPController("owner-cancel-acquire")
	spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: "ws_test", Prompt: "cancel while acquiring",
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("browser acquire did not start")
	}
	if _, err := manager.Cancel(context.Background(), owner, spawned.ID); err != nil {
		t.Fatal(err)
	}
	close(gate)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		acquired, released := runtime.counts()
		if acquired == 1 && released == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	acquired, released := runtime.counts()
	t.Fatalf("late browser lease leaked: acquired=%d released=%d", acquired, released)
}

func TestAgentBackendBrowserLeaseFailureBecomesStickyManagedFailure(t *testing.T) {
	manager := newBackendTestManager(t, 2)
	runtime := newBackendTestRuntime()
	startCalled := make(chan struct{}, 1)
	startGate := make(chan struct{})
	registerBackendTestAdapter(t, manager, runtime, 2, func(browser.BrowserTab) (AgentTurnDriver, error) {
		return &backendTestDriver{
			startCalled: startCalled,
			startGate:   startGate,
			startResult: TurnResult{State: TurnFinal, Text: "late final must not revive crash"},
		}, nil
	})
	owner, _ := managedagent.NewMCPController("owner-browser-crash")
	spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: "ws_test", Prompt: "work through browser crash",
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-startCalled:
	case <-time.After(time.Second):
		t.Fatal("driver did not enter active turn")
	}
	runtime.setLeaseState(string(spawned.ID), browser.LeaseFailed, "browser disconnected during active turn")
	failed, err := manager.Get(context.Background(), owner, spawned.ID)
	if err != nil || failed.State != managedagent.StateFailed || !strings.Contains(failed.Error, "browser disconnected") {
		t.Fatalf("browser crash snapshot=%#v err=%v", failed, err)
	}
	close(startGate)
	time.Sleep(10 * time.Millisecond)
	again, err := manager.Get(context.Background(), owner, spawned.ID)
	if err != nil || again.State != managedagent.StateFailed {
		t.Fatalf("late final revived failed browser agent: %#v err=%v", again, err)
	}
	acquired, released := runtime.counts()
	if acquired != 1 || released != 1 {
		t.Fatalf("browser crash cleanup acquired=%d released=%d", acquired, released)
	}
}

func TestAgentBackendSendFailsWhenIdleBrowserLeaseExpired(t *testing.T) {
	manager := newBackendTestManager(t, 2)
	runtime := newBackendTestRuntime()
	driver := &backendTestDriver{startResult: TurnResult{State: TurnFinal, Text: "idle"}, followUpResult: TurnResult{State: TurnFinal, Text: "must not run"}}
	registerBackendTestAdapter(t, manager, runtime, 2, func(browser.BrowserTab) (AgentTurnDriver, error) { return driver, nil })
	owner, _ := managedagent.NewMCPController("owner-expired-send")
	spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: "ws_test", Prompt: "become idle",
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateIdle)
	runtime.setLeaseState(string(spawned.ID), browser.LeaseExpired, "idle browser lease expired")
	if _, err := manager.Send(context.Background(), owner, spawned.ID, managedagent.Message{Content: "too late"}); err == nil {
		t.Fatal("send unexpectedly accepted expired browser lease")
	}
	failed, err := manager.Get(context.Background(), owner, spawned.ID)
	if err != nil || failed.State != managedagent.StateFailed || !strings.Contains(failed.Error, "idle browser lease expired") {
		t.Fatalf("expired send snapshot=%#v err=%v", failed, err)
	}
	if got := driver.followUpSnapshot(); len(got) != 0 {
		t.Fatalf("expired lease started hidden follow-up: %v", got)
	}
	_, released := runtime.counts()
	if released != 1 {
		t.Fatalf("expired send did not release tab: %d", released)
	}
}

func TestAgentBackendMapsAuthenticationAndConnectorLossToManagedFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "authentication", err: driverError(ErrorAuthentication, "follow-up", "ChatGPT session expired", nil), want: "session expired"},
		{name: "connector", err: driverError(ErrorConnectorMismatch, "connector", "CodeMCP connector disappeared", nil), want: "connector disappeared"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newBackendTestManager(t, 2)
			runtime := newBackendTestRuntime()
			driver := &backendTestDriver{startResult: TurnResult{State: TurnFinal, Text: "idle"}, followUpErr: test.err}
			registerBackendTestAdapter(t, manager, runtime, 2, func(browser.BrowserTab) (AgentTurnDriver, error) { return driver, nil })
			owner, _ := managedagent.NewMCPController("owner-loss-" + test.name)
			spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
				WorkspaceID: "ws_test", Prompt: "become idle",
			}})
			if err != nil {
				t.Fatal(err)
			}
			waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateIdle)
			if _, err := manager.Send(context.Background(), owner, spawned.ID, managedagent.Message{Content: "follow up"}); err != nil {
				t.Fatal(err)
			}
			failed := waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateFailed)
			if !strings.Contains(strings.ToLower(failed.Error), strings.ToLower(test.want)) {
				t.Fatalf("mapped failure=%#v", failed)
			}
			_, released := runtime.counts()
			if released != 1 {
				t.Fatalf("failure did not release tab: %d", released)
			}
		})
	}
}

func TestAgentBackendManagedIdleExpiryReleasesTab(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	manager, err := managedagent.NewManager(managedagent.ManagerOptions{
		DefaultBackend: AgentBackendID,
		GlobalCapacity: managedagent.Capacity{MaxParallel: 2},
		PollInterval:   time.Millisecond,
		IdleTTL:        time.Minute,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newBackendTestRuntime()
	registerBackendTestAdapter(t, manager, runtime, 2, func(browser.BrowserTab) (AgentTurnDriver, error) {
		return &backendTestDriver{startResult: TurnResult{State: TurnFinal, Text: "idle"}}, nil
	})
	owner, _ := managedagent.NewMCPController("owner-idle-expiry")
	spawned, err := manager.Spawn(context.Background(), owner, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: "ws_test", Prompt: "idle until expiry",
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitBackendTestState(t, manager, owner, spawned.ID, managedagent.StateIdle)
	now = now.Add(2 * time.Minute)
	if expired := manager.SweepExpired(context.Background()); expired != 1 {
		t.Fatalf("expired=%d want=1", expired)
	}
	snapshot, err := manager.Get(context.Background(), owner, spawned.ID)
	if err != nil || snapshot.State != managedagent.StateExpired {
		t.Fatalf("expired snapshot=%#v err=%v", snapshot, err)
	}
	_, released := runtime.counts()
	if released != 1 {
		t.Fatalf("idle expiry did not release tab: %d", released)
	}
}

func waitBackendTestState(t *testing.T, manager *managedagent.Manager, owner managedagent.Controller, id managedagent.ID, want managedagent.State) managedagent.Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Get(context.Background(), owner, id)
		if err == nil && snapshot.State == want {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, err := manager.Get(context.Background(), owner, id)
	t.Fatalf("agent %s state=%s err=%v want=%s", id, snapshot.State, err, want)
	return managedagent.Snapshot{}
}
