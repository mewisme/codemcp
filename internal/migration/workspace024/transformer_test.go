package workspace024

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/checkpoint"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestTransformReleasedWorkspaceStateIdempotentAndTyped(t *testing.T) {
	input, source := releasedFixture(t)
	result, err := Transform(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyApplied {
		t.Fatal("first transform reported already applied")
	}
	if result.Manifest.SourceRelease != SourceRelease || result.Manifest.SourceSHA256 == "" {
		t.Fatalf("manifest=%#v", result.Manifest)
	}

	memory, err := os.ReadFile(filepath.Join(input.Destination, "memory", "MEMORY.md"))
	if err != nil || string(memory) != "# Memory\nkeep me\n" {
		t.Fatalf("memory=%q err=%v", memory, err)
	}
	var shell shellruntime.SessionState
	readJSON(t, filepath.Join(input.Destination, "state", "shell.json"), &shell)
	if shell.Version != 1 || shell.WorkspaceID != input.TargetWorkspaceID || shell.CWD != input.WorkspaceRoot {
		t.Fatalf("shell=%#v", shell)
	}
	var manifest checkpoint.Manifest
	readJSON(t, filepath.Join(input.Destination, "checkpoints", "data", "cp_one", "manifest.json"), &manifest)
	if manifest.WorkspaceID != input.TargetWorkspaceID || manifest.WorkspaceRoot != input.WorkspaceRoot {
		t.Fatalf("checkpoint manifest=%#v", manifest)
	}
	if _, err := os.Stat(filepath.Join(source, "MEMORY.md")); err != nil {
		t.Fatalf("source mutated: %v", err)
	}

	again, err := Transform(input)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyApplied || again.Manifest.SourceSHA256 != result.Manifest.SourceSHA256 {
		t.Fatalf("second=%#v", again)
	}
}

func TestTransformDetectsChangedSourceAndDestinationConflicts(t *testing.T) {
	input, source := releasedFixture(t)
	if _, err := Transform(input); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "MEMORY.md"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(input); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("changed source err=%v", err)
	}

	input2, _ := releasedFixture(t)
	if _, err := Transform(input2); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input2.Destination, "unexpected.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(input2); err == nil || !strings.Contains(err.Error(), "unexpected file") {
		t.Fatalf("extra output err=%v", err)
	}
}

func TestTransformRollbackSafeOnInvalidReleasedState(t *testing.T) {
	input, source := releasedFixture(t)
	if err := os.WriteFile(filepath.Join(source, "shell.json"), []byte(`{"workspace_id":"ws_other","cwd":"/tmp","started_at":"x","updated_at":"x","recent_commands":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Transform(input)
	if err == nil || !strings.Contains(err.Error(), "unexpected workspace") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(input.Destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after failure: %v", statErr)
	}
	entries, readErr := os.ReadDir(filepath.Dir(input.Destination))
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".workspace024-") {
			t.Fatalf("staging residue: %s", entry.Name())
		}
	}
}

func TestTransformSkipsOversizedLegacyCheckpointWithoutDroppingOtherHistory(t *testing.T) {
	input, source := releasedFixture(t)
	indexPath := filepath.Join(source, "checkpoints", "index.json")
	var index checkpoint.Index
	readJSON(t, indexPath, &index)
	index.Checkpoints = append(index.Checkpoints, checkpoint.Summary{ID: "cp_large", CreatedAt: "2026-01-02T00:00:00Z", Tool: "edit_file", Summary: "oversized"})
	writeJSONFixture(t, indexPath, index)

	largeRoot := filepath.Join(source, "checkpoints", "data", "cp_large")
	if err := os.MkdirAll(filepath.Join(largeRoot, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	largeManifest := filepath.Join(largeRoot, "manifest.json")
	file, err := os.OpenFile(largeManifest, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxStructuredBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(largeRoot, "blobs", "ignored.bin"), []byte("retained in rollback source"), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := Transform(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Manifest.SkippedCheckpoints, []string{"cp_large"}) {
		t.Fatalf("skipped checkpoints=%#v", result.Manifest.SkippedCheckpoints)
	}
	var migrated checkpoint.Index
	readJSON(t, filepath.Join(input.Destination, "checkpoints", "index.json"), &migrated)
	if len(migrated.Checkpoints) != 1 || migrated.Checkpoints[0].ID != "cp_one" {
		t.Fatalf("migrated checkpoint index=%#v", migrated)
	}
	if _, err := os.Stat(filepath.Join(input.Destination, "checkpoints", "data", "cp_large")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized checkpoint was copied: %v", err)
	}
	if info, err := os.Stat(largeManifest); err != nil || info.Size() != maxStructuredBytes+1 {
		t.Fatalf("rollback source changed: info=%v err=%v", info, err)
	}
}

func TestTransformRejectsUnknownStateAndLiveWorkspaceDestination(t *testing.T) {
	input, source := releasedFixture(t)
	if err := os.WriteFile(filepath.Join(source, "unknown.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(input); err == nil || !strings.Contains(err.Error(), "unsupported released workspace state entry") {
		t.Fatalf("unknown err=%v", err)
	}

	input, _ = releasedFixture(t)
	input.Destination = filepath.Join(input.WorkspaceRoot, ".cm", "migration-stage")
	if _, err := Transform(input); err == nil || !strings.Contains(err.Error(), "external staging") {
		t.Fatalf("live destination err=%v", err)
	}
}

func releasedFixture(t *testing.T) (Input, string) {
	t.Helper()
	configRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	stageRoot := t.TempDir()
	sourceID := "ws_source024"
	legacyID := "ws_legacy024"
	targetID := "ws_targetlocal"
	source := filepath.Join(configRoot, "workspaces", sourceID)
	if err := os.MkdirAll(filepath.Join(source, "checkpoints", "data", "cp_one", "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "MEMORY.md"), []byte("# Memory\nkeep me\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeJSONFixture(t, filepath.Join(source, "shell.json"), map[string]any{
		"workspace_id": legacyID, "cwd": workspaceRoot, "started_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z", "recent_commands": []string{"pwd"},
	})
	writeJSONFixture(t, filepath.Join(source, "checkpoints", "index.json"), checkpoint.Index{Version: 1, Checkpoints: []checkpoint.Summary{{ID: "cp_one", CreatedAt: "2026-01-01T00:00:00Z", Tool: "edit_file", Summary: "fixture"}}})
	writeJSONFixture(t, filepath.Join(source, "checkpoints", "data", "cp_one", "manifest.json"), checkpoint.Manifest{
		Version: 1, ID: "cp_one", WorkspaceID: legacyID, WorkspaceRoot: workspaceRoot, CreatedAt: "2026-01-01T00:00:00Z", Tool: "edit_file", Summary: "fixture", Files: []checkpoint.FileSnapshot{{Path: filepath.Join(workspaceRoot, "artifact.bin"), Existed: true, Blob: "blobs/blob.bin"}},
	})
	if err := os.WriteFile(filepath.Join(source, "checkpoints", "data", "cp_one", "blobs", "blob.bin"), []byte("blob-data"), 0600); err != nil {
		t.Fatal(err)
	}
	return Input{SourceConfigRoot: configRoot, SourceWorkspaceID: sourceID, LegacyWorkspaceID: legacyID, TargetWorkspaceID: targetID, WorkspaceRoot: workspaceRoot, Destination: filepath.Join(stageRoot, "workspace")}, source
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}
