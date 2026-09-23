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

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	"go.mewis.me/codemcp/internal/secretstore"
	managed "go.mewis.me/codemcp/internal/service"
	"go.mewis.me/codemcp/internal/tunnel"
)

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
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root, Binary: "/fake/cm", Account: managed.Account{Username: "mew", HomeDir: t.TempDir()}}
	manager := &fakeServiceManager{}
	var output bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	if err := runManagedUp(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if manager.installs != 1 || manager.starts != 1 || !manager.running {
		t.Fatalf("manager after up = %#v", manager)
	}
	text := output.String()
	for _, expected := range []string{"Managed service definition installed", "Managed service backend started", "Managed runtime ready", "Managed service installed", "Server started", "OpenAI Secure MCP Tunnel is disabled", "View logs: cm logs -f", "Stop service: cm down", "session", "pid"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("up output missing %q: %s", expected, text)
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
	if err := runManagedDown(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if manager.removes != 1 || manager.installed {
		t.Fatalf("manager after down = %#v", manager)
	}
	if _, err := os.Stat(config.Path()); err != nil {
		t.Fatalf("down removed config: %v", err)
	}
	for _, expected := range []string{"Managed runtime stopped", "Managed service backend stopped", "Managed service definition removed", "Server stopped", "Managed service removed", "config preserved", "logs preserved"} {
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
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Server.Enabled = true
	cfg.Tunnel.Enabled = false
	cfg.Tunnel.ID = "tunnel_disabled"
	cfg.Tunnel.APIKey = "stale-runtime-key"
	cfg.Tunnel.AdminKey = "stale-admin-key"
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
	cmd := &cobra.Command{Use: "test"}
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
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root, Binary: "/fake/cm", Account: managed.Account{Username: "mew", HomeDir: t.TempDir()}}
	manager := &fakeServiceManager{}
	var output bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	if err := runManagedUp(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	previousRunID := manager.control.state.RunID
	output.Reset()
	starts, stops, installs, removes := manager.starts, manager.stops, manager.installs, manager.removes
	if err := runManagedRestart(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if !manager.running || manager.starts != starts+1 || manager.stops != stops+1 || manager.installs != installs || manager.removes != removes {
		t.Fatalf("manager after restart = %#v", manager)
	}
	if manager.control.state.RunID == previousRunID {
		t.Fatalf("restart reused runtime session %q", previousRunID)
	}
	text := output.String()
	for _, expected := range []string{"Managed runtime stopped", "Managed service backend stopped", "Managed service backend started", "Managed runtime ready", "Managed service restarted", "Server started"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("restart output missing %q: %s", expected, text)
		}
	}
	for _, unexpected := range []string{"Managed service removed", "Managed service installed"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("restart unexpectedly reinstalled service: %s", text)
		}
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
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_connecting", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: root, ServerEnabled: false, TunnelEnabled: true, TunnelConfigured: true, TunnelRunning: true, TunnelReady: false}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	status, err := waitManagedRuntimeReady(t.Context(), spec, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !status.TunnelRunning || status.TunnelReady {
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
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root, Binary: "/fake/cm", Account: managed.Account{Username: "mew", HomeDir: t.TempDir()}}
	manager := &fakeServiceManager{}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	if err := runManagedUp(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	manager.matches = false
	starts, installs, removes := manager.starts, manager.installs, manager.removes
	if err := runManagedRestart(cmd, spec, manager); err != nil {
		t.Fatal(err)
	}
	if manager.starts != starts+1 || manager.installs != installs+1 || manager.removes != removes || !manager.matches {
		t.Fatalf("manager after definition update = %#v", manager)
	}
}

func TestManagedUpRejectsForegroundRuntime(t *testing.T) {
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
