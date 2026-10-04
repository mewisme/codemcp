package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneStaleInstallStages(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * staleInstallStageAge)
	stale := filepath.Join(root, ".staging-v1-old")
	fresh := filepath.Join(root, ".staging-v2-fresh")
	version := filepath.Join(root, "v1")
	for _, path := range []string{stale, fresh, version} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	pruneStaleInstallStages(root, now)
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale install stage still exists: %v", err)
	}
	for _, path := range []string{fresh, version} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept install path removed %q: %v", path, err)
		}
	}
}
