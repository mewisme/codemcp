package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectRegistryReadsWithoutChangingRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	manager := NewManager(path)
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	inspection, err := manager.InspectRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Exists || inspection.Version != storeVersion || len(inspection.Workspaces) != 1 || inspection.Workspaces[0].ID != first.ID {
		t.Fatalf("inspection=%#v", inspection)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("registry inspection mutated persistent state")
	}
}

func TestInspectRegistryAllowsUninitializedStore(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "missing.json"))
	inspection, err := manager.InspectRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Exists || len(inspection.Workspaces) != 0 || inspection.Containers != 0 {
		t.Fatalf("inspection=%#v", inspection)
	}
}
