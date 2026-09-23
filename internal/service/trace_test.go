package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/runtimecontrol"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type traceTestManager struct {
	installed bool
	running   bool
	pid       int
	matches   bool
}

func (m *traceTestManager) Backend() string                      { return "test-backend" }
func (m *traceTestManager) DefinitionMatches(Spec) (bool, error) { return m.matches, nil }
func (m *traceTestManager) Install(Spec) error                   { m.installed, m.matches = true, true; return nil }
func (m *traceTestManager) Start(Spec) error                     { m.running, m.pid = true, 4242; return nil }
func (m *traceTestManager) Stop(Spec) error                      { m.running, m.pid = false, 0; return nil }
func (m *traceTestManager) Uninstall(Spec) error                 { m.installed, m.matches = false, false; return nil }
func (m *traceTestManager) Status(Spec) (Status, error) {
	return Status{Installed: m.installed, Running: m.running, PID: m.pid, Backend: m.Backend()}, nil
}

func TestLifecycleEmitsBackendInspectionAndOperationFacts(t *testing.T) {
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	manager := &traceTestManager{}
	spec := Spec{ID: "cm-user-trace", Scope: ScopeUser, ConfigRoot: t.TempDir()}
	probeCalls := 0
	probe := func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
		probeCalls++
		if probeCalls == 1 {
			return runtimecontrol.RuntimeStatus{}, false, nil
		}
		return runtimecontrol.RuntimeStatus{PID: 4242, RunID: "run-new", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope), Lifecycle: "ready"}, true, nil
	}
	lifecycle := Lifecycle{Manager: manager, Spec: spec, Probe: probe, Shutdown: func(context.Context) error { return nil }, Timeout: time.Second}
	result, err := lifecycle.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Status.PID != 4242 {
		t.Fatalf("result=%#v", result)
	}
	for _, name := range []string{"service.runtime.inspect.started", "service.runtime.inspect.completed", "service.backend.inspect.started", "service.backend.inspect.completed", "service.definition.inspect.completed", "service.backend.install.completed", "service.backend.start.completed", "service.runtime.ready.wait.completed"} {
		if !serviceTraceContains(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	if !serviceTraceFieldEquals(events, "service.backend.inspect.completed", "installed", false) || !serviceTraceFieldEquals(events, "service.backend.inspect.completed", "backend", "test-backend") {
		t.Fatalf("backend inspection trace=%#v", events)
	}
	if !serviceTraceFieldEquals(events, "service.definition.inspect.completed", "definition_matches", false) {
		t.Fatalf("definition trace=%#v", events)
	}
	if !serviceTraceFieldEquals(events, "service.backend.start.completed", "installed", true) || !serviceTraceFieldEquals(events, "service.backend.start.completed", "running", true) || !serviceTraceFieldEquals(events, "service.backend.start.completed", "pid", 4242) {
		t.Fatalf("backend start trace=%#v", events)
	}
	for _, name := range []string{"service.backend.inspect.completed", "service.backend.install.completed", "service.backend.start.completed"} {
		if !serviceTraceHasField(events, name, "duration_ms") {
			t.Fatalf("%s missing duration: %#v", name, events)
		}
	}
}

func TestWaitRuntimeReadyEmitsOnlyMeaningfulProbeChanges(t *testing.T) {
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	spec := Spec{ID: "cm-user-trace", Scope: ScopeUser}
	responses := []struct {
		status  runtimecontrol.RuntimeStatus
		running bool
		err     error
	}{
		{err: errors.New("control unavailable")},
		{err: errors.New("control unavailable")},
		{err: errors.New("connection reset")},
		{status: runtimecontrol.RuntimeStatus{PID: 4200, RunID: "old", Lifecycle: "bootstrapping", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope)}, running: true},
		{status: runtimecontrol.RuntimeStatus{PID: 4201, RunID: "new", Lifecycle: "listeners_ready", Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope)}, running: true},
	}
	index := 0
	probe := func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
		if index >= len(responses) {
			return responses[len(responses)-1].status, true, nil
		}
		response := responses[index]
		index++
		return response.status, response.running, response.err
	}
	status, err := WaitRuntimeReady(ctx, spec, probe, "old", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.RunID != "new" {
		t.Fatalf("status=%#v", status)
	}
	if got := serviceTraceCount(events, "service.runtime.probe"); got != 1 {
		t.Fatalf("first probe events=%d want=1: %#v", got, events)
	}
	if got := serviceTraceCount(events, "service.runtime.probe-error.changed"); got != 2 {
		t.Fatalf("probe error change events=%d want=2: %#v", got, events)
	}
	if got := serviceTraceCount(events, "service.runtime.control.discovered"); got != 1 {
		t.Fatalf("control discovery events=%d want=1: %#v", got, events)
	}
	if got := serviceTraceCount(events, "service.runtime.lifecycle.changed"); got != 2 {
		t.Fatalf("lifecycle change events=%d want=2: %#v", got, events)
	}
	if !serviceTraceFieldEquals(events, "service.runtime.ready.wait.completed", "attempts", 5) || !serviceTraceHasField(events, "service.runtime.ready.wait.completed", "elapsed_ms") {
		t.Fatalf("readiness completion trace=%#v", events)
	}
}

func TestManagedEnvironmentTraceDoesNotLeakValues(t *testing.T) {
	const secret = "trace-environment-secret"
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	root := t.TempDir()
	snapshot := EnvironmentSnapshot{Version: environmentVersion, Values: map[string]string{"PATH": secret, "LANG": "C.UTF-8"}}
	hash, err := SaveEnvironmentContext(ctx, root, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("missing environment hash")
	}
	if !serviceTraceFieldEquals(events, "service.environment.persist.completed", "variable_count", 2) || !serviceTraceFieldEquals(events, "service.environment.persist.completed", "environment_hash", hash) || !serviceTraceHasField(events, "service.environment.persist.completed", "bytes") {
		t.Fatalf("environment trace=%#v", events)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("environment trace leaked value: %s", encoded)
	}
}

func TestManagedBinaryTraceReportsStagingAndReuse(t *testing.T) {
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "go-build-trace", "b001", "exe", "cm")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("trace-build"), 0755); err != nil {
		t.Fatal(err)
	}
	first, err := PrepareManagedBinaryContext(ctx, root, source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareManagedBinaryContext(ctx, root, source)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("first=%q second=%q", first, second)
	}
	if !serviceTraceFieldEquals(events, "service.binary.prepare.completed", "reused", false) || !serviceTraceFieldEqualsNth(events, "service.binary.prepare.completed", "reused", true, 2) {
		t.Fatalf("binary trace=%#v", events)
	}
}

func TestManagedCommandTraceRedactsSensitiveArgsAndRecordsExit(t *testing.T) {
	const secret = "trace-command-secret"
	events := []tracepkg.Event{}
	observer := func(event tracepkg.Event) { events = append(events, event) }
	if _, err := runCommandObserver(observer, "go", "env", "GOOS"); err != nil {
		t.Fatal(err)
	}
	_, _ = runCommandObserver(observer, "go", "env", "--api-key", secret)
	if !serviceTraceFieldEquals(events, "service.process.exec.completed", "exit_code", 0) || !serviceTraceHasField(events, "service.process.exec.completed", "duration_ms") {
		t.Fatalf("command success trace=%#v", events)
	}
	if !serviceTraceHasField(events, "service.process.exec.failed", "exit_code") || !serviceTraceHasField(events, "service.process.exec.failed", "duration_ms") {
		t.Fatalf("command failure trace=%#v", events)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("command trace leaked sensitive arg: %s", encoded)
	}
}

func serviceTraceContains(events []tracepkg.Event, name string) bool {
	return serviceTraceCount(events, name) > 0
}

func serviceTraceCount(events []tracepkg.Event, name string) int {
	count := 0
	for _, event := range events {
		if event.Name == name {
			count++
		}
	}
	return count
}

func serviceTraceFieldEquals(events []tracepkg.Event, name, key string, want any) bool {
	return serviceTraceFieldEqualsNth(events, name, key, want, 1)
}

func serviceTraceFieldEqualsNth(events []tracepkg.Event, name, key string, want any, occurrence int) bool {
	seen := 0
	for _, event := range events {
		if event.Name != name {
			continue
		}
		seen++
		if seen != occurrence {
			continue
		}
		for _, field := range event.Fields {
			if field.Key == key && field.Value == want {
				return true
			}
		}
	}
	return false
}

func serviceTraceHasField(events []tracepkg.Event, name, key string) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		for _, field := range event.Fields {
			if field.Key == key {
				return true
			}
		}
	}
	return false
}
