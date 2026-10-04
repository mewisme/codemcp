package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneStaleUpdateWorkspaces(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * staleWorkspaceAge)
	stale := filepath.Join(root, "cm-update-stale")
	stalePackage := filepath.Join(root, "cm-package-update-stale")
	fresh := filepath.Join(root, "cm-update-fresh")
	foreign := filepath.Join(root, "other")
	for _, path := range []string{stale, stalePackage, fresh, foreign} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{stale, stalePackage} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	pruneStaleUpdateWorkspaces(root, now)
	for _, path := range []string{stale, stalePackage} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale update workspace still exists %q: %v", path, err)
		}
	}
	for _, path := range []string{fresh, foreign} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept workspace removed %q: %v", path, err)
		}
	}
}
