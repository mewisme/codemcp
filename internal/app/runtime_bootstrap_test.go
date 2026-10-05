package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestNewReturnsCorruptRuntimeStateErrorWithoutPanic(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	path := filepath.Join(shellruntime.DefaultStateRoot(), "executions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	application, err := New(config.Default())
	if err == nil || application != nil || !strings.Contains(err.Error(), "load execution state") {
		t.Fatalf("application=%#v err=%v", application, err)
	}
}
