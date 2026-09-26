package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestRelocateDuplicateWithoutResolutionRemainsNonDestructive(t *testing.T) {
	manager := newTestManager(t)
	source := t.TempDir()
	item, err := manager.Register(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(item.ID); err != nil {
		t.Fatal(err)
	}
	sourceIdentity := workspacestate.New(source).IdentityPath()
	destinationIdentity := workspacestate.New(destination).IdentityPath()

	_, err = manager.Relocate(item.ID, destination)
	var conflict *DuplicateWorkspaceIdentityError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrDuplicateWorkspaceIdentity) {
		t.Fatalf("duplicate error=%T %v", err, err)
	}
	if _, err := os.Stat(sourceIdentity); err != nil {
		t.Fatalf("source identity changed: %v", err)
	}
	if _, err := os.Stat(destinationIdentity); err != nil {
		t.Fatalf("destination identity changed: %v", err)
	}
	got, err := manager.Get(item.ID)
	if err != nil || got.Path != canonicalRoot(source) {
		t.Fatalf("registry changed: got=%#v err=%v", got, err)
	}
}

func TestResolveDuplicateRelocationDestinationPreservesDestinationState(t *testing.T) {
	manager, item, source, destination := duplicateRelocationFixture(t)
	sourceProject := writeRelocationFixtureFile(t, filepath.Join(source, "source-project.txt"), "source-project")
	destinationProject := writeRelocationFixtureFile(t, filepath.Join(destination, "destination-project.txt"), "destination-project")
	sourceState := writeRelocationFixtureFile(t, filepath.Join(workspacestate.New(source).MemoryRoot(), "source.txt"), "source-state")
	destinationState := writeRelocationFixtureFile(t, filepath.Join(workspacestate.New(destination).MemoryRoot(), "destination.txt"), "destination-state")
	nested := filepath.Join(source, "generated")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	item, err := manager.AddAllowDir(item.ID, nested)
	if err != nil {
		t.Fatal(err)
	}
	item, err = manager.AddAllowDir(item.ID, external)
	if err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, item.ID); err != nil {
		t.Fatal(err)
	}

	resolved, err := manager.ResolveDuplicateRelocation(item.ID, destination, RelocationResolutionDestination)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != item.ID || resolved.Path != canonicalRoot(destination) {
		t.Fatalf("resolved=%#v", resolved)
	}
	if data, err := os.ReadFile(destinationState); err != nil || string(data) != "destination-state" {
		t.Fatalf("destination state lost: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(sourceState); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source local state still active: %v", err)
	}
	assertRelocationProjectFile(t, sourceProject, "source-project")
	assertRelocationProjectFile(t, destinationProject, "destination-project")

	wantNested := filepath.Join(canonicalRoot(destination), "generated")
	wantExternal := canonicalRoot(external)
	if !reflect.DeepEqual(resolved.AllowDirs, []string{wantNested, wantExternal}) &&
		!reflect.DeepEqual(resolved.AllowDirs, []string{wantExternal, wantNested}) {
		t.Fatalf("allow dirs=%#v", resolved.AllowDirs)
	}
	gotContainer, err := manager.GetContainer(container.ID)
	if err != nil || !reflect.DeepEqual(gotContainer.WorkspaceIDs, []string{item.ID}) {
		t.Fatalf("container=%#v err=%v", gotContainer, err)
	}
	assertResolvedWorkspaceReloadsAndActivates(t, manager.path, item.ID, destination)
	assertNoReconciliationTransactions(t, manager.path)
}

func TestResolveDuplicateRelocationRegisteredMovesRegisteredState(t *testing.T) {
	manager, item, source, destination := duplicateRelocationFixture(t)
	sourceProject := writeRelocationFixtureFile(t, filepath.Join(source, "source-project.txt"), "source-project")
	destinationProject := writeRelocationFixtureFile(t, filepath.Join(destination, "destination-project.txt"), "destination-project")
	sourceState := writeRelocationFixtureFile(t, filepath.Join(workspacestate.New(source).MemoryRoot(), "selected.txt"), "registered-state")
	destinationOld := writeRelocationFixtureFile(t, filepath.Join(workspacestate.New(destination).MemoryRoot(), "discarded.txt"), "destination-state")

	resolved, err := manager.ResolveDuplicateRelocation(item.ID, destination, RelocationResolutionRegistered)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Path != canonicalRoot(destination) {
		t.Fatalf("resolved=%#v", resolved)
	}
	selectedAtDestination := filepath.Join(workspacestate.New(destination).MemoryRoot(), filepath.Base(sourceState))
	if data, err := os.ReadFile(selectedAtDestination); err != nil || string(data) != "registered-state" {
		t.Fatalf("registered state not activated: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(destinationOld); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old destination state survived replacement: %v", err)
	}
	if _, err := os.Stat(workspacestate.New(source).IdentityPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source identity still active: %v", err)
	}
	assertRelocationProjectFile(t, sourceProject, "source-project")
	assertRelocationProjectFile(t, destinationProject, "destination-project")
	assertResolvedWorkspaceReloadsAndActivates(t, manager.path, item.ID, destination)
	assertNoReconciliationTransactions(t, manager.path)
}

func TestResolveDuplicateRelocationRollbackRestoresAllState(t *testing.T) {
	tests := []struct {
		name       string
		resolution RelocationResolution
		stage      string
	}{
		{name: "destination_after_registry", resolution: RelocationResolutionDestination, stage: "after_registry_write"},
		{name: "destination_after_source_retire", resolution: RelocationResolutionDestination, stage: "after_source_retire"},
		{name: "registered_after_destination", resolution: RelocationResolutionRegistered, stage: "after_destination_write"},
		{name: "registered_after_registry", resolution: RelocationResolutionRegistered, stage: "after_registry_write"},
		{name: "registered_after_source_retire", resolution: RelocationResolutionRegistered, stage: "after_source_retire"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, item, source, destination := duplicateRelocationFixture(t)
			sourceState := writeRelocationFixtureFile(t, filepath.Join(workspacestate.New(source).MemoryRoot(), "source.txt"), "source-state")
			destinationState := writeRelocationFixtureFile(t, filepath.Join(workspacestate.New(destination).MemoryRoot(), "destination.txt"), "destination-state")

			previous := duplicateRelocationFailureHook
			duplicateRelocationFailureHook = func(stage string) error {
				if stage == test.stage {
					return errors.New("injected relocation failure")
				}
				return nil
			}
			t.Cleanup(func() { duplicateRelocationFailureHook = previous })

			if _, err := manager.ResolveDuplicateRelocation(item.ID, destination, test.resolution); err == nil || !strings.Contains(err.Error(), "injected relocation failure") {
				t.Fatalf("resolution error=%v", err)
			}
			if data, err := os.ReadFile(sourceState); err != nil || string(data) != "source-state" {
				t.Fatalf("source rollback failed: data=%q err=%v", data, err)
			}
			if data, err := os.ReadFile(destinationState); err != nil || string(data) != "destination-state" {
				t.Fatalf("destination rollback failed: data=%q err=%v", data, err)
			}
			got, err := manager.Get(item.ID)
			if err != nil || got.Path != canonicalRoot(source) {
				t.Fatalf("registry rollback failed: got=%#v err=%v", got, err)
			}
			assertNoReconciliationTransactions(t, manager.path)
		})
	}
}

func TestResolveDuplicateRelocationRejectsForeignRuntimeOwnership(t *testing.T) {
	for _, locked := range []string{"registered", "destination"} {
		t.Run(locked, func(t *testing.T) {
			manager, item, source, destination := duplicateRelocationFixture(t)
			target := source
			if locked == "destination" {
				target = destination
			}
			foreignStore := filepath.Join(t.TempDir(), "foreign.json")
			foreign := NewManager(foreignStore)
			if _, err := foreign.Register(target); err != nil {
				t.Fatal(err)
			}
			if err := foreign.Activate(); err != nil {
				t.Fatal(err)
			}
			defer foreign.Deactivate()

			if _, err := manager.ResolveDuplicateRelocation(item.ID, destination, RelocationResolutionDestination); !errors.Is(err, ErrAlreadyActive) {
				t.Fatalf("foreign lock error=%v", err)
			}
			got, err := manager.Get(item.ID)
			if err != nil || got.Path != canonicalRoot(source) {
				t.Fatalf("registry changed: got=%#v err=%v", got, err)
			}
			if _, err := os.Stat(workspacestate.New(source).IdentityPath()); err != nil {
				t.Fatalf("source identity changed: %v", err)
			}
			if _, err := os.Stat(workspacestate.New(destination).IdentityPath()); err != nil {
				t.Fatalf("destination identity changed: %v", err)
			}
		})
	}
}

func TestResolveDuplicateRelocationMergeFailsClosed(t *testing.T) {
	manager, item, source, destination := duplicateRelocationFixture(t)
	if _, err := manager.ResolveDuplicateRelocation(item.ID, destination, RelocationResolutionMerge); !errors.Is(err, ErrRelocationMergeUnavailable) {
		t.Fatalf("merge error=%v", err)
	}
	got, err := manager.Get(item.ID)
	if err != nil || got.Path != canonicalRoot(source) {
		t.Fatalf("registry changed: got=%#v err=%v", got, err)
	}
	if _, err := os.Stat(workspacestate.New(source).IdentityPath()); err != nil {
		t.Fatalf("source identity changed: %v", err)
	}
	if _, err := os.Stat(workspacestate.New(destination).IdentityPath()); err != nil {
		t.Fatalf("destination identity changed: %v", err)
	}
}

func TestDuplicateRelocationJournalContainsMetadataOnly(t *testing.T) {
	manager, item, _, destination := duplicateRelocationFixture(t)
	secretMarker := "state-content-must-not-enter-journal"
	writeRelocationFixtureFile(t, filepath.Join(workspacestate.New(destination).MemoryRoot(), "private.txt"), secretMarker)

	previous := duplicateRelocationFailureHook
	duplicateRelocationFailureHook = func(stage string) error {
		if stage != "after_staging" {
			return nil
		}
		transactionRoot := filepath.Join(filepath.Dir(manager.path), "workspace-reconciliation")
		entries, err := os.ReadDir(transactionRoot)
		if err != nil || len(entries) != 1 {
			t.Fatalf("transaction entries=%#v err=%v", entries, err)
		}
		journal, err := os.ReadFile(filepath.Join(transactionRoot, entries[0].Name(), "journal.json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(journal), secretMarker) {
			t.Fatalf("journal leaked workspace content: %s", journal)
		}
		if !strings.Contains(string(journal), "registered_fingerprint") || !strings.Contains(string(journal), "destination_fingerprint") {
			t.Fatalf("journal missing fingerprints: %s", journal)
		}
		return errors.New("stop after journal inspection")
	}
	t.Cleanup(func() { duplicateRelocationFailureHook = previous })

	if _, err := manager.ResolveDuplicateRelocation(item.ID, destination, RelocationResolutionDestination); err == nil {
		t.Fatal("expected injected stop")
	}
	assertNoReconciliationTransactions(t, manager.path)
}

func duplicateRelocationFixture(t *testing.T) (*Manager, Workspace, string, string) {
	t.Helper()
	store := filepath.Join(t.TempDir(), "workspaces.json")
	manager := NewManager(store)
	source := t.TempDir()
	item, err := manager.Register(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(item.ID); err != nil {
		t.Fatal(err)
	}
	return manager, item, source, destination
}

func writeRelocationFixtureFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertRelocationProjectFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("project file %s changed: data=%q err=%v", path, data, err)
	}
}

func assertResolvedWorkspaceReloadsAndActivates(t *testing.T, store, id, destination string) {
	t.Helper()
	reloaded := NewManager(store)
	got, err := reloaded.Get(id)
	if err != nil || got.Path != canonicalRoot(destination) {
		t.Fatalf("reload got=%#v err=%v", got, err)
	}
	if err := reloaded.Activate(); err != nil {
		t.Fatalf("activate resolved workspace: %v", err)
	}
	if err := reloaded.Deactivate(); err != nil {
		t.Fatalf("deactivate resolved workspace: %v", err)
	}
}

func assertNoReconciliationTransactions(t *testing.T, store string) {
	t.Helper()
	root := filepath.Join(filepath.Dir(store), "workspace-reconciliation")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("reconciliation transactions left behind: %#v", entries)
	}
}
