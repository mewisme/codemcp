package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/oslock"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestMergeRelocationRejectsOwnedCodeGraphLock(t *testing.T) {
	manager, item, source, destination := duplicateRelocationFixture(t)
	local := workspacestate.New(destination)
	if err := os.MkdirAll(local.RuntimeRoot(), 0700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(local.RuntimeRoot(), "codegraph.lock")
	lock, ok, err := oslock.TryAcquire(lockPath, oslock.Exclusive)
	if err != nil || !ok {
		t.Fatalf("acquire codegraph lock ok=%v err=%v", ok, err)
	}
	defer lock.Release()

	_, err = manager.ResolveDuplicateRelocationWithMerge(item.ID, destination, RelocationResolutionMerge, func(DuplicateMergeRequest) error {
		t.Fatal("merge resolver ran while derived-state lock was owned")
		return nil
	})
	if !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("err=%v", err)
	}
	got, getErr := manager.Get(item.ID)
	if getErr != nil || got.Path != canonicalRoot(source) {
		t.Fatalf("registry=%#v err=%v", got, getErr)
	}
	if _, err := os.Stat(workspacestate.New(source).IdentityPath()); err != nil {
		t.Fatalf("source identity changed: %v", err)
	}
	if _, err := os.Stat(local.IdentityPath()); err != nil {
		t.Fatalf("destination identity changed: %v", err)
	}
}

func TestDiagnoseRelocationClassifiesWithoutMutation(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		manager, item, source, destination := duplicateRelocationFixture(t)
		before, err := os.ReadFile(manager.path)
		if err != nil {
			t.Fatal(err)
		}
		diagnostic, err := manager.DiagnoseRelocation(t.Context(), item.ID, destination)
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic.Kind != ReconciliationDuplicate || len(diagnostic.Resolutions) != 3 {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
		after, err := os.ReadFile(manager.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatal("doctor mutated workspace registry")
		}
		if _, err := os.Stat(workspacestate.New(source).IdentityPath()); err != nil {
			t.Fatalf("doctor mutated source state: %v", err)
		}
	})

	t.Run("reconnectable stale path", func(t *testing.T) {
		manager := newTestManager(t)
		parent := t.TempDir()
		source := filepath.Join(parent, "old")
		destination := filepath.Join(parent, "new")
		if err := os.MkdirAll(source, 0755); err != nil {
			t.Fatal(err)
		}
		item, err := manager.Register(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(source, destination); err != nil {
			t.Fatal(err)
		}
		diagnostic, err := manager.DiagnoseRelocation(t.Context(), item.ID, destination)
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic.Kind != ReconciliationReconnectable {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
		got, err := manager.Get(item.ID)
		if err != nil || got.Path != canonicalRoot(source) {
			t.Fatalf("doctor changed registry: %#v err=%v", got, err)
		}
	})

	t.Run("different destination identity", func(t *testing.T) {
		manager := newTestManager(t)
		first, err := manager.Register(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		secondRoot := t.TempDir()
		second, err := manager.Register(secondRoot)
		if err != nil {
			t.Fatal(err)
		}
		if second.ID == first.ID {
			t.Fatal("fixture produced duplicate ids")
		}
		diagnostic, err := manager.DiagnoseRelocation(t.Context(), first.ID, secondRoot)
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic.Kind != ReconciliationDestinationIssue || diagnostic.DestinationLocalID != second.ID {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
	})
}
