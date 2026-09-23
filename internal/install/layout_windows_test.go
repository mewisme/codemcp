//go:build windows

package install

import (
	"path/filepath"
	"testing"
)

func TestDefaultLayout(t *testing.T) {
	home := `C:\Users\Mew`
	layout, err := defaultLayout(home, `C:\Users\Mew\AppData\Local`)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".cm")
	if layout.Root != root || layout.Current != filepath.Join(root, "current") || layout.State != filepath.Join(root, "state") || layout.UpdateCache != filepath.Join(root, "state", "update.json") {
		t.Fatalf("unexpected install layout: %+v", layout)
	}
	if layout.CanonicalBinary != filepath.Join(root, "current", "cm.exe") {
		t.Fatalf("unexpected command paths: %+v", layout)
	}
}

func TestDefaultLayoutIgnoresLocalAppData(t *testing.T) {
	home := `C:\Users\Mew`
	layout, err := defaultLayout(home, `D:\Other\Local`)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".cm")
	if layout.Root != expected {
		t.Fatalf("root = %q, want %q", layout.Root, expected)
	}
}
