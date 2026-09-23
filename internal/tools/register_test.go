package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/workspace"
)

func newToolTestRuntime(t *testing.T) (*Runtime, string, string) {
	t.Helper()
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "state"))
	registry := NewRegistry()
	RegisterWorkspaceTools(registry, manager)
	RegisterCore(registry, manager, checkpoints)
	return &Runtime{Registry: registry, Workspaces: manager, Checkpoints: checkpoints}, item.ID, root
}

func callTool(t *testing.T, runtime *Runtime, name string, args map[string]any) Result {
	t.Helper()
	result, err := runtime.Call(context.Background(), name, args)
	if err != nil {
		t.Fatalf("%s protocol error: %v", name, err)
	}
	return result
}

func baseArgs(workspaceID, _ string) map[string]any {
	return map[string]any{"workspace_id": workspaceID}
}

func TestReadTextFilePartialLines(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("one\ntwo\nthree\nfour"), 0644); err != nil {
		t.Fatal(err)
	}
	args := baseArgs(workspaceID, root)
	args["path"] = "file.txt"
	args["offset"] = 2
	args["limit"] = 2
	result := callTool(t, runtime, "read_text_file", args)
	value := result.StructuredContent.(ReadTextFileResult)
	if value.Content != "     2|two\n     3|three" {
		t.Fatalf("content = %q", value.Content)
	}
	if value.Lines == nil || *value.Lines != 2 {
		t.Fatalf("lines = %#v", value.Lines)
	}
}

func TestReadTextFileLargeFileRequiresBoundedSelection(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	paddingLines := maxTextReadBytes/8 + 8
	var content strings.Builder
	content.Grow(maxTextReadBytes + 128)
	content.WriteString("first\n")
	for range paddingLines {
		content.WriteString("padding\n")
	}
	content.WriteString("last-a\nlast-b")
	file := filepath.Join(root, "large.txt")
	if err := os.WriteFile(file, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}
	whole := callTool(t, runtime, "read_text_file", map[string]any{"workspace_id": workspaceID, "path": "large.txt"})
	if !whole.IsError || len(whole.Content) == 0 || !strings.Contains(whole.Content[0].Text, "4 MiB") {
		t.Fatalf("whole read was not bounded: %#v", whole)
	}
	head := callTool(t, runtime, "read_text_file", map[string]any{"workspace_id": workspaceID, "path": "large.txt", "head": 1})
	if head.IsError || head.StructuredContent.(ReadTextFileResult).Content != "first" {
		t.Fatalf("head read=%#v", head)
	}
	tail := callTool(t, runtime, "read_text_file", map[string]any{"workspace_id": workspaceID, "path": "large.txt", "tail": 2})
	if tail.IsError || tail.StructuredContent.(ReadTextFileResult).Content != "last-a\nlast-b" {
		t.Fatalf("tail read=%#v", tail)
	}
}

func TestTextMutationRejectsOversizedFile(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	file := filepath.Join(root, "large.txt")
	if err := os.WriteFile(file, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, maxMutationFileBytes+1); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, runtime, "edit_file", map[string]any{"workspace_id": workspaceID, "path": "large.txt", "old_text": "old", "new_text": "new"})
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "16 MiB") {
		t.Fatalf("oversized edit was not rejected: %#v", result)
	}
	info, err := os.Stat(file)
	if err != nil || info.Size() != maxMutationFileBytes+1 {
		t.Fatalf("oversized file changed: size=%d err=%v", info.Size(), err)
	}
}

func TestReadFileBase64Chunk(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	data := []byte("abcdefghij")
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}
	args := baseArgs(workspaceID, root)
	args["path"] = "binary.bin"
	args["offset"] = 2
	args["length"] = 4
	result := callTool(t, runtime, "read_file_base64", args)
	value := result.StructuredContent.(ReadFileBase64Result)
	if decoded, _ := base64.StdEncoding.DecodeString(value.Content); string(decoded) != "cdef" {
		t.Fatalf("chunk = %q", decoded)
	}
	if value.NextOffset == nil || *value.NextOffset != 6 || value.Done {
		t.Fatalf("unexpected offsets: %#v", value)
	}
}

func TestReadFilesUsesRootedBoundedReads(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err == nil {
		result := callTool(t, runtime, "read_files", map[string]any{"workspace_id": workspaceID, "paths": []any{"outside-link/secret.txt"}})
		if !result.IsError {
			t.Fatalf("symlink escape read succeeded: %#v", result)
		}
	}
	first := bytes.Repeat([]byte("a"), maxTextReadBytes/2+1)
	second := bytes.Repeat([]byte("b"), maxTextReadBytes/2+1)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), first, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), second, 0644); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, runtime, "read_files", map[string]any{"workspace_id": workspaceID, "paths": []any{"a.txt", "b.txt"}})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "combined multi-file text read exceeds") {
		t.Fatalf("combined read was not bounded: %#v", result)
	}
}

func TestReadFilesRejectsTooManyPaths(t *testing.T) {
	runtime, workspaceID, _ := newToolTestRuntime(t)
	paths := make([]any, maxReadFiles+1)
	for index := range paths {
		paths[index] = "missing.txt"
	}
	result := callTool(t, runtime, "read_files", map[string]any{"workspace_id": workspaceID, "paths": paths})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "at most 32") {
		t.Fatalf("path count was not bounded: %#v", result)
	}
}

func TestWriteFilesAllowEmptyContent(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "write_file", path: "empty.txt"},
		{name: "write_file_base64", path: "empty.bin"},
	} {
		result := callTool(t, runtime, test.name, map[string]any{"workspace_id": workspaceID, "path": test.path, "content": ""})
		if result.IsError {
			t.Fatalf("%s failed: %#v", test.name, result)
		}
		info, err := os.Stat(filepath.Join(root, test.path))
		if err != nil {
			t.Fatalf("%s stat: %v", test.name, err)
		}
		if info.Size() != 0 {
			t.Fatalf("%s size=%d", test.name, info.Size())
		}
	}
}

func TestReadTextFileRejectsConflictingSelectors(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("one\ntwo"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "limit_without_offset", args: map[string]any{"workspace_id": workspaceID, "path": "file.txt", "limit": 1}, want: "limit requires offset"},
		{name: "head_and_tail", args: map[string]any{"workspace_id": workspaceID, "path": "file.txt", "head": 1, "tail": 1}, want: "mutually exclusive"},
	} {
		result := callTool(t, runtime, "read_text_file", test.args)
		if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, test.want) {
			t.Fatalf("%s result=%#v", test.name, result)
		}
	}
	result := callTool(t, runtime, "read_text_file", map[string]any{"workspace_id": workspaceID, "path": "file.txt", "offset": 100001, "limit": 1})
	if result.IsError {
		t.Fatalf("large offset rejected: %#v", result)
	}
}

func TestWriteAndEditCreateCheckpoints(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	args := baseArgs(workspaceID, root)
	args["path"] = "file.txt"
	args["content"] = "alpha beta"
	result := callTool(t, runtime, "write_file", args)
	write := result.StructuredContent.(WriteFileResult)
	if write.CheckpointID == nil {
		t.Fatal("write_file did not create checkpoint")
	}

	editArgs := baseArgs(workspaceID, root)
	editArgs["path"] = "file.txt"
	editArgs["old_text"] = "beta"
	editArgs["new_text"] = "gamma"
	result = callTool(t, runtime, "edit_file", editArgs)
	edit := result.StructuredContent.(EditFileResult)
	if edit.CheckpointID == nil || !strings.Contains(edit.Diff, "+ alpha gamma") {
		t.Fatalf("unexpected edit result: %#v", edit)
	}
	data, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha gamma" {
		t.Fatalf("content = %q", data)
	}
}

func TestEditDryRunDoesNotWriteOrCheckpoint(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	args := baseArgs(workspaceID, root)
	args["path"] = "file.txt"
	args["old_text"] = "old"
	args["new_text"] = "new"
	args["dry_run"] = true
	result := callTool(t, runtime, "edit_file", args)
	value := result.StructuredContent.(EditFileResult)
	if value.CheckpointID != nil || !value.DryRun {
		t.Fatalf("unexpected dry run: %#v", value)
	}
	data, _ := os.ReadFile(file)
	if string(data) != "old" {
		t.Fatalf("dry-run mutated file: %q", data)
	}
}

func TestMultiFilePatchRejectsEscapeBeforeMutation(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	file := filepath.Join(root, "safe.txt")
	if err := os.WriteFile(file, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	args := baseArgs(workspaceID, root)
	args["patch"] = "*** Begin Patch\n*** Update File: safe.txt\n@@\n-old\n+new\n*** Add File: ../escape.txt\n+bad\n*** End Patch"
	result := callTool(t, runtime, "apply_patch", args)
	if !result.IsError {
		t.Fatalf("escape patch was not rejected: %#v", result)
	}
	data, _ := os.ReadFile(file)
	if string(data) != "old\n" {
		t.Fatalf("safe file mutated before validation: %q", data)
	}
}

func TestGlobAndGrep(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.ts"), []byte("const hello = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "b.go"), []byte("package test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	globArgs := baseArgs(workspaceID, root)
	globArgs["pattern"] = "**/*.ts"
	globResult := callTool(t, runtime, "glob", globArgs).StructuredContent.(GlobResult)
	if len(globResult.Matches) != 1 || !strings.HasSuffix(globResult.Matches[0], filepath.Join("src", "a.ts")) {
		t.Fatalf("glob = %#v", globResult)
	}

	grepArgs := baseArgs(workspaceID, root)
	grepArgs["pattern"] = "hello"
	grepArgs["glob"] = "*.ts"
	grepResult := callTool(t, runtime, "grep", grepArgs).StructuredContent.(GrepResult)
	if !strings.Contains(grepResult.Output, "a.ts:1") {
		t.Fatalf("grep = %#v", grepResult)
	}
}

func TestDeleteAndMoveStayInsideWorkspace(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	moveArgs := baseArgs(workspaceID, root)
	moveArgs["source"] = "a.txt"
	moveArgs["destination"] = filepath.Join(t.TempDir(), "outside.txt")
	result := callTool(t, runtime, "move_file", moveArgs)
	if !result.IsError {
		t.Fatalf("outside move was not rejected: %#v", result)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("source changed after denied move: %v", err)
	}

	deleteArgs := baseArgs(workspaceID, root)
	deleteArgs["path"] = filepath.Join(t.TempDir(), "outside.txt")
	result = callTool(t, runtime, "delete_file", deleteArgs)
	if !result.IsError {
		t.Fatalf("outside delete was not rejected: %#v", result)
	}
}

func TestRootedToolPathRejectsSymlinkSwapEscape(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	runtime, workspaceID, root := newToolTestRuntime(t)
	safe := filepath.Join(root, "safe")
	outside := t.TempDir()
	if err := os.Mkdir(safe, 0755); err != nil {
		t.Fatal(err)
	}
	resolved, err := runtime.Workspaces.ResolvePath(workspaceID, root, filepath.Join("safe", "file.txt"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(safe); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, safe); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rooted, err := openRootedPath(runtime.Workspaces, workspaceID, resolved)
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(statErr) {
			t.Fatalf("outside file was created: %v", statErr)
		}
		return
	}
	defer rooted.Close()
	if err := rooted.WriteFile([]byte("escape"), 0644); err == nil {
		t.Fatal("rooted tool write followed swapped symlink outside workspace")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestRootedRecursiveSearchRejectsSymlinkSwapEscape(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	runtime, workspaceID, root := newToolTestRuntime(t)
	child := filepath.Join(root, "child")
	outside := t.TempDir()
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rooted, err := openRootedDirectory(runtime.Workspaces, workspaceID, root)
	if err != nil {
		t.Fatal(err)
	}
	defer rooted.Close()
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, child); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	output, err := grepSearch(rooted, GrepOptions{Pattern: "needle", Glob: "*.txt", OutputMode: "content", HeadLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if output != "No matches found" || strings.Contains(output, "secret.txt") {
		t.Fatalf("recursive search escaped rooted workspace: %q", output)
	}
}

func TestCopyAndMoveAcrossAllowedRootsStayRooted(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	allowed := t.TempDir()
	if _, err := runtime.Workspaces.AddAllowDir(workspaceID, allowed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "copy.txt"), []byte("copy"), 0644); err != nil {
		t.Fatal(err)
	}
	copyResult := callTool(t, runtime, "copy_file", map[string]any{"workspace_id": workspaceID, "source": "copy.txt", "destination": filepath.Join(allowed, "nested", "copy.txt")})
	if copyResult.IsError {
		t.Fatalf("copy across roots failed: %#v", copyResult)
	}
	data, err := os.ReadFile(filepath.Join(allowed, "nested", "copy.txt"))
	if err != nil || string(data) != "copy" {
		t.Fatalf("copied data=%q err=%v", data, err)
	}
	if err := os.WriteFile(filepath.Join(root, "move.txt"), []byte("move"), 0644); err != nil {
		t.Fatal(err)
	}
	moveResult := callTool(t, runtime, "move_file", map[string]any{"workspace_id": workspaceID, "source": "move.txt", "destination": filepath.Join(allowed, "move.txt")})
	if moveResult.IsError {
		t.Fatalf("move across roots failed: %#v", moveResult)
	}
	if _, err := os.Stat(filepath.Join(root, "move.txt")); !os.IsNotExist(err) {
		t.Fatalf("move source still exists: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(allowed, "move.txt"))
	if err != nil || string(data) != "move" {
		t.Fatalf("moved data=%q err=%v", data, err)
	}
}

func TestDeleteDirectoryLargeFileCanBeRewound(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	runtime.Checkpoints.MaxFileBytes = 1024
	dir := filepath.Join(root, "tree")
	file := filepath.Join(dir, "large.bin")
	content := bytes.Repeat([]byte("checkpoint-blob"), 1024)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, content, 0644); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, runtime, "delete_directory", map[string]any{"workspace_id": workspaceID, "path": "tree"})
	if result.IsError {
		t.Fatalf("delete_directory failed: %#v", result)
	}
	value := result.StructuredContent.(map[string]any)
	checkpointID, ok := value["checkpoint_id"].(*string)
	if !ok || checkpointID == nil || *checkpointID == "" {
		t.Fatalf("checkpoint id = %#v", value["checkpoint_id"])
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists after delete: %v", err)
	}
	restore := callTool(t, runtime, "rewind", map[string]any{"workspace_id": workspaceID, "action": "restore", "checkpoint_id": *checkpointID})
	if restore.IsError {
		t.Fatalf("rewind failed: %#v", restore)
	}
	restored, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, content) {
		t.Fatal("rewound directory content differs")
	}
}

func TestDeleteFileLargeFileCanBeRewound(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	runtime.Checkpoints.MaxFileBytes = 1024
	file := filepath.Join(root, "large.bin")
	content := bytes.Repeat([]byte("large-delete"), 1024)
	if err := os.WriteFile(file, content, 0644); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, runtime, "delete_file", map[string]any{"workspace_id": workspaceID, "path": "large.bin"})
	if result.IsError {
		t.Fatalf("delete_file failed: %#v", result)
	}
	value := result.StructuredContent.(DeleteResult)
	if value.CheckpointID == nil || *value.CheckpointID == "" {
		t.Fatalf("checkpoint id = %#v", value.CheckpointID)
	}
	restore := callTool(t, runtime, "rewind", map[string]any{"workspace_id": workspaceID, "action": "restore", "checkpoint_id": *value.CheckpointID})
	if restore.IsError {
		t.Fatalf("rewind failed: %#v", restore)
	}
	restored, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(restored, content) {
		t.Fatalf("rewound file differs: bytes=%d err=%v", len(restored), err)
	}
}

func TestDeleteDirectoryAbortsWhenCheckpointCannotBeComplete(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	runtime.Checkpoints.MaxDirectoryDepth = 1
	dir := filepath.Join(root, "tree")
	file := filepath.Join(dir, "one", "two", "file.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("must survive"), 0644); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, runtime, "delete_directory", map[string]any{"workspace_id": workspaceID, "path": "tree"})
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "checkpoint directory depth exceeds") {
		t.Fatalf("delete_directory result = %#v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "must survive" {
		t.Fatalf("mutation ran after incomplete checkpoint: content=%q err=%v", data, err)
	}
}

func TestDeleteDirectoryAbortsForExternalSymlink(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	runtime, workspaceID, root := newToolTestRuntime(t)
	dir := filepath.Join(root, "tree")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("external"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "external-link")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, runtime, "delete_directory", map[string]any{"workspace_id": workspaceID, "path": "tree"})
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "symlink target escapes allowed root") {
		t.Fatalf("delete_directory result = %#v", result)
	}
	if target, err := os.Readlink(link); err != nil || target != external {
		t.Fatalf("directory changed after rejected checkpoint: target=%q err=%v", target, err)
	}
}

func TestDeleteAndMoveRejectWorkspaceRoot(t *testing.T) {
	runtime, workspaceID, root := newToolTestRuntime(t)
	file := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(file, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	deleteResult := callTool(t, runtime, "delete_directory", map[string]any{"workspace_id": workspaceID, "path": root})
	if !deleteResult.IsError || len(deleteResult.Content) == 0 || !strings.Contains(deleteResult.Content[0].Text, "cannot delete workspace or allowed root") {
		t.Fatalf("delete workspace root result = %#v", deleteResult)
	}
	moveResult := callTool(t, runtime, "move_file", map[string]any{"workspace_id": workspaceID, "source": root, "destination": filepath.Join(root, "moved")})
	if !moveResult.IsError || len(moveResult.Content) == 0 || !strings.Contains(moveResult.Content[0].Text, "cannot move workspace or allowed root") {
		t.Fatalf("move workspace root result = %#v", moveResult)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "keep" {
		t.Fatalf("workspace root changed: content=%q err=%v", data, err)
	}
	allowed := t.TempDir()
	if _, err := runtime.Workspaces.AddAllowDir(workspaceID, allowed); err != nil {
		t.Fatal(err)
	}
	allowedResult := callTool(t, runtime, "delete_directory", map[string]any{"workspace_id": workspaceID, "path": allowed})
	if !allowedResult.IsError || len(allowedResult.Content) == 0 || !strings.Contains(allowedResult.Content[0].Text, "cannot delete workspace or allowed root") {
		t.Fatalf("delete allowed root result = %#v", allowedResult)
	}
}

func TestRunCommandMutationGuardUsesResolvedCWD(t *testing.T) {
	if os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	runtime, workspaceID, root := newToolTestRuntime(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child, "file.txt")
	moved := filepath.Join(child, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	args := baseArgs(workspaceID, root)
	args["command"] = "cd child && mv file.txt moved.txt"
	result := callTool(t, runtime, "run_command", args)
	if result.IsError {
		t.Fatalf("cwd-changing mutation failed: %#v", result)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestGitStatusUsesWorkspaceRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	runtime, workspaceID, root := newToolTestRuntime(t)
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	args := baseArgs(workspaceID, root)
	result := callTool(t, runtime, "git_status", args)
	value := result.StructuredContent.(GitStatusResult)
	if !strings.Contains(value.Output, "untracked.txt") {
		t.Fatalf("git status = %#v", value)
	}
}

func TestFilesystemToolCatalog(t *testing.T) {
	runtime, _, _ := newToolTestRuntime(t)
	names := map[string]bool{}
	for _, schema := range runtime.List() {
		names[schema.Name] = true
	}
	for _, name := range []string{
		"read_text_file", "read_file_base64", "write_file", "write_file_base64", "edit_file", "multi_edit",
		"replace_regex", "apply_patch", "list_directory", "glob", "grep", "delete_file", "create_directory",
		"delete_directory", "copy_file", "move_file", "directory_tree", "list_allowed_directories",
		"read_files",
	} {
		if !names[name] {
			t.Fatalf("missing tool %q", name)
		}
	}
	if names["search_files"] {
		t.Fatal("search_files overlaps grep and should not be exposed")
	}
}

func TestWorkspaceBoundSchemasDoNotExposeWorkingDirectory(t *testing.T) {
	runtime, _, _ := newToolTestRuntime(t)
	for _, schema := range runtime.List() {
		input := string(schema.InputSchema)
		if strings.Contains(input, `"workspace_id"`) && strings.Contains(input, `"working_directory"`) {
			t.Fatalf("%s still exposes working_directory: %s", schema.Name, schema.InputSchema)
		}
	}
}

func TestWorkspaceBoundSchemasRequireWorkspaceID(t *testing.T) {
	runtime, _, _ := newToolTestRuntime(t)
	for _, schema := range runtime.List() {
		workspaceScoped, err := schemaHasWorkspaceID(schema)
		if err != nil {
			t.Fatalf("%s input schema: %v", schema.Name, err)
		}
		if !workspaceScoped {
			continue
		}
		var input struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(schema.InputSchema, &input); err != nil {
			t.Fatalf("%s input schema: %v", schema.Name, err)
		}
		required := false
		for _, name := range input.Required {
			if name == "workspace_id" {
				required = true
				break
			}
		}
		if !required {
			t.Fatalf("%s declares workspace_id but does not require it: %s", schema.Name, schema.InputSchema)
		}
	}
}

func TestRegistryRejectsDuplicateTools(t *testing.T) {
	registry := NewRegistry()
	handler := func(context.Context, map[string]any) (Result, error) { return Result{}, nil }
	schema := DefaultSchema("test", "test")
	if err := registry.Register("test", schema, handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("test", schema, handler); !errors.Is(err, ErrToolAlreadyRegistered) {
		t.Fatalf("error = %v, want ErrToolAlreadyRegistered", err)
	}
}

func TestAllowedDirectorySupportsFilesystemAndRewind(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddAllowDir(item.ID, allowed); err != nil {
		t.Fatal(err)
	}
	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "state"))
	registry := NewRegistry()
	RegisterWorkspaceTools(registry, manager)
	RegisterCore(registry, manager, checkpoints)
	runtime := &Runtime{Registry: registry, Workspaces: manager, Checkpoints: checkpoints}

	file := filepath.Join(allowed, "artifact.txt")
	write := callTool(t, runtime, "write_file", map[string]any{"workspace_id": item.ID, "path": file, "content": "before"})
	if write.IsError || write.StructuredContent.(WriteFileResult).CheckpointID == nil {
		t.Fatalf("allowed write failed: %#v", write)
	}
	edit := callTool(t, runtime, "edit_file", map[string]any{"workspace_id": item.ID, "path": file, "old_text": "before", "new_text": "after"})
	editResult := edit.StructuredContent.(EditFileResult)
	if edit.IsError || editResult.CheckpointID == nil {
		t.Fatalf("allowed edit failed: %#v", edit)
	}
	restore := callTool(t, runtime, "rewind", map[string]any{"workspace_id": item.ID, "action": "restore", "checkpoint_id": *editResult.CheckpointID})
	if restore.IsError {
		t.Fatalf("allowed rewind failed: %#v", restore)
	}
	data, err := os.ReadFile(filepath.Join(allowed, "artifact.txt"))
	if err != nil || string(data) != "before" {
		t.Fatalf("restored content = %q err=%v", data, err)
	}

	edit = callTool(t, runtime, "edit_file", map[string]any{"workspace_id": item.ID, "path": file, "old_text": "before", "new_text": "after"})
	editResult = edit.StructuredContent.(EditFileResult)
	if _, err := manager.RemoveAllowDir(item.ID, allowed); err != nil {
		t.Fatal(err)
	}
	preview := callTool(t, runtime, "rewind", map[string]any{"workspace_id": item.ID, "action": "preview", "checkpoint_id": *editResult.CheckpointID})
	if !preview.IsError {
		t.Fatalf("revoked allow dir remained rewind-accessible: %#v", preview)
	}
}
