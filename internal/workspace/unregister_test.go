package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnregisterRemovesOnlyRegistryEntry(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	local, err := manager.LocalState(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	memoryFile := filepath.Join(local.MemoryRoot(), "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memoryFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memoryFile, []byte("local-state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unregister(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(item.ID); err == nil {
		t.Fatal("workspace still registered")
	}
	if data, err := os.ReadFile(memoryFile); err != nil || string(data) != "local-state" {
		t.Fatalf("local workspace state changed after unregister: data=%q err=%v", data, err)
	}
	if _, err := manager.Register(root); err != nil {
		t.Fatalf("project directory was affected: %v", err)
	}
}
