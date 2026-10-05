package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestCheckedRuntimeReturnsUpstreamLoadError(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	path := upstream.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntimeChecked()
	if err == nil || runtime != nil || !strings.Contains(err.Error(), "load upstream store") {
		t.Fatalf("runtime=%#v err=%v", runtime, err)
	}
}

func TestCheckedRuntimeReturnsExecutionStateLoadErrorAndLegacyConstructorDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	path := filepath.Join(shellruntime.DefaultStateRoot(), "executions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntimeChecked()
	if err == nil || runtime != nil || !strings.Contains(err.Error(), "load execution state") {
		t.Fatalf("runtime=%#v err=%v", runtime, err)
	}
	legacy := NewRuntime()
	if legacy == nil || legacy.InitError() == nil || !strings.Contains(legacy.InitError().Error(), "load execution state") {
		t.Fatalf("legacy runtime=%#v init_err=%v", legacy, legacy.InitError())
	}
	if _, err := legacy.Call(context.Background(), "workspace_list", nil); err == nil || !strings.Contains(err.Error(), "load execution state") {
		t.Fatalf("legacy runtime call err=%v", err)
	}
}
