package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
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

func TestMergeWorkspaceStateHandlesArchiveSubtreeThroughCheckpointOwner(t *testing.T) {
	workspaceID := "ws_archive_merge"
	sourceRoot := t.TempDir()
	sourceStore := NewStore(t.TempDir())
	sourceStore.MaxCount = 1
	sourceStore.Retention = 365 * 24 * time.Hour
	file := filepath.Join(sourceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := sourceStore.Before(workspaceID, sourceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	secondID, err := sourceStore.Before(workspaceID, sourceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}

	destinationRoot := t.TempDir()
	outputBase := t.TempDir()
	outputRoot := filepath.Join(outputBase, "workspaces", workspaceID, "checkpoints")
	emptyRoot := filepath.Join(t.TempDir(), "empty")
	if err := MergeWorkspaceState(sourceStore.Path(workspaceID), emptyRoot, outputRoot, workspaceID, sourceRoot, destinationRoot); err != nil {
		t.Fatal(err)
	}

	merged := NewStore(outputBase)
	active, err := merged.List(workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != secondID || active[0].Files[0] != filepath.Join(destinationRoot, "file.txt") {
		t.Fatalf("merged active=%#v", active)
	}
	archived, err := merged.ListArchived(workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].Checkpoint.ID != firstID || archived[0].Checkpoint.Files[0] != filepath.Join(destinationRoot, "file.txt") {
		t.Fatalf("merged archive=%#v", archived)
	}
	if err := merged.validateArchivedPayload(workspaceID, archived[0].Checkpoint); err != nil {
		t.Fatalf("merged archive payload invalid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "archive", "index.json")); err != nil {
		t.Fatalf("typed archive index missing: %v", err)
	}
}

func TestMergeWorkspaceStateRejectsUnclassifiedArchiveEntry(t *testing.T) {
	workspaceID := "ws_archive_unsupported"
	sourceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 1
	file := filepath.Join(sourceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before(workspaceID, sourceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before(workspaceID, sourceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(store.archiveRoot(workspaceID), "foreign.bin")
	if err := os.WriteFile(foreign, []byte("unowned"), 0600); err != nil {
		t.Fatal(err)
	}
	err := MergeWorkspaceState(store.Path(workspaceID), filepath.Join(t.TempDir(), "empty"), filepath.Join(t.TempDir(), "out"), workspaceID, sourceRoot, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsupported checkpoint archive entry") {
		t.Fatalf("unclassified archive entry was accepted: %v", err)
	}
}
