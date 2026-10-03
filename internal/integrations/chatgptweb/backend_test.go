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
	mu       sync.Mutex
	tabs     map[string]*backendTestTab
	acquired []string
	released []string
	touched  []string
}

func newBackendTestRuntime() *backendTestRuntime {
	return &backendTestRuntime{tabs: map[string]*backendTestTab{}}
}

func (runtime *backendTestRuntime) Acquire(_ context.Context, agentID string) (browser.LeaseSnapshot, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.tabs[agentID] != nil {
		return browser.LeaseSnapshot{}, browser.ErrLeaseExists
	}
	tab := &backendTestTab{id: agentID, done: make(chan struct{})}
	runtime.tabs[agentID] = tab
	runtime.acquired = append(runtime.acquired, agentID)
	return browser.LeaseSnapshot{AgentID: agentID, TabID: tab.id, State: browser.LeaseActive}, nil
}

func (runtime *backendTestRuntime) Tab(agentID string) (browser.BrowserTab, bool) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	tab, ok := runtime.tabs[agentID]
	return tab, ok
}

func (runtime *backendTestRuntime) Touch(agentID string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.tabs[agentID] == nil {
		return browser.ErrLeaseNotFound
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
	runtime.released = append(runtime.released, agentID)
	return nil
}

func (runtime *backendTestRuntime) counts() (int, int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return len(runtime.acquired), len(runtime.released)
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
