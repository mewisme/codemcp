package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnoseLocalStateIsReadOnlyAndReportsHealth(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(registry)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(root, LocalDirName, "state", "fixture.json")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, filepath.Join(root, LocalDirName))
	diagnostic, err := manager.Diagnose(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Health != LocalStateHealthy || !diagnostic.Available || diagnostic.FileCount < 2 || diagnostic.SizeBytes <= 0 || !diagnostic.GitHygiene.Protected {
		t.Fatalf("diagnostic=%#v", diagnostic)
	}
	after := snapshotTree(t, filepath.Join(root, LocalDirName))
	if string(before) != string(after) {
		t.Fatalf("doctor mutated local state\nbefore=%s\nafter=%s", before, after)
	}
}

func TestDiagnoseReportsOwnedRuntimeLock(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	manager := NewManager(registry)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Deactivate(); err != nil {
			t.Errorf("deactivate: %v", err)
		}
	}()
	diagnostic, err := manager.Diagnose(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Health != LocalStateHealthy || !diagnostic.Locked || diagnostic.LockPID == 0 || diagnostic.LockPath == "" {
		t.Fatalf("diagnostic=%#v", diagnostic)
	}
}

func TestDiagnoseLegacyRegistryDoesNotMigrateOrCreateLocalState(t *testing.T) {
	configRoot := t.TempDir()
	root := t.TempDir()
	legacyID := instanceScopedWorkspaceID("inst_66666666666666666666666666666666", root)
	registry := filepath.Join(configRoot, "workspaces.json")
	writeRegistryVersion(t, registry, 4, Workspace{ID: legacyID, Path: root})
	before, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := NewManager(registry).Diagnose(context.Background(), legacyID)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Health != LocalStateCorrupt {
		t.Fatalf("diagnostic=%#v", diagnostic)
	}
	after, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("doctor migrated legacy registry")
	}
	if _, err := os.Stat(filepath.Join(root, LocalDirName)); !os.IsNotExist(err) {
		t.Fatalf("doctor created workspace-local state: %v", err)
	}
}

func TestDiagnoseUnavailableCorruptAndConflictingState(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "workspaces.json")
		root := t.TempDir()
		manager := NewManager(registry)
		item, err := manager.Register(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		diagnostic, err := manager.Diagnose(context.Background(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic.Health != LocalStateUnavailable || diagnostic.Available {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "workspaces.json")
		root := t.TempDir()
		manager := NewManager(registry)
		item, err := manager.Register(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, LocalDirName, "workspace.json"), []byte("{bad"), 0600); err != nil {
			t.Fatal(err)
		}
		diagnostic, err := manager.Diagnose(context.Background(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic.Health != LocalStateCorrupt {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "workspaces.json")
		root := t.TempDir()
		manager := NewManager(registry)
		item, err := manager.Register(root)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, LocalDirName, "workspace.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var marker map[string]any
		if err := json.Unmarshal(data, &marker); err != nil {
			t.Fatal(err)
		}
		marker["id"] = "ws_conflicting"
		data, err = json.Marshal(marker)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		diagnostic, err := manager.Diagnose(context.Background(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic.Health != LocalStateConflict {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
	})
}

func snapshotTree(t *testing.T, root string) []byte {
	t.Helper()
	var snapshot []byte
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot = append(snapshot, []byte(rel)...)
		snapshot = append(snapshot, 0)
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot = append(snapshot, data...)
		snapshot = append(snapshot, 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
