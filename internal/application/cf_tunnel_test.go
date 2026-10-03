package application

import (
	"context"
	"errors"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/cftunnel"
)

type fakeCFTunnelManager struct {
	status        cftunnel.Status
	statusErr     error
	installResult cftunnel.InstallResult
	installErr    error
	calls         []string
}

func (manager *fakeCFTunnelManager) Status() (cftunnel.Status, error) {
	manager.calls = append(manager.calls, "status")
	return manager.status, manager.statusErr
}
func (manager *fakeCFTunnelManager) Probe(context.Context) (cftunnel.ProbeResult, error) {
	manager.calls = append(manager.calls, "probe")
	return cftunnel.ProbeResult{Status: manager.status, Version: "v0.0.1"}, nil
}
func (manager *fakeCFTunnelManager) Install(context.Context) (cftunnel.InstallResult, error) {
	manager.calls = append(manager.calls, "install")
	if manager.installResult.Status.Source == "" {
		manager.installResult = cftunnel.InstallResult{Status: manager.status, Installed: true}
	}
	return manager.installResult, manager.installErr
}

func TestCFTunnelEnsureAvailableMatrix(t *testing.T) {
	baseConfig := config.Default()
	tests := []struct {
		name          string
		cfg           config.Config
		status        cftunnel.Status
		statusErr     error
		installResult cftunnel.InstallResult
		installErr    error
		skip          bool
		wantState     string
		wantSource    string
		wantInstall   bool
		wantErr       bool
	}{
		{name: "ineligible telegram", cfg: func() config.Config { v := baseConfig; v.Telegram.Enabled = false; return v }(), wantState: "skipped"},
		{name: "ineligible mini app", cfg: func() config.Config { v := baseConfig; v.Telegram.LogsMiniApp.Enabled = false; return v }(), wantState: "skipped"},
		{name: "system", cfg: baseConfig, status: cftunnel.Status{Source: cftunnel.SourceSystem, Path: "/usr/bin/cf-tunnel", Verified: true, ManagedSupported: true}, wantState: "available", wantSource: "system"},
		{name: "managed existing", cfg: baseConfig, status: cftunnel.Status{Source: cftunnel.SourceManaged, Path: "/managed/cf-tunnel", Verified: true, ManagedSupported: true, ManagedInstalled: true}, wantState: "available", wantSource: "managed"},
		{name: "missing opt out", cfg: baseConfig, status: cftunnel.Status{Source: cftunnel.SourceUnavailable, ManagedSupported: true}, skip: true, wantState: "skipped", wantSource: "unavailable"},
		{name: "unsupported", cfg: baseConfig, status: cftunnel.Status{Source: cftunnel.SourceUnavailable, ManagedSupported: false}, wantState: "unavailable", wantSource: "unavailable"},
		{name: "managed install", cfg: baseConfig, status: cftunnel.Status{Source: cftunnel.SourceUnavailable, ManagedSupported: true}, installResult: cftunnel.InstallResult{Status: cftunnel.Status{Source: cftunnel.SourceManaged, Path: "/managed/cf-tunnel", Verified: true, ManagedSupported: true}, Installed: true}, wantState: "installed", wantSource: "managed", wantInstall: true},
		{name: "verification failure", cfg: baseConfig, status: cftunnel.Status{Source: cftunnel.SourceUnavailable, ManagedSupported: true}, statusErr: errors.New("managed asset checksum mismatch"), wantState: "failed", wantSource: "unavailable", wantErr: true},
		{name: "install failure", cfg: baseConfig, status: cftunnel.Status{Source: cftunnel.SourceUnavailable, ManagedSupported: true}, installErr: errors.New("download failed"), wantState: "failed", wantSource: "unavailable", wantInstall: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &fakeCFTunnelManager{status: test.status, statusErr: test.statusErr, installResult: test.installResult, installErr: test.installErr}
			service := NewCFTunnelServiceWithManager(manager)
			service.LoadConfig = func() (config.Config, error) { return test.cfg, nil }
			var events []IntegrationEnsureEvent
			result, err := service.EnsureAvailable(t.Context(), IntegrationEnsureOptions{SkipManagedInstall: test.skip, Observe: func(event IntegrationEnsureEvent) { events = append(events, event) }})
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v wantErr=%t result=%#v", err, test.wantErr, result)
			}
			if result.State != test.wantState || result.Source != test.wantSource {
				t.Fatalf("result=%#v", result)
			}
			installCalls := 0
			for _, call := range manager.calls {
				if call == "install" {
					installCalls++
				}
			}
			if (installCalls > 0) != test.wantInstall {
				t.Fatalf("calls=%v wantInstall=%t", manager.calls, test.wantInstall)
			}
			if (test.name == "ineligible telegram" || test.name == "ineligible mini app") && len(manager.calls) != 0 {
				t.Fatalf("ineligible cf-tunnel touched manager: %v", manager.calls)
			}
			if test.name == "managed install" {
				if len(events) != 3 || events[0].Phase != "check" || events[1].Phase != "install" || events[1].State != "running" || events[2].State != "installed" {
					t.Fatalf("events=%#v", events)
				}
			}
		})
	}
}

func TestCFTunnelEnsureEligibilityDoesNotRequireTelegramCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Telegram.Enabled = true
	cfg.Telegram.LogsMiniApp.Enabled = true
	cfg.Telegram.AllowedUserIDs = nil
	manager := &fakeCFTunnelManager{status: cftunnel.Status{Source: cftunnel.SourceSystem, Path: "/usr/bin/cf-tunnel", Verified: true, ManagedSupported: true}}
	service := NewCFTunnelServiceWithManager(manager)
	service.LoadConfig = func() (config.Config, error) { return cfg, nil }
	result, err := service.EnsureAvailable(t.Context())
	if err != nil || result.State != "available" || len(manager.calls) == 0 || manager.calls[0] != "status" {
		t.Fatalf("result=%#v err=%v calls=%v", result, err, manager.calls)
	}
}
func (manager *fakeCFTunnelManager) Update(context.Context) (cftunnel.InstallResult, error) {
	manager.calls = append(manager.calls, "update")
	return cftunnel.InstallResult{Status: manager.status, AlreadyInstalled: true}, nil
}
func (manager *fakeCFTunnelManager) Remove() (cftunnel.RemoveResult, error) {
	manager.calls = append(manager.calls, "remove")
	return cftunnel.RemoveResult{Status: manager.status, Removed: true}, nil
}
func (manager *fakeCFTunnelManager) ResolvePath() (string, error) {
	return "/managed/cf-tunnel", nil
}

func TestCFTunnelOperationsBindCanonicalOwner(t *testing.T) {
	manager := &fakeCFTunnelManager{status: cftunnel.Status{Source: cftunnel.SourceManaged, Version: "v0.0.1", Verified: true}}
	dispatcher := NewDispatcher()
	if err := BindCFTunnelOperations(dispatcher, NewCFTunnelServiceWithManager(manager)); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []capability.ID{capability.IntegrationCFStatus, capability.IntegrationCFProbe, capability.IntegrationCFInstall, capability.IntegrationCFUpdate, capability.IntegrationCFRemove} {
		if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: operation}); err != nil {
			t.Fatalf("dispatch %s: %v", operation, err)
		}
	}
	want := []string{"status", "probe", "install", "update", "remove"}
	if len(manager.calls) != len(want) {
		t.Fatalf("calls=%#v", manager.calls)
	}
	for index := range want {
		if manager.calls[index] != want[index] {
			t.Fatalf("calls=%#v want=%#v", manager.calls, want)
		}
	}
}
