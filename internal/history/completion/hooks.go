package completion

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

type CompletionHook interface {
	Name() string
	Handle(context.Context, HookInvocation) error
}

type HookInvocation struct {
	Event          Event  `json:"event"`
	IdempotencyKey string `json:"idempotency_key"`
	CorrelationID  string `json:"correlation_id"`
}

type HookStatus string

const (
	HookSucceeded HookStatus = "succeeded"
	HookFailed    HookStatus = "failed"
	HookTimedOut  HookStatus = "timed_out"
	HookCancelled HookStatus = "cancelled"
	HookDuplicate HookStatus = "duplicate"
)

type HookDiagnostic struct {
	Hook         string     `json:"hook"`
	EventID      string     `json:"event_id"`
	CompletionID string     `json:"completion_id"`
	WorkspaceID  string     `json:"workspace_id"`
	Status       HookStatus `json:"status"`
	DurationMS   int64      `json:"duration_ms"`
	Timestamp    time.Time  `json:"timestamp"`
}

type HookBusOptions struct {
	Timeout   time.Duration
	MaxRecent int
	MaxSeen   int
}

type CompletionHookBus struct {
	timeout   time.Duration
	maxRecent int
	maxSeen   int

	hooksMu sync.RWMutex
	hooks   map[string]CompletionHook

	seenMu    sync.Mutex
	seen      map[string]struct{}
	seenOrder []string

	diagnosticsMu sync.RWMutex
	diagnostics   []HookDiagnostic

	lifecycleMu sync.RWMutex
	stopped     bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewCompletionHookBus(options HookBusOptions) *CompletionHookBus {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	maxRecent := options.MaxRecent
	if maxRecent <= 0 {
		maxRecent = 64
	}
	maxSeen := options.MaxSeen
	if maxSeen <= 0 {
		maxSeen = 512
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CompletionHookBus{
		timeout: timeout, maxRecent: maxRecent, maxSeen: maxSeen,
		hooks: map[string]CompletionHook{}, seen: map[string]struct{}{},
		ctx: ctx, cancel: cancel,
	}
}

func (b *CompletionHookBus) Register(hook CompletionHook) error {
	if b == nil {
		return errors.New("completion hook bus is unavailable")
	}
	if hook == nil {
		return errors.New("completion hook is required")
	}
	name := strings.TrimSpace(hook.Name())
	if name == "" {
		return errors.New("completion hook name is required")
	}
	b.lifecycleMu.RLock()
	defer b.lifecycleMu.RUnlock()
	if b.stopped {
		return errors.New("completion hook bus is stopped")
	}
	b.hooksMu.Lock()
	b.hooks[name] = hook
	b.hooksMu.Unlock()
	return nil
}

func (b *CompletionHookBus) Unregister(name string) {
	if b == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	b.hooksMu.Lock()
	delete(b.hooks, name)
	b.hooksMu.Unlock()
}

func (b *CompletionHookBus) Dispatch(event Event) error {
	if b == nil {
		return errors.New("completion hook bus is unavailable")
	}
	if event.Name != EventAccepted || strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Record.ID) == "" {
		return errors.New("completion hook dispatch requires an accepted event")
	}

	b.lifecycleMu.RLock()
	if b.stopped {
		b.lifecycleMu.RUnlock()
		return errors.New("completion hook bus is stopped")
	}
	hooks := b.snapshotHooks()
	for name, hook := range hooks {
		key := name + ":" + event.ID
		if !b.reserve(key) {
			b.record(HookDiagnostic{
				Hook: name, EventID: event.ID, CompletionID: event.Record.ID, WorkspaceID: event.Record.WorkspaceID,
				Status: HookDuplicate, Timestamp: time.Now().UTC(),
			})
			continue
		}
		invocation := HookInvocation{Event: event, IdempotencyKey: key, CorrelationID: event.Record.ID}
		b.wg.Add(1)
		go b.execute(name, hook, invocation)
	}
	b.lifecycleMu.RUnlock()
	return nil
}

func (b *CompletionHookBus) Diagnostics() []HookDiagnostic {
	if b == nil {
		return nil
	}
	b.diagnosticsMu.RLock()
	defer b.diagnosticsMu.RUnlock()
	result := make([]HookDiagnostic, len(b.diagnostics))
	copy(result, b.diagnostics)
	return result
}

func (b *CompletionHookBus) Stop() {
	if b == nil {
		return
	}
	b.lifecycleMu.Lock()
	if !b.stopped {
		b.stopped = true
		b.cancel()
	}
	b.lifecycleMu.Unlock()
	b.wg.Wait()
}

func (b *CompletionHookBus) snapshotHooks() map[string]CompletionHook {
	b.hooksMu.RLock()
	defer b.hooksMu.RUnlock()
	result := make(map[string]CompletionHook, len(b.hooks))
	for name, hook := range b.hooks {
		result[name] = hook
	}
	return result
}

func (b *CompletionHookBus) reserve(key string) bool {
	b.seenMu.Lock()
	defer b.seenMu.Unlock()
	if _, exists := b.seen[key]; exists {
		return false
	}
	b.seen[key] = struct{}{}
	b.seenOrder = append(b.seenOrder, key)
	if overflow := len(b.seenOrder) - b.maxSeen; overflow > 0 {
		for _, stale := range b.seenOrder[:overflow] {
			delete(b.seen, stale)
		}
		b.seenOrder = append([]string(nil), b.seenOrder[overflow:]...)
	}
	return true
}

func (b *CompletionHookBus) execute(name string, hook CompletionHook, invocation HookInvocation) {
	defer b.wg.Done()
	started := time.Now()
	ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- hook.Handle(ctx, invocation)
	}()

	status := HookFailed
	select {
	case err := <-done:
		switch {
		case err == nil:
			status = HookSucceeded
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			status = HookTimedOut
		case ctx.Err() != nil:
			status = HookCancelled
		default:
			status = HookFailed
		}
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = HookTimedOut
		} else {
			status = HookCancelled
		}
	}
	b.record(HookDiagnostic{
		Hook: name, EventID: invocation.Event.ID, CompletionID: invocation.Event.Record.ID,
		WorkspaceID: invocation.Event.Record.WorkspaceID, Status: status,
		DurationMS: time.Since(started).Milliseconds(), Timestamp: time.Now().UTC(),
	})
}

func (b *CompletionHookBus) record(value HookDiagnostic) {
	b.diagnosticsMu.Lock()
	b.diagnostics = append(b.diagnostics, value)
	if overflow := len(b.diagnostics) - b.maxRecent; overflow > 0 {
		b.diagnostics = append([]HookDiagnostic(nil), b.diagnostics[overflow:]...)
	}
	b.diagnosticsMu.Unlock()
}
