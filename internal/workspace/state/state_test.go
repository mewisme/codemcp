package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnsureIdentityCreatesLazyAtomicLayout(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := New(workspaceRoot)
	if _, err := os.Stat(store.Root()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local root exists before first use: %v", err)
	}
	identity, created, err := store.EnsureIdentity("")
	if err != nil || !created {
		t.Fatalf("identity=%#v created=%t err=%v", identity, created, err)
	}
	if !strings.HasPrefix(identity.ID, "ws_") || identity.Kind != identityKind || identity.Version != identityVersion {
		t.Fatalf("identity=%#v", identity)
	}
	info, err := os.Stat(store.IdentityPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("identity marker mode=%v err=%v", info.Mode(), err)
	}
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != identityFileName {
		t.Fatalf("layout was not lazy: %#v", entries)
	}
	second, created, err := store.EnsureIdentity("")
	if err != nil || created || second.ID != identity.ID {
		t.Fatalf("second=%#v created=%t err=%v", second, created, err)
	}
}

func TestLayoutPathsStayUnderWorkspaceLocalRootWithoutEagerCreation(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := New(workspaceRoot)
	paths := []string{
		store.IdentityPath(), store.ConfigPath(), store.StateRoot(), store.MemoryRoot(),
		store.CheckpointRoot(), store.CacheRoot(), store.RuntimeRoot(), store.RuntimeLockPath(),
		store.RulesRoot(), store.SkillsRoot(), store.PromptRoot(), store.PlansRoot(),
	}
	for _, path := range paths {
		if !contained(store.Root(), path) || !contained(workspaceRoot, path) {
			t.Fatalf("layout path escaped local root: %s", path)
		}
	}
	if _, err := os.Stat(store.Root()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("layout was created eagerly: %v", err)
	}
	statePath, err := store.StatePath("completion.json")
	if err != nil || statePath != filepath.Join(store.StateRoot(), "completion.json") {
		t.Fatalf("state path=%q err=%v", statePath, err)
	}
}

func TestValidateIdentityIDRejectsPathLikeValues(t *testing.T) {
	for _, id := range []string{"ws_../escape", "ws_a/b", "ws_a\\b", "ws_a:b", "ws_a.b"} {
		if err := ValidateIdentityID(id); err == nil {
			t.Fatalf("unsafe id accepted: %q", id)
		}
	}
	for _, id := range []string{"ws_test", "ws_0123456789abcdef", "ws_A-B_1"} {
		if err := ValidateIdentityID(id); err != nil {
			t.Fatalf("safe id %q rejected: %v", id, err)
		}
	}
}

func TestEnsureIdentityConcurrentCallsResolveOneIdentity(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := New(workspaceRoot)
	const workers = 16
	identities := make(chan Identity, workers)
	errorsOut := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			identity, _, err := store.EnsureIdentity("")
			if err != nil {
				errorsOut <- err
				return
			}
			identities <- identity
		}()
	}
	group.Wait()
	close(identities)
	close(errorsOut)
	for err := range errorsOut {
		t.Fatalf("concurrent identity initialization failed: %v", err)
	}
	final, err := store.LoadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for identity := range identities {
		count++
		if identity.ID != final.ID || !identity.CreatedAt.Equal(final.CreatedAt) {
			t.Fatalf("split workspace identity: got=%#v final=%#v", identity, final)
		}
	}
	if count != workers {
		t.Fatalf("successful workers=%d want=%d", count, workers)
	}
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != identityFileName {
		t.Fatalf("identity initialization left residue: %#v", entries)
	}
}

func TestEnsureIdentityCanClaimEmptyLocalRootButRejectsUnownedContent(t *testing.T) {
	emptyRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(emptyRoot, DirectoryName), 0700); err != nil {
		t.Fatal(err)
	}
	if _, created, err := New(emptyRoot).EnsureIdentity("ws_existing"); err != nil || !created {
		t.Fatalf("empty local root claim created=%t err=%v", created, err)
	}

	unownedRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(unownedRoot, DirectoryName), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unownedRoot, DirectoryName, "foreign.txt"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := New(unownedRoot).EnsureIdentity(""); err == nil || !strings.Contains(err.Error(), "ownership marker") {
		t.Fatalf("unowned local root error=%v", err)
	}
}

func TestEnsureIdentityRejectsSymlinkedLocalRoot(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	workspaceRoot := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspaceRoot, DirectoryName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := New(workspaceRoot).EnsureIdentity(""); err == nil {
		t.Fatal("symlinked .cm was accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory was modified: entries=%#v err=%v", entries, err)
	}
}

func TestLoadIdentityRejectsInvalidSchemaAndSymlinkedMarker(t *testing.T) {
	workspaceRoot := t.TempDir()
	store := New(workspaceRoot)
	if _, _, err := store.EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.IdentityPath(), []byte(`{"version":1,"kind":"foreign","id":"ws_bad","created_at":"2026-09-24T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadIdentity(); err == nil {
		t.Fatal("invalid ownership marker was accepted")
	}

	if os.PathSeparator == '\\' {
		return
	}
	outside := filepath.Join(t.TempDir(), "workspace.json")
	valid := Identity{Version: identityVersion, Kind: identityKind, ID: "ws_external", CreatedAt: testTime(t)}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.IdentityPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, store.IdentityPath()); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.LoadIdentity(); err == nil {
		t.Fatal("symlinked identity marker was accepted")
	}
}

func TestJoinCannotEscapeWorkspaceLocalRoot(t *testing.T) {
	store := New(t.TempDir())
	if got, err := store.Join("memory", "MEMORY.md"); err != nil || got != filepath.Join(store.MemoryRoot(), "MEMORY.md") {
		t.Fatalf("join=%q err=%v", got, err)
	}
	if _, err := store.Join("..", "outside"); err == nil {
		t.Fatal("escaped join was accepted")
	}
}

func testTime(t *testing.T) time.Time {
	t.Helper()
	value, err := time.Parse(time.RFC3339, "2026-09-24T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return value
}
