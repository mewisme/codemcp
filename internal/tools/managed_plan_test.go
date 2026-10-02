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

func TestFilesystemMutationToolsKeepDynamicInstructionProvidersReadOnly(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	providerRoot := filepath.Join(root, "packages", "app", ".future-provider")
	if err := os.MkdirAll(filepath.Join(providerRoot, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(providerRoot, "AGENTS.md")
	if err := os.WriteFile(contextPath, []byte("provider sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}

	read := callTool(t, runtime, "read_text_file", map[string]any{
		"workspace_id": workspaceID,
		"path":         "packages/app/.future-provider/AGENTS.md",
	})
	if read.IsError || read.StructuredContent.(ReadTextFileResult).Content != "provider sentinel" {
		t.Fatalf("provider read should remain available: %#v", read)
	}

	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "overwrite context", tool: "write_file", args: map[string]any{"path": "packages/app/.future-provider/AGENTS.md", "content": "changed"}},
		{name: "create future context", tool: "write_file", args: map[string]any{"path": "packages/app/.brand-new-provider/AGENTS.md", "content": "changed"}},
		{name: "create rule", tool: "write_file", args: map[string]any{"path": "packages/app/.future-provider/rules/new.md", "content": "changed"}},
		{name: "edit provider file", tool: "edit_file", args: map[string]any{"path": "packages/app/.future-provider/AGENTS.md", "old_text": "provider", "new_text": "changed"}},
		{name: "patch provider file", tool: "apply_patch", args: map[string]any{
			"patch": "*** Begin Patch\n*** Update File: packages/app/.future-provider/AGENTS.md\n@@\n-provider sentinel\n+changed\n*** End Patch",
		}},
		{name: "delete provider", tool: "delete_directory", args: map[string]any{"path": "packages/app/.future-provider"}},
		{name: "copy into provider", tool: "copy_file", args: map[string]any{"source": "source.txt", "destination": "packages/app/.future-provider/copied.txt"}},
		{name: "move provider file out", tool: "move_file", args: map[string]any{"source": "packages/app/.future-provider/AGENTS.md", "destination": "moved.md"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{"workspace_id": workspaceID}
			for key, value := range test.args {
				args[key] = value
			}
			result := callTool(t, runtime, test.tool, args)
			if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "read-only dynamic instruction provider") {
				t.Fatalf("%s bypass result=%#v", test.tool, result)
			}
			data, err := os.ReadFile(contextPath)
			if err != nil || string(data) != "provider sentinel" {
				t.Fatalf("provider changed after %s: err=%v data=%q", test.tool, err, data)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(providerRoot, "rules", "new.md")); !os.IsNotExist(err) {
		t.Fatalf("provider rule was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "app", ".brand-new-provider")); !os.IsNotExist(err) {
		t.Fatalf("future provider was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(providerRoot, "copied.txt")); !os.IsNotExist(err) {
		t.Fatalf("copy entered provider: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "moved.md")); !os.IsNotExist(err) {
		t.Fatalf("provider file moved out: %v", err)
	}
}

func TestFilesystemMutationToolsCannotBypassNativeInstructionAuthoring(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	store := workspacestate.New(root)
	if err := os.MkdirAll(store.RulesRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	rulePath := filepath.Join(store.RulesRoot(), "managed.md")
	if err := os.WriteFile(rulePath, []byte("rule sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}

	read := callTool(t, runtime, "read_text_file", map[string]any{
		"workspace_id": workspaceID,
		"path":         ".cm/rules/managed.md",
	})
	if read.IsError || read.StructuredContent.(ReadTextFileResult).Content != "rule sentinel" {
		t.Fatalf("native instruction read should remain available: %#v", read)
	}

	for _, test := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "overwrite rule", tool: "write_file", args: map[string]any{"path": ".cm/rules/managed.md", "content": "changed"}},
		{name: "create skill", tool: "write_file", args: map[string]any{"path": ".cm/skills/new/SKILL.md", "content": "changed"}},
		{name: "patch rule", tool: "apply_patch", args: map[string]any{
			"patch": "*** Begin Patch\n*** Update File: .cm/rules/managed.md\n@@\n-rule sentinel\n+changed\n*** End Patch",
		}},
		{name: "delete rules", tool: "delete_directory", args: map[string]any{"path": ".cm/rules"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{"workspace_id": workspaceID}
			for key, value := range test.args {
				args[key] = value
			}
			result := callTool(t, runtime, test.tool, args)
			if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "canonical instruction authoring service") {
				t.Fatalf("%s bypass result=%#v", test.tool, result)
			}
		})
	}

	if data, err := os.ReadFile(rulePath); err != nil || string(data) != "rule sentinel" {
		t.Fatalf("native rule changed: err=%v data=%q", err, data)
	}
	if _, err := os.Stat(filepath.Join(store.SkillsRoot(), "new")); !os.IsNotExist(err) {
		t.Fatalf("native skill was created: %v", err)
	}
}
