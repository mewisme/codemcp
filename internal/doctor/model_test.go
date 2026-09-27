package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCollectorPreservesPartialResultsAndRedactsProviderErrors(t *testing.T) {
	healthy := ProviderFunc{
		Definition: ProviderSpec{ID: "config.overview", Domain: "config", Owner: "application.config", Probe: ProbeLocalRead},
		Run: func(context.Context) (Component, error) {
			return Component{State: StateHealthy, Severity: SeverityInfo, Summary: "configuration is readable"}, nil
		},
	}
	failing := ProviderFunc{
		Definition: ProviderSpec{ID: "storage.secrets", Domain: "storage", Owner: "secretstore", Probe: ProbeLocalRead},
		Run: func(context.Context) (Component, error) {
			return Component{}, errors.New("provider failed with raw-secret-value")
		},
	}
	collector, err := New(failing, healthy)
	if err != nil {
		t.Fatal(err)
	}
	report := collector.Collect(context.Background())
	if len(report.Components) != 2 || report.ProviderFailures != 1 || report.Healthy {
		t.Fatalf("report=%#v", report)
	}
	if report.Components[0].ID != "config.overview" || report.Components[1].ID != "storage.secrets" {
		t.Fatalf("components are not deterministically ordered: %#v", report.Components)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "raw-secret-value") {
		t.Fatalf("provider error leaked into diagnostic report: %s", data)
	}
	if report.Components[1].State != StateUnknown || report.Components[1].Summary != "diagnostic provider unavailable" {
		t.Fatalf("failure component=%#v", report.Components[1])
	}
}

func TestCollectorBoundsProviderTimeoutAndInvalidPayloads(t *testing.T) {
	timeoutProvider := ProviderFunc{
		Definition: ProviderSpec{ID: "upstream.health", Domain: "upstream", Owner: "upstream", Probe: ProbeBoundedNetworkRead, Timeout: 10 * time.Millisecond},
		Run: func(context.Context) (Component, error) {
			time.Sleep(100 * time.Millisecond)
			return Component{State: StateHealthy, Severity: SeverityInfo, Summary: "late result"}, nil
		},
	}
	invalidProvider := ProviderFunc{
		Definition: ProviderSpec{ID: "mcp.registry", Domain: "mcp", Owner: "mcp.registry", Probe: ProbeLocalRead},
		Run: func(context.Context) (Component, error) {
			return Component{State: StateHealthy, Severity: SeverityInfo, Summary: strings.Repeat("x", MaxSummaryRunes+1)}, nil
		},
	}
	collector, err := New(timeoutProvider, invalidProvider)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	report := collector.Collect(context.Background())
	if elapsed := time.Since(started); elapsed >= 80*time.Millisecond {
		t.Fatalf("collector did not enforce provider timeout: %s", elapsed)
	}
	if report.ProviderFailures != 2 || len(report.Components) != 2 {
		t.Fatalf("report=%#v", report)
	}
	byID := map[ComponentID]Component{}
	for _, component := range report.Components {
		byID[component.ID] = component
	}
	if byID["upstream.health"].Summary != "diagnostic provider timed out" {
		t.Fatalf("timeout component=%#v", byID["upstream.health"])
	}
	if byID["mcp.registry"].Summary != "diagnostic provider returned an invalid result" {
		t.Fatalf("invalid component=%#v", byID["mcp.registry"])
	}
}

func TestCollectorSupportsIsolatedConfiguredAndDegradedStates(t *testing.T) {
	tests := []struct {
		name     string
		state    State
		severity Severity
		healthy  bool
	}{
		{name: "isolated", state: StateDisabled, severity: SeverityInfo, healthy: true},
		{name: "configured", state: StateHealthy, severity: SeverityInfo, healthy: true},
		{name: "degraded", state: StateDegraded, severity: SeverityWarning, healthy: false},
		{name: "uninitialized", state: StateUnavailable, severity: SeverityWarning, healthy: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			collector, err := New(ProviderFunc{
				Definition: ProviderSpec{ID: "config.overview", Domain: "config", Owner: "application.config", Probe: ProbeLocalRead},
				Run: func(context.Context) (Component, error) {
					return Component{State: test.state, Severity: test.severity, Summary: "bounded diagnostic state"}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if report := collector.Collect(context.Background()); report.Healthy != test.healthy {
				t.Fatalf("report=%#v", report)
			}
		})
	}
}

func TestCollectorRecoversProviderPanicWithoutLosingOtherDiagnostics(t *testing.T) {
	collector, err := New(
		ProviderFunc{
			Definition: ProviderSpec{ID: "approval.lifecycle", Domain: "approval", Owner: "approval", Probe: ProbeLocalRead},
			Run:        func(context.Context) (Component, error) { panic("raw-secret-panic") },
		},
		ProviderFunc{
			Definition: ProviderSpec{ID: "background.delivery", Domain: "background", Owner: "backgrounddelivery", Probe: ProbeLocalRead},
			Run: func(context.Context) (Component, error) {
				return Component{State: StateHealthy, Severity: SeverityInfo, Summary: "background delivery is readable"}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	report := collector.Collect(context.Background())
	if len(report.Components) != 2 || report.ProviderFailures != 1 {
		t.Fatalf("report=%#v", report)
	}
	data, _ := json.Marshal(report)
	if strings.Contains(string(data), "raw-secret-panic") {
		t.Fatalf("panic payload leaked: %s", data)
	}
}
