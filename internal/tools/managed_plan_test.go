package tools

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestFilesystemMutationToolsCannotBypassManagedPlanAuthoring(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	store := workspacestate.New(root)
	if err := os.MkdirAll(store.PlansRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	managedPath := filepath.Join(store.PlansRoot(), "managed.md")
	const managedContent = "managed plan sentinel"
	if err := os.WriteFile(managedPath, []byte(managedContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}

	read := callTool(t, runtime, "read_text_file", map[string]any{
		"workspace_id": workspaceID,
		"path":         ".cm/plans/managed.md",
	})
	if read.IsError || read.StructuredContent.(ReadTextFileResult).Content != managedContent {
		t.Fatalf("managed plan read should remain available: %#v", read)
	}

	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{
			name: "write text",
			tool: "write_file",
			args: map[string]any{"path": ".cm/plans/managed.md", "content": "replacement"},
		},
		{
			name: "write base64",
			tool: "write_file_base64",
			args: map[string]any{"path": ".cm/plans/managed.md", "content": base64.StdEncoding.EncodeToString([]byte("replacement"))},
		},
		{
			name: "edit",
			tool: "edit_file",
			args: map[string]any{"path": ".cm/plans/managed.md", "old_text": "managed", "new_text": "changed"},
		},
		{
			name: "multi edit",
			tool: "multi_edit",
			args: map[string]any{
				"path":  ".cm/plans/managed.md",
				"edits": []any{map[string]any{"old_text": "managed", "new_text": "changed"}},
			},
		},
		{
			name: "regex replace",
			tool: "replace_regex",
			args: map[string]any{"path": ".cm/plans/managed.md", "pattern": "managed", "replacement": "changed"},
		},
		{
			name: "patch",
			tool: "apply_patch",
			args: map[string]any{
				"patch": "*** Begin Patch\n*** Update File: .cm/plans/managed.md\n@@\n-managed plan sentinel\n+changed\n*** End Patch",
			},
		},
		{
			name: "delete file",
			tool: "delete_file",
			args: map[string]any{"path": ".cm/plans/managed.md"},
		},
		{
			name: "create managed directory",
			tool: "create_directory",
			args: map[string]any{"path": ".cm/plans/nested"},
		},
		{
			name: "delete plans directory",
			tool: "delete_directory",
			args: map[string]any{"path": ".cm/plans"},
		},
		{
			name: "delete parent state directory",
			tool: "delete_directory",
			args: map[string]any{"path": ".cm"},
		},
		{
			name: "copy into managed plans",
			tool: "copy_file",
			args: map[string]any{"source": "source.txt", "destination": ".cm/plans/copied.md"},
		},
		{
			name: "move managed plan out",
			tool: "move_file",
			args: map[string]any{"source": ".cm/plans/managed.md", "destination": "moved.md"},
		},
		{
			name: "move file into managed plans",
			tool: "move_file",
			args: map[string]any{"source": "source.txt", "destination": ".cm/plans/moved.md"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{"workspace_id": workspaceID}
			for key, value := range test.args {
				args[key] = value
			}
			result := callTool(t, runtime, test.tool, args)
			if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "canonical plan authoring service") {
				t.Fatalf("%s bypass result=%#v", test.tool, result)
			}
			data, err := os.ReadFile(managedPath)
			if err != nil || string(data) != managedContent {
				t.Fatalf("managed plan changed after %s: err=%v data=%q", test.tool, err, data)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(store.PlansRoot(), "nested")); !os.IsNotExist(err) {
		t.Fatalf("managed directory was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.PlansRoot(), "copied.md")); !os.IsNotExist(err) {
		t.Fatalf("copy entered managed plans: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.PlansRoot(), "moved.md")); !os.IsNotExist(err) {
		t.Fatalf("move entered managed plans: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "source.txt")); err != nil || string(data) != "source" {
		t.Fatalf("source file changed: err=%v data=%q", err, data)
	}
}
