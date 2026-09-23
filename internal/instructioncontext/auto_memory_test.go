package instructioncontext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/memory"
)

func TestLoadAutoMemory(t *testing.T) {
	store := memory.NewStore(t.TempDir())
	path, err := store.Upsert("ws_test", "tooling", "package-manager", "use pnpm")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadAutoMemory(store, "ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Loaded || snapshot.Bytes != len([]byte(snapshot.Content)) || !strings.Contains(snapshot.Content, "use pnpm") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if !strings.HasSuffix(path, filepath.Join("ws_test", "MEMORY.md")) {
		t.Fatalf("path = %q", path)
	}
}

func TestLoadAutoMemoryMissingIsEmpty(t *testing.T) {
	store := memory.NewStore(t.TempDir())
	snapshot, err := LoadAutoMemory(store, "ws_missing")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Loaded || snapshot.Content != "" || snapshot.Bytes != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLoadAutoMemoryRejectsMissingWorkspaceID(t *testing.T) {
	store := memory.NewStore(t.TempDir())
	if _, err := LoadAutoMemory(store, "  "); err == nil {
		t.Fatal("expected missing workspace id to fail")
	}
}

func TestLoadAutoMemoryPropagatesReadErrors(t *testing.T) {
	root := t.TempDir()
	store := memory.NewStore(root)
	path := store.WorkspacePath("ws_test")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAutoMemory(store, "ws_test"); err == nil {
		t.Fatal("expected memory read error")
	}
}

func TestLoadAutoMemorySelectedUsesQueryAndBudget(t *testing.T) {
	store := memory.NewStore(t.TempDir())
	for _, entry := range []memory.Entry{{Scope: "tui", Key: "theme", Note: "Use Charm defaults"}, {Scope: "release", Key: "ci", Note: "Use GitHub Actions"}, {Scope: "coding-style", Key: "imports", Note: "Keep imports contiguous"}} {
		if _, err := store.Upsert("ws_test", entry.Scope, entry.Key, entry.Note); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := LoadAutoMemorySelected(store, "ws_test", "tui theme", 1, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Loaded || snapshot.Entries != 1 || !snapshot.Truncated || snapshot.Query != "tui theme" || !strings.Contains(snapshot.Content, "### theme") || strings.Contains(snapshot.Content, "### ci") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLoadAutoMemorySurfacesCompactionRecommendation(t *testing.T) {
	store := memory.NewStore(t.TempDir())
	for index := 0; index <= memory.ScopeEntriesSoftLimit; index++ {
		key := fmt.Sprintf("key-%02d", index)
		if _, err := store.Upsert("ws_test", "large-scope", key, "distinct note "+key); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := LoadAutoMemorySelected(store, "ws_test", "", 100, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.OptimizationRecommended {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
