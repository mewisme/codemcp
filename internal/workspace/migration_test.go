package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func writeTestInstanceIdentity(t *testing.T, root, id string) {
	t.Helper()
	path := filepath.Join(root, "state", "instance.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 1, "identity": map[string]any{"id": id, "name": "test", "created_at": time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeRegistryVersion(t *testing.T, path string, version int, item Workspace) {
	t.Helper()
	data, err := json.MarshalIndent(struct {
		Version    int         `json:"version"`
		Workspaces []Workspace `json:"workspaces"`
	}{Version: version, Workspaces: []Workspace{item}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceIDsAreStableAcrossInstances(t *testing.T) {
	workspaceRoot := t.TempDir()
	rootA, rootB := t.TempDir(), t.TempDir()
	writeTestInstanceIdentity(t, rootA, "inst_11111111111111111111111111111111")
	writeTestInstanceIdentity(t, rootB, "inst_22222222222222222222222222222222")
	first, err := NewManager(filepath.Join(rootA, "workspaces.json")).Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(filepath.Join(rootB, "workspaces.json")).Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Path != second.Path {
		t.Fatalf("workspace ids are not locally stable: %#v %#v", first, second)
	}
	identity, err := workspacestate.New(workspaceRoot).LoadIdentity()
	if err != nil || identity.ID != first.ID {
		t.Fatalf("local identity=%#v err=%v", identity, err)
	}
}

func TestWorkspaceRegistryMigratesV2InstanceIDAndState(t *testing.T) {
	configRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	instanceID := "inst_33333333333333333333333333333333"
	oldID := instanceScopedWorkspaceID(instanceID, workspaceRoot)
	canonicalID := workspaceID(workspaceRoot)
	registryPath := filepath.Join(configRoot, "workspaces.json")
	writeRegistryVersion(t, registryPath, 2, Workspace{ID: oldID, Path: workspaceRoot, LegacyIDs: []string{canonicalID}})
	oldState := filepath.Join(configRoot, "workspaces", oldID)
	if err := os.MkdirAll(oldState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldState, "marker.txt"), []byte("v2"), 0600); err != nil {
		t.Fatal(err)
	}
	shellState, err := json.Marshal(map[string]any{"workspace_id": oldID, "cwd": workspaceRoot, "started_at": "x", "updated_at": "x", "recent_commands": []string{"pwd"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldState, "shell.json"), shellState, 0600); err != nil {
		t.Fatal(err)
	}
	manifestDir := filepath.Join(oldState, "checkpoints", "cp_test")
	if err := os.MkdirAll(manifestDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifestState, err := json.Marshal(map[string]any{"version": 1, "id": "cp_test", "workspace_id": oldID, "workspace_root": workspaceRoot, "created_at": "x", "tool": "edit_file", "summary": "test", "files": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.json"), manifestState, 0600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(registryPath)
	items, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != canonicalID || len(items[0].LegacyIDs) != 1 || items[0].LegacyIDs[0] != oldID {
		t.Fatalf("migrated workspace = %#v", items)
	}
	resolved, err := manager.Get(oldID)
	if err != nil || resolved.ID != canonicalID {
		t.Fatalf("v2 alias resolved to %#v err=%v", resolved, err)
	}
	if id, err := manager.CanonicalID(oldID); err != nil || id != canonicalID {
		t.Fatalf("canonical id = %q err=%v", id, err)
	}
	if _, err := os.Stat(oldState); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("v2 state still exists: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(configRoot, "workspaces", canonicalID, "marker.txt"))
	if err != nil || string(marker) != "v2" {
		t.Fatalf("migrated state marker = %q err=%v", marker, err)
	}
	for _, path := range []string{
		filepath.Join(configRoot, "workspaces", canonicalID, "shell.json"),
		filepath.Join(configRoot, "workspaces", canonicalID, "checkpoints", "cp_test", "manifest.json"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		if object["workspace_id"] != canonicalID {
			t.Fatalf("state workspace id in %s = %#v", path, object["workspace_id"])
		}
	}

	reloaded := NewManager(registryPath)
	resolved, err = reloaded.Get(oldID)
	if err != nil || resolved.ID != canonicalID {
		t.Fatalf("persisted v2 alias resolved to %#v err=%v", resolved, err)
	}
	var stored storeFile
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Version != storeVersion || len(stored.Workspaces) != 1 || stored.Workspaces[0].ID != canonicalID {
		t.Fatalf("stored registry = %#v", stored)
	}
}

func TestWorkspaceRegistryV1UpgradeKeepsStableID(t *testing.T) {
	configRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	canonicalID := workspaceID(workspaceRoot)
	registryPath := filepath.Join(configRoot, "workspaces.json")
	writeRegistryVersion(t, registryPath, 1, Workspace{ID: canonicalID, Path: workspaceRoot})
	manager := NewManager(registryPath)
	item, err := manager.Get(canonicalID)
	if err != nil || item.ID != canonicalID || len(item.LegacyIDs) != 0 {
		t.Fatalf("v1 workspace = %#v err=%v", item, err)
	}
	var stored storeFile
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Version != storeVersion || stored.Workspaces[0].ID != canonicalID {
		t.Fatalf("stored registry = %#v", stored)
	}
}

func TestLegacyAliasSupportsWorkspaceMutations(t *testing.T) {
	configRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	allowed := t.TempDir()
	oldID := instanceScopedWorkspaceID("inst_44444444444444444444444444444444", workspaceRoot)
	registryPath := filepath.Join(configRoot, "workspaces.json")
	writeRegistryVersion(t, registryPath, 2, Workspace{ID: oldID, Path: workspaceRoot})
	manager := NewManager(registryPath)
	item, err := manager.AddAllowDir(oldID, allowed)
	if err != nil || len(item.AllowDirs) != 1 {
		t.Fatalf("add allow dir = %#v err=%v", item, err)
	}
	if _, err := manager.RemoveAllowDir(oldID, allowed); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unregister(oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(oldID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy alias remained after unregister: %v", err)
	}
}

func TestWorkspaceMigrationRejectsConflictingStateDirectories(t *testing.T) {
	configRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	oldID := instanceScopedWorkspaceID("inst_55555555555555555555555555555555", workspaceRoot)
	canonicalID := workspaceID(workspaceRoot)
	registryPath := filepath.Join(configRoot, "workspaces.json")
	writeRegistryVersion(t, registryPath, 2, Workspace{ID: oldID, Path: workspaceRoot})
	for _, id := range []string{oldID, canonicalID} {
		if err := os.MkdirAll(filepath.Join(configRoot, "workspaces", id), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewManager(registryPath).List(); err == nil {
		t.Fatal("expected conflicting workspace state migration to fail")
	}
}
