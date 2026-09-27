package doctor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxSummaryRunes            = 240
	MaxRemediations            = 4
	MaxRemediationSummaryRunes = 160
	MaxMetrics                 = 24
	MaxFlags                   = 24
	defaultProviderTimeout     = 2 * time.Second
	maxProviderTimeout         = 15 * time.Second
)

type ComponentID string

type State string

const (
	StateHealthy     State = "healthy"
	StateDegraded    State = "degraded"
	StateUnavailable State = "unavailable"
	StateDisabled    State = "disabled"
	StateUnknown     State = "unknown"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type ProbeKind string

const (
	ProbeLocalRead          ProbeKind = "local_read"
	ProbeBoundedNetworkRead ProbeKind = "bounded_network_read"
)

type Metric struct {
	ID    string `json:"id"`
	Value int64  `json:"value"`
}

type Flag struct {
	ID    string `json:"id"`
	Value bool   `json:"value"`
}

type Remediation struct {
	ID        string `json:"id"`
	Summary   string `json:"summary"`
	Operation string `json:"operation,omitempty"`
}

type Component struct {
	ID           ComponentID   `json:"id"`
	Domain       string        `json:"domain"`
	Probe        ProbeKind     `json:"probe"`
	State        State         `json:"state"`
	Severity     Severity      `json:"severity"`
	Summary      string        `json:"summary"`
	Metrics      []Metric      `json:"metrics,omitempty"`
	Flags        []Flag        `json:"flags,omitempty"`
	Remediations []Remediation `json:"remediations,omitempty"`
}

type ProviderSpec struct {
	ID      ComponentID
	Domain  string
	Owner   string
	Probe   ProbeKind
	Timeout time.Duration
}

type Provider interface {
	Spec() ProviderSpec
	Diagnose(context.Context) (Component, error)
}

type ProviderFunc struct {
	Definition ProviderSpec
	Run        func(context.Context) (Component, error)
}

func (p ProviderFunc) Spec() ProviderSpec { return p.Definition }

func (p ProviderFunc) Diagnose(ctx context.Context) (Component, error) {
	if p.Run == nil {
		return Component{}, errors.New("diagnostic provider is unavailable")
	}
	return p.Run(ctx)
}

type Report struct {
	Components       []Component `json:"components"`
	Healthy          bool        `json:"healthy"`
	Warnings         int         `json:"warnings"`
	Errors           int         `json:"errors"`
	ProviderFailures int         `json:"provider_failures"`
}

type Collector struct {
	providers []Provider
}

func New(providers ...Provider) (*Collector, error) {
	copied := append([]Provider(nil), providers...)
	seen := map[ComponentID]struct{}{}
	for _, provider := range copied {
		if provider == nil {
			return nil, errors.New("diagnostic provider is nil")
		}
		spec := normalizedSpec(provider.Spec())
		if err := validateSpec(spec); err != nil {
			return nil, err
		}
		if _, ok := seen[spec.ID]; ok {
			return nil, fmt.Errorf("duplicate diagnostic provider %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}
	}
	sort.Slice(copied, func(i, j int) bool {
		return normalizedSpec(copied[i].Spec()).ID < normalizedSpec(copied[j].Spec()).ID
	})
	return &Collector{providers: copied}, nil
}

func (c *Collector) Collect(ctx context.Context) Report {
	if ctx == nil {
		ctx = context.Background()
	}
	report := Report{Healthy: true, Components: []Component{}}
	if c == nil {
		return report
	}
	for _, provider := range c.providers {
		spec := normalizedSpec(provider.Spec())
		component, failed := collectProvider(ctx, provider, spec)
		report.Components = append(report.Components, component)
		if failed {
			report.ProviderFailures++
		}
		switch component.Severity {
		case SeverityWarning:
			report.Warnings++
			report.Healthy = false
		case SeverityError:
			report.Errors++
			report.Healthy = false
		}
		switch component.State {
		case StateDegraded, StateUnavailable, StateUnknown:
			report.Healthy = false
		}
	}
	return report
}

func collectProvider(parent context.Context, provider Provider, spec ProviderSpec) (component Component, failed bool) {
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultProviderTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	type outcome struct {
		value Component
		err   error
		panic bool
	}
	outcomes := make(chan outcome, 1)
	go func() {
		result := outcome{}
		defer func() {
			if recover() != nil {
				result.panic = true
			}
			outcomes <- result
		}()
		result.value, result.err = provider.Diagnose(ctx)
	}()

	var result outcome
	select {
	case result = <-outcomes:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return providerFailure(spec, "diagnostic provider timed out"), true
		}
		return providerFailure(spec, "diagnostic provider unavailable"), true
	}
	if result.panic {
		return providerFailure(spec, "diagnostic provider failed"), true
	}
	if result.err != nil {
		return providerFailure(spec, "diagnostic provider unavailable"), true
	}
	value := result.value
	value.ID = spec.ID
	value.Domain = spec.Domain
	value.Probe = spec.Probe
	if err := validateComponent(value); err != nil {
		return providerFailure(spec, "diagnostic provider returned an invalid result"), true
	}
	return value, false
}

func providerFailure(spec ProviderSpec, summary string) Component {
	return Component{
		ID:       spec.ID,
		Domain:   spec.Domain,
		Probe:    spec.Probe,
		State:    StateUnknown,
		Severity: SeverityWarning,
		Summary:  summary,
	}
}

func normalizedSpec(spec ProviderSpec) ProviderSpec {
	spec.ID = ComponentID(strings.TrimSpace(string(spec.ID)))
	spec.Domain = strings.TrimSpace(spec.Domain)
	spec.Owner = strings.TrimSpace(spec.Owner)
	if spec.Timeout <= 0 {
		spec.Timeout = defaultProviderTimeout
	}
	return spec
}

func validateSpec(spec ProviderSpec) error {
	if !validIdentifier(string(spec.ID), 80) {
		return fmt.Errorf("invalid diagnostic component id %q", spec.ID)
	}
	if !validIdentifier(spec.Domain, 48) {
		return fmt.Errorf("invalid diagnostic domain %q", spec.Domain)
	}
	if !validIdentifier(spec.Owner, 80) {
		return fmt.Errorf("invalid diagnostic owner %q", spec.Owner)
	}
	switch spec.Probe {
	case ProbeLocalRead, ProbeBoundedNetworkRead:
	default:
		return fmt.Errorf("invalid diagnostic probe kind %q", spec.Probe)
	}
	if spec.Timeout <= 0 || spec.Timeout > maxProviderTimeout {
		return fmt.Errorf("diagnostic provider %q timeout must be within 0-%s", spec.ID, maxProviderTimeout)
	}
	return nil
}

func validateComponent(component Component) error {
	switch component.State {
	case StateHealthy, StateDegraded, StateUnavailable, StateDisabled, StateUnknown:
	default:
		return fmt.Errorf("invalid diagnostic state %q", component.State)
	}
	switch component.Severity {
	case SeverityInfo, SeverityWarning, SeverityError:
	default:
		return fmt.Errorf("invalid diagnostic severity %q", component.Severity)
	}
	if strings.TrimSpace(component.Summary) == "" || utf8.RuneCountInString(component.Summary) > MaxSummaryRunes {
		return errors.New("diagnostic summary is empty or exceeds the size limit")
	}
	if len(component.Remediations) > MaxRemediations {
		return errors.New("diagnostic remediation list exceeds the size limit")
	}
	if len(component.Metrics) > MaxMetrics || len(component.Flags) > MaxFlags {
		return errors.New("diagnostic metadata exceeds the size limit")
	}
	if err := validateMetrics(component.Metrics); err != nil {
		return err
	}
	if err := validateFlags(component.Flags); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, remediation := range component.Remediations {
		id := strings.TrimSpace(remediation.ID)
		if !validIdentifier(id, 80) {
			return fmt.Errorf("invalid remediation id %q", remediation.ID)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate remediation id %q", id)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(remediation.Summary) == "" || utf8.RuneCountInString(remediation.Summary) > MaxRemediationSummaryRunes {
			return fmt.Errorf("remediation %q summary is empty or exceeds the size limit", id)
		}
		if operation := strings.TrimSpace(remediation.Operation); operation != "" && !validIdentifier(operation, 96) {
			return fmt.Errorf("invalid remediation operation %q", remediation.Operation)
		}
	}
	return nil
}

func validateMetrics(values []Metric) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		id := strings.TrimSpace(value.ID)
		if !validIdentifier(id, 80) {
			return fmt.Errorf("invalid diagnostic metric id %q", value.ID)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate diagnostic metric id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateFlags(values []Flag) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		id := strings.TrimSpace(value.ID)
		if !validIdentifier(id, 80) {
			return fmt.Errorf("invalid diagnostic flag id %q", value.ID)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate diagnostic flag id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validIdentifier(value string, max int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
