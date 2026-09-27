package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreArchivesRewoundTail(t *testing.T) {
	root := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 100
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(root, "file.txt")

	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	secondID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	thirdID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("three"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := store.Restore("ws_test", root, secondID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RestoredCount != 1 || result.Archived != 2 {
		t.Fatalf("restore result=%#v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "one" {
		t.Fatalf("restored data=%q err=%v", data, err)
	}
	active, err := store.List("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != firstID {
		t.Fatalf("active=%#v", active)
	}
	archived, err := store.ListArchived("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("archive=%#v", archived)
	}
	want := map[string]bool{secondID: false, thirdID: false}
	for _, item := range archived {
		if _, ok := want[item.Checkpoint.ID]; !ok || item.Reason != ArchiveReasonRestore {
			t.Fatalf("unexpected archived checkpoint=%#v", item)
		}
		want[item.Checkpoint.ID] = true
		if _, err := os.Stat(store.archiveCheckpointDir("ws_test", item.Checkpoint.ID)); err != nil {
			t.Fatalf("archived payload %s missing: %v", item.Checkpoint.ID, err)
		}
		if _, err := os.Stat(store.checkpointDir("ws_test", item.Checkpoint.ID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rewound active payload %s remains: %v", item.Checkpoint.ID, err)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Fatalf("checkpoint %s missing from archive", id)
		}
	}
	if _, err := store.Restore("ws_test", root, secondID); err == nil {
		t.Fatal("archived rewind target unexpectedly remained restorable")
	}
}

func TestClearArchivesActiveHistoryWithoutDestroyingExistingArchive(t *testing.T) {
	root := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 1
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(root, "file.txt")

	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	secondID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.Clear("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Cleared != 1 || result.Archived != 1 {
		t.Fatalf("clear result=%#v", result)
	}
	active, err := store.List("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("active after clear=%#v", active)
	}
	archived, err := store.ListArchived("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("archive after clear=%#v", archived)
	}
	reasons := map[string]ArchiveReason{}
	for _, item := range archived {
		reasons[item.Checkpoint.ID] = item.Reason
	}
	if reasons[firstID] != ArchiveReasonRetentionCount || reasons[secondID] != ArchiveReasonClear {
		t.Fatalf("archive reasons=%#v", reasons)
	}
	for _, id := range []string{firstID, secondID} {
		if _, err := os.Stat(store.archiveCheckpointDir("ws_test", id)); err != nil {
			t.Fatalf("clear destroyed archived checkpoint %s: %v", id, err)
		}
	}
	again, err := store.Clear("ws_test")
	if err != nil || again.Cleared != 0 || again.Archived != 0 {
		t.Fatalf("idempotent clear=%#v err=%v", again, err)
	}
	archived, err = store.ListArchived("ws_test", 10)
	if err != nil || len(archived) != 2 {
		t.Fatalf("idempotent clear changed archive=%#v err=%v", archived, err)
	}
}

func TestPurgeExplicitlyDestroysActiveAndArchivedHistory(t *testing.T) {
	root := t.TempDir()
	store := NewStore(t.TempDir())
	store.MaxCount = 1
	store.Retention = 365 * 24 * time.Hour
	file := filepath.Join(root, "file.txt")

	if err := os.WriteFile(file, []byte("zero"), 0644); err != nil {
		t.Fatal(err)
	}
	firstID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	secondID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	store.MaxCount = 100
	if err := os.WriteFile(file, []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	thirdID, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.Purge("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Purged != 3 || result.ActivePurged != 2 || result.ArchivedPurged != 1 {
		t.Fatalf("purge result=%#v", result)
	}
	active, err := store.List("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := store.ListArchived("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 || len(archived) != 0 {
		t.Fatalf("history remains active=%#v archived=%#v", active, archived)
	}
	for _, id := range []string{firstID, secondID, thirdID} {
		if _, err := os.Stat(store.checkpointDir("ws_test", id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("active payload %s survived purge: %v", id, err)
		}
		if _, err := os.Stat(store.archiveCheckpointDir("ws_test", id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("archive payload %s survived purge: %v", id, err)
		}
	}
}

func TestRestoreArchiveFailureRollsBackFilesAndIndexes(t *testing.T) {
	root := t.TempDir()
	store := NewStore(t.TempDir())
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	current := []byte("current")
	if err := os.WriteFile(file, current, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.archiveRoot("ws_test"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.archiveIndexPath("ws_test"), []byte("{truncated"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Restore("ws_test", root, id); err == nil {
		t.Fatal("restore unexpectedly succeeded with corrupt archive index")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != string(current) {
		t.Fatalf("failed restore did not roll back current file: data=%q err=%v", data, err)
	}
	active, err := store.List("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != id {
		t.Fatalf("failed restore changed active index: %#v", active)
	}
	if _, err := os.Stat(store.checkpointDir("ws_test", id)); err != nil {
		t.Fatalf("failed restore removed active payload: %v", err)
	}
}

func TestRestoreArchiveFailureRollsBackDirectoryExactly(t *testing.T) {
	root := t.TempDir()
	store := NewStore(t.TempDir())
	dir := filepath.Join(root, "dir")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(oldPath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", root, "delete_directory", []string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(dir, "current.txt")
	if err := os.WriteFile(currentPath, []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.archiveRoot("ws_test"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.archiveIndexPath("ws_test"), []byte("{truncated"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Restore("ws_test", root, id); err == nil {
		t.Fatal("restore unexpectedly succeeded with corrupt archive index")
	}
	data, err := os.ReadFile(currentPath)
	if err != nil || string(data) != "current" {
		t.Fatalf("directory rollback lost current state: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory rollback left restored-only entry behind: %v", err)
	}
}

func TestClearAndPurgeFailuresLeaveCanonicalActiveIndexIntact(t *testing.T) {
	for _, operation := range []string{"clear", "purge"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			store := NewStore(t.TempDir())
			file := filepath.Join(root, "file.txt")
			if err := os.WriteFile(file, []byte("before"), 0644); err != nil {
				t.Fatal(err)
			}
			id, err := store.Before("ws_test", root, "write_file", []string{file}, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(store.archiveRoot("ws_test"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store.archiveIndexPath("ws_test"), []byte("{truncated"), 0600); err != nil {
				t.Fatal(err)
			}

			switch operation {
			case "clear":
				_, err = store.Clear("ws_test")
			case "purge":
				_, err = store.Purge("ws_test")
			}
			if err == nil {
				t.Fatalf("%s unexpectedly succeeded with corrupt archive index", operation)
			}
			active, readErr := store.List("ws_test", 10)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(active) != 1 || active[0].ID != id {
				t.Fatalf("%s changed active index: %#v", operation, active)
			}
			if _, statErr := os.Stat(store.checkpointDir("ws_test", id)); statErr != nil {
				t.Fatalf("%s removed active payload: %v", operation, statErr)
			}
		})
	}
}
