package instance

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreCreatesPersistentIdentity(t *testing.T) {
	store := NewStore(t.TempDir())
	store.hostname = func() (string, error) { return "mew-wsl", nil }
	store.random = bytes.NewReader(make([]byte, 32))
	first, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.ID != "inst_00000000000000000000000000000000" || first.Name != "mew-wsl" {
		t.Fatalf("identity = %#v / %#v", first, second)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestStoreIdentityIsIndependentFromConfigState(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	store.hostname = func() (string, error) { return "node", nil }
	store.random = bytes.NewReader(make([]byte, 32))
	identity, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded != identity || filepath.Ext(store.Path()) != ".json" {
		t.Fatalf("identity = %#v want %#v path=%s", reloaded, identity, store.Path())
	}
}

func TestStoreSetNamePreservesID(t *testing.T) {
	store := NewStore(t.TempDir())
	store.hostname = func() (string, error) { return "old", nil }
	store.random = bytes.NewReader(make([]byte, 32))
	first, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.SetName("new")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != first.ID || updated.Name != "new" || updated.CreatedAt != first.CreatedAt {
		t.Fatalf("updated = %#v first=%#v", updated, first)
	}
}
