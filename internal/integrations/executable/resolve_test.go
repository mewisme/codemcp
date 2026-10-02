package executable

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveExternalCanonicalizesSafeSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available on Windows test hosts")
	}
	target := writeExecutable(t, "target", 0755, "binary")
	link := filepath.Join(t.TempDir(), "tool")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resolved, err := ResolveExternal(link, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target {
		t.Fatalf("resolved=%q want=%q", resolved, target)
	}
}

func TestResolveExternalRejectsUnsafeTargets(t *testing.T) {
	if runtime.GOOS != "windows" {
		dangling := filepath.Join(t.TempDir(), "dangling")
		if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), dangling); err == nil {
			if _, err := ResolveExternal(dangling, runtime.GOOS); err == nil {
				t.Fatal("dangling symlink accepted")
			}
		}
		cycleRoot := t.TempDir()
		left, right := filepath.Join(cycleRoot, "left"), filepath.Join(cycleRoot, "right")
		if os.Symlink(right, left) == nil && os.Symlink(left, right) == nil {
			if _, err := ResolveExternal(left, runtime.GOOS); err == nil {
				t.Fatal("cyclic symlink accepted")
			}
		}
	}

	directory := t.TempDir()
	if _, err := ResolveExternal(directory, runtime.GOOS); err == nil {
		t.Fatal("directory accepted")
	}
	empty := writeExecutable(t, "empty", 0755, "")
	if _, err := ResolveExternal(empty, runtime.GOOS); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty executable err=%v", err)
	}
}

func TestResolveExternalPlatformPermissionRules(t *testing.T) {
	path := writeExecutable(t, "tool", 0644, "binary")
	if runtime.GOOS != "windows" {
		if _, err := ResolveExternal(path, "linux"); err == nil {
			t.Fatal("linux non-executable target accepted")
		}
		if _, err := ResolveExternal(path, "darwin"); err == nil {
			t.Fatal("darwin non-executable target accepted")
		}
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := ResolveExternal(path, "windows"); err != nil || resolved != want {
		t.Fatalf("windows executable resolution=%q err=%v", resolved, err)
	}
}

func writeExecutable(t *testing.T, name string, mode os.FileMode, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}
