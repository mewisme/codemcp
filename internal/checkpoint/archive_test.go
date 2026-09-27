package checkpoint

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCountRetentionArchivesWithoutDestroyingHistory(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	store := NewStore(storeRoot)
	store.MaxCount = 1
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(workspaceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.archiveRoot("ws_test")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active-only store unexpectedly created archive state: %v", err)
	}
	first, err := store.Get("ws_test", firstID)
	if err != nil || first == nil {
		t.Fatalf("first checkpoint=%#v err=%v", first, err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	secondID, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}

	active, err := store.List("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != secondID {
		t.Fatalf("active=%#v", active)
	}
	archived, err := store.ListArchived("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].Checkpoint.ID != firstID || archived[0].Reason != ArchiveReasonRetentionCount {
		t.Fatalf("archived=%#v", archived)
	}
	if archived[0].Checkpoint.CreatedAt != first.CreatedAt || archived[0].Checkpoint.Summary != first.Summary {
		t.Fatalf("archived metadata changed: before=%#v after=%#v", first, archived[0].Checkpoint)
	}
	if _, err := os.Stat(store.archiveCheckpointDir("ws_test", firstID)); err != nil {
		t.Fatalf("archived payload missing: %v", err)
	}
	if _, err := os.Stat(store.checkpointDir("ws_test", firstID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retained checkpoint remained in active data: %v", err)
	}
	restarted := NewStore(storeRoot)
	got, err := restarted.GetArchived("ws_test", firstID)
	if err != nil || got == nil || got.Checkpoint.ID != firstID {
		t.Fatalf("restarted archived checkpoint=%#v err=%v", got, err)
	}
}

func TestTimeRetentionArchivesWithoutDestroyingHistory(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 100
	store.Retention = time.Hour
	file := filepath.Join(workspaceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	index, err := store.readIndex("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	index.Checkpoints[0].CreatedAt = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	manifest, err := store.readManifest("ws_test", firstID)
	if err != nil || manifest == nil {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
	manifest.CreatedAt = index.Checkpoints[0].CreatedAt
	if err := writeStructuredAtomic(store.manifestPath("ws_test", firstID), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	index.Checkpoints[0] = buildSummary(*manifest)
	if err := store.writeIndex("ws_test", index); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}
	archived, err := store.GetArchived("ws_test", firstID)
	if err != nil || archived == nil {
		t.Fatalf("archived=%#v err=%v", archived, err)
	}
	if archived.Reason != ArchiveReasonRetentionAge {
		t.Fatalf("reason=%q", archived.Reason)
	}
	if _, err := os.Stat(store.archiveCheckpointDir("ws_test", firstID)); err != nil {
		t.Fatalf("expired checkpoint payload was destroyed: %v", err)
	}
}

func TestArchivedCheckpointPreservesLargeBlobIntegrity(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 1
	store.Retention = 365 * 24 * time.Hour
	store.MaxFileBytes = 4
	file := filepath.Join(workspaceRoot, "large.bin")
	before := []byte("0123456789abcdef")
	if err := os.WriteFile(file, before, 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("fedcba9876543210"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}
	archived, err := store.GetArchived("ws_test", firstID)
	if err != nil || archived == nil {
		t.Fatalf("archived=%#v err=%v", archived, err)
	}
	manifestData, err := os.ReadFile(filepath.Join(store.archiveCheckpointDir("ws_test", firstID), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Blob == "" || manifest.Files[0].BlobSHA256 == "" || manifest.Files[0].Size != int64(len(before)) {
		t.Fatalf("archived manifest=%#v", manifest)
	}
	if err := store.validateArchivedPayload("ws_test", archived.Checkpoint); err != nil {
		t.Fatalf("valid archived blob failed integrity check: %v", err)
	}
	blob := filepath.Join(store.archiveCheckpointDir("ws_test", firstID), filepath.FromSlash(manifest.Files[0].Blob))
	if err := os.WriteFile(blob, []byte("xxxxxxxxxxxxxxxx"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.validateArchivedPayload("ws_test", archived.Checkpoint); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered archived blob was accepted: %v", err)
	}
}

func TestInterruptedRetentionTransitionKeepsActiveOrArchivedCheckpointRecoverable(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	store := NewStore(storeRoot)
	store.MaxCount = 10
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(workspaceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}

	index, err := store.readIndex("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	store.MaxCount = 1
	removed, err := store.archiveRetentionLocked("ws_test", &index)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].ID != firstID || len(index.Checkpoints) != 1 {
		t.Fatalf("transition result removed=%#v active=%#v", removed, index.Checkpoints)
	}

	restarted := NewStore(storeRoot)
	active, err := restarted.List("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID == "" || active[1].ID == "" {
		t.Fatalf("disk active index changed before commit: %#v", active)
	}
	if _, err := os.Stat(store.checkpointDir("ws_test", firstID)); err != nil {
		t.Fatalf("active payload disappeared before active-index commit: %v", err)
	}
	archived, err := restarted.GetArchived("ws_test", firstID)
	if err != nil || archived == nil {
		t.Fatalf("published archive not recoverable: %#v err=%v", archived, err)
	}
	if err := restarted.validateArchivedPayload("ws_test", archived.Checkpoint); err != nil {
		t.Fatalf("published archive payload invalid: %v", err)
	}
}

func TestArchivalRejectsSymlinkPayloadWithoutMutatingActiveIndex(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	workspaceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 10
	file := filepath.Join(workspaceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(store.checkpointDir("ws_test", id), "unsafe")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	index, err := store.readIndex("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	before := append([]Summary(nil), index.Checkpoints...)
	store.Retention = time.Nanosecond
	time.Sleep(time.Millisecond)
	if _, err := store.archiveRetentionLocked("ws_test", &index); err == nil {
		t.Fatal("archive accepted symlink payload")
	}
	if len(index.Checkpoints) != len(before) || index.Checkpoints[0].ID != before[0].ID {
		t.Fatalf("in-memory active index mutated on failed archive: %#v", index.Checkpoints)
	}
	disk, err := store.readIndex("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.Checkpoints) != 1 || disk.Checkpoints[0].ID != id {
		t.Fatalf("disk active index mutated on failed archive: %#v", disk)
	}
}

func TestArchiveIndexReadAndListAreBoundedAndStrict(t *testing.T) {
	store := NewStore(t.TempDir())
	records := make([]ArchivedSummary, 1001)
	for i := range records {
		records[i] = ArchivedSummary{
			Checkpoint: Summary{ID: "cp_" + time.Unix(int64(i), 0).UTC().Format("150405.000000000"), CreatedAt: time.Unix(int64(i), 0).UTC().Format(time.RFC3339Nano)},
			ArchivedAt: time.Unix(int64(i+1), 0).UTC().Format(time.RFC3339Nano),
			Reason:     ArchiveReasonRetentionCount,
		}
	}
	if err := store.writeArchiveIndex("ws_test", ArchiveIndex{Version: archiveIndexVersion, Checkpoints: records}); err != nil {
		t.Fatal(err)
	}
	values, err := store.ListArchived("ws_test", 100000)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != maxArchiveListLimit {
		t.Fatalf("bounded list length=%d want=%d", len(values), maxArchiveListLimit)
	}
	if err := os.WriteFile(store.archiveIndexPath("ws_test"), []byte(`{"version":1,"checkpoints":[],"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListArchived("ws_test", 10); err == nil {
		t.Fatal("strict archive index accepted unknown field")
	}
}
