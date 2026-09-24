package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestWorkspaceLifecycleMetadataTracksFreshAndDirty(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	store := workspacestate.New(root)
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(project, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeStatus := Status{Enabled: true, Resolution: Resolution{Source: ExecutableSystem, Path: "/tmp/codegraph", Verified: true}}
	workspaceID := "ws_test"
	if err := RecordWorkspaceLifecycle(store, workspaceID, project, "project", "init", time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	status := InspectWorkspace(runtimeStatus, store, workspaceID, project, "project")
	if status.IndexState != IndexIndexed || status.Freshness != FreshnessFresh || status.LastInitializedAt == "" || status.Diagnostic != "" {
		t.Fatalf("fresh status=%#v", status)
	}
	if got, err := WorkspaceStatePath(store); err != nil || got != filepath.Join(root, ".cm", "state", "codegraph.json") {
		t.Fatalf("state path=%q err=%v", got, err)
	}
	if err := os.WriteFile(source, []byte("package main\nvar changed = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dirty := InspectWorkspace(runtimeStatus, store, workspaceID, project, "project")
	if dirty.Freshness != FreshnessDirty || dirty.DirtyReason != "source_changed" {
		t.Fatalf("dirty status=%#v", dirty)
	}
	if err := RecordWorkspaceLifecycle(store, workspaceID, project, "project", "sync", time.Unix(20, 0)); err != nil {
		t.Fatal(err)
	}
	synced := InspectWorkspace(runtimeStatus, store, workspaceID, project, "project")
	if synced.Freshness != FreshnessFresh || synced.LastSyncedAt == "" || synced.LastInitializedAt == "" {
		t.Fatalf("synced status=%#v", synced)
	}
}

func TestWorkspaceStatusTreatsUntrackedIndexAsDirty(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	status := InspectWorkspace(Status{}, workspacestate.New(root), "ws_test", project, "project")
	if status.IndexState != IndexIndexed || status.Freshness != FreshnessDirty || status.DirtyReason != "index_not_recorded" {
		t.Fatalf("status=%#v", status)
	}
}

func TestWorkspaceStatusReportsCorruptMetadataDiagnostic(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	store := workspacestate.New(root)
	statePath, err := WorkspaceStatePath(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	status := InspectWorkspace(Status{}, store, "ws_test", project, "project")
	if status.Freshness != FreshnessDirty || status.DirtyReason != "metadata_unavailable" || status.Diagnostic == "" {
		t.Fatalf("status=%#v", status)
	}
}

func TestWorkspaceMutationLockSerializesCallers(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	store := workspacestate.New(t.TempDir())
	first, err := AcquireWorkspaceMutationLock(store)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan *struct{}, 1)
	errs := make(chan error, 1)
	go func() {
		second, err := AcquireWorkspaceMutationLock(store)
		if err != nil {
			errs <- err
			return
		}
		defer second.Release()
		token := struct{}{}
		acquired <- &token
	}()

	select {
	case err := <-errs:
		t.Fatal(err)
	case <-acquired:
		t.Fatal("second CodeGraph mutation acquired workspace lock concurrently")
	case <-time.After(25 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case err := <-errs:
		t.Fatal(err)
	case <-acquired:
	case <-ctx.Done():
		t.Fatal("second CodeGraph mutation did not acquire workspace lock after release")
	}
}

func TestWorkspaceIndexMarkerRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "actual")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".codegraph")); err != nil {
		t.Fatal(err)
	}
	if InspectIndex(root) {
		t.Fatal("symlinked .codegraph marker accepted")
	}
}
