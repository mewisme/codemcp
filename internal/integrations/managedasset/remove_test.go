package managedasset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveDeletesOnlyManagedPlatformDirectory(t *testing.T) {
	root := t.TempDir()
	manager := Manager{Root: root}
	spec := Spec{Name: "tool", Version: "v1", Platform: "linux-amd64", URL: "https://example.com/tool.tar.gz", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Archive: "tar.gz", Entrypoint: "tool"}
	path, err := manager.Path(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("asset"), 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(root, "other", "v1", "linux-amd64", "other")
	if err := os.MkdirAll(filepath.Dir(sibling), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("sibling"), 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := manager.Remove(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("managed asset was not removed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("managed asset path still exists: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling managed asset was removed: %v", err)
	}
	removed, err = manager.Remove(spec)
	if err != nil || removed {
		t.Fatalf("idempotent remove=%v err=%v", removed, err)
	}
}

func TestRemoveOtherVersionsKeepsRequestedVersion(t *testing.T) {
	root := t.TempDir()
	manager := Manager{Root: root}
	for _, version := range []string{"v1", "v2", "v3"} {
		path := filepath.Join(root, "tool", version, "linux-amd64")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.RemoveOtherVersions("tool", "v3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tool", "v3")); err != nil {
		t.Fatalf("kept version removed: %v", err)
	}
	for _, version := range []string{"v1", "v2"} {
		if _, err := os.Stat(filepath.Join(root, "tool", version)); !os.IsNotExist(err) {
			t.Fatalf("old version %s still exists: %v", version, err)
		}
	}
}
