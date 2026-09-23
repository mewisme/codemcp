//go:build !windows

package install

import (
	"path/filepath"
	"testing"
)

func TestDefaultLayout(t *testing.T) {
	layout, err := defaultLayout(filepath.Join(string(filepath.Separator), "home", "mew"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(string(filepath.Separator), "home", "mew")
	root := filepath.Join(home, ".cm")
	if layout.Root != root || layout.Versions != filepath.Join(root, "versions") || layout.Current != filepath.Join(root, "current") || layout.State != filepath.Join(root, "state") || layout.UpdateCache != filepath.Join(root, "state", "update.json") {
		t.Fatalf("unexpected install layout: %+v", layout)
	}
	if layout.CanonicalBinary != filepath.Join(home, ".local", "bin", "cm") {
		t.Fatalf("unexpected command paths: %+v", layout)
	}
}

func TestVersionDirRejectsPaths(t *testing.T) {
	layout, err := defaultLayout(filepath.Join(string(filepath.Separator), "home", "mew"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layout.VersionDir("../v1.0.0"); err == nil {
		t.Fatal("expected path-like version to be rejected")
	}
	dir, err := layout.VersionDir("v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(layout.Versions, "v1.0.0") {
		t.Fatalf("version dir = %q", dir)
	}
}
