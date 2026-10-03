package application

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/integrations/cftunnel"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/integrations/rtk"
)

type failRoundTripper struct {
	calls int
}

func (transport *failRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, errors.New("network must not be used")
}

func TestIntegrationEnsureAvailableUsesResolvedExecutableWithoutManagedDownload(t *testing.T) {
	root := t.TempDir()
	rtkPath := filepath.Join(root, "rtk")
	codeGraphPath := filepath.Join(root, "codegraph")
	for _, path := range []string{rtkPath, codeGraphPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Integrations.RTK.Path = rtkPath
	cfg.Integrations.CodeGraph.Path = codeGraphPath
	transport := &failRoundTripper{}
	client := &http.Client{Transport: transport}

	rtkResult, err := (&RTKService{
		LoadConfig:  func() (config.Config, error) { return cfg, nil },
		ManagedRoot: filepath.Join(root, "managed-rtk"),
		HTTPClient:  client,
	}).EnsureAvailable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rtkResult.State != "available" || rtkResult.Source != "configured" {
		t.Fatalf("rtk result=%#v", rtkResult)
	}

	codeGraphResult, err := (&CodeGraphService{
		LoadConfig:  func() (config.Config, error) { return cfg, nil },
		ManagedRoot: filepath.Join(root, "managed-codegraph"),
		HTTPClient:  client,
	}).EnsureAvailable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if codeGraphResult.State != "available" || codeGraphResult.Source != "configured" {
		t.Fatalf("codegraph result=%#v", codeGraphResult)
	}
	if transport.calls != 0 {
		t.Fatalf("managed download attempted %d time(s)", transport.calls)
	}
}

func TestResolveInstallCurrentOptionsEnvironmentPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		options  InstallCurrentOptions
		raw      string
		present  bool
		wantSkip bool
		wantErr  bool
	}{
		{name: "default", present: false, wantSkip: false},
		{name: "env true", raw: " YES ", present: true, wantSkip: false},
		{name: "env false", raw: " OFF ", present: true, wantSkip: true},
		{name: "explicit skip wins true env", options: InstallCurrentOptions{SkipMissingIntegrations: true}, raw: "on", present: true, wantSkip: true},
		{name: "explicit skip wins invalid env", options: InstallCurrentOptions{SkipMissingIntegrations: true}, raw: "invalid", present: true, wantSkip: true},
		{name: "invalid env", raw: "invalid", present: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveInstallCurrentOptionsWithLookup(test.options, func(key string) (string, bool) {
				if key != InstallIntegrationsEnv || !test.present {
					return "", false
				}
				return test.raw, true
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v wantErr=%t", err, test.wantErr)
			}
			if test.wantErr {
				if !strings.Contains(err.Error(), InstallIntegrationsEnv) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if got.SkipMissingIntegrations != test.wantSkip {
				t.Fatalf("options=%#v", got)
			}
		})
	}
}

func TestInstallCurrentContextRejectsInvalidIntegrationEnvBeforeInstall(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	t.Setenv(InstallIntegrationsEnv, "maybe")
	_, err := InstallCurrentContext(t.Context(), InstallCurrentOptions{})
	if err == nil || !strings.Contains(err.Error(), InstallIntegrationsEnv) {
		t.Fatalf("err=%v", err)
	}
}

type fakeRTKEnsureManager struct {
	status        rtk.Status
	statusErr     error
	installResult rtk.InstallResult
	installErr    error
	installCalls  int
}

func (manager *fakeRTKEnsureManager) Status() (rtk.Status, error) {
	return manager.status, manager.statusErr
}
func (manager *fakeRTKEnsureManager) Install(context.Context) (rtk.InstallResult, error) {
	manager.installCalls++
	return manager.installResult, manager.installErr
}

type fakeCodeGraphEnsureRuntime struct {
	status        codegraph.Status
	statusErr     error
	installResult codegraph.InstallResult
	installErr    error
	installCalls  int
}

func (runtime *fakeCodeGraphEnsureRuntime) Status() (codegraph.Status, error) {
	return runtime.status, runtime.statusErr
}
func (runtime *fakeCodeGraphEnsureRuntime) Install(context.Context) (codegraph.InstallResult, error) {
	runtime.installCalls++
	return runtime.installResult, runtime.installErr
}

func TestRTKEnsureAvailableMatrix(t *testing.T) {
	tests := []struct {
		name          string
		status        rtk.Status
		statusErr     error
		installResult rtk.InstallResult
		installErr    error
		skip          bool
		wantState     string
		wantSource    string
		wantInstall   bool
		wantErr       bool
	}{
		{name: "disabled", status: rtk.Status{Enabled: false, Source: rtk.SourceDisabled}, wantState: "skipped", wantSource: "disabled"},
		{name: "configured", status: rtk.Status{Enabled: true, Source: rtk.SourceConfigured, Path: "/configured/rtk", Verified: true, ManagedSupported: true}, wantState: "available", wantSource: "configured"},
		{name: "system", status: rtk.Status{Enabled: true, Source: rtk.SourceSystem, Path: "/usr/bin/rtk", Verified: true, ManagedSupported: true}, wantState: "available", wantSource: "system"},
		{name: "managed existing", status: rtk.Status{Enabled: true, Source: rtk.SourceManaged, Path: "/managed/rtk", Verified: true, ManagedSupported: true, ManagedInstalled: true}, wantState: "available", wantSource: "managed"},
		{name: "missing opt out", status: rtk.Status{Enabled: true, Source: rtk.SourceUnavailable, ManagedSupported: true}, skip: true, wantState: "skipped", wantSource: "unavailable"},
		{name: "unsupported", status: rtk.Status{Enabled: true, Source: rtk.SourceUnavailable, ManagedSupported: false}, wantState: "unavailable", wantSource: "unavailable"},
		{name: "managed install", status: rtk.Status{Enabled: true, Source: rtk.SourceUnavailable, ManagedSupported: true}, installResult: rtk.InstallResult{Status: rtk.Status{Enabled: true, Source: rtk.SourceManaged, Path: "/managed/rtk", Verified: true, ManagedSupported: true}, Installed: true}, wantState: "installed", wantSource: "managed", wantInstall: true},
		{name: "verification failure", status: rtk.Status{Enabled: true, Source: rtk.SourceUnavailable, ManagedSupported: true}, statusErr: errors.New("managed asset verification failed"), wantState: "failed", wantSource: "unavailable", wantErr: true},
		{name: "install failure", status: rtk.Status{Enabled: true, Source: rtk.SourceUnavailable, ManagedSupported: true}, installErr: errors.New("download failed"), wantState: "failed", wantSource: "unavailable", wantInstall: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &fakeRTKEnsureManager{status: test.status, statusErr: test.statusErr, installResult: test.installResult, installErr: test.installErr}
			service := &RTKService{ensureManager: func() (rtkEnsureManager, error) { return manager, nil }}
			var events []IntegrationEnsureEvent
			result, err := service.EnsureAvailable(t.Context(), IntegrationEnsureOptions{SkipManagedInstall: test.skip, Observe: func(event IntegrationEnsureEvent) { events = append(events, event) }})
			if (err != nil) != test.wantErr || result.State != test.wantState || result.Source != test.wantSource {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if (manager.installCalls > 0) != test.wantInstall {
				t.Fatalf("installCalls=%d wantInstall=%t", manager.installCalls, test.wantInstall)
			}
			if test.name == "managed install" && (len(events) != 3 || events[1].Phase != "install" || events[2].State != "installed") {
				t.Fatalf("events=%#v", events)
			}
		})
	}
}

func TestCodeGraphEnsureAvailableMatrix(t *testing.T) {
	status := func(enabled bool, source codegraph.ExecutableState, path string, verified, supported bool) codegraph.Status {
		return codegraph.Status{Enabled: enabled, Resolution: codegraph.Resolution{Source: source, Path: path, Verified: verified}, ManagedSupported: supported}
	}
	tests := []struct {
		name          string
		status        codegraph.Status
		statusErr     error
		installResult codegraph.InstallResult
		installErr    error
		skip          bool
		wantState     string
		wantSource    string
		wantInstall   bool
		wantErr       bool
	}{
		{name: "disabled", status: status(false, codegraph.ExecutableDisabled, "", false, false), wantState: "skipped", wantSource: "disabled"},
		{name: "configured", status: status(true, codegraph.ExecutableConfigured, "/configured/codegraph", true, true), wantState: "available", wantSource: "configured"},
		{name: "system", status: status(true, codegraph.ExecutableSystem, "/usr/bin/codegraph", true, true), wantState: "available", wantSource: "system"},
		{name: "managed existing", status: status(true, codegraph.ExecutableManaged, "/managed/codegraph", true, true), wantState: "available", wantSource: "managed"},
		{name: "missing opt out", status: status(true, codegraph.ExecutableUnavailable, "", false, true), skip: true, wantState: "skipped", wantSource: "unavailable"},
		{name: "unsupported", status: status(true, codegraph.ExecutableUnavailable, "", false, false), wantState: "unavailable", wantSource: "unavailable"},
		{name: "managed install", status: status(true, codegraph.ExecutableUnavailable, "", false, true), installResult: codegraph.InstallResult{Status: status(true, codegraph.ExecutableManaged, "/managed/codegraph", true, true), Installed: true}, wantState: "installed", wantSource: "managed", wantInstall: true},
		{name: "verification failure", status: status(true, codegraph.ExecutableUnavailable, "", false, true), statusErr: errors.New("managed asset verification failed"), wantState: "failed", wantSource: "unavailable", wantErr: true},
		{name: "install failure", status: status(true, codegraph.ExecutableUnavailable, "", false, true), installErr: errors.New("download failed"), wantState: "failed", wantSource: "unavailable", wantInstall: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &fakeCodeGraphEnsureRuntime{status: test.status, statusErr: test.statusErr, installResult: test.installResult, installErr: test.installErr}
			service := &CodeGraphService{ensureRuntime: func() (codeGraphEnsureRuntime, error) { return runtime, nil }}
			var events []IntegrationEnsureEvent
			result, err := service.EnsureAvailable(t.Context(), IntegrationEnsureOptions{SkipManagedInstall: test.skip, Observe: func(event IntegrationEnsureEvent) { events = append(events, event) }})
			if (err != nil) != test.wantErr || result.State != test.wantState || result.Source != test.wantSource {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if (runtime.installCalls > 0) != test.wantInstall {
				t.Fatalf("installCalls=%d wantInstall=%t", runtime.installCalls, test.wantInstall)
			}
			if test.name == "managed install" && (len(events) != 3 || events[1].Phase != "install" || events[2].State != "installed") {
				t.Fatalf("events=%#v", events)
			}
		})
	}
}

func TestPostInstallCoordinatorIncludesCFTunnelAndKeepsFailuresSupplemental(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	t.Setenv(config.TelemetryEnv, "0")
	cfg := config.Default()
	cfg.Telemetry.Enabled = false

	rtkManager := &fakeRTKEnsureManager{status: rtk.Status{Enabled: true, Source: rtk.SourceSystem, Path: "/usr/bin/rtk", Verified: true, ManagedSupported: true}}
	codeGraphRuntime := &fakeCodeGraphEnsureRuntime{status: codegraph.Status{
		Enabled: true, ManagedSupported: true,
		Resolution: codegraph.Resolution{Source: codegraph.ExecutableSystem, Path: "/usr/bin/codegraph", Verified: true},
	}}
	cfManager := &fakeCFTunnelManager{
		status:     cftunnelStatusUnavailableSupported(),
		installErr: errors.New("cf download failed"),
	}
	cfService := NewCFTunnelServiceWithManager(cfManager)
	cfService.LoadConfig = func() (config.Config, error) { return cfg, nil }

	coordinator := &postInstallCoordinator{
		Telemetry: &TelemetryService{
			LoadConfig: func(context.Context) (config.Config, error) { return cfg, nil },
			Source:     func() (configformat.Source, error) { return configformat.Source{}, nil },
			Endpoint:   func() string { return "https://telemetry.invalid/v1/products/codemcp/events" },
		},
		RTK:       &RTKService{ensureManager: func() (rtkEnsureManager, error) { return rtkManager, nil }},
		CodeGraph: &CodeGraphService{ensureRuntime: func() (codeGraphEnsureRuntime, error) { return codeGraphRuntime, nil }},
		CFTunnel:  cfService,
	}
	result := coordinator.Run(t.Context(), PostInstallBootstrapOptions{})
	if len(result.Integrations) != 3 {
		t.Fatalf("integrations=%#v", result.Integrations)
	}
	if result.Integrations[0].Integration != "rtk" || result.Integrations[1].Integration != "codegraph" || result.Integrations[2].Integration != "cf-tunnel" {
		t.Fatalf("integration order=%#v", result.Integrations)
	}
	if result.Integrations[2].State != "failed" || len(result.Warnings) == 0 {
		t.Fatalf("cf outcome=%#v warnings=%#v", result.Integrations[2], result.Warnings)
	}
}

func cftunnelStatusUnavailableSupported() cftunnel.Status {
	return cftunnel.Status{Source: cftunnel.SourceUnavailable, ManagedSupported: true}
}
