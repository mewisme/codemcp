package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	DefaultMaxParallel = 5
	DefaultMaxTerminal = 256
)

var (
	DefaultTerminalTTL  = time.Hour
	DefaultIdleTTL      = 15 * time.Minute
	DefaultPollInterval = 100 * time.Millisecond
)

type ManagerOptions struct {
	DefaultBackend BackendID
	GlobalCapacity Capacity
	MaxTerminal    int
	TerminalTTL    time.Duration
	IdleTTL        time.Duration
	PollInterval   time.Duration
	Now            func() time.Time
}

type ManagedSpawnRequest struct {
	Input    SpawnInput
	ParentID ID
	Depth    int
}

type Manager struct {
	mu sync.RWMutex

	backends      map[BackendID]Backend
	entries       map[ID]*entry
	terminalOrder []ID

	defaultBackend BackendID
	globalCapacity Capacity
	maxTerminal    int
	terminalTTL    time.Duration
	idleTTL        time.Duration
	pollInterval   time.Duration
	now            func() time.Time
}

func NewManager(options ManagerOptions) (*Manager, error) {
	if options.GlobalCapacity.MaxParallel == 0 {
		options.GlobalCapacity.MaxParallel = DefaultMaxParallel
	}
	if err := options.GlobalCapacity.Validate(); err != nil {
		return nil, fmt.Errorf("global capacity: %w", err)
	}
	if options.DefaultBackend != "" {
		id, err := NormalizeBackendID(string(options.DefaultBackend))
		if err != nil {
			return nil, fmt.Errorf("default backend: %w", err)
		}
		options.DefaultBackend = id
	}
	if options.MaxTerminal == 0 {
		options.MaxTerminal = DefaultMaxTerminal
	}
	if options.MaxTerminal < 1 {
		return nil, errors.New("max terminal records must be positive")
	}
	if options.TerminalTTL == 0 {
		options.TerminalTTL = DefaultTerminalTTL
	}
	if options.TerminalTTL < 0 {
		return nil, errors.New("terminal ttl cannot be negative")
	}
	if options.IdleTTL == 0 {
		options.IdleTTL = DefaultIdleTTL
	}
	if options.IdleTTL < 0 {
		return nil, errors.New("idle ttl cannot be negative")
	}
	if options.PollInterval == 0 {
		options.PollInterval = DefaultPollInterval
	}
	if options.PollInterval < 0 {
		return nil, errors.New("poll interval cannot be negative")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Manager{
		backends: map[BackendID]Backend{}, entries: map[ID]*entry{},
		defaultBackend: options.DefaultBackend, globalCapacity: options.GlobalCapacity,
		maxTerminal: options.MaxTerminal, terminalTTL: options.TerminalTTL,
		idleTTL: options.IdleTTL, pollInterval: options.PollInterval, now: options.Now,
	}, nil
}

func (manager *Manager) RegisterBackend(backend Backend) error {
	if manager == nil {
		return errors.New("managed agent manager is unavailable")
	}
	if backend == nil {
		return errors.New("managed agent backend is required")
	}
	id, err := NormalizeBackendID(string(backend.ID()))
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.backends[id]; exists {
		return fmt.Errorf("managed agent backend %q is already registered", id)
	}
	manager.backends[id] = backend
	return nil
}

func (manager *Manager) Spawn(ctx context.Context, controller Controller, request ManagedSpawnRequest) (Snapshot, error) {
	if manager == nil {
		return Snapshot{}, errors.New("managed agent manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := controller.Validate(); err != nil {
		return Snapshot{}, err
	}
	input, err := NormalizeSpawnInput(request.Input)
	if err != nil {
		return Snapshot{}, err
	}
	if request.Depth == 0 {
		request.Depth = 1
	}
	if err := ValidateDelegation(request.ParentID, request.Depth); err != nil {
		return Snapshot{}, err
	}

	manager.mu.RLock()
	backends := make(map[BackendID]Backend, len(manager.backends))
	for id, backend := range manager.backends {
		backends[id] = backend
	}
	defaultBackend := manager.defaultBackend
	manager.mu.RUnlock()

	backend, readiness, err := ResolveBackend(ctx, backends, input.Backend, defaultBackend)
	if err != nil {
		return Snapshot{}, err
	}
	backendID, _ := NormalizeBackendID(string(backend.ID()))
	_, err = EffectiveCapacity(manager.globalCapacity, readiness.Capacity)
	if err != nil {
		return Snapshot{}, err
	}
	id, err := NewID()
	if err != nil {
		return Snapshot{}, err
	}
	now := manager.now().UTC()
	record := Record{
		ID: id, Backend: backendID, WorkspaceID: input.WorkspaceID,
		ParentID: request.ParentID, Depth: request.Depth, Model: input.Model,
		ReasoningEffort: input.ReasoningEffort, State: StateStarting,
		CreatedAt: now, UpdatedAt: now, Turn: 0, Revision: 1, Owner: controller,
	}
	item := newEntry(record, backend)

	manager.mu.Lock()
	manager.pruneTerminalLocked(now)
	if manager.activeCountLocked() >= manager.globalCapacity.MaxParallel {
		manager.mu.Unlock()
		return Snapshot{}, fmt.Errorf("%w: global limit %d", ErrCapacityReached, manager.globalCapacity.MaxParallel)
	}
	backendLimit := readiness.Capacity.MaxParallel
	if manager.backendActiveCountLocked(backendID) >= backendLimit {
		manager.mu.Unlock()
		return Snapshot{}, fmt.Errorf("%w: backend %s limit %d", ErrCapacityReached, backendID, backendLimit)
	}
	manager.entries[id] = item
	manager.mu.Unlock()

	spawnRequest := BackendSpawnRequest{
		AgentID: id, WorkspaceID: input.WorkspaceID, Prompt: input.Prompt,
		Model: input.Model, ReasoningEffort: input.ReasoningEffort,
		ParentID: request.ParentID, Depth: request.Depth,
	}
	handle, spawnErr := backend.Spawn(ctx, spawnRequest)
	now = manager.now()
	manager.mu.Lock()
	current := manager.entries[id]
	if current == nil {
		manager.mu.Unlock()
		if handle != nil {
			_ = backend.Close(context.Background(), handle)
		}
		return Snapshot{}, ErrAgentNotFound
	}
	if spawnErr != nil {
		_ = manager.markTerminalLocked(current, StateFailed, "", BoundError(spawnErr), now)
		snapshot := current.record.Snapshot()
		manager.mu.Unlock()
		if handle != nil {
			_ = backend.Close(context.Background(), handle)
		}
		return snapshot, fmt.Errorf("spawn managed agent: %w", spawnErr)
	}
	if handle == nil {
		nilHandleErr := errors.New("managed agent backend returned nil handle")
		_ = manager.markTerminalLocked(current, StateFailed, "", nilHandleErr.Error(), now)
		snapshot := current.record.Snapshot()
		manager.mu.Unlock()
		return snapshot, nilHandleErr
	}
	if current.record.State.Terminal() {
		snapshot := current.record.Snapshot()
		manager.mu.Unlock()
		if handle != nil {
			_ = backend.Close(context.Background(), handle)
		}
		return snapshot, fmt.Errorf("managed agent terminated during spawn: %s", snapshot.State)
	}
	current.handle = handle
	current.record.Turn = 1
	if err := current.transitionLocked(StateWorking, now); err != nil {
		manager.mu.Unlock()
		_ = backend.Close(context.Background(), handle)
		return Snapshot{}, err
	}
	snapshot := current.record.Snapshot()
	manager.mu.Unlock()
	return snapshot, nil
}

func (manager *Manager) Get(ctx context.Context, controller Controller, id ID) (Snapshot, error) {
	if manager == nil {
		return Snapshot{}, errors.New("managed agent manager is unavailable")
	}
	if err := controller.Validate(); err != nil {
		return Snapshot{}, err
	}
	if err := ValidateID(id); err != nil {
		return Snapshot{}, err
	}
	if err := manager.refresh(ctx, controller, id); err != nil && !errors.Is(err, ErrAgentNotFound) {
		return Snapshot{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneTerminalLocked(manager.now())
	item, err := manager.authorizedEntryLocked(controller, id)
	if err != nil {
		return Snapshot{}, err
	}
	return item.record.Snapshot(), nil
}

func (manager *Manager) List(ctx context.Context, controller Controller) ([]Snapshot, error) {
	if manager == nil {
		return nil, errors.New("managed agent manager is unavailable")
	}
	if err := controller.Validate(); err != nil {
		return nil, err
	}
	manager.mu.RLock()
	ids := make([]ID, 0, len(manager.entries))
	for id, item := range manager.entries {
		if controller.CanControl(item.record.Owner) {
			ids = append(ids, id)
		}
	}
	manager.mu.RUnlock()
	for _, id := range ids {
		_ = manager.refresh(ctx, controller, id)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneTerminalLocked(manager.now())
	result := make([]Snapshot, 0, len(ids))
	for _, item := range manager.entries {
		if controller.CanControl(item.record.Owner) {
			result = append(result, item.record.Snapshot())
		}
	}
	sortSnapshots(result)
	return result, nil
}

func (manager *Manager) Send(ctx context.Context, controller Controller, id ID, message Message) (Snapshot, error) {
	if manager == nil {
		return Snapshot{}, errors.New("managed agent manager is unavailable")
	}
	if err := controller.Validate(); err != nil {
		return Snapshot{}, err
	}
	if err := message.Validate(); err != nil {
		return Snapshot{}, err
	}
	manager.mu.Lock()
	item, err := manager.authorizedEntryLocked(controller, id)
	if err != nil {
		manager.mu.Unlock()
		return Snapshot{}, err
	}
	if item.record.State != StateIdle {
		state := item.record.State
		manager.mu.Unlock()
		if state.Terminal() || state == StateCompletionPending || state == StateWorking || state == StateStarting {
			return Snapshot{}, fmt.Errorf("%w: state %s", ErrAgentBusy, state)
		}
		return Snapshot{}, fmt.Errorf("%w: state %s", ErrAgentNotIdle, state)
	}
	backend, handle := item.backend, item.handle
	item.record.Turn++
	if err := item.transitionLocked(StateWorking, manager.now()); err != nil {
		manager.mu.Unlock()
		return Snapshot{}, err
	}
	started := item.record.Snapshot()
	manager.mu.Unlock()

	if err := backend.Send(ctx, handle, message); err != nil {
		manager.failBackend(id, item, err)
		return Snapshot{}, fmt.Errorf("send managed agent message: %w", err)
	}
	return started, nil
}

func (manager *Manager) Cancel(ctx context.Context, controller Controller, id ID) (Snapshot, error) {
	if manager == nil {
		return Snapshot{}, errors.New("managed agent manager is unavailable")
	}
	if err := controller.Validate(); err != nil {
		return Snapshot{}, err
	}
	manager.mu.Lock()
	item, err := manager.authorizedEntryLocked(controller, id)
	if err != nil {
		manager.mu.Unlock()
		return Snapshot{}, err
	}
	if item.record.State.Terminal() {
		snapshot := item.record.Snapshot()
		manager.mu.Unlock()
		return snapshot, nil
	}
	backend, handle := item.backend, item.handle
	if err := manager.markTerminalLocked(item, StateCancelled, item.record.Result, item.record.Error, manager.now()); err != nil {
		manager.mu.Unlock()
		return Snapshot{}, err
	}
	snapshot := item.record.Snapshot()
	manager.mu.Unlock()

	var cleanupErr error
	if handle != nil {
		if err := backend.Cancel(ctx, handle); err != nil {
			cleanupErr = err
		}
		if err := backend.Close(context.Background(), handle); err != nil && cleanupErr == nil {
			cleanupErr = err
		}
	}
	if cleanupErr != nil {
		manager.annotateTerminalError(id, cleanupErr)
		snapshot, _ = manager.Get(context.Background(), controller, id)
	}
	return snapshot, nil
}

func (manager *Manager) refresh(ctx context.Context, controller Controller, id ID) error {
	if ctx == nil {
		ctx = context.Background()
	}
	manager.mu.RLock()
	item, err := manager.authorizedEntryLocked(controller, id)
	if err != nil {
		manager.mu.RUnlock()
		return err
	}
	if item.record.State.Terminal() || item.record.State == StateStarting || item.handle == nil {
		manager.mu.RUnlock()
		return nil
	}
	backend, handle := item.backend, item.handle
	manager.mu.RUnlock()

	snapshot, err := backend.Snapshot(ctx, handle)
	if err != nil {
		manager.failBackend(id, item, err)
		return nil
	}
	snapshot, err = NormalizeBackendSnapshot(snapshot)
	if err != nil {
		manager.failBackend(id, item, err)
		return nil
	}
	return manager.applyBackendSnapshot(id, item, snapshot)
}

func (manager *Manager) applyBackendSnapshot(id ID, expected *entry, snapshot BackendSnapshot) error {
	now := manager.now()
	manager.mu.Lock()
	item := manager.entries[id]
	if item == nil || item != expected || item.record.State.Terminal() {
		manager.mu.Unlock()
		return nil
	}
	backend, handle := item.backend, item.handle
	closeHandle := false
	switch snapshot.Phase {
	case BackendPhaseWorking:
		if item.record.State == StateIdle {
			if err := item.transitionLocked(StateWorking, now); err != nil {
				manager.mu.Unlock()
				return err
			}
		}
	case BackendPhaseIdle:
		if item.record.State == StateCompletionPending {
			if item.pendingTerminal == "" {
				manager.mu.Unlock()
				return errors.New("completion-pending agent has no pending terminal status")
			}
			if err := manager.markTerminalLocked(item, item.pendingTerminal, snapshot.Result, snapshot.Error, now); err != nil {
				manager.mu.Unlock()
				return err
			}
			closeHandle = true
		} else {
			changed := item.record.Result != snapshot.Result || item.record.Error != snapshot.Error
			item.record.Result = BoundResult(snapshot.Result)
			item.record.Error = BoundErrorText(snapshot.Error)
			if item.record.State == StateWorking {
				if err := item.transitionLocked(StateIdle, now); err != nil {
					manager.mu.Unlock()
					return err
				}
			} else if changed {
				item.changedLocked(now)
			}
		}
	case BackendPhaseFailed:
		if err := manager.markTerminalLocked(item, StateFailed, snapshot.Result, snapshot.Error, now); err != nil {
			manager.mu.Unlock()
			return err
		}
		closeHandle = true
	}
	manager.mu.Unlock()
	if closeHandle && handle != nil {
		_ = backend.Close(context.Background(), handle)
	}
	return nil
}

func (manager *Manager) failBackend(id ID, expected *entry, failure error) {
	manager.mu.Lock()
	item := manager.entries[id]
	if item == nil || item != expected || item.record.State.Terminal() {
		manager.mu.Unlock()
		return
	}
	backend, handle := item.backend, item.handle
	_ = manager.markTerminalLocked(item, StateFailed, item.record.Result, BoundError(failure), manager.now())
	manager.mu.Unlock()
	if handle != nil {
		_ = backend.Close(context.Background(), handle)
	}
}

func (manager *Manager) annotateTerminalError(id ID, failure error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	item := manager.entries[id]
	if item == nil || !item.record.State.Terminal() {
		return
	}
	value := BoundError(failure)
	if value == "" || value == item.record.Error {
		return
	}
	item.record.Error = value
	item.changedLocked(manager.now())
}

func (manager *Manager) SweepExpired(ctx context.Context) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if manager == nil {
		return 0
	}
	now := manager.now()
	type closing struct {
		backend Backend
		handle  Handle
	}
	var closings []closing
	manager.mu.Lock()
	expired := 0
	for _, item := range manager.entries {
		if item.record.State != StateIdle || manager.idleTTL <= 0 || now.Sub(item.record.UpdatedAt) < manager.idleTTL {
			continue
		}
		backend, handle := item.backend, item.handle
		if manager.markTerminalLocked(item, StateExpired, item.record.Result, item.record.Error, now) == nil {
			expired++
			if handle != nil {
				closings = append(closings, closing{backend: backend, handle: handle})
			}
		}
	}
	manager.pruneTerminalLocked(now)
	manager.mu.Unlock()
	for _, item := range closings {
		_ = item.backend.Cancel(ctx, item.handle)
		_ = item.backend.Close(context.Background(), item.handle)
	}
	return expired
}
