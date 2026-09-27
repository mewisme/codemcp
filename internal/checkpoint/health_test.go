package checkpoint

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCheckpointDiagnoseHealthyAndReadOnly(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 1
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(workspaceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}

	before := checkpointTreeSnapshot(t, store.Path("ws_test"))
	var provider DiagnosticProvider = store
	health := provider.Diagnose("ws_test")
	after := checkpointTreeSnapshot(t, store.Path("ws_test"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("diagnostics mutated checkpoint state\nbefore=%#v\nafter=%#v", before, after)
	}
	if !health.Healthy || health.Status != HealthHealthy || health.ComponentID != DiagnosticComponentID {
		t.Fatalf("health=%#v", health)
	}
	if health.ActiveIndexed != 1 || health.ArchivedIndexed != 1 || health.ActivePayloads != 1 || health.ArchivedPayloads != 1 || len(health.Issues) != 0 {
		t.Fatalf("health counts=%#v", health)
	}
}

func TestCheckpointDiagnoseCorruptionFixtures(t *testing.T) {
	t.Run("active index", func(t *testing.T) {
		root, store, _ := healthyCheckpointFixture(t, false)
		before := []byte("{truncated")
		if err := os.WriteFile(store.indexPath("ws_test"), before, 0600); err != nil {
			t.Fatal(err)
		}
		health := store.Diagnose("ws_test")
		assertHealthIssue(t, health, "active_index_corrupt")
		if health.Status != HealthCorrupt {
			t.Fatalf("status=%s", health.Status)
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ambiguous active index", func(t *testing.T) {
		_, store, id := healthyCheckpointFixture(t, false)
		index, err := store.readIndex("ws_test")
		if err != nil || len(index.Checkpoints) != 1 {
			t.Fatalf("index=%#v err=%v", index, err)
		}
		index.Checkpoints = append(index.Checkpoints, index.Checkpoints[0])
		if err := writeStructuredAtomic(store.indexPath("ws_test"), index, 0600); err != nil {
			t.Fatal(err)
		}
		health := store.Diagnose("ws_test")
		issue := assertHealthIssue(t, health, "active_index_corrupt")
		if issue.ID != "" || !strings.Contains(issue.Error, "duplicate id") || !strings.Contains(issue.Error, id) {
			t.Fatalf("ambiguous index issue=%#v", issue)
		}
	})

	t.Run("archive index", func(t *testing.T) {
		_, store, _ := healthyCheckpointFixture(t, true)
		if err := os.WriteFile(store.archiveIndexPath("ws_test"), []byte("{truncated"), 0600); err != nil {
			t.Fatal(err)
		}
		health := store.Diagnose("ws_test")
		assertHealthIssue(t, health, "archive_index_corrupt")
	})

	t.Run("manifest", func(t *testing.T) {
		_, store, id := healthyCheckpointFixture(t, false)
		if err := os.WriteFile(store.manifestPath("ws_test", id), []byte("{truncated"), 0600); err != nil {
			t.Fatal(err)
		}
		health := store.Diagnose("ws_test")
		assertHealthIssue(t, health, "active_manifest_corrupt")
	})

	t.Run("blob checksum", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		store := NewStore(t.TempDir())
		store.MaxFileBytes = 4
		file := filepath.Join(workspaceRoot, "large.bin")
		if err := os.WriteFile(file, []byte("0123456789abcdef"), 0644); err != nil {
			t.Fatal(err)
		}
		id, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := store.readManifest("ws_test", id)
		if err != nil || manifest == nil || len(manifest.Files) != 1 {
			t.Fatalf("manifest=%#v err=%v", manifest, err)
		}
		blob := filepath.Join(store.checkpointDir("ws_test", id), filepath.FromSlash(manifest.Files[0].Blob))
		if err := os.WriteFile(blob, []byte("xxxxxxxxxxxxxxxx"), 0600); err != nil {
			t.Fatal(err)
		}
		health := store.Diagnose("ws_test")
		assertHealthIssue(t, health, "active_blob_checksum_failure")
	})

	t.Run("orphan active", func(t *testing.T) {
		_, store, id := healthyCheckpointFixture(t, false)
		if err := store.writeIndex("ws_test", Index{Version: indexVersion, Checkpoints: []Summary{}}); err != nil {
			t.Fatal(err)
		}
		health := store.Diagnose("ws_test")
		issue := assertHealthIssue(t, health, "orphan_active_payload")
		if issue.ID != id || issue.Path == "" {
			t.Fatalf("orphan issue=%#v", issue)
		}
	})

	t.Run("orphan archive", func(t *testing.T) {
		_, store, id := healthyCheckpointFixture(t, true)
		if err := store.writeArchiveIndex("ws_test", ArchiveIndex{Version: archiveIndexVersion, Checkpoints: []ArchivedSummary{}}); err != nil {
			t.Fatal(err)
		}
		health := store.Diagnose("ws_test")
		issue := assertHealthIssue(t, health, "orphan_archive_payload")
		if issue.ID != id || issue.Error == "" {
			t.Fatalf("orphan issue=%#v", issue)
		}
	})
}

func TestCheckpointRecoverRebuildsMissingActiveIndexBoundedly(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 100
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(workspaceRoot, "file.txt")
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(file, []byte(fmt.Sprintf("v%d", i)), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(store.indexPath("ws_test")); err != nil {
		t.Fatal(err)
	}
	health := store.Diagnose("ws_test")
	assertHealthIssue(t, health, "active_index_missing")

	first, err := store.Recover("ws_test", 1)
	if err == nil || first.Pending != 1 || first.RebuiltActive {
		t.Fatalf("bounded recovery=%#v err=%v", first, err)
	}
	if _, statErr := os.Stat(store.indexPath("ws_test")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("bounded recovery mutated active index: %v", statErr)
	}

	recovered, err := store.Recover("ws_test", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.RebuiltActive || recovered.Recovered != 2 || recovered.Pending != 0 || !recovered.Health.Healthy {
		t.Fatalf("recovered=%#v", recovered)
	}
	active, err := store.List("ws_test", 10)
	if err != nil || len(active) != 2 {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	index, err := store.readIndex("ws_test")
	if err != nil || len(index.Checkpoints) != 2 {
		t.Fatalf("rebuilt index=%#v err=%v", index, err)
	}
	if index.Checkpoints[0].CreatedAt > index.Checkpoints[1].CreatedAt {
		t.Fatalf("rebuilt canonical index order=%#v", index.Checkpoints)
	}
}

func TestCheckpointRecoverRefusesCorruptOrAmbiguousCanonicalState(t *testing.T) {
	t.Run("corrupt active index", func(t *testing.T) {
		_, store, _ := healthyCheckpointFixture(t, false)
		corrupt := []byte("{truncated")
		if err := os.WriteFile(store.indexPath("ws_test"), corrupt, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Recover("ws_test", 8); err == nil || !strings.Contains(err.Error(), "active index is corrupt") {
			t.Fatalf("recover err=%v", err)
		}
		got, err := os.ReadFile(store.indexPath("ws_test"))
		if err != nil || !bytes.Equal(got, corrupt) {
			t.Fatalf("corrupt index was discarded: %q err=%v", got, err)
		}
	})

	t.Run("archive payload without metadata", func(t *testing.T) {
		_, store, archivedID := healthyCheckpointFixture(t, true)
		if err := os.Remove(store.archiveIndexPath("ws_test")); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Recover("ws_test", 8); err == nil || !strings.Contains(err.Error(), "has no canonical archive metadata") {
			t.Fatalf("recover err=%v", err)
		}
		if _, err := os.Stat(store.archiveCheckpointDir("ws_test", archivedID)); err != nil {
			t.Fatalf("ambiguous archive payload was discarded: %v", err)
		}
	})
}

func TestCheckpointRecoverRepairsMissingArchivePayloadFromProvableActiveCopy(t *testing.T) {
	_, store, archivedID := healthyCheckpointFixture(t, true)
	archived, err := store.GetArchived("ws_test", archivedID)
	if err != nil || archived == nil {
		t.Fatalf("archived=%#v err=%v", archived, err)
	}
	source := store.archiveCheckpointDir("ws_test", archivedID)
	staleActive := store.checkpointDir("ws_test", archivedID)
	if err := os.MkdirAll(staleActive, 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyCheckpointPayloadTree(source, staleActive); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}

	health := store.Diagnose("ws_test")
	assertHealthIssue(t, health, "archive_payload_missing")
	assertHealthIssue(t, health, "orphan_active_duplicate")

	result, err := store.Recover("ws_test", 8)
	if err != nil {
		t.Fatal(err)
	}
	if result.Recovered != 1 || result.RemovedStale != 1 || !result.Health.Healthy {
		t.Fatalf("recovery=%#v", result)
	}
	if err := store.validateArchivedPayload("ws_test", archived.Checkpoint); err != nil {
		t.Fatalf("recovered archive invalid: %v", err)
	}
	if _, err := os.Stat(staleActive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale active duplicate remains: %v", err)
	}
}

func TestCheckpointRecoverPublishesValidInterruptedArchiveStaging(t *testing.T) {
	_, store, archivedID := healthyCheckpointFixture(t, true)
	final := store.archiveCheckpointDir("ws_test", archivedID)
	staging := filepath.Join(store.archiveDataRoot("ws_test"), "."+archivedID+".tmp-fixture")
	if err := os.Rename(final, staging); err != nil {
		t.Fatal(err)
	}

	health := store.Diagnose("ws_test")
	assertHealthIssue(t, health, "archive_payload_missing")
	assertHealthIssue(t, health, "archive_temporary_payload")

	result, err := store.Recover("ws_test", 8)
	if err != nil {
		t.Fatal(err)
	}
	if result.Recovered != 1 || !result.Health.Healthy {
		t.Fatalf("recovery=%#v", result)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("staging was not published: %v", err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestCheckpointRetentionRestartCyclesPreserveHistory(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	file := filepath.Join(workspaceRoot, "file.txt")
	const checkpoints = 8

	for i := 0; i < checkpoints; i++ {
		store := NewStore(storeRoot)
		store.MaxCount = 2
		store.Retention = 365 * 24 * time.Hour
		if err := os.WriteFile(file, []byte(fmt.Sprintf("v%d", i)), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
			t.Fatal(err)
		}
		health := NewStore(storeRoot).Diagnose("ws_test")
		if !health.Healthy {
			t.Fatalf("restart cycle %d health=%#v", i, health)
		}
	}

	restarted := NewStore(storeRoot)
	active, err := restarted.List("ws_test", 20)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := restarted.ListArchived("ws_test", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || len(archived) != checkpoints-2 || len(active)+len(archived) != checkpoints {
		t.Fatalf("history active=%d archived=%d", len(active), len(archived))
	}
	if health := restarted.Diagnose("ws_test"); !health.Healthy {
		t.Fatalf("restart health=%#v", health)
	}
}

func TestCheckpointConcurrentDiagnosticsAndRetentionKeepIndexesConsistent(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 2
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(workspaceRoot, "file.txt")

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				health := store.Diagnose("ws_test")
				if health.Status == HealthCorrupt {
					errs <- fmt.Errorf("diagnostics observed corrupt state: %#v", health)
					return
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		if err := os.WriteFile(file, []byte(fmt.Sprintf("v%d", i)), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	health := store.Diagnose("ws_test")
	if !health.Healthy || health.ActiveIndexed+health.ArchivedIndexed != 8 {
		t.Fatalf("final health=%#v", health)
	}
}

func healthyCheckpointFixture(t *testing.T, archived bool) (string, *Store, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 100
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(workspaceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !archived {
		return workspaceRoot, store, firstID
	}
	store.MaxCount = 1
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false); err != nil {
		t.Fatal(err)
	}
	return workspaceRoot, store, firstID
}

func assertHealthIssue(t *testing.T, health StorageHealth, kind string) StorageIssue {
	t.Helper()
	for _, issue := range health.Issues {
		if issue.Kind == kind {
			if issue.Severity == "" {
				t.Fatalf("issue %s missing severity: %#v", kind, issue)
			}
			if strings.Contains(kind, "corrupt") || strings.Contains(kind, "failure") {
				if issue.Error == "" {
					t.Fatalf("issue %s missing actionable error: %#v", kind, issue)
				}
			}
			return issue
		}
	}
	t.Fatalf("missing issue %q in %#v", kind, health)
	return StorageIssue{}
}

type checkpointTreeEntry struct {
	Mode os.FileMode
	Data string
}

func checkpointTreeSnapshot(t *testing.T, root string) map[string]checkpointTreeEntry {
	t.Helper()
	result := map[string]checkpointTreeEntry{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := checkpointTreeEntry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Data = string(data)
		}
		result[relative] = item
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
