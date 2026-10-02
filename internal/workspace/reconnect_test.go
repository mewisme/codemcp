package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestRegisteredRootClassifierStates(t *testing.T) {
	manager := newTestManager(t)

	missing := Workspace{ID: "ws_missing", Path: filepath.Join(t.TempDir(), "gone")}
	if got := manager.classifyRegisteredRoot(missing); got.State != RegisteredRootMissing {
		t.Fatalf("missing root classification=%#v", got)
	}

	localAbsentRoot := t.TempDir()
	localAbsent := Workspace{ID: "ws_absent", Path: localAbsentRoot}
	if got := manager.classifyRegisteredRoot(localAbsent); got.State != RegisteredLocalRootAbsent {
		t.Fatalf("local absent classification=%#v", got)
	}
	if _, err := os.Stat(filepath.Join(localAbsentRoot, LocalDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("classifier mutated local root: %v", err)
	}

	sameRoot := t.TempDir()
	if _, _, err := workspacestate.New(sameRoot).EnsureIdentity("ws_same"); err != nil {
		t.Fatal(err)
	}
	if got := manager.classifyRegisteredRoot(Workspace{ID: "ws_same", Path: sameRoot}); got.State != RegisteredLocalValidSameID || got.LocalID != "ws_same" {
		t.Fatalf("same id classification=%#v", got)
	}

	differentRoot := t.TempDir()
	if _, _, err := workspacestate.New(differentRoot).EnsureIdentity("ws_other"); err != nil {
		t.Fatal(err)
	}
	if got := manager.classifyRegisteredRoot(Workspace{ID: "ws_expected", Path: differentRoot}); got.State != RegisteredLocalValidDifferentID || got.LocalID != "ws_other" {
		t.Fatalf("different id classification=%#v", got)
	}

	corruptRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(corruptRoot, LocalDirName), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptRoot, LocalDirName, "foreign.txt"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := manager.classifyRegisteredRoot(Workspace{ID: "ws_corrupt", Path: corruptRoot}); got.State != RegisteredLocalCorruptOrUnowned {
		t.Fatalf("corrupt classification=%#v", got)
	}

	if os.PathSeparator != '\\' {
		symlinkRoot := t.TempDir()
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(symlinkRoot, LocalDirName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if got := manager.classifyRegisteredRoot(Workspace{ID: "ws_symlink", Path: symlinkRoot}); got.State != RegisteredLocalSymlinkOrUnsafe {
			t.Fatalf("symlink classification=%#v", got)
		}
	}
}

func TestRegisterReusesLocalIdentityWhenRegistryMissing(t *testing.T) {
	root := t.TempDir()
	local := workspacestate.New(root)
	identity, _, err := local.EnsureIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(local.MemoryRoot(), "sentinel.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}

	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != identity.ID {
		t.Fatalf("registered id=%s want local id=%s", item.ID, identity.ID)
	}
	after, err := local.LoadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if after != identity {
		t.Fatalf("local identity changed: before=%#v after=%#v", identity, after)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
		t.Fatalf("local state changed: data=%q err=%v", data, err)
	}
}

func TestRegisterReconnectsMissingRootPreservingIdentityReferencesAndState(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	manager := NewManager(store)
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	nested := filepath.Join(source, "generated")
	external := t.TempDir()
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddAllowDir(item.ID, nested); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddAllowDir(item.ID, external); err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, item.ID); err != nil {
		t.Fatal(err)
	}

	manager.mu.Lock()
	withLegacy := manager.items[item.ID]
	withLegacy.LegacyIDs = []string{"ws_legacy"}
	manager.items[item.ID] = withLegacy
	manager.aliases["ws_legacy"] = item.ID
	if err := manager.saveLocked(); err != nil {
		manager.mu.Unlock()
		t.Fatal(err)
	}
	manager.mu.Unlock()

	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		t.Fatal(err)
	}
	local := workspacestate.New(destination)
	destinationIdentity, _, err := local.EnsureIdentity(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(local.MemoryRoot(), "sentinel.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("destination-state"), 0600); err != nil {
		t.Fatal(err)
	}

	reconnected, err := manager.Register(destination)
	if err != nil {
		t.Fatal(err)
	}
	if reconnected.ID != item.ID || reconnected.Path != canonicalRoot(destination) {
		t.Fatalf("reconnected=%#v", reconnected)
	}
	if got, err := local.LoadIdentity(); err != nil || got != destinationIdentity {
		t.Fatalf("destination identity changed: got=%#v err=%v want=%#v", got, err, destinationIdentity)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "destination-state" {
		t.Fatalf("destination local state changed: data=%q err=%v", data, err)
	}

	wantNested := filepath.Join(canonicalRoot(destination), "generated")
	wantExternal := canonicalRoot(external)
	if !reflect.DeepEqual(reconnected.AllowDirs, []string{wantNested, wantExternal}) &&
		!reflect.DeepEqual(reconnected.AllowDirs, []string{wantExternal, wantNested}) {
		t.Fatalf("allow dirs=%#v want nested=%q external=%q", reconnected.AllowDirs, wantNested, wantExternal)
	}
	if !reflect.DeepEqual(reconnected.LegacyIDs, []string{"ws_legacy"}) {
		t.Fatalf("legacy ids=%#v", reconnected.LegacyIDs)
	}
	registeredAgain, err := manager.Register(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(registeredAgain.AllowDirs, reconnected.AllowDirs) {
		t.Fatalf("allow dirs relocated more than once: first=%#v second=%#v", reconnected.AllowDirs, registeredAgain.AllowDirs)
	}
	gotContainer, err := manager.GetContainer(container.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotContainer.WorkspaceIDs, []string{item.ID}) {
		t.Fatalf("container membership=%#v", gotContainer.WorkspaceIDs)
	}

	reloaded := NewManager(store)
	viaLegacy, err := reloaded.Get("ws_legacy")
	if err != nil || viaLegacy.ID != item.ID || viaLegacy.Path != canonicalRoot(destination) {
		t.Fatalf("legacy reference after reload=%#v err=%v", viaLegacy, err)
	}
}

func TestRegisterReconnectsWhenRegisteredLocalRootIsAbsent(t *testing.T) {
	manager := newTestManager(t)
	source := t.TempDir()
	item, err := manager.Register(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(source, LocalDirName)); err != nil {
		t.Fatal(err)
	}

	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(item.ID); err != nil {
		t.Fatal(err)
	}
	reconnected, err := manager.Register(destination)
	if err != nil {
		t.Fatal(err)
	}
	if reconnected.ID != item.ID || reconnected.Path != canonicalRoot(destination) {
		t.Fatalf("reconnected=%#v", reconnected)
	}
}

func TestRegisterReconnectConflictsDoNotMutateRegistry(t *testing.T) {
	tests := []struct {
		name      string
		mutateOld func(*testing.T, string, string)
		wantState RegisteredRootState
		duplicate bool
	}{
		{
			name:      "duplicate_same_id",
			mutateOld: func(t *testing.T, _, _ string) {},
			wantState: RegisteredLocalValidSameID,
			duplicate: true,
		},
		{
			name: "corrupt_unowned",
			mutateOld: func(t *testing.T, source, _ string) {
				t.Helper()
				if err := os.WriteFile(workspacestate.New(source).IdentityPath(), []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			},
			wantState: RegisteredLocalCorruptOrUnowned,
		},
		{
			name: "different_id",
			mutateOld: func(t *testing.T, source, _ string) {
				t.Helper()
				if err := os.RemoveAll(filepath.Join(source, LocalDirName)); err != nil {
					t.Fatal(err)
				}
				if _, _, err := workspacestate.New(source).EnsureIdentity("ws_different"); err != nil {
					t.Fatal(err)
				}
			},
			wantState: RegisteredLocalValidDifferentID,
		},
	}
	if os.PathSeparator != '\\' {
		tests = append(tests, struct {
			name      string
			mutateOld func(*testing.T, string, string)
			wantState RegisteredRootState
			duplicate bool
		}{
			name: "symlinked_local_root",
			mutateOld: func(t *testing.T, source, _ string) {
				t.Helper()
				if err := os.RemoveAll(filepath.Join(source, LocalDirName)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(source, LocalDirName)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantState: RegisteredLocalSymlinkOrUnsafe,
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			test.mutateOld(t, source, item.ID)

			before, err := os.ReadFile(store)
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.Register(destination)
			if test.duplicate {
				var typed *DuplicateWorkspaceIdentityError
				if !errors.Is(err, ErrDuplicateWorkspaceIdentity) || !errors.As(err, &typed) {
					t.Fatalf("duplicate error=%T %v", err, err)
				}
				if !strings.Contains(err.Error(), "--resolve destination") {
					t.Fatalf("duplicate guidance missing: %v", err)
				}
			} else {
				var typed *WorkspaceReconnectConflictError
				if !errors.Is(err, ErrWorkspaceReconnectConflict) || !errors.As(err, &typed) || typed.State != test.wantState {
					t.Fatalf("conflict error=%T %#v", err, err)
				}
			}
			after, readErr := os.ReadFile(store)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("registry mutated on conflict\nbefore=%s\nafter=%s", before, after)
			}
			manager.mu.RLock()
			current := manager.items[item.ID]
			manager.mu.RUnlock()
			if current.Path != canonicalRoot(source) {
				t.Fatalf("in-memory registry mutated on conflict: %#v", current)
			}
		})
	}
}

func TestRegisterReconnectRespectsForeignAndOwnedRuntimeLocks(t *testing.T) {
	t.Run("foreign_lock", func(t *testing.T) {
		store := filepath.Join(t.TempDir(), "workspaces.json")
		manager := NewManager(store)
		source := t.TempDir()
		item, err := manager.Register(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(source); err != nil {
			t.Fatal(err)
		}
		destination := t.TempDir()
		if _, _, err := workspacestate.New(destination).EnsureIdentity(item.ID); err != nil {
			t.Fatal(err)
		}

		foreign := NewManager(filepath.Join(t.TempDir(), "foreign.json"))
		if _, err := foreign.Register(destination); err != nil {
			t.Fatal(err)
		}
		if err := foreign.Activate(); err != nil {
			t.Fatal(err)
		}
		defer foreign.Deactivate()

		if _, err := manager.Register(destination); !errors.Is(err, ErrAlreadyActive) {
			t.Fatalf("foreign runtime lock error=%v", err)
		}
		manager.mu.RLock()
		current := manager.items[item.ID]
		manager.mu.RUnlock()
		if current.Path != canonicalRoot(source) {
			t.Fatalf("registry changed despite foreign lock: %#v", current)
		}
	})

	t.Run("owned_same_file_move", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows does not permit moving a workspace tree with an open runtime lock")
		}
		manager := newTestManager(t)
		parent := t.TempDir()
		source := filepath.Join(parent, "source")
		destination := filepath.Join(parent, "destination")
		if err := os.Mkdir(source, 0755); err != nil {
			t.Fatal(err)
		}
		item, err := manager.Register(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Activate(); err != nil {
			t.Fatal(err)
		}
		defer manager.Deactivate()
		if err := os.Rename(source, destination); err != nil {
			t.Fatal(err)
		}

		reconnected, err := manager.Register(destination)
		if err != nil {
			t.Fatal(err)
		}
		if reconnected.ID != item.ID || reconnected.Path != canonicalRoot(destination) {
			t.Fatalf("reconnected=%#v", reconnected)
		}
		diagnostics := manager.RuntimeDiagnostics()
		if diagnostics.Owned != 1 || len(diagnostics.Workspaces) != 1 ||
			diagnostics.Workspaces[0].Root != canonicalRoot(destination) ||
			!diagnostics.Workspaces[0].Owned || !diagnostics.Workspaces[0].Valid {
			t.Fatalf("runtime diagnostics=%#v", diagnostics)
		}
	})
}

func TestRegisterReconnectRollsBackRegistryOnPersistenceFailure(t *testing.T) {
	manager := newTestManager(t)
	source := t.TempDir()
	item, err := manager.Register(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(item.ID); err != nil {
		t.Fatal(err)
	}

	previous := registerRegistrySave
	registerRegistrySave = func(*Manager) error { return errors.New("sentinel persistence failure") }
	t.Cleanup(func() { registerRegistrySave = previous })

	if _, err := manager.Register(destination); err == nil || !strings.Contains(err.Error(), "sentinel persistence failure") {
		t.Fatalf("persistence error=%v", err)
	}
	manager.mu.RLock()
	current := manager.items[item.ID]
	manager.mu.RUnlock()
	if current.Path != canonicalRoot(source) {
		t.Fatalf("registry rollback path=%q want=%q", current.Path, canonicalRoot(source))
	}
}
