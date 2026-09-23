package configformat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRootPathUsesHomeCM(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("home unavailable: %v", err)
	}
	if got, want := DefaultRootPath(), filepath.Join(home, ".cm"); got != want {
		t.Fatalf("default root = %q, want %q", got, want)
	}
}

func TestLegacyConfigDirEnvIsIgnored(t *testing.T) {
	defer SetRootPath("")
	t.Setenv("CHATGPT_MCP_CONFIG_DIR", filepath.Join(t.TempDir(), "legacy"))
	t.Setenv(EnvConfigDir, "")
	if err := SetRootPath(""); err != nil {
		t.Fatal(err)
	}
	if got := RootPath(); got != DefaultRootPath() {
		t.Fatalf("legacy config env changed root: got %q want %q", got, DefaultRootPath())
	}
}

func TestRootPathPrecedence(t *testing.T) {
	defer SetRootPath("")
	envRoot := filepath.Join(t.TempDir(), "env")
	flagRoot := filepath.Join(t.TempDir(), "flag")
	t.Setenv(EnvConfigDir, envRoot)
	if err := SetRootPath(""); err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.Abs(RootPath()); got != filepath.Clean(envRoot) {
		t.Fatalf("env root = %q, want %q", got, envRoot)
	}
	if err := SetRootPath(flagRoot); err != nil {
		t.Fatal(err)
	}
	if got := RootPath(); got != filepath.Clean(flagRoot) {
		t.Fatalf("override root = %q, want %q", got, flagRoot)
	}
}

func TestManagedRootMarker(t *testing.T) {
	root := filepath.Join(t.TempDir(), "isolated")
	if IsManagedRoot(root) {
		t.Fatal("uninitialized root reported as managed")
	}
	if err := MarkRoot(root); err != nil {
		t.Fatal(err)
	}
	if !IsManagedRoot(root) {
		t.Fatal("marked root was not recognized")
	}
	info, err := os.Stat(filepath.Join(root, rootMarker))
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Fatal("root marker is a directory")
	}
}

func TestSetRootPathRejectsCurrentWorkingDirectoryAndHome(t *testing.T) {
	defer SetRootPath("")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := SetRootPath(cwd); err == nil {
		t.Fatal("current working directory was accepted as config root")
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if err := SetRootPath(home); err == nil {
			t.Fatal("user home directory was accepted as config root")
		}
	}
}
