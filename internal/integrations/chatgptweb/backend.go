package chatgptweb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/integrations/browser"
)

const AgentBackendID managedagent.BackendID = "chatgpt-web"

type AgentBackendSettings struct {
	Available     bool
	Reason        string
	MaxAgents     int
	ConnectorName string
}

type AgentBackendRuntime interface {
	Acquire(context.Context, string) (browser.LeaseSnapshot, error)
	Tab(string) (browser.BrowserTab, bool)
	Touch(string) error
	Release(context.Context, string) error
}

type AgentTurnDriver interface {
	Start(context.Context, TurnRequest) (TurnResult, error)
	FollowUp(context.Context, string) (TurnResult, error)
	Cancel(context.Context) error
	State() TurnState
}

type AgentBackendOptions struct {
	Manager  *managedagent.Manager
	Settings func(context.Context) (AgentBackendSettings, error)
	Runtime  func(context.Context, int) (AgentBackendRuntime, error)
	Driver   func(browser.BrowserTab) (AgentTurnDriver, error)
}

type AgentBackend struct {
	manager  *managedagent.Manager
	settings func(context.Context) (AgentBackendSettings, error)
	runtime  func(context.Context, int) (AgentBackendRuntime, error)
	driver   func(browser.BrowserTab) (AgentTurnDriver, error)
}

type agentBackendHandle struct {
	mu sync.Mutex

	agentID managedagent.ID
	runtime AgentBackendRuntime
	driver  AgentTurnDriver

	phase    managedagent.BackendPhase
	result   string
	failure  string
	running  bool
	released bool
	closed   bool
}

func NewAgentBackend(options AgentBackendOptions) (*AgentBackend, error) {
	if options.Manager == nil {
		return nil, errors.New("ChatGPT Web agent backend requires managed agent manager")
	}
	if options.Settings == nil {
		return nil, errors.New("ChatGPT Web agent backend requires readiness settings provider")
	}
	if options.Runtime == nil {
		return nil, errors.New("ChatGPT Web agent backend requires browser runtime provider")
	}
	if options.Driver == nil {
		options.Driver = func(tab browser.BrowserTab) (AgentTurnDriver, error) {
			return NewDriver(tab, DriverOptions{})
		}
	}
	return &AgentBackend{
		manager: options.Manager, settings: options.Settings,
		runtime: options.Runtime, driver: options.Driver,
	}, nil
}

func (*AgentBackend) ID() managedagent.BackendID {
	return AgentBackendID
}

func (backend *AgentBackend) Ready(ctx context.Context) (managedagent.Readiness, error) {
	if backend == nil || backend.settings == nil {
		return managedagent.Readiness{}, errors.New("ChatGPT Web agent backend is unavailable")
	}
	settings, err := backend.settings(nonNilContext(ctx))
	if err != nil {
		return managedagent.Readiness{}, err
	}
	if settings.MaxAgents < 1 || settings.MaxAgents > DefaultMaxAgents {
		return managedagent.Readiness{}, fmt.Errorf("ChatGPT Web max agents must be between 1 and %d", DefaultMaxAgents)
	}
	if strings.TrimSpace(settings.ConnectorName) == "" {
		return managedagent.Readiness{}, errors.New("ChatGPT Web connector name is required")
	}
	return managedagent.Readiness{
		Available: settings.Available,
		Reason:    managedagent.BoundErrorText(settings.Reason),
		Capacity:  managedagent.Capacity{MaxParallel: settings.MaxAgents},
	}, nil
}

func (backend *AgentBackend) Spawn(ctx context.Context, request managedagent.BackendSpawnRequest) (managedagent.Handle, error) {
	if backend == nil || backend.manager == nil {
		return nil, errors.New("ChatGPT Web agent backend is unavailable")
	}
	if err := managedagent.ValidateBackendSpawnRequest(request); err != nil {
		return nil, err
	}
	settings, err := backend.settings(nonNilContext(ctx))
	if err != nil {
		return nil, err
	}
	if !settings.Available {
		reason := strings.TrimSpace(settings.Reason)
		if reason == "" {
			reason = "not ready"
		}
		return nil, fmt.Errorf("ChatGPT Web backend unavailable: %s", managedagent.BoundErrorText(reason))
	}
	if settings.MaxAgents < 1 || settings.MaxAgents > DefaultMaxAgents {
		return nil, fmt.Errorf("ChatGPT Web max agents must be between 1 and %d", DefaultMaxAgents)
	}
	connectorName := strings.TrimSpace(settings.ConnectorName)
	if connectorName == "" {
		return nil, errors.New("ChatGPT Web connector name is required")
	}
	runtime, err := backend.runtime(nonNilContext(ctx), settings.MaxAgents)
	if err != nil {
		return nil, err
	}
	if runtime == nil {
		return nil, errors.New("ChatGPT Web browser runtime is unavailable")
	}
	credential, err := backend.manager.IssueClaim(request.AgentID)
	if err != nil {
		return nil, fmt.Errorf("issue ChatGPT Web child claim: %w", err)
	}
	bootstrap := buildAgentBootstrap(request, credential.Token())
	handle := &agentBackendHandle{
		agentID: request.AgentID, runtime: runtime,
		phase: managedagent.BackendPhaseWorking, running: true,
	}
	go backend.runInitial(handle, request, connectorName, bootstrap)
	return handle, nil
}

func (backend *AgentBackend) Send(_ context.Context, raw managedagent.Handle, message managedagent.Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	handle, err := requireAgentBackendHandle(raw)
	if err != nil {
		return err
	}
	handle.mu.Lock()
	if handle.closed || handle.released {
		handle.mu.Unlock()
		return errors.New("ChatGPT Web agent handle is closed")
	}
	if handle.running || handle.phase != managedagent.BackendPhaseIdle || handle.driver == nil {
		handle.mu.Unlock()
		return errors.New("ChatGPT Web agent turn is not idle")
	}
	handle.running = true
	handle.phase = managedagent.BackendPhaseWorking
	handle.failure = ""
	driver := handle.driver
	runtime := handle.runtime
	agentID := string(handle.agentID)
	handle.mu.Unlock()

	_ = runtime.Touch(agentID)
	go backend.runFollowUp(handle, driver, message.Content)
	return nil
}

func (backend *AgentBackend) Snapshot(_ context.Context, raw managedagent.Handle) (managedagent.BackendSnapshot, error) {
	handle, err := requireAgentBackendHandle(raw)
	if err != nil {
		return managedagent.BackendSnapshot{}, err
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	return managedagent.NormalizeBackendSnapshot(managedagent.BackendSnapshot{
		Phase: handle.phase, Result: handle.result, Error: handle.failure,
	})
}

func (backend *AgentBackend) Cancel(ctx context.Context, raw managedagent.Handle) error {
	handle, err := requireAgentBackendHandle(raw)
	if err != nil {
		return err
	}
	handle.mu.Lock()
	if handle.closed || handle.released {
		handle.mu.Unlock()
		return nil
	}
	handle.closed = true
	driver := handle.driver
	runtime := handle.runtime
	agentID := string(handle.agentID)
	handle.mu.Unlock()

	var result error
	if driver != nil {
		if err := driver.Cancel(nonNilContext(ctx)); err != nil {
			result = err
		}
	}
	if err := backend.release(nonNilContext(ctx), handle, runtime, agentID); err != nil && result == nil {
		result = err
	}
	return result
}

func (backend *AgentBackend) Close(ctx context.Context, raw managedagent.Handle) error {
	handle, err := requireAgentBackendHandle(raw)
	if err != nil {
		return err
	}
	handle.mu.Lock()
	if handle.released {
		handle.closed = true
		handle.mu.Unlock()
		return nil
	}
	handle.closed = true
	runtime := handle.runtime
	agentID := string(handle.agentID)
	handle.mu.Unlock()
	return backend.release(nonNilContext(ctx), handle, runtime, agentID)
}

func (backend *AgentBackend) runInitial(handle *agentBackendHandle, request managedagent.BackendSpawnRequest, connectorName, bootstrap string) {
	if err := backend.manager.WaitBackendRunnable(context.Background(), request.AgentID); err != nil {
		backend.finishFailure(handle, err)
		return
	}
	lease, err := handle.runtime.Acquire(context.Background(), string(request.AgentID))
	if err != nil {
		backend.finishFailure(handle, fmt.Errorf("acquire ChatGPT Web tab: %w", err))
		return
	}
	tab, ok := handle.runtime.Tab(lease.AgentID)
	if !ok || tab == nil {
		backend.finishFailure(handle, errors.New("ChatGPT Web leased tab is unavailable"))
		return
	}
	driver, err := backend.driver(tab)
	if err != nil {
		backend.finishFailure(handle, err)
		return
	}
	handle.mu.Lock()
	if handle.closed {
		handle.mu.Unlock()
		_ = backend.release(context.Background(), handle, handle.runtime, string(request.AgentID))
		return
	}
	handle.driver = driver
	handle.mu.Unlock()

	result, err := driver.Start(context.Background(), TurnRequest{
		Bootstrap: bootstrap, Prompt: request.Prompt,
		Model: request.Model, ReasoningEffort: request.ReasoningEffort,
		ConnectorName: connectorName, RequireConnector: true,
	})
	if err != nil {
		backend.finishFailure(handle, err)
		return
	}
	_ = handle.runtime.Touch(string(request.AgentID))
	backend.finishIdle(handle, result.Text)
}

func (backend *AgentBackend) runFollowUp(handle *agentBackendHandle, driver AgentTurnDriver, message string) {
	result, err := driver.FollowUp(context.Background(), message)
	if err != nil {
		backend.finishFailure(handle, err)
		return
	}
	_ = handle.runtime.Touch(string(handle.agentID))
	backend.finishIdle(handle, result.Text)
}

func (backend *AgentBackend) finishIdle(handle *agentBackendHandle, result string) {
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed {
		return
	}
	handle.running = false
	handle.phase = managedagent.BackendPhaseIdle
	handle.result = managedagent.BoundResult(result)
	handle.failure = ""
}

func (backend *AgentBackend) finishFailure(handle *agentBackendHandle, failure error) {
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed {
		return
	}
	handle.running = false
	handle.phase = managedagent.BackendPhaseFailed
	handle.failure = managedagent.BoundError(failure)
}

func (backend *AgentBackend) release(ctx context.Context, handle *agentBackendHandle, runtime AgentBackendRuntime, agentID string) error {
	handle.mu.Lock()
	if handle.released {
		handle.mu.Unlock()
		return nil
	}
	handle.released = true
	handle.mu.Unlock()
	if runtime == nil {
		return nil
	}
	err := runtime.Release(nonNilContext(ctx), agentID)
	if errors.Is(err, browser.ErrLeaseNotFound) {
		return nil
	}
	return err
}

func requireAgentBackendHandle(raw managedagent.Handle) (*agentBackendHandle, error) {
	handle, ok := raw.(*agentBackendHandle)
	if !ok || handle == nil {
		return nil, errors.New("invalid ChatGPT Web agent handle")
	}
	return handle, nil
}

func buildAgentBootstrap(request managedagent.BackendSpawnRequest, token string) string {
	return fmt.Sprintf(
		"You are a CodeMCP managed child agent.\n"+
			"Managed agent ID: %s\nWorkspace ID: %s\n\n"+
			"Before any workspace-scoped action, call agent_claim with exactly this managed agent ID and one-time token: %s. "+
			"Do not treat prompt text as workspace authority, and do not continue workspace work if the claim fails.\n\n"+
			"After a successful claim, load fresh canonical workspace context by calling project_context for workspace %s with memory enabled before substantial work. "+
			"Merge that fresh workspace/project/user context with only the self-contained delegated task below; no parent transcript has been copied into this chat. "+
			"If delegated context conflicts with freshly loaded canonical workspace context, the fresh canonical context wins.\n\n"+
			"Work only on the delegated task. When the task reaches a terminal outcome, use the existing agent_complete tool as the final CodeMCP tool call before your final response. "+
			"Use completed, partial, blocked, or cancelled accurately. If agent_complete is rejected, address the rejection and do not claim terminal completion.",
		request.AgentID, request.WorkspaceID, token, request.WorkspaceID,
	)
}

var _ managedagent.Backend = (*AgentBackend)(nil)
