package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	updatepkg "go.mewis.me/codemcp/internal/update"
)

func TestStatusReportsManagedRuntime(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute).UTC()
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_status", Managed: true, ServiceID: "cm-system-test", ServiceScope: "system", StartedAt: started, Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_status", Managed: true, ServiceID: "cm-system-test", ServiceScope: "system", StartedAt: started, ConfigRoot: root, ServerPort: cfg.Server.Port, AdminEnabled: cfg.Admin.Enabled, AdminPort: cfg.Admin.Port, Exposure: cfg.Server.Expose.Mode, TunnelEnabled: true, TunnelConfigured: true, TunnelReady: true, TunnelID: "tunnel_status"}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"✓ CodeMCP is running", "Runtime", "session     run_status", "managed     system ·", "service     cm-system-test", "Endpoints", "Config", "auth        mcp off · admin off", "Tunnel", "✓ OpenAI Secure MCP Tunnel is connected", "id          tunnel_status"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("status missing %q: %s", expected, text)
		}
	}
	if strings.Index(text, "Config") > strings.Index(text, "Tunnel") {
		t.Fatalf("tunnel should render after the core status sections: %s", text)
	}
	for _, unexpected := range []string{"initialized:", "format:", "mcp local:"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("status unexpectedly contains %q: %s", unexpected, text)
		}
	}
}

func TestStatusReportsStartingRuntime(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_starting", Managed: true, ServiceID: "cm-user-test", ServiceScope: "user", StartedAt: time.Now().UTC(), Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_starting", Starting: true, Managed: true, ServiceID: "cm-user-test", ServiceScope: "user", ConfigRoot: root, ServerEnabled: false, TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "CodeMCP is starting") || strings.Contains(text, "CodeMCP is running") {
		t.Fatalf("starting status=%q", text)
	}
}

func TestStatusVerboseReportsOperationalDetails(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute).UTC()
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_verbose", Managed: true, ServiceID: "cm-system-test", ServiceScope: "system", StartedAt: started, Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_verbose", Managed: true, ServiceID: "cm-system-test", ServiceScope: "system", StartedAt: started, ConfigRoot: root, ServerPort: cfg.Server.Port, AdminEnabled: cfg.Admin.Enabled, AdminPort: cfg.Admin.Port, Exposure: cfg.Server.Expose.Mode, TunnelConfigured: true, TunnelID: "tunnel_verbose"}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "--verbose", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"started", "managed     true", "scope       system", "backend", "initialized true", "format"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("verbose status missing %q: %s", expected, text)
		}
	}
}

func TestStatusNotInitialized(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "! CodeMCP is not initialized") || !strings.Contains(text, "cm init") {
		t.Fatalf("unexpected uninitialized status: %s", text)
	}
}

func TestStatusHelpers(t *testing.T) {
	if got := compactStatusPath("/definitely/not/home/config.json"); got == "" {
		t.Fatal("compactStatusPath returned empty path")
	}
	for duration, expected := range map[time.Duration]string{5 * time.Second: "5s", 2*time.Minute + 3*time.Second: "2m 03s", 3*time.Hour + 4*time.Minute: "3h 04m", 25*time.Hour + 2*time.Minute: "1d 01h 02m"} {
		if got := formatStatusUptime(time.Now().Add(-duration)); got != expected {
			t.Fatalf("formatStatusUptime(%s) = %q, want %q", duration, got, expected)
		}
	}
}

func TestRenderStatusConfigUsesCachedUpdateWithoutNetwork(t *testing.T) {
	checkedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.Local)
	available := &updatepkg.CachedCheck{CheckResult: updatepkg.CheckResult{Current: "v1.0.0", Latest: "v1.1.0", Status: updatepkg.StatusAvailable}, CheckedAt: checkedAt}
	snapshot := statusSnapshot{Source: configformat.Source{Path: "/tmp/config.json", Exists: true}, Config: config.Default(), Update: available}
	var output bytes.Buffer
	renderStatusConfig(&output, snapshot, false)
	if !strings.Contains(output.String(), "v1.1.0 available") || strings.Contains(output.String(), "checked") {
		t.Fatalf("cached available output = %q", output.String())
	}

	output.Reset()
	snapshot.Update = &updatepkg.CachedCheck{CheckResult: updatepkg.CheckResult{Current: "v1.1.0", Latest: "v1.1.0", Status: updatepkg.StatusUpToDate}, CheckedAt: checkedAt}
	renderStatusConfig(&output, snapshot, false)
	if strings.Contains(output.String(), "update") {
		t.Fatalf("non-verbose up-to-date cache should stay hidden: %q", output.String())
	}

	output.Reset()
	renderStatusConfig(&output, snapshot, true)
	if !strings.Contains(output.String(), "up to date") || !strings.Contains(output.String(), "checked") {
		t.Fatalf("verbose cached update output = %q", output.String())
	}
}

func TestRenderStatusConfigSurfacesSecurityWarnings(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Expose.Mode = config.ExposureAll
	cfg.Server.AllowInsecureHTTP = true
	snapshot := statusSnapshot{Source: configformat.Source{Path: "/tmp/config.json", Exists: true}, Config: cfg}
	var output bytes.Buffer
	renderStatusConfig(&output, snapshot, false)
	text := output.String()
	if !strings.Contains(text, "cleartext HTTP") {
		t.Fatalf("expected cleartext warning: %q", text)
	}
}

func TestStatusTunnelStateTracksTransientStartup(t *testing.T) {
	base := runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true}
	for name, test := range map[string]struct {
		status runtimeStatusResult
		want   string
	}{
		"starting":     {status: base, want: "starting"},
		"connecting":   {status: runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true}, want: "connecting"},
		"reconnecting": {status: runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRestarting: true, TunnelLastError: "retry"}, want: "reconnecting"},
		"connected":    {status: runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelReady: true}, want: "connected"},
		"failed":       {status: runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelLastError: "failed"}, want: "failed"},
	} {
		t.Run(name, func(t *testing.T) {
			got := statusTunnelState(test.status, true)
			if got != test.want {
				t.Fatalf("state = %q, want %q", got, test.want)
			}
			if transientTunnelState(got) != (test.want == "starting" || test.want == "connecting" || test.want == "reconnecting") {
				t.Fatalf("transientTunnelState(%q) mismatch", got)
			}
		})
	}
}
