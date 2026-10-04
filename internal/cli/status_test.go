package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	mcpnetwork "go.mewis.me/codemcp/internal/network"
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
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute).UTC()
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_status", Managed: true, ServiceID: "cm-system-test", ServiceScope: "system", StartedAt: started, Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_status", Managed: true, ServiceID: "cm-system-test", ServiceScope: "system", StartedAt: started, ConfigRoot: root, ServerPort: cfg.HTTP.MCP.Port, AdminEnabled: cfg.HTTP.Admin.Enabled, AdminPort: cfg.HTTP.Admin.Port, Exposure: cfg.HTTP.Exposure.Mode, TunnelEnabled: true, TunnelConfigured: true, TunnelReady: true, TunnelID: "tunnel_status"}
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
	for _, expected := range []string{"✓ CodeMCP is running", "Runtime", "session", "run_status", "managed", "system · " + runtimeBackendLabel("system"), "service", "cm-system-test", "Endpoints", "Config", "mcp off · admin off", "Tunnel", "✓ OpenAI Secure MCP Tunnel is connected", "tunnel_status"} {
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
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
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
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_verbose", Managed: true, ServiceID: "cm-system-test", ServiceScope: "system", StartedAt: started, ConfigRoot: root, ServerPort: cfg.HTTP.MCP.Port, AdminEnabled: cfg.HTTP.Admin.Enabled, AdminPort: cfg.HTTP.Admin.Port, Exposure: cfg.HTTP.Exposure.Mode, TunnelConfigured: true, TunnelID: "tunnel_verbose"}
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
	for _, expected := range []string{"started", "managed", "true", "scope", "system", "backend", "initialized", "format"} {
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
	presenter := presentation.New(&output, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: true})
	renderStatusConfig(presenter, snapshot, false)
	if !strings.Contains(output.String(), "v1.1.0 available") || strings.Contains(output.String(), "checked") {
		t.Fatalf("cached available output = %q", output.String())
	}

	output.Reset()
	snapshot.Update = &updatepkg.CachedCheck{CheckResult: updatepkg.CheckResult{Current: "v1.1.0", Latest: "v1.1.0", Status: updatepkg.StatusUpToDate}, CheckedAt: checkedAt}
	renderStatusConfig(presenter, snapshot, false)
	if strings.Contains(output.String(), "update") {
		t.Fatalf("non-verbose up-to-date cache should stay hidden: %q", output.String())
	}

	output.Reset()
	renderStatusConfig(presenter, snapshot, true)
	if !strings.Contains(output.String(), "up to date") || !strings.Contains(output.String(), "checked") {
		t.Fatalf("verbose cached update output = %q", output.String())
	}
}

func TestRenderStatusConfigSurfacesSecurityWarnings(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Exposure.Mode = config.ExposureAll
	cfg.HTTP.Security.AllowInsecure = true
	snapshot := statusSnapshot{Source: configformat.Source{Path: "/tmp/config.json", Exists: true}, Config: cfg}
	var output bytes.Buffer
	renderStatusConfig(presentation.New(&output, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: true}), snapshot, false)
	text := output.String()
	if !strings.Contains(text, "cleartext HTTP") {
		t.Fatalf("expected cleartext warning: %q", text)
	}
}

func TestRenderStatusUsesCanonicalPresenterCapabilities(t *testing.T) {
	snapshot := statusSnapshot{
		Source:  configformat.Source{Path: "/tmp/config.json", Exists: true},
		Config:  config.Default(),
		Running: true,
		Runtime: runtimeStatusResult{
			PID:              4242,
			RunID:            "run_presenter",
			Managed:          true,
			ServiceScope:     "user",
			ServiceID:        "cm-user-test",
			TunnelEnabled:    true,
			TunnelConfigured: true,
			TunnelReady:      true,
			TunnelID:         "tunnel_presenter",
		},
	}

	var unicodeOutput bytes.Buffer
	presenter := presentation.New(&unicodeOutput, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, Color: false})
	renderStandalonePresentation(presenter, "CodeMCP status", func() {
		renderStatus(presenter, snapshot, false)
	})
	unicodeText := unicodeOutput.String()
	for _, expected := range []string{"┌  CodeMCP status", "✓  CodeMCP is running", "│  ▸ Runtime", "│  pid — 4242", "│  ▸ Endpoints", "│  ▸ Config", "│  ▸ Tunnel", "│  ✓ OpenAI Secure MCP Tunnel — connected", "└  Done"} {
		if !strings.Contains(unicodeText, expected) {
			t.Fatalf("unicode status missing %q: %s", expected, unicodeText)
		}
	}
	if strings.ContainsRune(unicodeText, 'ℹ') {
		t.Fatalf("unicode status contains information-source glyph: %q", unicodeText)
	}
	if strings.Contains(unicodeText, "\x1b[") {
		t.Fatalf("color-disabled status contains ANSI: %q", unicodeText)
	}

	var asciiOutput bytes.Buffer
	renderStatus(presentation.New(&asciiOutput, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: false, Color: false}), snapshot, false)
	asciiText := asciiOutput.String()
	for _, expected := range []string{"[OK] CodeMCP is running", "[OK] OpenAI Secure MCP Tunnel is connected", "user |"} {
		if !strings.Contains(asciiText, expected) {
			t.Fatalf("ASCII status missing %q: %s", expected, asciiText)
		}
	}
	for _, r := range asciiText {
		if r > 0x7f {
			t.Fatalf("ASCII status contains non-ASCII rune %q: %q", r, asciiText)
		}
	}
}

func TestRenderStatusRuntimeAndTunnelStatesAcrossPresentationModes(t *testing.T) {
	cases := []struct {
		name            string
		snapshot        statusSnapshot
		humanWant       string
		plainWant       string
		humanTunnelWant string
		plainTunnelWant string
	}{
		{
			name:            "starting",
			snapshot:        statusSnapshot{Source: configformat.Source{Path: "/tmp/config.json", Exists: true}, Config: config.Default(), Running: true, Runtime: runtimeStatusResult{Starting: true, TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true}},
			humanWant:       "!  CodeMCP is starting",
			plainWant:       "[!] CodeMCP is starting",
			humanTunnelWant: "│  ◇ OpenAI Secure MCP Tunnel — connecting",
			plainTunnelWant: "OpenAI Secure MCP Tunnel is connecting",
		},
		{
			name:            "stopped",
			snapshot:        statusSnapshot{Source: configformat.Source{Path: "/tmp/config.json", Exists: true}, Config: config.Default()},
			humanWant:       "×  CodeMCP is stopped",
			plainWant:       "[ERR] CodeMCP is stopped",
			humanTunnelWant: "│  ◇ OpenAI Secure MCP Tunnel — not configured",
			plainTunnelWant: "OpenAI Secure MCP Tunnel is not configured",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var human, plain bytes.Buffer
			renderStatus(presentation.New(&human, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, Color: false}), test.snapshot, false)
			renderStatus(presentation.New(&plain, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: false, Color: false}), test.snapshot, false)
			if !strings.Contains(human.String(), test.humanWant) || !strings.Contains(human.String(), test.humanTunnelWant) {
				t.Fatalf("human %s status=%q", test.name, human.String())
			}
			if !strings.Contains(plain.String(), test.plainWant) || !strings.Contains(plain.String(), test.plainTunnelWant) {
				t.Fatalf("plain %s status=%q", test.name, plain.String())
			}
			if strings.ContainsAny(plain.String(), "\r\x1b") {
				t.Fatalf("plain %s status contains terminal control bytes: %q", test.name, plain.String())
			}
		})
	}
}

func TestRenderStatusDisabledTunnelGoldenRailHierarchy(t *testing.T) {
	cfg := config.Default()
	cfg.Tunnel.Enabled = false
	snapshot := statusSnapshot{
		Source:  configformat.Source{Path: "/tmp/config.json", Exists: true},
		Config:  cfg,
		Running: true,
		Runtime: runtimeStatusResult{PID: 4242, RunID: "run_abcd"},
	}
	var output bytes.Buffer
	presenter := presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, Color: false})
	renderStandalonePresentation(presenter, "CodeMCP status", func() {
		renderStatus(presenter, snapshot, false)
	})
	text := output.String()
	ordered := []string{
		"┌  CodeMCP status",
		"✓  CodeMCP is running",
		"│  ▸ Runtime",
		"│  pid — 4242",
		"│  ▸ Endpoints",
		"│  mcp http",
		"│  ▸ Config",
		"│  transports",
		"◇  Tunnel",
		"│  ◇ OpenAI Secure MCP Tunnel — disabled",
		"└  Done",
	}
	position := -1
	for _, expected := range ordered {
		next := strings.Index(text[position+1:], expected)
		if next < 0 {
			t.Fatalf("status rail missing ordered segment %q: %q", expected, text)
		}
		position += next + 1
	}
	if strings.Contains(text, "\nRuntime\n") || strings.Contains(text, "\nEndpoints\n") {
		t.Fatalf("rich status contains detached section headings: %q", text)
	}
}

func TestRenderStatusRichPaletteKeepsSettledTextNeutral(t *testing.T) {
	snapshot := statusSnapshot{
		Source:  configformat.Source{Path: "/tmp/config.json", Exists: true},
		Config:  config.Default(),
		Running: true,
		Runtime: runtimeStatusResult{
			PID:              4242,
			RunID:            "run_palette",
			TunnelEnabled:    true,
			TunnelConfigured: true,
			TunnelReady:      true,
		},
	}
	var output bytes.Buffer
	caps := presentation.Capabilities{Width: 100, Unicode: true, Color: true}
	presenter := presentation.New(&output, presentation.ModeHuman, caps)
	renderStandalonePresentation(presenter, "CodeMCP status", func() {
		renderStatus(presenter, snapshot, false)
	})
	theme := presentation.NewTheme(caps)
	text := output.String()

	for _, expected := range []string{
		theme.Render(presentation.RoleRail, "┌"),
		theme.Render(presentation.RoleSuccess, "✓") + "  CodeMCP is running",
		theme.Render(presentation.RoleRail, "│") + "  " + theme.Render(presentation.RoleStructure, "▸") + " " + theme.Render(presentation.RoleHeading, "Runtime"),
		theme.Render(presentation.RoleLabel, "mcp http"),
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("status palette missing %q: %q", expected, text)
		}
	}
	for _, forbidden := range []string{
		theme.Render(presentation.RoleActive, "┌"),
		theme.Render(presentation.RoleActive, "Runtime"),
		theme.Render(presentation.RoleStructure, "Runtime"),
		theme.Render(presentation.RoleActive, "mcp http"),
		theme.Render(presentation.RoleSuccess, "CodeMCP is running"),
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status palette over-colored settled text %q: %q", forbidden, text)
		}
	}
}

func TestRenderStatusVerboseEndpointsUsePresenterNestedFields(t *testing.T) {
	cfg := config.Default()
	snapshot := statusSnapshot{
		Source: configformat.Source{Path: "/tmp/config.json", Exists: true},
		Config: cfg,
		ListenerPlan: listenerPlan{Addresses: []mcpnetwork.Address{{
			Host:      "127.0.0.1",
			Interface: "loopback",
		}}},
	}
	var output bytes.Buffer
	renderStatusEndpoints(presentation.New(&output, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: true}), snapshot, true)
	text := output.String()
	for _, expected := range []string{"Endpoints", "  loopback", "    mcp http", "    admin"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("verbose endpoint presentation missing %q: %s", expected, text)
		}
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
