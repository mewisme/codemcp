//go:build windows

package install

import (
	"path/filepath"
	"testing"
)

func TestDefaultLayout(t *testing.T) {
	home := `C:\Users\Mew`
	localAppData := `C:\Users\Mew\AppData\Local`
	layout, err := defaultLayout(home, localAppData)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(localAppData, "cm")
	if layout.Root != root || layout.Current != filepath.Join(root, "current") || layout.State != filepath.Join(root, "state") || layout.UpdateCache != filepath.Join(root, "state", "update.json") {
		t.Fatalf("unexpected install layout: %+v", layout)
	}
	if layout.CanonicalBinary != filepath.Join(root, "current", "cm.exe") {
		t.Fatalf("unexpected command paths: %+v", layout)
	}
}

func TestDefaultLayoutFallsBackToUserProfile(t *testing.T) {
	home := `C:\Users\Mew`
	layout, err := defaultLayout(home, "")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, "AppData", "Local", "cm")
	if layout.Root != expected {
		t.Fatalf("root = %q, want %q", layout.Root, expected)
	}
}
