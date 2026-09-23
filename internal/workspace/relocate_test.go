package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestRelocatePreservesLegacyIDContainersAndState(t *testing.T) {
	storeRoot := t.TempDir()
	store := filepath.Join(storeRoot, "workspaces.json")
	oldRoot := filepath.Join(t.TempDir(), "old")
	newRoot := filepath.Join(t.TempDir(), "new")
	if err := os.MkdirAll(filepath.Join(oldRoot, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	oldPlaceholder := filepath.Join(t.TempDir(), "registered")
	if err := os.MkdirAll(oldPlaceholder, 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(oldPlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	oldID := item.ID
	container, err := manager.CreateContainer("group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, oldID); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(storeRoot, "workspaces", oldID)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, "shell"+configformat.ExtensionForRoot(storeRoot))
	stateValue := map[string]any{"workspace_id": oldID, "cwd": filepath.Join(oldPlaceholder, "nested"), "paths": []any{oldPlaceholder, filepath.Join(oldPlaceholder, "file.txt")}}
	data, err := configformat.MarshalPath(statePath, stateValue)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(oldPlaceholder); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(newRoot, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	relocated, err := manager.Relocate(oldID, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if relocated.ID == oldID || relocated.Path != canonicalRoot(newRoot) || !containsString(relocated.LegacyIDs, oldID) {
		t.Fatalf("relocated=%#v", relocated)
	}
	if resolved, err := manager.Get(oldID); err != nil || resolved.ID != relocated.ID {
		t.Fatalf("legacy lookup=%#v err=%v", resolved, err)
	}
	updatedContainer, err := manager.GetContainer(container.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updatedContainer.WorkspaceIDs, []string{relocated.ID}) {
		t.Fatalf("container members=%#v", updatedContainer.WorkspaceIDs)
	}
	newStatePath := filepath.Join(storeRoot, "workspaces", relocated.ID, filepath.Base(statePath))
	encoded, err := os.ReadFile(newStatePath)
	if err != nil {
		t.Fatal(err)
	}
	format, err := configformat.Detect(newStatePath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := configformat.DecodeGeneric(format, encoded)
	if err != nil {
		t.Fatal(err)
	}
	stateMap := decoded.(map[string]any)
	if stateMap["workspace_id"] != relocated.ID || stateMap["cwd"] != filepath.Join(canonicalRoot(newRoot), "nested") {
		t.Fatalf("state=%#v", stateMap)
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

func TestRelocateRejectsDestinationReservedByLegacyAlias(t *testing.T) {
	manager := newTestManager(t)
	legacyRoot := t.TempDir()
	first, err := manager.Register(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	currentRoot := t.TempDir()
	current, err := manager.Relocate(first.ID, currentRoot)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID == first.ID {
		t.Fatal("expected first relocation to change workspace id")
	}
	other, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Relocate(other.ID, legacyRoot); err == nil {
		t.Fatal("expected destination legacy alias collision to be rejected")
	}
}

func TestRelocateRewritesAllowDirsUnderOldRoot(t *testing.T) {
	manager := newTestManager(t)
	oldRoot := t.TempDir()
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
	newRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(newRoot, "generated"), 0755); err != nil {
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

func TestRelocateRollsBackStateRewriteWhenStateRenameFails(t *testing.T) {
	storeRoot := t.TempDir()
	manager := NewManager(filepath.Join(storeRoot, "workspaces.json"))
	oldRoot := t.TempDir()
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(storeRoot, "workspaces", item.ID)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, "shell"+configformat.ExtensionForRoot(storeRoot))
	data, err := configformat.MarshalPath(statePath, map[string]any{"workspace_id": item.ID, "cwd": filepath.Join(oldRoot, "nested")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	previousRename := relocateStateRename
	relocateStateRename = func(string, string) error { return errors.New("sentinel rename failure") }
	t.Cleanup(func() { relocateStateRename = previousRename })
	if _, err := manager.Relocate(item.ID, newRoot); err == nil {
		t.Fatal("expected state rename failure")
	}
	encoded, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	format, err := configformat.Detect(statePath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := configformat.DecodeGeneric(format, encoded)
	if err != nil {
		t.Fatal(err)
	}
	stateMap := decoded.(map[string]any)
	if stateMap["workspace_id"] != item.ID || stateMap["cwd"] != filepath.Join(item.Path, "nested") {
		t.Fatalf("state was not rolled back: %#v", stateMap)
	}
}

func TestRelocateSkipsMalformedCheckpointManifest(t *testing.T) {
	storeRoot := t.TempDir()
	manager := NewManager(filepath.Join(storeRoot, "workspaces.json"))
	oldRoot := t.TempDir()
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestDir := filepath.Join(storeRoot, "workspaces", item.ID, "checkpoints", "data", "cp_broken")
	if err := os.MkdirAll(manifestDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(manifestDir, "manifest"+configformat.ExtensionForRoot(storeRoot))
	broken := []byte("version: 1\nfiles:\n  - content: first\n      broken: value\n")
	if err := os.WriteFile(manifestPath, broken, 0600); err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	relocated, err := manager.Relocate(item.ID, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	movedManifest := filepath.Join(storeRoot, "workspaces", relocated.ID, "checkpoints", "data", "cp_broken", filepath.Base(manifestPath))
	got, err := os.ReadFile(movedManifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, broken) {
		t.Fatalf("malformed checkpoint manifest changed during relocate:\n%s", got)
	}
}

func TestRelocateStillRejectsMalformedCoreState(t *testing.T) {
	storeRoot := t.TempDir()
	manager := NewManager(filepath.Join(storeRoot, "workspaces.json"))
	oldRoot := t.TempDir()
	item, err := manager.Register(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(storeRoot, "workspaces", item.ID)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, "shell"+configformat.ExtensionForRoot(storeRoot))
	if err := os.WriteFile(statePath, []byte("cwd: first\n  broken: value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Relocate(item.ID, t.TempDir()); err == nil {
		t.Fatal("expected malformed core workspace state to reject relocate")
	}
}
