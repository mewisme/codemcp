package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestMergeWorkspaceStateRewritesCheckpointPathsAndRestoreRemainsValid(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	destinationRoot := t.TempDir()
	item, err := manager.Register(destinationRoot)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := t.TempDir()
	sourceCheckpointRoot := filepath.Join(t.TempDir(), "checkpoints")
	destinationCheckpointRoot := workspacestate.New(destinationRoot).CheckpointRoot()
	checkpointID := "cp_merge"
	sourcePath := filepath.Join(sourceRoot, "file.txt")
	destinationPath := filepath.Join(destinationRoot, "file.txt")
	if err := os.WriteFile(destinationPath, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		Version: indexVersion, ID: checkpointID, WorkspaceID: item.ID, WorkspaceRoot: sourceRoot,
		AllowedRoots: []string{sourceRoot}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Tool: "write_file", Summary: "before edit",
		Files: []FileSnapshot{{
			Path: sourcePath, Existed: true, Mode: 0600, Encoding: "utf-8", Content: "before", Size: 6,
		}},
	}
	checkpointDir := filepath.Join(sourceCheckpointRoot, "data", checkpointID)
	if err := os.MkdirAll(checkpointDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeStructuredAtomic(filepath.Join(checkpointDir, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeStructuredAtomic(filepath.Join(sourceCheckpointRoot, "index.json"), Index{
		Version: indexVersion, Checkpoints: []Summary{buildSummary(manifest)},
	}, 0600); err != nil {
		t.Fatal(err)
	}

	if err := MergeWorkspaceState(sourceCheckpointRoot, filepath.Join(t.TempDir(), "empty"), destinationCheckpointRoot, item.ID, sourceRoot, destinationRoot); err != nil {
		t.Fatal(err)
	}
	store := NewWorkspaceStore(t.TempDir(), manager)
	preview, err := store.PreviewRestore(item.ID, destinationRoot, checkpointID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Path != destinationPath {
		t.Fatalf("preview=%#v", preview)
	}
	if _, err := store.Restore(item.ID, destinationRoot, checkpointID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destinationPath)
	if err != nil || string(data) != "before" {
		t.Fatalf("restored data=%q err=%v", data, err)
	}
}
