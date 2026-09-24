package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteStateRemovesOnlyLocalCMAndUnregisters(t *testing.T) {
	manager := newTestManager(t)
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(root, "project.txt")
	if err := os.WriteFile(projectFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	localFile := filepath.Join(LocalDir(root), "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(localFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localFile, []byte("delete"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DeleteState(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("workspace still registered: %v", err)
	}
	if _, err := os.Stat(LocalDir(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local state still exists: %v", err)
	}
	if data, err := os.ReadFile(projectFile); err != nil || string(data) != "keep" {
		t.Fatalf("project file data=%q err=%v", data, err)
	}
}

func TestDeleteStateRejectsSymlinkedLocalRoot(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, LocalDir(root)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manager := newTestManager(t)
	if _, err := manager.DeleteState(root); err == nil {
		t.Fatal("symlinked .cm purge was accepted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("outside target changed: data=%q err=%v", data, err)
	}
}

func TestDeleteStateDoesNotFollowNestedSymlink(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	manager := newTestManager(t)
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(LocalDir(root), "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := manager.DeleteState(item.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("nested symlink target changed: data=%q err=%v", data, err)
	}
}

func TestDeleteStateFromOwningRuntimeSucceeds(t *testing.T) {
	manager := newTestManager(t)
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer manager.Deactivate()
	if _, err := manager.DeleteState(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LocalDir(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local state still exists: %v", err)
	}
}

func TestDeleteStateRejectsOtherRuntimeOwnership(t *testing.T) {
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
	if _, err := other.DeleteState(item.ID); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("purge error=%v", err)
	}
	if _, err := os.Stat(LocalDir(root)); err != nil {
		t.Fatalf("local state removed despite ownership conflict: %v", err)
	}
}
