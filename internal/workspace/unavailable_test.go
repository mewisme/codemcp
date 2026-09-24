package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestUnavailableRootKeepsStableRegistryIdentity(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	fresh := NewManager(store)
	items, err := fresh.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != item.ID || items[0].Available() || items[0].Error == "" {
		t.Fatalf("unavailable workspace=%#v", items)
	}
	if _, err := fresh.LocalState(item.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("local state error=%v", err)
	}
}

func TestCorruptIdentityStaysListedAsUnavailable(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacestate.New(root).IdentityPath(), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := NewManager(store)
	items, err := fresh.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != item.ID || items[0].Available() {
		t.Fatalf("corrupt workspace=%#v", items)
	}
}

func TestMissingIdentityIsNotSilentlyRecreated(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	identityPath := workspacestate.New(root).IdentityPath()
	if err := os.Remove(identityPath); err != nil {
		t.Fatal(err)
	}
	fresh := NewManager(store)
	got, err := fresh.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Available() {
		t.Fatalf("workspace with deleted identity became available: %#v", got)
	}
	if _, err := os.Stat(identityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity marker was silently recreated: %v", err)
	}
}

func TestUnavailableSiblingDoesNotBlockHealthyRuntime(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	manager := NewManager(store)
	missingRoot := t.TempDir()
	missing, err := manager.Register(missingRoot)
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(missingRoot); err != nil {
		t.Fatal(err)
	}
	runtime := NewManager(store)
	if err := runtime.Activate(); err != nil {
		t.Fatal(err)
	}
	defer runtime.Deactivate()
	diagnostics := runtime.RuntimeDiagnostics()
	if diagnostics.Owned != 1 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if _, err := runtime.Get(healthy.ID); err != nil {
		t.Fatalf("healthy workspace unavailable: %v", err)
	}
	item, err := runtime.Get(missing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Available() {
		t.Fatalf("missing workspace unexpectedly available: %#v", item)
	}
}

func TestUnavailableWorkspaceRejectsFilesystemMetadataMutation(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	fresh := NewManager(store)
	if _, err := fresh.AddAllowDir(item.ID, t.TempDir()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("allow dir error=%v", err)
	}
}

func TestPurgeMissingRootDeletesRegistryIdentity(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DeleteState(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged identity error=%v", err)
	}
}
