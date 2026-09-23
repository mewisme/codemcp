package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/workspace"
)

func newGitToolTestRuntime(t *testing.T) (*Runtime, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoint-state"))
	registry := NewRegistry()
	RegisterWorkspaceTools(registry, workspaces)
	RegisterFilesystemTools(registry, workspaces, checkpoints)
	RegisterGitTools(registry, workspaces)
	initGitRepo(t, item.Path)
	return &Runtime{Registry: registry, Workspaces: workspaces, Checkpoints: checkpoints}, item.ID, item.Path
}

func initGitRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.com"},
		{"config", "core.autocrlf", "false"},
		{"config", "core.eol", "lf"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, output)
		}
	}
}

func gitToolCall(t *testing.T, runtime *Runtime, name string, args map[string]any) Result {
	t.Helper()
	result, err := runtime.Call(context.Background(), name, args)
	if err != nil {
		t.Fatalf("%s protocol error: %v", name, err)
	}
	return result
}

func gitBaseArgs(workspaceID, _ string) map[string]any {
	return map[string]any{"workspace_id": workspaceID}
}

func TestGitAddCommitLogDiffAndRestore(t *testing.T) {
	runtime, workspaceID, root := newGitToolTestRuntime(t)
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("initial\n"), 0644); err != nil {
		t.Fatal(err)
	}

	addArgs := gitBaseArgs(workspaceID, root)
	addArgs["files"] = []any{"file.txt"}
	addArgs["all"] = false
	if result := gitToolCall(t, runtime, "git_add", addArgs); result.IsError {
		t.Fatalf("git_add failed: %#v", result)
	}

	commitArgs := gitBaseArgs(workspaceID, root)
	commitArgs["message"] = "initial"
	commitArgs["stage_all"] = false
	commitResult := gitToolCall(t, runtime, "git_commit", commitArgs)
	if commitResult.IsError {
		t.Fatalf("git_commit failed: %#v", commitResult)
	}

	logArgs := gitBaseArgs(workspaceID, root)
	logArgs["count"] = 5
	logResult := gitToolCall(t, runtime, "git_log", logArgs).StructuredContent.(GitLogResult)
	if len(logResult.Commits) != 1 || !strings.Contains(logResult.Commits[0], "initial") {
		t.Fatalf("log = %#v", logResult)
	}

	if err := os.WriteFile(file, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	diffArgs := gitBaseArgs(workspaceID, root)
	diffArgs["file"] = "file.txt"
	diffResult := gitToolCall(t, runtime, "git_diff", diffArgs).StructuredContent.(GitDiffResult)
	if !strings.Contains(diffResult.Output, "-initial") || !strings.Contains(diffResult.Output, "+changed") {
		t.Fatalf("diff = %#v", diffResult)
	}

	restoreArgs := gitBaseArgs(workspaceID, root)
	restoreArgs["files"] = []any{"file.txt"}
	restoreCtx := controlguard.WithGrant(context.Background(), controlguard.Grant{RequestID: "req_test", Code: controlguard.CodeDestructiveMutation})
	restoreResult, err := runtime.Call(restoreCtx, "git_restore", restoreArgs)
	if err != nil {
		t.Fatal(err)
	}
	if restoreResult.IsError {
		t.Fatalf("git_restore failed: %#v", restoreResult)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "initial\n" {
		t.Fatalf("restored content = %q", data)
	}
}

func TestGitPushAllowsNormalPushAndForcePushIsDestructive(t *testing.T) {
	runtime, workspaceID, _ := newGitToolTestRuntime(t)
	identity, err := runtime.Workspaces.Instance()
	if err != nil {
		t.Fatal(err)
	}
	runtime.SessionAccess = NewSessionWorkspaceAccessManager()
	runtime.Approvals = approval.NewManager(identity.ID)
	RegisterApprovalTools(runtime.Registry, runtime)
	ctx := approvalContext("git-approval")
	result, err := runtime.Call(ctx, "git_push", map[string]any{"workspace_id": workspaceID, "remote": "origin"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError && strings.Contains(result.Content[0].Text, "local approval") {
		t.Fatalf("normal push unexpectedly required approval: %#v", result)
	}
	result, err = runtime.Call(ctx, "git_push", map[string]any{"workspace_id": workspaceID, "remote": "origin", "force": true})
	if err != nil || !result.IsError {
		t.Fatalf("force push result=%#v err=%v", result, err)
	}
	challenge, ok := result.StructuredContent.(approvalRequiredResponse)
	if !ok || challenge.TargetTool != "git_push" || challenge.GuardCode != string(controlguard.CodeDestructiveMutation) {
		t.Fatalf("challenge=%#v", result.StructuredContent)
	}
}

func TestGitBranchCheckoutStashAndReset(t *testing.T) {
	runtime, workspaceID, root := newGitToolTestRuntime(t)
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("initial\n"), 0644); err != nil {
		t.Fatal(err)
	}
	addArgs := gitBaseArgs(workspaceID, root)
	addArgs["all"] = true
	if result := gitToolCall(t, runtime, "git_add", addArgs); result.IsError {
		t.Fatal(result.Content)
	}
	commitArgs := gitBaseArgs(workspaceID, root)
	commitArgs["message"] = "initial"
	if result := gitToolCall(t, runtime, "git_commit", commitArgs); result.IsError {
		t.Fatal(result.Content)
	}

	branchArgs := gitBaseArgs(workspaceID, root)
	branchArgs["action"] = "create"
	branchArgs["name"] = "feature"
	if result := gitToolCall(t, runtime, "git_branch", branchArgs); result.IsError {
		t.Fatalf("git_branch failed: %#v", result)
	}
	checkoutArgs := gitBaseArgs(workspaceID, root)
	checkoutArgs["branch"] = "feature"
	if result := gitToolCall(t, runtime, "git_checkout", checkoutArgs); result.IsError {
		t.Fatalf("git_checkout failed: %#v", result)
	}

	if err := os.WriteFile(file, []byte("work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stashArgs := gitBaseArgs(workspaceID, root)
	stashArgs["action"] = "push"
	stashArgs["message"] = "work"
	if result := gitToolCall(t, runtime, "git_stash", stashArgs); result.IsError {
		t.Fatalf("git_stash push failed: %#v", result)
	}
	stashArgs["action"] = "list"
	list := gitToolCall(t, runtime, "git_stash", stashArgs).StructuredContent.(GitStashResult)
	if !strings.Contains(list.Output, "work") {
		t.Fatalf("stash list = %#v", list)
	}
	stashArgs["action"] = "pop"
	if result := gitToolCall(t, runtime, "git_stash", stashArgs); result.IsError {
		t.Fatalf("git_stash pop failed: %#v", result)
	}

	resetArgs := gitBaseArgs(workspaceID, root)
	resetArgs["mode"] = "mixed"
	resetArgs["ref"] = "HEAD"
	if result := gitToolCall(t, runtime, "git_reset", resetArgs); result.IsError {
		t.Fatalf("git_reset failed: %#v", result)
	}
}

func TestGitPathResolvesFromWorkspaceRoot(t *testing.T) {
	runtime, workspaceID, root := newGitToolTestRuntime(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	result := gitToolCall(t, runtime, "git_status", map[string]any{"workspace_id": workspaceID, "path": "child"})
	if result.IsError {
		t.Fatalf("git path failed: %#v", result)
	}
	if got := result.StructuredContent.(GitStatusResult).Path; filepath.Clean(got) != filepath.Clean(child) {
		t.Fatalf("git path = %s, want %s", got, child)
	}
}

func TestGitRepoRootOutsideWorkspaceIsRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	parent := t.TempDir()
	initGitRepo(t, parent)
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(child)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterGitTools(registry, workspaces)
	runtime := &Runtime{Registry: registry, Workspaces: workspaces}
	result := gitToolCall(t, runtime, "git_status", map[string]any{"workspace_id": item.ID})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "repository root escapes registered workspace") {
		t.Fatalf("outside repo root was not rejected: %#v", result)
	}
}

func TestGitRejectsPathspecMagicAndOptionInjection(t *testing.T) {
	runtime, workspaceID, root := newGitToolTestRuntime(t)
	diffArgs := gitBaseArgs(workspaceID, root)
	diffArgs["file"] = ":(top)file.txt"
	result := gitToolCall(t, runtime, "git_diff", diffArgs)
	if !result.IsError {
		t.Fatalf("pathspec magic was not rejected: %#v", result)
	}

	pushArgs := gitBaseArgs(workspaceID, root)
	pushArgs["remote"] = "--upload-pack=evil"
	result = gitToolCall(t, runtime, "git_push", pushArgs)
	if !result.IsError {
		t.Fatalf("remote option injection was not rejected: %#v", result)
	}
}

func TestGitToolCatalog(t *testing.T) {
	runtime, _, _ := newGitToolTestRuntime(t)
	names := map[string]bool{}
	for _, schema := range runtime.List() {
		names[schema.Name] = true
	}
	for _, name := range []string{
		"git_status", "git_diff", "git_log", "git_add", "git_commit", "git_branch",
		"git_checkout", "git_restore", "git_push", "git_pull", "git_stash", "git_reset",
	} {
		if !names[name] {
			t.Fatalf("missing git tool %q", name)
		}
	}
}
