package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	managed "go.mewis.me/codemcp/internal/service"
)

type selfRestartManager struct {
	status   managed.Status
	matches  bool
	starts   int
	stops    int
	installs int
}

func (m *selfRestartManager) Backend() string { return "test" }
func (m *selfRestartManager) DefinitionMatches(managed.Spec) (bool, error) {
	return m.matches, nil
}
func (m *selfRestartManager) Install(managed.Spec) error {
	m.installs++
	return nil
}
func (m *selfRestartManager) Start(managed.Spec) error {
	m.starts++
	return nil
}
func (m *selfRestartManager) Stop(managed.Spec) error {
	m.stops++
	return nil
}
func (m *selfRestartManager) Uninstall(managed.Spec) error { return nil }
func (m *selfRestartManager) Status(managed.Spec) (managed.Status, error) {
	return m.status, nil
}

func TestRuntimeStatusReturnsStoppedWithoutControlState(t *testing.T) {
	setupLogsRoot(t)
	status, running, err := RuntimeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if running || status.PID != 0 {
		t.Fatalf("status=%#v running=%t", status, running)
	}
}

func TestRuntimeStatusUsesAuthenticatedControlEndpoint(t *testing.T) {
	root := setupLogsRoot(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(runtimecontrol.RuntimeStatus{PID: os.Getpid(), RunID: "run_test", Managed: true, ServiceID: "service", ServiceScope: "user"})
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	writeRuntimeState(t, root, runtimecontrol.State{PID: os.Getpid(), Address: parsed.Host, Token: "token", ConfigRoot: root})
	status, running, err := RuntimeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !running || status.RunID != "run_test" || !status.Managed || status.ServiceScope != "user" {
		t.Fatalf("status=%#v running=%t", status, running)
	}
}

func TestWaitManagedReadyWaitsForRuntimeStartup(t *testing.T) {
	root := setupLogsRoot(t)
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root}
	var starting atomic.Bool
	starting.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(runtimecontrol.RuntimeStatus{PID: os.Getpid(), RunID: "run_starting", Starting: starting.Load(), Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: root, ServerEnabled: false, TunnelEnabled: true, TunnelRunning: true})
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	writeRuntimeState(t, root, runtimecontrol.State{PID: os.Getpid(), Address: parsed.Host, Token: "token", ConfigRoot: root})
	go func() {
		time.Sleep(50 * time.Millisecond)
		starting.Store(false)
	}()
	started := time.Now()
	status, err := waitManagedReady(context.Background(), spec, "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.Starting || status.TunnelReady || time.Since(started) < 100*time.Millisecond {
		t.Fatalf("runtime returned before startup completed: status=%#v elapsed=%s", status, time.Since(started))
	}
}

func TestManagedRuntimeSystemActionReturnsExternalElevationWorkflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("system scope is unsupported on Windows")
	}
	setupLogsRoot(t)
	previous := detectServiceScope
	detectServiceScope = func() managed.Scope { return managed.ScopeUser }
	t.Cleanup(func() { detectServiceScope = previous })
	result, err := ManagedRuntimeAction(t.Context(), "restart", managed.ScopeSystem)
	if err != nil {
		t.Fatal(err)
	}
	if result.External == nil || !strings.Contains(result.External.Command, "restart --system") || !strings.Contains(strings.ToLower(result.External.Reason), "elevation") {
		t.Fatalf("external=%#v", result.External)
	}
}

func TestManagedRuntimeActionStagesTransientGoRunBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("go-run staging path assertion is platform-specific")
	}
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "go-build123", "b001", "exe", "cm")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("dev-build"), 0755); err != nil {
		t.Fatal(err)
	}
	spec := managed.Spec{ConfigRoot: root, Binary: source}
	prepared, err := prepareManagedActionSpec(spec, "restart")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Binary == spec.Binary || !strings.Contains(filepath.ToSlash(prepared.Binary), "/runtime/bin/go-run/") {
		t.Fatalf("prepared binary = %q", prepared.Binary)
	}
	down, err := prepareManagedActionSpec(spec, "down")
	if err != nil {
		t.Fatal(err)
	}
	if down.Binary != spec.Binary {
		t.Fatalf("down staged binary = %q want %q", down.Binary, spec.Binary)
	}
}

func TestManagedSelfRestartRequestsRuntimeExitWithoutStoppingBackend(t *testing.T) {
	manager := &selfRestartManager{
		status:  managed.Status{Installed: true, Running: true, PID: os.Getpid()},
		matches: true,
	}
	spec := managed.Spec{ID: "cm-user-test", Scope: managed.ScopeUser, ConfigRoot: t.TempDir()}
	current := runtimecontrol.RuntimeStatus{
		PID: os.Getpid(), RunID: "run_old", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope),
	}
	restarts := 0
	result, err := managedSelfRestart(t.Context(), spec, manager, current, func(context.Context) error {
		restarts++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if restarts != 1 || !result.Changed || result.Action != "restart" {
		t.Fatalf("self restart result=%#v restarts=%d", result, restarts)
	}
	if manager.starts != 0 || manager.stops != 0 || manager.installs != 0 {
		t.Fatalf("self restart touched backend lifecycle: starts=%d stops=%d installs=%d", manager.starts, manager.stops, manager.installs)
	}
}

func TestManagedSelfRestartRequiresMatchingInstalledDefinition(t *testing.T) {
	spec := managed.Spec{ID: "cm-user-test", Scope: managed.ScopeUser, ConfigRoot: "/tmp/cm-test"}
	current := runtimecontrol.RuntimeStatus{
		PID: os.Getpid(), RunID: "run_old", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope),
	}
	manager := &selfRestartManager{status: managed.Status{Installed: true, Running: true}, matches: false}
	restarts := 0
	result, err := managedSelfRestart(t.Context(), spec, manager, current, func(context.Context) error {
		restarts++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if restarts != 0 || result.External == nil || !strings.Contains(result.External.Command, "restart") {
		t.Fatalf("mismatched self restart result=%#v restarts=%d", result, restarts)
	}
}

func TestLoadAboutReportsMachineAndServerUptime(t *testing.T) {
	root := setupLogsRoot(t)
	previous := machineUptime
	machineUptime = func() (time.Duration, error) { return 3*time.Hour + 4*time.Minute, nil }
	t.Cleanup(func() { machineUptime = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(runtimecontrol.RuntimeStatus{PID: os.Getpid(), StartedAt: time.Now().Add(-2 * time.Minute)})
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	writeRuntimeState(t, root, runtimecontrol.State{PID: os.Getpid(), Address: parsed.Host, Token: "token", ConfigRoot: root})
	info, err := LoadAbout(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !info.MachineUptimeOK || info.MachineUptime != 3*time.Hour+4*time.Minute || !info.RuntimeRunning || info.ServerUptime < time.Minute {
		t.Fatalf("about=%#v", info)
	}
}
