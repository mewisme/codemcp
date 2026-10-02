package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestRelocatePreservesStableIdentityContainerAndLocalState(t *testing.T) {
	manager := newTestManager(t)
	parent := t.TempDir()
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	if err := os.MkdirAll(filepath.Join(oldRoot, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	local := workspacestate.New(oldRoot)
	memory := filepath.Join(local.MemoryRoot(), "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memory), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memory, []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}

	relocated, err := manager.Relocate(item.ID, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if relocated.ID != item.ID || relocated.Path != canonicalRoot(newRoot) {
		t.Fatalf("relocated=%#v original=%#v", relocated, item)
	}
	updatedContainer, err := manager.GetContainer(container.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updatedContainer.WorkspaceIDs, []string{item.ID}) {
		t.Fatalf("container members=%#v", updatedContainer.WorkspaceIDs)
	}
	if data, err := os.ReadFile(filepath.Join(workspacestate.New(newRoot).MemoryRoot(), "MEMORY.md")); err != nil || string(data) != "state" {
		t.Fatalf("local state data=%q err=%v", data, err)
	}
}

func TestRelocateRejectsRegisteredDestination(t *testing.T) {
	manager := newTestManager(t)
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Relocate(first.ID, second.Path); err == nil {
		t.Fatal("expected registered destination to be rejected")
	}
}

func TestRelocateRejectsDestinationWithDifferentIdentity(t *testing.T) {
	manager := newTestManager(t)
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity("ws_other"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Relocate(item.ID, destination); err == nil {
		t.Fatal("expected identity mismatch")
	}
}

func TestRelocateRejectsCopiedLocalState(t *testing.T) {
	manager := newTestManager(t)
	source := t.TempDir()
	item, err := manager.Register(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Relocate(item.ID, destination); err == nil {
		t.Fatal("expected copied local state ambiguity")
	}
}

func TestRelocateRequiresDestinationIdentity(t *testing.T) {
	manager := newTestManager(t)
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Relocate(item.ID, t.TempDir()); err == nil {
		t.Fatal("expected missing destination identity to reject relocate")
	}
}

func TestRelocateRewritesAllowDirsUnderMovedRoot(t *testing.T) {
	manager := newTestManager(t)
	parent := t.TempDir()
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	nested := filepath.Join(oldRoot, "generated")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	item, err = manager.AddAllowDir(item.ID, nested)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	relocated, err := manager.Relocate(item.ID, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot(newRoot), "generated")
	if !reflect.DeepEqual(relocated.AllowDirs, []string{want}) {
		t.Fatalf("allow dirs=%#v want=%#v", relocated.AllowDirs, []string{want})
	}
}

func TestRelocateActiveWorkspaceAfterPhysicalMove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit moving a workspace tree with an open runtime lock")
	}
	manager := newTestManager(t)
	parent := t.TempDir()
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	if err := os.Mkdir(oldRoot, 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer manager.Deactivate()
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	relocated, err := manager.Relocate(item.ID, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if relocated.ID != item.ID || relocated.Path != canonicalRoot(newRoot) {
		t.Fatalf("relocated=%#v", relocated)
	}
	if _, err := manager.Get(item.ID); err != nil {
		t.Fatalf("active ownership did not follow moved state: %v", err)
	}
}

func TestRelocateRollsBackRegistryOnPersistenceFailure(t *testing.T) {
	manager := newTestManager(t)
	parent := t.TempDir()
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	if err := os.Mkdir(oldRoot, 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	previous := relocateRegistrySave
	relocateRegistrySave = func(*Manager) error { return errors.New("sentinel persistence failure") }
	t.Cleanup(func() { relocateRegistrySave = previous })
	if _, err := manager.Relocate(item.ID, newRoot); err == nil {
		t.Fatal("expected persistence failure")
	}
	manager.mu.RLock()
	got := manager.items[item.ID]
	manager.mu.RUnlock()
	if got.Path != item.Path {
		t.Fatalf("registry rollback path=%q want=%q", got.Path, item.Path)
	}
}
