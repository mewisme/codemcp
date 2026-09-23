package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestGrepAcceptsFilePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sample.ts")
	if err := os.WriteFile(file, []byte("ValkeyRedis cache\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedFile, err := workspaces.ResolvePath(item.ID, root, file, true)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterFilesystemTools(registry, workspaces, checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoints")))
	runtime := &Runtime{Registry: registry, Workspaces: workspaces}
	result, err := runtime.Call(context.Background(), "grep", map[string]any{
		"workspace_id": item.ID,
		"path":         file,
		"pattern":      "ValkeyRedis",
		"glob":         "*.go",
	})
	if err != nil || result.IsError {
		t.Fatalf("grep file failed: result=%#v err=%v", result, err)
	}
	value := result.StructuredContent.(GrepResult)
	if value.Path != expectedFile || !strings.Contains(value.Output, expectedFile+":1: ValkeyRedis cache") {
		t.Fatalf("grep file result=%#v", value)
	}
}

func TestGrepAcceptsHiddenFilePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".env")
	if err := os.WriteFile(file, []byte("CACHE=valkey\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedFile, err := workspaces.ResolvePath(item.ID, root, file, true)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterFilesystemTools(registry, workspaces, checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoints")))
	runtime := &Runtime{Registry: registry, Workspaces: workspaces}
	result, err := runtime.Call(context.Background(), "grep", map[string]any{"workspace_id": item.ID, "path": file, "pattern": "valkey"})
	if err != nil || result.IsError {
		t.Fatalf("grep hidden file failed: result=%#v err=%v", result, err)
	}
	if output := result.StructuredContent.(GrepResult).Output; !strings.Contains(output, expectedFile) {
		t.Fatalf("grep hidden file output=%q", output)
	}
}
