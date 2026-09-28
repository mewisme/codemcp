package notification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	ProviderDesktop  = "desktop"
	ProviderTelegram = "telegram"
)

type Kind string

const (
	KindApprovalPending  Kind = "approval.pending"
	KindApprovalResolved Kind = "approval.resolved"
)

type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Message struct {
	ID           string    `json:"id"`
	Kind         Kind      `json:"kind"`
	Title        string    `json:"title"`
	Subject      string    `json:"subject,omitempty"`
	Body         string    `json:"body"`
	Summary      string    `json:"summary,omitempty"`
	Status       string    `json:"status,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	CompletionID string    `json:"completion_id,omitempty"`
	WorkspaceID  string    `json:"workspace_id,omitempty"`
	ProcessID    string    `json:"process_id,omitempty"`
	ExecutionID  string    `json:"execution_id,omitempty"`
	TargetTool   string    `json:"target_tool,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	ExitCode     *int      `json:"exit_code,omitempty"`
	Signal       string    `json:"signal,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
	Actions      []Action  `json:"actions,omitempty"`
}

type Provider interface {
	Name() string
	Notify(context.Context, Message) error
}

type Availability interface {
	Available() bool
}

type DiagnosticStatus string

const (
	DiagnosticDelivered   DiagnosticStatus = "delivered"
	DiagnosticFailed      DiagnosticStatus = "failed"
	DiagnosticUnavailable DiagnosticStatus = "unavailable"
	DiagnosticCancelled   DiagnosticStatus = "cancelled"
)

type Diagnostic struct {
	Provider     string           `json:"provider"`
	Event        string           `json:"event"`
	RequestID    string           `json:"request_id,omitempty"`
	CompletionID string           `json:"completion_id,omitempty"`
	WorkspaceID  string           `json:"workspace_id,omitempty"`
	Status       DiagnosticStatus `json:"status"`
	Attempts     int              `json:"attempts"`
	DurationMS   int64            `json:"duration_ms"`
	Timestamp    time.Time        `json:"timestamp"`
}

type CoordinatorOptions struct {
	Timeout    time.Duration
	Attempts   int
	RetryDelay time.Duration
	MaxRecent  int
}

type Coordinator struct {
	timeout    time.Duration
	attempts   int
	retryDelay time.Duration
	maxRecent  int

	providersMu sync.RWMutex
	providers   map[string]Provider

	diagnosticsMu sync.RWMutex
	diagnostics   []Diagnostic

	lifecycleMu sync.RWMutex
	stopped     bool
	wg          sync.WaitGroup
}

func NewCoordinator(options CoordinatorOptions) *Coordinator {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	attempts := options.Attempts
	if attempts <= 0 {
		attempts = 2
	}
	if attempts > 3 {
		attempts = 3
	}
	retryDelay := options.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 100 * time.Millisecond
	}
	maxRecent := options.MaxRecent
	if maxRecent <= 0 {
		maxRecent = 64
	}
	return &Coordinator{
		timeout: timeout, attempts: attempts, retryDelay: retryDelay, maxRecent: maxRecent,
		providers: map[string]Provider{},
	}
}

func (c *Coordinator) Register(provider Provider) {
	if c == nil || provider == nil {
		return
	}
	name := strings.TrimSpace(provider.Name())
	if name == "" {
		return
	}
	c.providersMu.Lock()
	c.providers[name] = provider
	c.providersMu.Unlock()
}

func (c *Coordinator) Dispatch(parent context.Context, message Message, providers map[string]bool) error {
	if c == nil {
		return errors.New("notification coordinator is unavailable")
	}
	if parent == nil {
		parent = context.Background()
	}
	c.lifecycleMu.RLock()
	if c.stopped {
		c.lifecycleMu.RUnlock()
		return errors.New("notification coordinator is stopped")
	}
	for name, enabled := range providers {
		if !enabled {
			continue
		}
		provider := c.provider(name)
		if provider == nil {
			c.record(Diagnostic{Provider: name, Event: string(message.Kind), RequestID: message.RequestID, CompletionID: message.CompletionID, WorkspaceID: message.WorkspaceID, Status: DiagnosticUnavailable, Timestamp: time.Now().UTC()})
			continue
		}
		if available, ok := provider.(Availability); ok && !available.Available() {
			c.record(Diagnostic{Provider: name, Event: string(message.Kind), RequestID: message.RequestID, CompletionID: message.CompletionID, WorkspaceID: message.WorkspaceID, Status: DiagnosticUnavailable, Timestamp: time.Now().UTC()})
			continue
		}
		c.wg.Add(1)
		go c.deliver(parent, provider, message)
	}
	c.lifecycleMu.RUnlock()
	return nil
}

func (c *Coordinator) Stop() {
	if c == nil {
		return
	}
	c.lifecycleMu.Lock()
	c.stopped = true
	c.lifecycleMu.Unlock()
	c.wg.Wait()
}

func (c *Coordinator) Diagnostics() []Diagnostic {
	if c == nil {
		return nil
	}
	c.diagnosticsMu.RLock()
	defer c.diagnosticsMu.RUnlock()
	result := make([]Diagnostic, len(c.diagnostics))
	copy(result, c.diagnostics)
	return result
}

func (c *Coordinator) provider(name string) Provider {
	c.providersMu.RLock()
	defer c.providersMu.RUnlock()
	return c.providers[strings.TrimSpace(name)]
}

func (c *Coordinator) deliver(parent context.Context, provider Provider, message Message) {
	defer c.wg.Done()
	started := time.Now()
	attempts := 0
	status := DiagnosticFailed
	for attempts < c.attempts {
		if parent.Err() != nil {
			status = DiagnosticCancelled
			break
		}
		attempts++
		ctx, cancel := context.WithTimeout(parent, c.timeout)
		err := provider.Notify(ctx, message)
		cancel()
		if err == nil {
			status = DiagnosticDelivered
			break
		}
		if parent.Err() != nil {
			status = DiagnosticCancelled
			break
		}
		if attempts >= c.attempts {
			break
		}
		timer := time.NewTimer(c.retryDelay)
		select {
		case <-parent.Done():
			timer.Stop()
			status = DiagnosticCancelled
			goto done
		case <-timer.C:
		}
	}

done:
	c.record(Diagnostic{
		Provider: provider.Name(), Event: string(message.Kind), RequestID: message.RequestID, CompletionID: message.CompletionID, WorkspaceID: message.WorkspaceID,
		Status: status, Attempts: attempts, DurationMS: time.Since(started).Milliseconds(), Timestamp: time.Now().UTC(),
	})
}

func (c *Coordinator) record(value Diagnostic) {
	c.diagnosticsMu.Lock()
	c.diagnostics = append(c.diagnostics, value)
	if overflow := len(c.diagnostics) - c.maxRecent; overflow > 0 {
		c.diagnostics = append([]Diagnostic(nil), c.diagnostics[overflow:]...)
	}
	c.diagnosticsMu.Unlock()
}
