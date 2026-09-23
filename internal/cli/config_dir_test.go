package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestConfigDirFlagOverridesEnvironment(t *testing.T) {
	defer configformat.SetRootPath("")
	envRoot := filepath.Join(t.TempDir(), "env")
	flagRoot := filepath.Join(t.TempDir(), "flag")
	t.Setenv(configformat.EnvConfigDir, envRoot)
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", flagRoot, "config", "path"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), filepath.Join(flagRoot, "config.json")) {
		t.Fatalf("config path output = %q", output.String())
	}
}

func TestConfigDirEnvironmentSelectsRoot(t *testing.T) {
	defer configformat.SetRootPath("")
	envRoot := filepath.Join(t.TempDir(), "env")
	t.Setenv(configformat.EnvConfigDir, envRoot)
	if got := configformat.RootPath(); got != envRoot {
		t.Fatalf("root path = %q, want %q", got, envRoot)
	}
}

func TestInitWithConfigDirDoesNotTouchDefaultRoot(t *testing.T) {
	defer configformat.SetRootPath("")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(configformat.EnvConfigDir, "")
	defaultRoot := filepath.Join(home, ".config", "chatgpt-mcp")
	if err := os.MkdirAll(defaultRoot, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(defaultRoot, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	isolated := filepath.Join(t.TempDir(), "test-config")
	cmd := newRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--config-dir", isolated, "init"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(isolated, "config.json")); err != nil {
		t.Fatalf("isolated config missing: %v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep" {
		t.Fatalf("default config root was touched: data=%q err=%v", data, err)
	}
}

func TestRemoveConfigRootRequiresManagedCustomRoot(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom")
	if err := os.MkdirAll(custom, 0700); err != nil {
		t.Fatal(err)
	}
	if err := removeConfigRoot(custom); err == nil {
		t.Fatal("unmanaged custom root was removed")
	}
	if err := configformat.MarkRoot(custom); err != nil {
		t.Fatal(err)
	}
	if err := removeConfigRoot(custom); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Fatalf("managed custom root still exists: %v", err)
	}
}
