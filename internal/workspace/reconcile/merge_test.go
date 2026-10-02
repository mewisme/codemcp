package reconcile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyLocalTreeRejectsUnownedAndSymlinkState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := classifyLocalTree(root); !errors.Is(err, ErrUnsupportedWorkspaceState) {
		t.Fatalf("unowned state err=%v", err)
	}
	if err := os.Remove(filepath.Join(root, "unexpected")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "memory")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := classifyLocalTree(root); !errors.Is(err, ErrUnsupportedWorkspaceState) {
		t.Fatalf("symlink state err=%v", err)
	}
}

func TestMergeValidatedTreeRejectsDivergentDurableFiles(t *testing.T) {
	left, right, output := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(left, "item.txt"), []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "item.txt"), []byte("right"), 0o600); err != nil {
		t.Fatal(err)
	}
	validate := func(root string) error {
		info, err := os.Stat(root)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("not a directory")
		}
		return nil
	}
	if err := mergeValidatedTree("fixture", left, right, output, validate, nil); !errors.Is(err, ErrDurableStateConflict) {
		t.Fatalf("divergent merge err=%v", err)
	}
}

func TestJSONEquivalentIgnoresObjectKeyOrder(t *testing.T) {
	equal, err := jsonEquivalent([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`))
	if err != nil || !equal {
		t.Fatalf("equal=%t err=%v", equal, err)
	}
}
