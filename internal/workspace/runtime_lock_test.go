package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestRuntimeOwnershipRejectsSecondManager(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	first := NewManager(store)
	item, err := first.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Activate(); err != nil {
		t.Fatal(err)
	}
	defer first.Deactivate()

	second := NewManager(store)
	if err := second.Activate(); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("second activation error=%v", err)
	}
	diagnostics := first.RuntimeDiagnostics()
	if !diagnostics.Active || diagnostics.Owned != 1 || len(diagnostics.Workspaces) != 1 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	got := diagnostics.Workspaces[0]
	if got.WorkspaceID != item.ID || !got.Owned || !got.Locked || !got.Valid || got.LockPath != workspacestate.New(root).RuntimeLockPath() {
		t.Fatalf("workspace diagnostics=%#v", got)
	}

	if err := first.Deactivate(); err != nil {
		t.Fatal(err)
	}
	if err := second.Activate(); err != nil {
		t.Fatalf("second activation after release: %v", err)
	}
	if err := second.Deactivate(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeOwnershipDetectsLostIdentity(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer manager.Deactivate()

	if err := os.Remove(workspacestate.New(root).IdentityPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(item.ID); !errors.Is(err, ErrStateLost) {
		t.Fatalf("lost identity error=%v", err)
	}
}

func TestRuntimeOwnershipDetectsReplacedLockFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open locked file")
	}
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer manager.Deactivate()

	lockPath := workspacestate.New(root).RuntimeLockPath()
	oldPath := lockPath + ".old"
	if err := os.Rename(lockPath, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(item.ID); !errors.Is(err, ErrStateLost) {
		t.Fatalf("replaced lock error=%v", err)
	}
}

func TestSafeRegistryMutationDoesNotStealRuntimeOwnership(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	runtimeManager := NewManager(store)
	item, err := runtimeManager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeManager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer runtimeManager.Deactivate()

	writer := NewManager(store)
	if _, err := writer.CreateContainer("safe mutation"); err != nil {
		t.Fatalf("safe registry mutation failed: %v", err)
	}
	if _, err := runtimeManager.Get(item.ID); err != nil {
		t.Fatalf("runtime ownership changed after safe mutation: %v", err)
	}
	if err := runtimeManager.Reload(); err != nil {
		t.Fatalf("runtime reload after safe mutation: %v", err)
	}
	if diagnostics := runtimeManager.RuntimeDiagnostics(); diagnostics.Owned != 1 || !diagnostics.Workspaces[0].Owned {
		t.Fatalf("diagnostics after safe mutation=%#v", diagnostics)
	}
}

func TestExternalUnregisterAndRelocateRejectActiveWorkspace(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Manager, string, string) error
	}{
		{name: "unregister", run: func(manager *Manager, id, _ string) error {
			return manager.Unregister(id)
		}},
		{name: "relocate", run: func(manager *Manager, id, destination string) error {
			_, err := manager.Relocate(id, destination)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := filepath.Join(t.TempDir(), "workspaces.json")
			root := t.TempDir()
			owner := NewManager(store)
			item, err := owner.Register(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.Activate(); err != nil {
				t.Fatal(err)
			}
			defer owner.Deactivate()

			other := NewManager(store)
			if err := test.run(other, item.ID, t.TempDir()); !errors.Is(err, ErrAlreadyActive) {
				t.Fatalf("operation error=%v", err)
			}
			if _, err := other.Get(item.ID); err != nil {
				t.Fatalf("registry mutated despite rejection: %v", err)
			}
		})
	}
}

func TestConcurrentRegistryMutationsDoNotLoseRegistrations(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	roots := []string{t.TempDir(), t.TempDir()}
	managers := []*Manager{NewManager(store), NewManager(store)}
	errs := make(chan error, len(managers))
	var wg sync.WaitGroup
	for index := range managers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := managers[index].Register(roots[index])
			errs <- err
		}(index)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	items, err := NewManager(store).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("registrations lost: %#v", items)
	}
}

func TestRegistryMutationLockIsSeparateFromRuntimeLock(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if manager.registryMutationLockPath() == workspacestate.New(item.Path).RuntimeLockPath() {
		t.Fatal("registry mutation lock aliases workspace runtime lock")
	}
}
