package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/workspace"
)

func TestParseRenderCanonicalMemoryRoundTrip(t *testing.T) {
	input := "## tui\n\n### theme\n- Use Charm defaults.\n\n### layout\n- Center the main layout.\n\n## coding-style\n\n### imports\n- Keep imports contiguous.\n"
	document := Parse(input)
	if len(document.Entries) != 3 {
		t.Fatalf("entries = %#v", document.Entries)
	}
	rendered := Render(document)
	if reparsed := Parse(rendered); Render(reparsed) != rendered {
		t.Fatalf("round trip changed\nfirst:\n%s\nsecond:\n%s", rendered, Render(reparsed))
	}
}

func TestParseDeduplicatesCaseInsensitiveScopeKey(t *testing.T) {
	document := Parse("## TUI\n### Theme\n- Use Charm defaults.\n## tui\n### theme\n- Preserve light and dark adaptation.\n")
	if len(document.Entries) != 1 {
		t.Fatalf("entries = %#v", document.Entries)
	}
	entry := document.Entries[0]
	if entry.Scope != "TUI" || entry.Key != "Theme" || entry.Note != "Use Charm defaults. Preserve light and dark adaptation." {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestParseKeepsScopedParagraphAsScopeLevelNote(t *testing.T) {
	document := Parse("## tooling\n- use pnpm\n\n## tui\n- use Charm defaults\n")
	want := "## tooling\n\n- use pnpm\n\n## tui\n\n- use Charm defaults\n"
	if rendered := Render(document); rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
	for _, entry := range document.Entries {
		if entry.Key != "" {
			t.Fatalf("scope-level entry unexpectedly has key: %#v", entry)
		}
	}
}

func TestParseMigratesLegacyDateNotesToGeneralScope(t *testing.T) {
	document := Parse("# Auto memory (cross-session notes)\n\n- 2026-09-04: use compact imports\n- 2026-09-05: prefer pnpm\n")
	want := "## general\n\n- use compact imports prefer pnpm\n"
	if rendered := Render(document); rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
	if len(document.Entries) != 1 || document.Entries[0].Key != "" {
		t.Fatalf("document = %#v", document)
	}
}

func TestParseCollapsesDuplicateScopeKey(t *testing.T) {
	document := Parse("## general\n\n### general\n- use Charm defaults\n")
	want := "## general\n\n- use Charm defaults\n"
	if rendered := Render(document); rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
	if len(document.Entries) != 1 || document.Entries[0].Key != "" {
		t.Fatalf("document = %#v", document)
	}
}

func TestParseNormalizesWhitespaceAndWrappedNotes(t *testing.T) {
	document := Parse("##  coding-style  \n### imports\n- Keep imports\n  contiguous   and compact.\n")
	if len(document.Entries) != 1 || document.Entries[0] != (Entry{Scope: "coding-style", Key: "imports", Note: "Keep imports contiguous and compact."}) {
		t.Fatalf("entries = %#v", document.Entries)
	}
}

func TestRenderUsesDeterministicScopeAndKeyOrdering(t *testing.T) {
	document := Document{Entries: []Entry{{Scope: "tui", Key: "theme", Note: "theme"}, {Scope: "coding-style", Key: "typescript", Note: "ts"}, {Scope: "coding-style", Key: "imports", Note: "imports"}}}
	rendered := Render(document)
	if strings.Index(rendered, "## coding-style") > strings.Index(rendered, "## tui") || strings.Index(rendered, "### imports") > strings.Index(rendered, "### typescript") {
		t.Fatalf("non-deterministic ordering:\n%s", rendered)
	}
}

func TestStoreLoadDocumentAndSaveDocument(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	path, err := store.SaveDocument("ws_test", Document{Entries: []Entry{{Scope: "tooling", Key: "package-manager", Note: "use pnpm"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join("ws_test", "MEMORY.md")) {
		t.Fatalf("path = %q", path)
	}
	document, err := store.LoadDocument("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Entries) != 1 || document.Entries[0].Key != "package-manager" {
		t.Fatalf("document = %#v", document)
	}
}

func TestWorkspaceStoreUsesLocalCMRoot(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	store := NewWorkspaceStore(t.TempDir(), manager)
	path, err := store.SaveDocument(item.ID, Document{Entries: []Entry{{Scope: "project", Note: "local"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(item.Path, ".cm", "memory", "MEMORY.md")
	if path != want {
		t.Fatalf("memory path=%q want=%q", path, want)
	}
}

func TestStoreMigrationPersistsOnlyOnSave(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	path := store.WorkspacePath("ws_test")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	legacy := "## tooling\n- use pnpm\n"
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	document, err := store.LoadDocument("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != legacy {
		t.Fatalf("load persisted migration: %q", raw)
	}
	if _, err := store.SaveDocument("ws_test", document); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "### general") || !strings.Contains(string(raw), "## tooling\n\n- use pnpm") {
		t.Fatalf("save did not persist migration: %q", raw)
	}
}

func TestUpsertInsertsAndReplacesCanonicalEntry(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	if _, err := store.Upsert("ws_test", "tui", "theme", "Use Nord colors."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert("ws_test", "TUI", "THEME", "Use Charm default component styles."); err != nil {
		t.Fatal(err)
	}
	document, err := store.LoadDocument("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Entries) != 1 || document.Entries[0].Note != "Use Charm default component styles." {
		t.Fatalf("document = %#v", document)
	}
}

func TestUpsertAllowsSameKeyInDifferentScopes(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	if _, err := store.Upsert("ws_test", "tui", "theme", "Charm"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert("ws_test", "web", "theme", "System"); err != nil {
		t.Fatal(err)
	}
	document, err := store.LoadDocument("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Entries) != 2 {
		t.Fatalf("document = %#v", document)
	}
}

func TestUpsertAllowsScopeLevelNoteAndCollapsesDuplicateKey(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	if _, err := store.Upsert("ws_test", "general", "general", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert("ws_test", "GENERAL", "", "replacement"); err != nil {
		t.Fatal(err)
	}
	document, err := store.LoadDocument("ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Entries) != 1 || document.Entries[0].Key != "" || document.Entries[0].Note != "replacement" {
		t.Fatalf("document = %#v", document)
	}
	if rendered := Render(document); strings.Contains(rendered, "### general") || rendered != "## general\n\n- replacement\n" {
		t.Fatalf("rendered = %q", rendered)
	}
}

func TestUpsertRequiresScopeAndNote(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	for _, args := range [][3]string{{"", "key", "note"}, {"scope", "key", ""}} {
		if _, err := store.Upsert("ws_test", args[0], args[1], args[2]); err == nil {
			t.Fatalf("expected error for %#v", args)
		}
	}
}

func TestGetFiltersScopeAndExactKey(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	for _, item := range []Entry{{Scope: "tui", Key: "theme", Note: "Charm"}, {Scope: "tui", Key: "layout", Note: "Center"}, {Scope: "web", Key: "theme", Note: "System"}} {
		if _, err := store.Upsert("ws_test", item.Scope, item.Key, item.Note); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.Get("ws_test", "TUI", "THEME")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Note != "Charm" {
		t.Fatalf("entries = %#v", entries)
	}
	entries, err = store.Get("ws_test", "tui", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	entries, err = store.Get("ws_test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestGetRejectsKeyWithoutScope(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	if _, err := store.Get("ws_test", "", "theme"); err == nil {
		t.Fatal("expected key without scope to fail")
	}
}

func TestGetCanAddressScopeLevelNoteWithRepeatedScopeKey(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	if _, err := store.Upsert("ws_test", "general", "", "scope note"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert("ws_test", "general", "detail", "child note"); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Get("ws_test", "general", "general")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Key != "" || entries[0].Note != "scope note" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestRemoveDeletesExactEntryOrWholeScope(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	for _, item := range []Entry{{Scope: "tui", Key: "theme", Note: "Charm"}, {Scope: "tui", Key: "layout", Note: "Center"}, {Scope: "web", Key: "theme", Note: "System"}} {
		if _, err := store.Upsert("ws_test", item.Scope, item.Key, item.Note); err != nil {
			t.Fatal(err)
		}
	}
	removed, _, err := store.Remove("ws_test", "TUI", "THEME")
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	removed, _, err = store.Remove("ws_test", "tui", "")
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	entries, err := store.Get("ws_test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Scope != "web" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestRemoveMissingEntryIsNoop(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	removed, _, err := store.Remove("ws_test", "missing", "key")
	if err != nil || removed != 0 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
}

func TestRemoveCanTargetScopeLevelNoteWithoutRemovingChildren(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	if _, err := store.Upsert("ws_test", "general", "", "scope note"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert("ws_test", "general", "detail", "child note"); err != nil {
		t.Fatal(err)
	}
	removed, _, err := store.Remove("ws_test", "general", "general")
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	entries, err := store.Get("ws_test", "general", "")
	if err != nil || len(entries) != 1 || entries[0].Key != "detail" {
		t.Fatalf("entries=%#v err=%v", entries, err)
	}
}
