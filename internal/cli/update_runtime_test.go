package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	managed "go.mewis.me/codemcp/internal/service"
)

type updateRuntimeManager struct {
	installed bool
	starts    int
	stops     int
	control   *runtimeControl
}

type updateLifecycleManager struct {
	installed bool
	running   bool
	starts    int
	stops     int
	runID     string
	sequence  []string
}

func (m *updateLifecycleManager) Backend() string                              { return "fake" }
func (m *updateLifecycleManager) DefinitionMatches(managed.Spec) (bool, error) { return true, nil }
func (m *updateLifecycleManager) Install(managed.Spec) error                   { m.installed = true; return nil }
func (m *updateLifecycleManager) Uninstall(managed.Spec) error                 { m.installed = false; return nil }
func (m *updateLifecycleManager) Status(managed.Spec) (managed.Status, error) {
	return managed.Status{Installed: m.installed, Running: m.running, Backend: "fake"}, nil
}
func (m *updateLifecycleManager) Start(managed.Spec) error {
	m.starts++
	m.running = true
	m.runID = fmt.Sprintf("run_after_update_%d", m.starts)
	m.sequence = append(m.sequence, "start")
	return nil
}
func (m *updateLifecycleManager) Stop(managed.Spec) error {
	m.stops++
	m.running = false
	m.sequence = append(m.sequence, "stop")
	return nil
}

func (m *updateRuntimeManager) Backend() string                              { return "fake" }
func (m *updateRuntimeManager) DefinitionMatches(managed.Spec) (bool, error) { return true, nil }
func (m *updateRuntimeManager) Install(managed.Spec) error                   { m.installed = true; return nil }
func (m *updateRuntimeManager) Uninstall(managed.Spec) error                 { m.installed = false; return nil }
func (m *updateRuntimeManager) Status(managed.Spec) (managed.Status, error) {
	return managed.Status{Installed: m.installed, Running: m.control != nil, Backend: "fake"}, nil
}
func (m *updateRuntimeManager) Start(spec managed.Spec) error {
	m.starts++
	runID := fmt.Sprintf("run_update_%d", m.starts)
	stream := runtimeevent.NewStream(runtimeevent.Metadata{RunID: runID, PID: os.Getpid(), Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope)})
	control, err := startRuntimeControl(runtimeControlOptions{RunID: runID, Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), StartedAt: time.Now(), Events: stream, Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: os.Getpid(), ServerPort: 41001}, nil
	}, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: runID, Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: spec.ConfigRoot, ServerPort: 41001}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		return err
	}
	m.control = control
	return nil
}
func (m *updateRuntimeManager) Stop(managed.Spec) error {
	m.stops++
	if m.control != nil {
		err := m.control.Close()
		m.control = nil
		return err
	}
	return nil
}

func TestRestartManagedRuntimeInPlace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	spec := managed.Spec{ID: managed.ID(root, managed.ScopeUser), Scope: managed.ScopeUser, ConfigRoot: root}
	manager := &updateLifecycleManager{installed: true, running: true, runID: "run_before_update"}
	probe := func(context.Context) (runtimeStatusResult, bool, error) {
		if !manager.running {
			return runtimeStatusResult{}, false, nil
		}
		return runtimeStatusResult{PID: 123, RunID: manager.runID, Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), ConfigRoot: root}, true, nil
	}
	shutdown := func(context.Context) error {
		manager.sequence = append(manager.sequence, "shutdown")
		manager.running = false
		return nil
	}
	if err := restartManagedRuntimeInPlaceWith(context.Background(), spec, manager, probe, shutdown); err != nil {
		t.Fatal(err)
	}
	if manager.stops != 1 || manager.starts != 1 || !manager.running {
		t.Fatalf("manager = %+v", manager)
	}
	if got := strings.Join(manager.sequence, ","); got != "shutdown,stop,start" {
		t.Fatalf("restart sequence = %q", got)
	}
}

func TestRestartManagedRuntimeInPlaceRequiresInstalledService(t *testing.T) {
	manager := &updateRuntimeManager{}
	if err := restartManagedRuntimeInPlace(context.Background(), managed.Spec{}, manager); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error = %v", err)
	}
	if manager.starts != 0 || manager.stops != 0 {
		t.Fatalf("manager mutated = %+v", manager)
	}
}

func TestCoordinateUpdatedRuntimeSkipsRestart(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{PID: 123, Managed: true}}
	if err := coordinateUpdatedRuntime(cmd, install.Result{}, state, true); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "Runtime restart skipped") || !strings.Contains(text, "pid: 123") {
		t.Fatalf("output = %q", text)
	}
}

func TestCoordinateUpdatedRuntimeLeavesForegroundServerRunning(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{PID: 456}}
	if err := coordinateUpdatedRuntime(cmd, install.Result{}, state, false); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "Foreground runtime is still using the previous version") || !strings.Contains(text, "pid: 456") {
		t.Fatalf("output = %q", text)
	}
}

func TestCoordinateUpdatedRuntimeRollsBackAndRestartsPreviousVersion(t *testing.T) {
	layout := updateInstallLayout(t)
	installed := updateInstallVersions(t, layout)
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true, PID: 789}}
	calls := 0
	restart := func(*cobra.Command, install.Layout, runtimeStatusResult) error {
		calls++
		if calls == 1 {
			return errors.New("new runtime unhealthy")
		}
		return nil
	}
	err := coordinateUpdatedRuntimeWith(cmd, installed, state, false, restart)
	if err == nil || !strings.Contains(err.Error(), "rolled back to v1.0.0") {
		t.Fatalf("error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("restart calls = %d", calls)
	}
	version, _, currentErr := install.CurrentVersion(layout)
	if currentErr != nil {
		t.Fatal(currentErr)
	}
	if version != "v1.0.0" {
		t.Fatalf("current version = %q", version)
	}
	metadata, metadataErr := install.ReadMetadata(layout.Metadata)
	if metadataErr != nil {
		t.Fatal(metadataErr)
	}
	if metadata.Version != "v1.0.0" {
		t.Fatalf("metadata version = %q", metadata.Version)
	}
	text := output.String()
	for _, expected := range []string{"rolling back", "Previous version restored", "Previous managed runtime restarted"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output missing %q: %s", expected, text)
		}
	}
}

func TestCoordinateUpdatedRuntimeReportsPreviousRestartFailure(t *testing.T) {
	layout := updateInstallLayout(t)
	installed := updateInstallVersions(t, layout)
	cmd := newRootCommand()
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true}}
	calls := 0
	restart := func(*cobra.Command, install.Layout, runtimeStatusResult) error {
		calls++
		if calls == 1 {
			return errors.New("new runtime unhealthy")
		}
		return errors.New("old runtime unhealthy")
	}
	err := coordinateUpdatedRuntimeWith(cmd, installed, state, false, restart)
	if err == nil || !strings.Contains(err.Error(), "previous runtime restart failed") || !strings.Contains(err.Error(), "old runtime unhealthy") {
		t.Fatalf("error = %v", err)
	}
}

func TestCoordinateUpdatedRuntimeReportsRollbackFailure(t *testing.T) {
	layout := updateInstallLayout(t)
	installed := updateInstallVersions(t, layout)
	installed.Activation.PreviousTarget = filepath.Join(t.TempDir(), "outside")
	cmd := newRootCommand()
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true}}
	restart := func(*cobra.Command, install.Layout, runtimeStatusResult) error {
		return errors.New("new runtime unhealthy")
	}
	err := coordinateUpdatedRuntimeWith(cmd, installed, state, false, restart)
	if err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestartManagedRuntimeAfterUpdateRejectsServiceMismatch(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	layout, err := install.NewLayout(filepath.Join(t.TempDir(), "install"), filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := newRootCommand()
	status := runtimeStatusResult{Managed: true, ConfigRoot: root, ServiceScope: string(managed.ScopeUser), ServiceID: "wrong"}
	if err := restartManagedRuntimeAfterUpdate(cmd, layout, status); err == nil || !strings.Contains(err.Error(), "service mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestSaveManagedEnvironmentUsesSelectedConfig(t *testing.T) {
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
	spec := managed.Spec{ConfigRoot: root, Account: managed.Account{HomeDir: t.TempDir()}}
	hash, err := saveManagedEnvironment(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(hash) == "" {
		t.Fatal("environment hash is empty")
	}
}

func updateInstallLayout(t *testing.T) install.Layout {
	t.Helper()
	root := filepath.Join(t.TempDir(), "install")
	layout, err := install.NewLayout(root, filepath.Join(root, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func updateInstallVersions(t *testing.T, layout install.Layout) install.Result {
	t.Helper()
	oldBinary := filepath.Join(t.TempDir(), "old")
	newBinary := filepath.Join(t.TempDir(), "new")
	if err := os.WriteFile(oldBinary, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBinary, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := install.Install(install.Options{Layout: layout, Version: "v1.0.0", Source: oldBinary}); err != nil {
		t.Fatal(err)
	}
	result, err := install.Install(install.Options{Layout: layout, Version: "v1.1.0", Source: newBinary})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
