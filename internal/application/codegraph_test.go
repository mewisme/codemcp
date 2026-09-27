package application

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestCodeGraphOperationsUseCanonicalDispatcher(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Integrations.CodeGraph.Enabled = false
	service := &CodeGraphService{LoadConfig: func() (config.Config, error) { return cfg, nil }}
	dispatcher := NewDispatcher()
	if err := BindCodeGraphOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capability.ID{
		capability.IntegrationCodeGraphStatus,
		capability.IntegrationCodeGraphProbe,
		capability.IntegrationCodeGraphInstall,
		capability.IntegrationCodeGraphWorkspaceStatus,
		capability.IntegrationCodeGraphWorkspaceInit,
		capability.IntegrationCodeGraphWorkspaceSync,
	} {
		if dispatcher.handlers[id] == nil {
			t.Fatalf("operation %s is not bound", id)
		}
		if _, ok := capability.Lookup(id); !ok {
			t.Fatalf("operation %s is not canonical", id)
		}
	}

	result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: capability.IntegrationCodeGraphStatus})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := result.Value.(codegraph.Status)
	if !ok || status.Enabled || status.Resolution.Source != codegraph.ExecutableDisabled || status.PinnedVersion != codegraph.Version {
		t.Fatalf("status=%#v", result.Value)
	}
}

func TestCodeGraphServiceProbesSystemSymlinkThroughCanonicalRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink fixture")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	target := filepath.Join(t.TempDir(), "codegraph-real")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho 1.6.0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	binRoot := t.TempDir()
	link := filepath.Join(binRoot, "codegraph")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("PATH", binRoot)
	cfg := config.Default()
	cfg.Integrations.CodeGraph.Enabled = true
	cfg.Integrations.CodeGraph.Path = ""
	service := &CodeGraphService{LoadConfig: func() (config.Config, error) { return cfg, nil }}

	result, err := service.Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Resolution.Source != codegraph.ExecutableSystem || result.Path != target || result.Version != "1.6.0" {
		t.Fatalf("probe=%#v", result)
	}
}

func TestCodeGraphWorkspaceLifecyclePersistsSharedReadModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	serviceA, serviceB, item, project, source := newCodeGraphWorkspaceServices(t)
	first := NewDispatcher()
	second := NewDispatcher()
	if err := BindCodeGraphOperations(first, serviceA); err != nil {
		t.Fatal(err)
	}
	if err := BindCodeGraphOperations(second, serviceB); err != nil {
		t.Fatal(err)
	}
	input := CodeGraphWorkspaceInput{WorkspaceID: item.ID, Path: "project"}

	initResult, err := first.Dispatch(t.Context(), DispatchRequest{Operation: capability.IntegrationCodeGraphWorkspaceInit, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	initAction, ok := initResult.Value.(CodeGraphWorkspaceActionResult)
	if !ok || initAction.Status.IndexState != codegraph.IndexIndexed || initAction.Status.Freshness != codegraph.FreshnessFresh {
		t.Fatalf("init result=%#v", initResult.Value)
	}
	statePath := filepath.Join(item.Path, ".cm", "state", "codegraph.json")
	if info, err := os.Lstat(statePath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("workspace state path=%s info=%v err=%v", statePath, info, err)
	}

	statusResult, err := second.Dispatch(t.Context(), DispatchRequest{Operation: capability.IntegrationCodeGraphWorkspaceStatus, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	status := statusResult.Value.(codegraph.WorkspaceStatus)
	if status.Freshness != codegraph.FreshnessFresh || status.WorkspaceID != item.ID || status.ProjectPath != project {
		t.Fatalf("shared status=%#v", status)
	}
	if err := os.WriteFile(source, []byte("package main\nvar changed = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	statusResult, err = first.Dispatch(t.Context(), DispatchRequest{Operation: capability.IntegrationCodeGraphWorkspaceStatus, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	dirty := statusResult.Value.(codegraph.WorkspaceStatus)
	if dirty.Freshness != codegraph.FreshnessDirty || dirty.DirtyReason != "source_changed" {
		t.Fatalf("dirty status=%#v", dirty)
	}

	syncResult, err := second.Dispatch(t.Context(), DispatchRequest{Operation: capability.IntegrationCodeGraphWorkspaceSync, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	syncAction := syncResult.Value.(CodeGraphWorkspaceActionResult)
	if syncAction.Status.Freshness != codegraph.FreshnessFresh || syncAction.Status.LastSyncedAt == "" {
		t.Fatalf("sync result=%#v", syncAction)
	}
}

func TestConcurrentCodeGraphSyncIsSerializedAcrossServiceInstances(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	serviceA, serviceB, item, _, source := newCodeGraphWorkspaceServices(t)
	input := CodeGraphWorkspaceInput{WorkspaceID: item.ID, Path: "project"}
	if _, err := serviceA.InitWorkspace(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package main\nvar dirty = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	type result struct {
		value CodeGraphWorkspaceActionResult
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	run := func(service *CodeGraphService) {
		defer wg.Done()
		value, err := service.SyncWorkspace(t.Context(), input)
		results <- result{value: value, err: err}
	}
	go run(serviceA)
	go run(serviceB)
	wg.Wait()
	close(results)
	skipped := 0
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.value.Skipped {
			skipped++
		}
		if got.value.Status.Freshness != codegraph.FreshnessFresh {
			t.Fatalf("sync status=%#v", got.value.Status)
		}
	}
	if skipped != 1 {
		t.Fatalf("expected one serialized sync to skip, skipped=%d", skipped)
	}
}

func TestCodeGraphWorkspaceRejectsUnregisteredAndEscapingTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	service, _, item, _, _ := newCodeGraphWorkspaceServices(t)
	if _, err := service.WorkspaceStatus(t.Context(), CodeGraphWorkspaceInput{WorkspaceID: "ws_missing"}); err == nil {
		t.Fatal("unregistered workspace accepted")
	}
	outside := t.TempDir()
	if _, err := service.WorkspaceStatus(t.Context(), CodeGraphWorkspaceInput{WorkspaceID: item.ID, Path: outside}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "escapes workspace") {
		t.Fatalf("outside workspace err=%v", err)
	}
}

func TestConcurrentCodeGraphInitIsSerializedAcrossServiceInstances(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	serviceA, serviceB, item, project, _ := newCodeGraphWorkspaceServices(t)
	input := CodeGraphWorkspaceInput{WorkspaceID: item.ID, Path: "project"}
	type result struct {
		value CodeGraphWorkspaceActionResult
		err   error
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(2)
	run := func(service *CodeGraphService) {
		defer start.Done()
		value, err := service.InitWorkspace(t.Context(), input)
		results <- result{value: value, err: err}
	}
	go run(serviceA)
	go run(serviceB)
	start.Wait()
	close(results)
	skipped := 0
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.value.Skipped {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("expected one serialized init to skip, skipped=%d", skipped)
	}
	data, err := os.ReadFile(filepath.Join(project, "init-count"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "init") != 1 {
		t.Fatalf("init command ran more than once: %q", data)
	}
}

func newCodeGraphWorkspaceServices(t *testing.T) (*CodeGraphService, *CodeGraphService, workspace.Workspace, string, string) {
	t.Helper()
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	manager := workspace.NewManager(filepath.Join(configRoot, "workspaces.json"))
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(project, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable := writeCodeGraphLifecycleFixture(t)
	cfg := config.Default()
	cfg.Integrations.CodeGraph.Enabled = true
	cfg.Integrations.CodeGraph.Path = executable
	load := func() (config.Config, error) { return cfg, nil }
	nowA := time.Unix(100, 0)
	nowB := time.Unix(200, 0)
	return &CodeGraphService{LoadConfig: load, Workspaces: manager, Now: func() time.Time { return nowA }},
		&CodeGraphService{LoadConfig: load, Workspaces: manager, Now: func() time.Time { return nowB }},
		item, project, source
}

func writeCodeGraphLifecycleFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codegraph")
	script := `#!/bin/sh
case "$1" in
  --version)
    printf 'codegraph 1.6.0\n'
    ;;
  init)
    project="$3"
    mkdir -p "$project/.codegraph"
    printf 'init\n' >> "$project/init-count"
    printf 'initialized\n'
    ;;
  sync)
    project="$2"
    mkdir -p "$project/.codegraph"
    printf 'sync\n' >> "$project/.codegraph/sync-count"
    printf 'synced\n'
    ;;
  *)
    printf 'unexpected command\n' >&2
    exit 9
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
