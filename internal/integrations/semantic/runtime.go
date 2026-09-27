package semantic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	DefaultOperationTimeout = 3 * time.Second
	DefaultMaxAttempts      = 3
	DefaultRetryBaseDelay   = 25 * time.Millisecond
)

type ManagerOptions struct {
	Timeout        time.Duration
	MaxAttempts    int
	RetryBaseDelay time.Duration
	Now            func() time.Time
	Sleep          func(context.Context, time.Duration) error
}

type ProviderRegistration struct {
	Provider       Provider
	RiskClassifier RiskClassifier
	Model          string
}

type Health struct {
	Available         bool
	Provider          string
	Model             string
	LastAttemptAt     time.Time
	LastSuccessAt     time.Time
	LastErrorCategory ErrorCategory
	Reuse             ReuseDiagnostics
}

type Manager struct {
	mu        sync.RWMutex
	providers map[string]*managedProvider
	selected  string
	health    Health
	observer  tracepkg.Observer
	options   ManagerOptions
}

func NewManager(options ManagerOptions) *Manager {
	options = normalizeManagerOptions(options)
	return &Manager{
		providers: make(map[string]*managedProvider),
		health:    Health{LastErrorCategory: ErrorDisabled},
		options:   options,
	}
}

func (m *Manager) RegisterProvider(name string, registration ProviderRegistration) error {
	if m == nil {
		return errors.New("semantic manager is nil")
	}
	name = strings.TrimSpace(name)
	if err := validateSafeMetadata("provider", name, MaxProviderIDBytes); err != nil {
		return err
	}
	if registration.Provider == nil && registration.RiskClassifier == nil {
		return errors.New("semantic provider registration has no capabilities")
	}
	model := strings.TrimSpace(registration.Model)
	if model != "" {
		if err := validateSafeMetadata("model", model, MaxModelIDBytes); err != nil {
			return err
		}
	}
	managed := newManagedProvider(name, model, registration, m.options)
	m.mu.Lock()
	previous := m.providers[name]
	m.providers[name] = managed
	if m.selected == name {
		m.health = Health{Available: true, Provider: name, Model: model}
	}
	m.mu.Unlock()
	if previous != nil {
		previous.invalidate()
	}
	return nil
}

func (m *Manager) SelectProvider(name string) error {
	if m == nil {
		return errors.New("semantic manager is nil")
	}
	name = strings.TrimSpace(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	provider, ok := m.providers[name]
	if !ok {
		return fmt.Errorf("semantic provider %q is unavailable", name)
	}
	m.selected = name
	m.health = Health{Available: true, Provider: name, Model: provider.model}
	return nil
}

func (m *Manager) SetUnavailable(category ErrorCategory) {
	if m == nil {
		return
	}
	if category == "" {
		category = ErrorDisabled
	}
	m.mu.Lock()
	previous := m.providers[m.selected]
	m.selected = ""
	m.health = Health{LastErrorCategory: category}
	m.mu.Unlock()
	if previous != nil {
		previous.invalidate()
	}
}

func (m *Manager) Evaluate(ctx context.Context, request Request) (Result, error) {
	started := time.Now()
	provider, observer, err := m.selectedProvider()
	if err != nil {
		emitEvaluation(ctx, observer, request, "", Result{}, err, time.Since(started))
		return Result{}, err
	}
	result, err := provider.evaluate(ctx, request)
	m.record(provider, result, err)
	emitEvaluation(ctx, observer, request, provider.name, result, err, time.Since(started))
	return result, err
}

// EvaluateOptional is for ranking/search-style consumers whose deterministic
// native result remains authoritative when semantic evaluation is unavailable.
func (m *Manager) EvaluateOptional(ctx context.Context, request Request, native func() Result) (Result, bool) {
	result, err := m.Evaluate(ctx, request)
	if err == nil {
		emitConsumerOutcome(ctx, m.traceObserver(), request.Consumer.ID, false, "", "")
		return result, true
	}
	fallback := Result{}
	if native != nil {
		fallback = native()
	}
	emitConsumerOutcome(ctx, m.traceObserver(), request.Consumer.ID, true, fallbackReasonForError(err), semanticErrorCategory(err))
	return fallback, false
}

// ClassifyRisk deliberately has no native-allow fallback and no cache. Callers
// must interpret any error through their own canonical approval policy.
func (m *Manager) ClassifyRisk(ctx context.Context, input RiskInput, minConfidence float64) (RiskAssessment, error) {
	started := time.Now()
	provider, observer, err := m.selectedProvider()
	if err != nil {
		emitRiskClassification(ctx, observer, input.Consumer.ID, "", RiskAssessment{}, err, time.Since(started))
		return RiskAssessment{}, err
	}
	assessment, err := provider.classifyRisk(ctx, input, minConfidence)
	m.recordRisk(provider, assessment, err)
	emitRiskClassification(ctx, observer, input.Consumer.ID, provider.name, assessment, err, time.Since(started))
	return assessment, err
}

func (m *Manager) Health() Health {
	if m == nil {
		return Health{LastErrorCategory: ErrorDisabled}
	}
	m.mu.RLock()
	health := m.health
	provider := m.providers[m.selected]
	m.mu.RUnlock()
	if provider != nil {
		health.Reuse = provider.diagnostics()
	}
	return health
}

func (m *Manager) SetTraceObserver(observer tracepkg.Observer) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.observer = observer
	m.mu.Unlock()
}

func (m *Manager) traceObserver() tracepkg.Observer {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.observer
}

func (m *Manager) selectedProvider() (*managedProvider, tracepkg.Observer, error) {
	if m == nil {
		return nil, nil, NewError(ErrorDisabled, "")
	}
	m.mu.RLock()
	selected, observer := m.selected, m.observer
	provider := m.providers[selected]
	healthCategory := m.health.LastErrorCategory
	m.mu.RUnlock()
	if provider == nil {
		if healthCategory == "" {
			healthCategory = ErrorDisabled
		}
		return nil, observer, NewError(healthCategory, "")
	}
	return provider, observer, nil
}

func (m *Manager) record(provider *managedProvider, result Result, err error) {
	if m == nil || provider == nil {
		return
	}
	now := m.options.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.providers[m.selected] != provider {
		return
	}
	m.health.LastAttemptAt = now
	if err == nil {
		m.health.LastSuccessAt = now
		m.health.LastErrorCategory = ""
	} else {
		m.health.LastErrorCategory = semanticErrorCategory(err)
	}
}

func (m *Manager) recordRisk(provider *managedProvider, _ RiskAssessment, err error) {
	m.record(provider, Result{}, err)
}

func normalizeManagerOptions(options ManagerOptions) ManagerOptions {
	if options.Timeout <= 0 {
		options.Timeout = DefaultOperationTimeout
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = DefaultMaxAttempts
	}
	if options.MaxAttempts > 5 {
		options.MaxAttempts = 5
	}
	if options.RetryBaseDelay <= 0 {
		options.RetryBaseDelay = DefaultRetryBaseDelay
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Sleep == nil {
		options.Sleep = sleepContext
	}
	return options
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return contextSemanticError(ctx)
	}
}
