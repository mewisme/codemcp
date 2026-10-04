package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	"go.mewis.me/codemcp/internal/secretstore"
	managed "go.mewis.me/codemcp/internal/service"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestInternalPostinstallRejectsInvalidInstallIntegrationEnv(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	t.Setenv(application.InstallIntegrationsEnv, "invalid")
	var output bytes.Buffer
	root := newRootCommand()
	root.SetContext(context.Background())
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"_service", "postinstall"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), application.InstallIntegrationsEnv) {
		t.Fatalf("err=%v output=%q", err, output.String())
	}
}

type fakeServiceManager struct {
	installed bool
	running   bool
	matches   bool
	starts    int
	stops     int
	installs  int
	removes   int
	control   *runtimeControl
	spec      managed.Spec
}

func (m *fakeServiceManager) Backend() string                              { return "fake" }
func (m *fakeServiceManager) DefinitionMatches(managed.Spec) (bool, error) { return m.matches, nil }
func (m *fakeServiceManager) Install(spec managed.Spec) error {
	m.installed, m.matches, m.installs, m.spec = true, true, m.installs+1, spec
	return nil
}
func (m *fakeServiceManager) Start(spec managed.Spec) error {
	m.running, m.starts, m.spec = true, m.starts+1, spec
	runID := fmt.Sprintf("run_test_%d", m.starts)
	stream := runtimeevent.NewStream(runtimeevent.Metadata{RunID: runID, PID: os.Getpid(), Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope)})
	var control *runtimeControl
	created, err := startRuntimeControl(runtimeControlOptions{RunID: runID, Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), StartedAt: time.Now(), Events: stream, Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: os.Getpid(), ServerPort: 41001, AdminEnabled: true, AdminPort: 41002, Exposure: config.ExposureNone}, nil
	}, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: runID, Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: spec.ConfigRoot, ServerPort: 41001, AdminEnabled: true, AdminPort: 41002, Exposure: config.ExposureNone}
	}, Shutdown: func() {
		m.running = false
		go func() {
			time.Sleep(10 * time.Millisecond)
			if control != nil {
				_ = control.Close()
			}
		}()
	}, ClearLogs: func() error { return nil }})
	if err != nil {
		return err
	}
	control = created
	m.control = created
	return nil
}
func (m *fakeServiceManager) Stop(managed.Spec) error {
	m.running, m.stops = false, m.stops+1
	return nil
}
func (m *fakeServiceManager) Uninstall(managed.Spec) error {
	m.installed, m.matches, m.removes = false, false, m.removes+1
	return nil
}
func (m *fakeServiceManager) Status(managed.Spec) (managed.Status, error) {
	return managed.Status{Installed: m.installed, Running: m.running, Backend: "fake"}, nil
}

func TestManagedUpAndDownLifecycle(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root, Binary: "/fake/cm", Account: managed.Account{Username: "mew", HomeDir: t.TempDir()}}
	manager := &fakeServiceManager{}
	var output bytes.Buffer
	rootCmd := &cobra.Command{Use: "cm"}
	cmd := &cobra.Command{Use: "up"}
	rootCmd.AddCommand(cmd)
	cmd.SetContext(context.Background())
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}))
	if err := runManagedUp(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if manager.installs != 1 || manager.starts != 1 || !manager.running {
		t.Fatalf("manager after up = %#v", manager)
	}
	text := output.String()
	for _, expected := range []string{"Installed managed service definition", "Started managed service backend", "Managed runtime ready", "Managed service installed", "Server started", "OpenAI Secure MCP Tunnel", "View logs", "cm logs -f", "Stop service", "cm down", "session", "pid"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("up output missing %q: %s", expected, text)
		}
	}
	for _, expected := range []string{
		"│  ✓ Server started",
		"│  │  scope — user",
		"│  │  config — " + root,
		"│  · OpenAI Secure MCP Tunnel — disabled",
		"│  ▸ Actions",
		"│  │  View logs — cm logs -f",
		"│  │  Stop service — cm down",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("up output hierarchy missing %q: %s", expected, text)
		}
	}
	for _, unexpected := range []string{"│  ◆ scope —", "│  ◆ config —", "│  ◆ View logs —", "│  ◆ Stop service —"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("up output still renders detail as peer node %q: %s", unexpected, text)
		}
	}
	output.Reset()
	if err := runManagedUp(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if manager.installs != 1 || manager.starts != 1 || !strings.Contains(output.String(), "Managed service already running") {
		t.Fatalf("idempotent up failed: installs=%d starts=%d output=%q", manager.installs, manager.starts, output.String())
	}
	output.Reset()
	downRoot := &cobra.Command{Use: "cm"}
	downCmd := &cobra.Command{Use: "down"}
	downRoot.AddCommand(downCmd)
	downCmd.SetContext(context.Background())
	downCmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}))
	if err := runManagedDown(downCmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if manager.removes != 1 || manager.installed {
		t.Fatalf("manager after down = %#v", manager)
	}
	if _, err := os.Stat(config.Path()); err != nil {
		t.Fatalf("down removed config: %v", err)
	}
	for _, expected := range []string{"Stopped managed runtime", "Stopped managed service backend", "Removed managed service definition", "Server stopped", "Managed service removed", "config preserved", "logs preserved"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("down output missing %q: %s", expected, output.String())
		}
	}
}

func TestManagedUpAllowsHTTPTransportWhenDisabledTunnelSecretIsMissing(t *testing.T) {
	defer configformat.SetRootPath("")
	restoreSecrets := secretstore.UseMemoryForTesting()
	defer restoreSecrets()
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.HTTP.MCP.Enabled = true
	cfg.Tunnel.Enabled = false
	cfg.Tunnel.ID = "tunnel_disabled"
	cfg.Tunnel.APIKey = "stale-runtime-key"
	cfg.Tunnel.Admin.Key = "stale-admin-key"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := config.TunnelSecretEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	store := secretstore.New(root)
	for _, entry := range entries {
		if err := store.Set(entry, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := config.Load(); err == nil {
		t.Fatal("strict config load unexpectedly accepted missing tunnel secrets")
	}

	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root, Binary: "/fake/cm", Account: managed.Account{Username: "mew", HomeDir: t.TempDir()}}
	manager := &fakeServiceManager{}
	cmd := &cobra.Command{Use: "up"}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	if _, err := saveManagedEnvironment(spec); err != nil {
		t.Fatalf("disabled tunnel blocked managed environment capture: %v", err)
	}
	if err := runManagedUp(cmd, spec, manager); err != nil {
		t.Fatalf("HTTP-only managed up failed: %v", err)
	}
	defer func() {
		if manager.control != nil {
			_ = manager.control.Close()
		}
	}()
	if !manager.running || manager.starts != 1 {
		t.Fatalf("managed HTTP runtime did not start: %#v", manager)
	}
}

func TestManagedRestartKeepsServiceInstalledAndStartsNewRuntime(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root, Binary: "/fake/cm", Account: managed.Account{Username: "mew", HomeDir: t.TempDir()}}
	manager := &fakeServiceManager{}
	var output bytes.Buffer
	setupRoot := &cobra.Command{Use: "cm"}
	cmd := &cobra.Command{Use: "up"}
	setupRoot.AddCommand(cmd)
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	if err := runManagedUp(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	previousRunID := manager.control.state.RunID
	output.Reset()
	starts, stops, installs, removes := manager.starts, manager.stops, manager.installs, manager.removes
	restartRoot := &cobra.Command{Use: "cm"}
	restartCmd := &cobra.Command{Use: "restart"}
	restartRoot.AddCommand(restartCmd)
	restartCmd.SetContext(context.Background())
	restartCmd.SetOut(&output)
	if err := runManagedRestart(restartCmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if !manager.running || manager.starts != starts+1 || manager.stops != stops+1 || manager.installs != installs || manager.removes != removes {
		t.Fatalf("manager after restart = %#v", manager)
	}
	if manager.control.state.RunID == previousRunID {
		t.Fatalf("restart reused runtime session %q", previousRunID)
	}
	text := output.String()
	for _, expected := range []string{"Stopped managed runtime", "Stopped managed service backend", "Started managed service backend", "Server", "service", "runtime", "backend", "config", "mcp http", "Actions", "Logs", "cm logs -f", "Stop", "cm down"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("restart output missing %q: %s", expected, text)
		}
	}
	for _, unexpected := range []string{"Managed service removed", "Managed service installed", "Managed service restarted", "Server started", "Semantic — ready", "Notifications — ready"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("restart output contains obsolete status %q: %s", unexpected, text)
		}
	}
	if count := strings.Count(text, "Managed runtime ready"); count != 1 {
		t.Fatalf("managed runtime readiness count=%d want=1: %s", count, text)
	}
}

func TestManagedLifecycleResultRendersConnectedTunnelAsNestedList(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	metadata := tunnel.Metadata{
		ID:              "tunnel_demo",
		Name:            "MCP_Tunnel_WSL",
		Description:     "MCP Tunnel WSL",
		OrganizationIDs: []string{"org_demo"},
		WorkspaceIDs:    []string{"ws_demo"},
	}
	if _, err := config.SaveTunnelMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: "cm-user-demo", Scope: managed.ScopeUser, ConfigRoot: root}
	status := runtimeStatusResult{
		PID:              4242,
		RunID:            "run_demo",
		TunnelEnabled:    true,
		TunnelConfigured: true,
		TunnelRunning:    true,
		TunnelReady:      true,
		TunnelID:         metadata.ID,
	}
	var output bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}))
	renderManagedLifecycleResult(cmd, "Managed service restarted", spec, &fakeServiceManager{}, status, tunnel.Config{ID: metadata.ID})

	text := output.String()
	for _, expected := range []string{
		"│  ✓ OpenAI Secure MCP Tunnel — connected",
		"│  │  id — tunnel_demo",
		"│  │  name — MCP_Tunnel_WSL",
		"│  │  description — MCP Tunnel WSL",
		"│  │  scope",
		"│  │  │  organization — org_demo",
		"│  │  │  workspace — ws_demo",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("connected tunnel hierarchy missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "scope — organization:") {
		t.Fatalf("connected tunnel scope still renders as one long field: %s", text)
	}
	for _, unexpected := range []string{"│  ◆ tunnel id —", "│  ◆ tunnel name —", "│  ◆ tunnel description —", "│  ◆ tunnel scope —"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("connected tunnel metadata still renders as peer node %q: %s", unexpected, text)
		}
	}
}

func TestManagedLifecycleResultGroupsReadinessByScope(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}))
	status := runtimeStatusResult{
		PID: 4242,
		Readiness: []runtimecontrol.ReadinessComponent{
			{ID: "typesafe", Label: "TypeSafe semantic provider", Configured: true, Ready: true},
			{ID: "semantic-approval", Label: "Semantic approval", Configured: true, Ready: true},
			{ID: "telegram", Label: "Telegram runtime", Configured: true, Ready: true},
			{ID: "telegram-topics", Label: "Telegram topics", Configured: true, Ready: true},
			{ID: "telegram-logs-mini-app", Label: "Telegram Logs Mini App", Configured: true, Ready: true},
			{ID: "approval-notifications", Label: "Approval notifications", Configured: true, Ready: true},
			{ID: "completion-notifications", Label: "Completion notifications", Configured: true, Ready: true},
			{ID: "ignored", Label: "Unconfigured", Configured: false, Ready: true},
		},
	}
	renderManagedLifecycleResult(cmd, "Managed service restarted", managed.Spec{Scope: managed.ScopeUser}, &fakeServiceManager{}, status, tunnel.Config{})
	text := output.String()
	for _, expected := range []string{
		"│  ✓ Semantic — ready",
		"│  │  TypeSafe semantic provider — ready",
		"│  │  Semantic approval — ready",
		"│  ✓ Telegram — ready",
		"│  │  Telegram runtime — ready",
		"│  │  Telegram topics — ready",
		"│  │  Telegram Logs Mini App — ready",
		"│  ✓ Notifications — ready",
		"│  │  Approval notifications — ready",
		"│  │  Completion notifications — ready",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("grouped readiness missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "Unconfigured") {
		t.Fatalf("unconfigured readiness leaked into output: %s", text)
	}
}

func TestManagedRestartReadinessObserverStreamsScopesAsTheyBecomeReady(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true}))
	session := commandProgressSession(cmd)
	session.SetTitle("Restart CodeMCP")
	traceObserve := commandTraceObserver(cmd)
	traceObserve(tracepkg.Event{Component: "SERVICE", Name: "service.runtime.ready.wait.started", Message: "Waiting for managed runtime readiness", Phase: tracepkg.PhaseStart})
	observe, streamed := managedRestartReadinessObserver(session)

	readiness := []runtimecontrol.ReadinessComponent{
		{ID: "typesafe", Label: "TypeSafe semantic provider", Configured: true, Ready: true},
		{ID: "semantic-approval", Label: "Semantic approval", Configured: true, Ready: true},
		{ID: "telegram", Label: "Telegram runtime", Configured: true, Ready: false},
		{ID: "approval-notifications", Label: "Approval notifications", Configured: true, Ready: true},
		{ID: "completion-notifications", Label: "Completion notifications", Configured: true, Ready: true},
	}
	observe(runtimeStatusResult{Readiness: readiness})

	telegram := append([]runtimecontrol.ReadinessComponent(nil), readiness...)
	telegram[2].Ready = true
	observe(runtimeStatusResult{Readiness: telegram})

	observe(runtimeStatusResult{Readiness: telegram, TunnelEnabled: true, TunnelConfigured: true, TunnelReady: true})
	observe(runtimeStatusResult{Readiness: telegram, TunnelEnabled: true, TunnelConfigured: true, TunnelReady: true})
	traceObserve(tracepkg.Event{Component: "SERVICE", Name: "service.runtime.ready.wait.completed", Message: "Managed runtime ready", Phase: tracepkg.PhaseEnd})

	text := output.String()
	telegramLine := "✓ Telegram — ready"
	tunnelLine := "✓ OpenAI Secure MCP Tunnel — ready"
	for _, line := range []string{telegramLine, tunnelLine} {
		if count := strings.Count(text, line); count != 1 {
			t.Fatalf("streamed readiness line %q count=%d: %q", line, count, text)
		}
	}
	telegramIndex, tunnelIndex := strings.Index(text, telegramLine), strings.Index(text, tunnelLine)
	if telegramIndex < 0 || tunnelIndex <= telegramIndex {
		t.Fatalf("readiness scopes were not emitted in observed completion order: %q", text)
	}
	if !strings.Contains(text, tunnelLine+"\n│\n") {
		t.Fatalf("final tunnel readiness scope is missing its trailing spacer: %q", text)
	}
	for _, unexpected := range []string{"✓ Semantic — ready", "✓ Notifications — ready"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("non-lifecycle readiness leaked into restart output %q: %q", unexpected, text)
		}
	}
	for _, id := range []string{"telegram", "tunnel"} {
		if _, ok := streamed[id]; !ok {
			t.Fatalf("readiness scope %q was not recorded as streamed: %#v", id, streamed)
		}
	}
}

func TestManagedRestartResultUsesCompactServerSummary(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}))
	spec := managed.Spec{ID: "cm-user-test", Scope: managed.ScopeUser, ConfigRoot: "/tmp/cm"}
	status := runtimeStatusResult{PID: 4242, RunID: "run_1234567890abcdef", ServerEnabled: false}
	renderManagedRestartResult(cmd, spec, &fakeServiceManager{}, status)
	text := output.String()
	for _, expected := range []string{
		"│  ✓ Server — running",
		"│  │  service — cm-user-test",
		"│  │  runtime — pid 4242 · run_1234567890abcdef",
		"│  │  backend — " + managedBackendLabel(&fakeServiceManager{}, spec) + " · user",
		"│  │  config — /tmp/cm",
		"│  │  mcp http — disabled",
		"│  ▸ Actions",
		"│  │  Logs — cm logs -f",
		"│  │  Stop — cm down",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("compact restart summary missing %q: %s", expected, text)
		}
	}
	for _, unexpected := range []string{"Managed service restarted", "Server started", "Semantic", "Notifications"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("compact restart summary contains obsolete content %q: %s", unexpected, text)
		}
	}
}

func TestManagedRestartStatusReadyWaitsForTelegramAndTunnel(t *testing.T) {
	base := runtimeStatusResult{
		TunnelEnabled:    true,
		TunnelConfigured: true,
		TunnelReady:      true,
		Readiness: []runtimecontrol.ReadinessComponent{
			{ID: "telegram", Configured: true, Ready: true},
			{ID: "telegram-topics", Configured: true, Ready: true},
			{ID: "telegram-logs-mini-app", Configured: true, Ready: true},
		},
	}
	if !managedRestartStatusReady(base) {
		t.Fatal("fully ready Telegram and tunnel should complete restart")
	}
	optionalPending := base
	optionalPending.Readiness = append([]runtimecontrol.ReadinessComponent(nil), base.Readiness...)
	optionalPending.Readiness[1].Ready = false
	optionalPending.Readiness[2].Ready = false
	if !managedRestartStatusReady(optionalPending) {
		t.Fatal("Telegram topics or Logs Mini App blocked restart")
	}
	telegramPending := base
	telegramPending.Readiness = append([]runtimecontrol.ReadinessComponent(nil), base.Readiness...)
	telegramPending.Readiness[0].Ready = false
	if managedRestartStatusReady(telegramPending) {
		t.Fatal("restart completed before Telegram runtime became ready")
	}
	tunnelPending := base
	tunnelPending.TunnelReady = false
	if managedRestartStatusReady(tunnelPending) {
		t.Fatal("restart completed before tunnel became ready")
	}
	corePending := base
	corePending.Starting = true
	if managedRestartStatusReady(corePending) {
		t.Fatal("restart completed while core runtime was still starting")
	}
}

func TestManagedRestartReadinessScopeTreatsTelegramFeaturesAsNonBlocking(t *testing.T) {
	states := managedRestartReadinessScopeStates(runtimeStatusResult{Readiness: []runtimecontrol.ReadinessComponent{
		{ID: "telegram", Label: "Telegram runtime", Configured: true, Ready: true},
		{ID: "telegram-topics", Label: "Telegram topics", Configured: true, Ready: false},
		{ID: "telegram-logs-mini-app", Label: "Telegram Logs Mini App", Configured: true, Ready: false},
	}})
	if len(states) != 1 || states[0].Scope.ID != "telegram" || !states[0].Ready {
		t.Fatalf("restart Telegram readiness=%#v", states)
	}
	if len(states[0].Fields) != 1 || states[0].Fields[0].Label != "Telegram runtime" {
		t.Fatalf("restart Telegram fields=%#v", states[0].Fields)
	}
	statusStates := managedReadinessScopeStates(runtimeStatusResult{Readiness: []runtimecontrol.ReadinessComponent{
		{ID: "telegram", Label: "Telegram runtime", Configured: true, Ready: true},
		{ID: "telegram-topics", Label: "Telegram topics", Configured: true, Ready: false},
		{ID: "telegram-logs-mini-app", Label: "Telegram Logs Mini App", Configured: true, Ready: false},
	}})
	if len(statusStates) != 1 || statusStates[0].Ready {
		t.Fatalf("full status should still expose optional Telegram readiness: %#v", statusStates)
	}
}

func TestManagedRestartRuntimeStatusWaitsForTelegramAndTunnel(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root}
	var ready atomic.Bool
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_restart_ready", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: os.Getpid()}, nil
	}, Status: func() runtimeStatusResult {
		isReady := ready.Load()
		return runtimeStatusResult{
			PID: os.Getpid(), RunID: "run_restart_ready", Lifecycle: "tunnel_connecting",
			Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: root,
			TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelReady: isReady,
			Readiness: []runtimecontrol.ReadinessComponent{{ID: "telegram", Configured: true, Ready: isReady}},
		}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		ready.Store(true)
	}()
	status, err := managed.WaitRuntimeReady(t.Context(), spec, managedRestartRuntimeStatus, "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !status.TunnelReady || status.Starting || len(status.Readiness) != 1 || !status.Readiness[0].Ready {
		t.Fatalf("restart readiness returned early: %#v", status)
	}
}

func TestWaitManagedRuntimeReadyWaitsForRuntimeStartup(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root}
	var starting atomic.Bool
	starting.Store(true)
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_ready", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: os.Getpid()}, nil
	}, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_ready", Starting: starting.Load(), Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: root, TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		starting.Store(false)
	}()
	started := time.Now()
	status, err := waitManagedRuntimeReady(t.Context(), spec, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.Starting || time.Since(started) < 100*time.Millisecond {
		t.Fatalf("returned before runtime startup completed: status=%#v elapsed=%s", status, time.Since(started))
	}
}

func TestWaitManagedRuntimeReadyDoesNotRequireTunnelConnection(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root}
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_connecting", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: os.Getpid()}, nil
	}, Status: func() runtimeStatusResult {
		lifecycle := "tunnel_connecting"
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_connecting", Lifecycle: lifecycle, Starting: runtimeLifecycleStarting(lifecycle), Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: root, ServerEnabled: false, TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelReady: false}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	status, err := waitManagedRuntimeReady(t.Context(), spec, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.Starting || status.Lifecycle != "tunnel_connecting" || !status.TunnelRunning || status.TunnelReady {
		t.Fatalf("connecting tunnel status=%#v", status)
	}
}

func TestManagedRestartUpdatesChangedDefinitionWithoutUninstall(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled = false, false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root, Binary: "/fake/cm", Account: managed.Account{Username: "mew", HomeDir: t.TempDir()}}
	manager := &fakeServiceManager{}
	setupRoot := &cobra.Command{Use: "cm"}
	setupCmd := &cobra.Command{Use: "up"}
	setupRoot.AddCommand(setupCmd)
	setupCmd.SetContext(context.Background())
	setupCmd.SetOut(&bytes.Buffer{})
	if err := runManagedUp(setupCmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	manager.matches = false
	var output bytes.Buffer
	restartRoot := &cobra.Command{Use: "cm"}
	cmd := &cobra.Command{Use: "restart"}
	restartRoot.AddCommand(cmd)
	cmd.SetContext(context.Background())
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}))
	commandProgressSession(cmd).SetTitle("Restart CodeMCP")
	starts, installs, removes := manager.starts, manager.installs, manager.removes
	if err := runManagedRestart(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if manager.starts != starts+1 || manager.installs != installs+1 || manager.removes != removes || !manager.matches {
		t.Fatalf("manager after definition update = %#v", manager)
	}
	text := output.String()
	for _, grouped := range []string{
		"Stopped managed service backend\n│\n◇  Updating managed service definition",
		"Updated managed service definition\n│\n◇  Starting managed service backend",
	} {
		if !strings.Contains(text, grouped) {
			t.Fatalf("restart lifecycle groups are cramped; missing %q: %s", grouped, text)
		}
	}
}

func TestManagedUpRejectsForegroundRuntime(t *testing.T) {
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
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "foreground", Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "foreground", ConfigRoot: root}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root}
	manager := &fakeServiceManager{}
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	if err := runManagedUp(cmd, spec, manager); err == nil || !strings.Contains(err.Error(), "outside the managed service") {
		t.Fatalf("foreground runtime was not rejected: %v", err)
	}
	if manager.installs != 0 || manager.starts != 0 {
		t.Fatalf("foreground conflict mutated service: %#v", manager)
	}
}

func TestResolveManagedConfigRootUsesInvokingUserForSudoDefault(t *testing.T) {
	defer configformat.SetRootPath("")
	t.Setenv(configformat.EnvConfigDir, "")
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"up"})
	if err != nil {
		t.Fatal(err)
	}
	account := managed.Account{Username: "mew", HomeDir: filepath.Join(t.TempDir(), "home")}
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "root-home-config")); err != nil {
		t.Fatal(err)
	}
	if err := resolveManagedConfigRoot(cmd, managed.ScopeSystem, account); err != nil {
		t.Fatal(err)
	}
	if got, want := config.RootPath(), managed.DefaultConfigRoot(account); got != want {
		t.Fatalf("root = %q, want invoking-user root %q", got, want)
	}
}

func TestManagedSystemFlagSelectsSystemScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows always uses per-user Task Scheduler")
	}
	root := newRootCommand()
	root.SetArgs([]string{"up", "--system"})
	cmd, _, err := root.Find([]string{"up", "--system"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("system", "true"); err != nil {
		t.Fatal(err)
	}
	scope, err := managedScopeForCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if scope != managed.ScopeSystem {
		t.Fatalf("scope = %q, want system", scope)
	}
}

func TestManagedSystemSpecStagesTransientGoRunBinaryBeforeElevation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("system scope is unsupported on Windows")
	}
	if managed.DetectScope() != managed.ScopeUser {
		t.Skip("requires an unprivileged process to exercise pre-elevation staging")
	}
	defer configformat.SetRootPath("")
	rootPath := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, rootPath)
	if err := configformat.SetRootPath(rootPath); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "go-build123", "b001", "exe", "cm")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("dev-build"), 0755); err != nil {
		t.Fatal(err)
	}
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"up"})
	if err != nil {
		t.Fatal(err)
	}
	spec, _, err := managedServiceForCommandWithBinary(cmd, managed.ScopeSystem, source)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Binary == filepath.Clean(source) || !strings.Contains(filepath.ToSlash(spec.Binary), "/runtime/bin/go-run/") {
		t.Fatalf("system pre-elevation binary = %q", spec.Binary)
	}
	if !strings.HasPrefix(filepath.Clean(spec.Binary), filepath.Clean(rootPath)+string(filepath.Separator)) {
		t.Fatalf("staged binary %q escaped config root %q", spec.Binary, rootPath)
	}
	if _, err := os.Stat(spec.Binary); err != nil {
		t.Fatalf("staged binary is not accessible to invoking user: %v", err)
	}
}

func TestManagedScopeConflictUsesSystemFlagHint(t *testing.T) {
	spec := managed.Spec{Scope: managed.ScopeUser}
	err := managedScopeConflict(runtimeStatusResult{Managed: true, ServiceID: "system", ServiceScope: string(managed.ScopeSystem), PID: 123}, spec, "down")
	if err == nil || !strings.Contains(err.Error(), "cm down --system") {
		t.Fatalf("error = %v, want --system hint", err)
	}
}

func TestRuntimeTunnelSummary(t *testing.T) {
	cases := []struct {
		status runtimeStatusResult
		want   string
	}{
		{runtimeStatusResult{}, "disabled · not configured"},
		{runtimeStatusResult{TunnelConfigured: true}, "disabled · configured"},
		{runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true}, "enabled · configured · starting"},
		{runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true}, "enabled · configured · connecting"},
		{runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelReady: true}, "enabled · configured · connected"},
		{runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRestarting: true}, "enabled · configured · reconnecting"},
	}
	for _, test := range cases {
		if got := runtimeTunnelSummary(test.status); got != test.want {
			t.Fatalf("summary = %q, want %q", got, test.want)
		}
	}
}

func TestLogManagedStartupFailureShowsRuntimeError(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Time: time.Now().UTC(), Level: "error", Kind: "error", Name: "tunnel.start.failed", Message: "Tunnel start failed", Error: "invalid runtime key"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--verbose", "up"})
	resolved, _, err := cmd.Find([]string{"--verbose", "up"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.PersistentFlags().Set("verbose", "true"); err != nil {
		t.Fatal(err)
	}
	manager := &fakeServiceManager{installed: true}
	logManagedStartupFailure(resolved, managed.Spec{ConfigRoot: root}, manager, errors.New("managed service did not become ready"))
	text := output.String()
	for _, expected := range []string{"Managed runtime failed readiness", "Managed runtime error", "tunnel.start.failed", "Tunnel start failed: invalid runtime key"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("startup diagnostics missing %q: %s", expected, text)
		}
	}
}

func TestLogRuntimeTunnelMetadata(t *testing.T) {
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Writer: &output})
	status := runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelReady: true, TunnelID: "tunnel_runtime"}
	cfg := tunnel.Config{ID: "tunnel_config", APIKey: "runtime-key"}
	var loadedID string
	logRuntimeTunnelMetadata(log, cfg, status, func(id string) (tunnel.Metadata, error) {
		loadedID = id
		return tunnel.Metadata{ID: id, Name: "MCP Tunnel WSL", Description: "Development tunnel", OrganizationIDs: []string{"org_test"}, WorkspaceIDs: []string{"ws_test"}}, nil
	})
	if loadedID != "tunnel_runtime" {
		t.Fatalf("loaded id = %q", loadedID)
	}
	text := output.String()
	for _, expected := range []string{"tunnel name: MCP Tunnel WSL", "tunnel description: Development tunnel", "tunnel scope: organization:org_test · workspace:ws_test"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metadata output missing %q: %s", expected, text)
		}
	}
}

func TestLogRuntimeTunnelMetadataSkipsUntilConnected(t *testing.T) {
	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Writer: &output})
	called := false
	status := runtimeStatusResult{TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelID: "tunnel_runtime"}
	logRuntimeTunnelMetadata(log, tunnel.Config{ID: "tunnel_runtime", APIKey: "runtime-key"}, status, func(string) (tunnel.Metadata, error) {
		called = true
		return tunnel.Metadata{}, nil
	})
	if called || output.Len() != 0 {
		t.Fatalf("metadata fetched before tunnel connected: called=%v output=%q", called, output.String())
	}
}
