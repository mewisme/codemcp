package notification

import (
	"sort"
	"strings"
	"time"
)

type ProviderHealth string

const (
	ProviderHealthHealthy     ProviderHealth = "healthy"
	ProviderHealthDegraded    ProviderHealth = "degraded"
	ProviderHealthUnavailable ProviderHealth = "unavailable"
	ProviderHealthStopped     ProviderHealth = "stopped"
)

type ProviderStatus struct {
	Provider   string           `json:"provider"`
	Enabled    bool             `json:"enabled"`
	Registered bool             `json:"registered"`
	Available  bool             `json:"available"`
	Health     ProviderHealth   `json:"health"`
	LastStatus DiagnosticStatus `json:"last_status,omitempty"`
	LastEvent  string           `json:"last_event,omitempty"`
	LastError  string           `json:"last_error,omitempty"`
	UpdatedAt  time.Time        `json:"updated_at,omitempty"`
}

type StatusSnapshot struct {
	Stopped   bool             `json:"stopped"`
	Providers []ProviderStatus `json:"providers"`
}

func (c *Coordinator) Status(enabled map[string]bool) StatusSnapshot {
	if c == nil {
		return StatusSnapshot{}
	}
	c.lifecycleMu.RLock()
	stopped := c.stopped
	c.lifecycleMu.RUnlock()

	names := map[string]bool{}
	for name := range enabled {
		name = strings.TrimSpace(name)
		if name != "" {
			names[name] = true
		}
	}
	c.providersMu.RLock()
	providers := make(map[string]Provider, len(c.providers))
	for name, provider := range c.providers {
		names[name] = true
		providers[name] = provider
	}
	c.providersMu.RUnlock()

	c.diagnosticsMu.RLock()
	last := make(map[string]Diagnostic, len(names))
	for _, diagnostic := range c.diagnostics {
		last[diagnostic.Provider] = diagnostic
	}
	c.diagnosticsMu.RUnlock()

	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	result := StatusSnapshot{Stopped: stopped, Providers: make([]ProviderStatus, 0, len(ordered))}
	for _, name := range ordered {
		provider := providers[name]
		status := ProviderStatus{
			Provider:   name,
			Enabled:    enabled[name],
			Registered: provider != nil,
		}
		if provider != nil {
			status.Available = true
			if available, ok := provider.(Availability); ok {
				status.Available = available.Available()
			}
		}
		if stopped {
			status.Health = ProviderHealthStopped
		} else if !status.Registered || !status.Available {
			status.Health = ProviderHealthUnavailable
			status.LastError = "notification provider unavailable"
		} else {
			status.Health = ProviderHealthHealthy
		}
		if diagnostic, ok := last[name]; ok {
			status.LastStatus = diagnostic.Status
			status.LastEvent = diagnostic.Event
			status.UpdatedAt = diagnostic.Timestamp
			switch diagnostic.Status {
			case DiagnosticFailed:
				status.Health = ProviderHealthDegraded
				status.LastError = "notification delivery failed"
			case DiagnosticUnavailable:
				status.Health = ProviderHealthUnavailable
				status.LastError = "notification provider unavailable"
			case DiagnosticCancelled:
				status.Health = ProviderHealthDegraded
				status.LastError = "notification delivery cancelled"
			case DiagnosticDelivered:
				if !stopped && status.Available {
					status.Health = ProviderHealthHealthy
					status.LastError = ""
				}
			}
		}
		result.Providers = append(result.Providers, status)
	}
	return result
}
