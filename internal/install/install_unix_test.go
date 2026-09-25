//go:build !windows

package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestInstallLifecycle(t *testing.T) {
	layout := testLayout(t)
	source := testBinary(t, "release-v1")
	result, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyInstalled {
		t.Fatal("first install reported already installed")
	}
	if result.Canonical.State != CanonicalInstalled {
		t.Fatalf("install result = %+v", result)
	}
	assertCurrentVersion(t, layout, "v1.0.0")
	metadata, err := ReadMetadata(layout.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Method != MethodDirect || metadata.Version != "v1.0.0" || metadata.InstallDir != layout.Root || metadata.BinDir != layout.BinDir {
		t.Fatalf("metadata = %+v", metadata)
	}
	result, err = Install(Options{Layout: layout, Version: "v1.0.0", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyInstalled {
		t.Fatal("second install was not idempotent")
	}
}

func TestInstallDoesNotCreateAlias(t *testing.T) {
	layout := testLayout(t)
	source := testBinary(t, "release-v1")
	if _, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: source}); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(layout.BinDir, historicalAliasName())
	if _, err := os.Lstat(aliasPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("historical alias unexpectedly created: %v", err)
	}
	result, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyInstalled {
		t.Fatal("second install without alias was not idempotent")
	}
}

func TestInstallDevelopmentRequiresForce(t *testing.T) {
	layout := testLayout(t)
	source := testBinary(t, "0.0.1-dev")
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	if _, err := Install(Options{Context: ctx, Layout: layout, Version: "0.0.1-dev", Source: source}); !errors.Is(err, ErrDevelopmentBuild) {
		t.Fatalf("error = %v", err)
	}
	if !installTraceFields(events, "install.development-policy", map[string]any{"development_build": true, "force": false, "allowed": false}) {
		t.Fatalf("missing development policy trace: %#v", events)
	}
	if _, err := os.Stat(layout.Versions); !os.IsNotExist(err) {
		t.Fatalf("development refusal mutated install: %v", err)
	}
	if _, err := Install(Options{Layout: layout, Version: "0.0.1-dev", Source: source, Force: true}); err != nil {
		t.Fatalf("forced development install: %v", err)
	}
	assertCurrentVersion(t, layout, "0.0.1-dev")
}

func TestInstallNormalizesReleaseVersionPrefix(t *testing.T) {
	layout := testLayout(t)
	result, err := Install(Options{Layout: layout, Version: "1.2.3", Source: testBinary(t, "release")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v1.2.3" {
		t.Fatalf("version = %q", result.Version)
	}
	assertCurrentVersion(t, layout, "v1.2.3")
	metadata, err := ReadMetadata(layout.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Version != "v1.2.3" {
		t.Fatalf("metadata version = %q", metadata.Version)
	}
}

func TestInstallPreflightsCanonicalConflict(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BinDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.CanonicalBinary, []byte("unrelated"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: testBinary(t, "release-v1")})
	if !errors.Is(err, ErrCanonicalConflict) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(layout.Versions); !os.IsNotExist(err) {
		t.Fatalf("conflict preflight mutated versions: %v", err)
	}
}

func TestInstallEmitsDeepTrace(t *testing.T) {
	layout := testLayout(t)
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	result, err := Install(Options{Context: ctx, Layout: layout, Version: "v1.0.0", Source: testBinary(t, "release-v1")})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.apply.started", "install.development-policy", "install.source.validate.completed", "install.canonical.inspect.completed", "install.metadata.read.completed", "install.metadata.match", "install.stage.completed", "install.activate.completed", "install.canonical.install.completed", "install.metadata.write.completed", "install.apply.completed"} {
		found := false
		for _, event := range events {
			if event.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	if result.Staged.Binary == "" {
		t.Fatal("install result has no staged binary")
	}
	if !installTraceFields(events, "install.metadata.write.completed", map[string]any{"atomic": true}) || !installTraceFields(events, "install.metadata.match", map[string]any{"matches": false, "metadata_exists": false}) {
		t.Fatalf("missing install metadata facts: %#v", events)
	}
}

func TestInstallRollbackTraceReportsSubResults(t *testing.T) {
	layout := testLayout(t)
	source := testBinary(t, "release-v1")
	if _, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: source}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(layout.Root, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(layout.Root, 0755)
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	_, err := Install(Options{Context: ctx, Layout: layout, Version: "v1.0.0", Source: source})
	if err == nil {
		t.Skip("filesystem permissions did not force metadata write failure")
	}
	var rollback *tracepkg.Event
	for index := range events {
		if events[index].Name == "install.rollback.completed" || events[index].Name == "install.rollback.failed" {
			rollback = &events[index]
			break
		}
	}
	if rollback == nil {
		t.Fatalf("missing rollback trace: %#v", events)
	}
	for _, key := range []string{"activation_rollback_ok", "canonical_cleanup_attempted", "canonical_cleanup_ok", "duration_ms"} {
		if !installTraceEventHasField(*rollback, key) {
			t.Fatalf("rollback trace missing %q: %#v", key, *rollback)
		}
	}
}

func installTraceFields(events []tracepkg.Event, name string, expected map[string]any) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		values := map[string]any{}
		for _, field := range event.Fields {
			values[field.Key] = field.Value
		}
		matched := true
		for key, want := range expected {
			if values[key] != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func installTraceEventHasField(event tracepkg.Event, key string) bool {
	for _, field := range event.Fields {
		if field.Key == key {
			return true
		}
	}
	return false
}

func TestDefaultLayoutHonorsInstallEnvironment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	binDir := filepath.Join(t.TempDir(), "bin")
	t.Setenv(EnvInstallDir, root)
	t.Setenv(EnvBinDir, binDir)
	layout, err := DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	if layout.Root != root || layout.BinDir != binDir {
		t.Fatalf("layout = %+v", layout)
	}
}
