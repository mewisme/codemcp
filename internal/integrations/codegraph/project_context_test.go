package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestProjectContextProjectionTracksEffectiveAvailability(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	executable := testExecutable(t, "codegraph")

	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}

	providerFor := func(runtime *Runtime) func(context.Context, string, string) (projectcontext.IntegrationProjection, error) {
		return ProjectContextProjectionProvider(func() (*Runtime, error) { return runtime, nil }, manager)
	}

	disabled, err := providerFor(New(Options{Enabled: false}))(t.Context(), item.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.Instructions) != 0 || len(disabled.Diagnostics) != 0 {
		t.Fatalf("disabled projection=%#v", disabled)
	}

	unavailableRuntime := New(Options{Enabled: true, ManagedRoot: t.TempDir()})
	unavailableRuntime.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	unavailableRuntime.goos, unavailableRuntime.goarch = "unsupported", "unsupported"
	unavailable, err := providerFor(unavailableRuntime)(t.Context(), item.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(unavailable.Instructions) != 0 || len(unavailable.Diagnostics) != 1 || unavailable.Diagnostics[0].State != "unavailable" {
		t.Fatalf("unavailable projection=%#v", unavailable)
	}

	invalidRuntime := New(Options{Enabled: true, ConfiguredPath: filepath.Join(t.TempDir(), "missing-codegraph")})
	invalid, err := providerFor(invalidRuntime)(t.Context(), item.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid.Instructions) != 0 || len(invalid.Diagnostics) != 1 || invalid.Diagnostics[0].State != "unavailable" {
		t.Fatalf("invalid configured executable projection=%#v", invalid)
	}

	runtime := New(Options{Enabled: true, ConfiguredPath: executable})
	uninitialized, err := providerFor(runtime)(t.Context(), item.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(uninitialized.Instructions) != 0 || len(uninitialized.Diagnostics) != 1 || uninitialized.Diagnostics[0].State != "uninitialized" {
		t.Fatalf("uninitialized projection=%#v", uninitialized)
	}

	if err := os.MkdirAll(filepath.Join(project, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	stale, err := providerFor(runtime)(t.Context(), item.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale.Instructions) != 1 || len(stale.Diagnostics) != 1 || stale.Diagnostics[0].State != "stale" {
		t.Fatalf("stale projection=%#v", stale)
	}
	guidance := stale.Instructions[0].Content
	for _, expected := range []string{"codegraph_explore", "architecture", "dependency", "precise workspace file reads", "serve --mcp"} {
		if !strings.Contains(guidance, expected) {
			t.Fatalf("guidance missing %q: %q", expected, guidance)
		}
	}

	store, err := manager.LocalState(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(item.Path, project)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordWorkspaceLifecycle(store, item.ID, project, relative, "sync", time.Now()); err != nil {
		t.Fatal(err)
	}
	fresh, err := providerFor(runtime)(t.Context(), item.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Instructions) != 1 || len(fresh.Diagnostics) != 0 {
		t.Fatalf("fresh projection=%#v", fresh)
	}
}

func TestProjectContextProjectionDoesNotAdvertiseOutsideRegisteredWorkspace(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	outside := t.TempDir()
	manager := workspace.NewManagerWithGlobalAllowDirs(filepath.Join(t.TempDir(), "workspaces.json"), []string{outside})
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	runtime := New(Options{Enabled: true, ConfiguredPath: testExecutable(t, "codegraph")})
	provider := ProjectContextProjectionProvider(func() (*Runtime, error) { return runtime, nil }, manager)
	projection, err := provider(t.Context(), item.ID, outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Instructions) != 0 || len(projection.Diagnostics) != 0 {
		t.Fatalf("outside projection=%#v", projection)
	}
}
