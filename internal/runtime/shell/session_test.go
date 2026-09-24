package shell

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/controlplane"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestShellEnvironmentMarksMCPToolContext(t *testing.T) {
	values := shellEnvironmentMap(context.Background(), nil)
	if values[controlplane.ToolContextEnv] != "1" {
		t.Fatalf("tool context = %q", values[controlplane.ToolContextEnv])
	}
	if values[configformat.EnvConfigDir] != configformat.RootPath() {
		t.Fatalf("config root = %q want %q", values[configformat.EnvConfigDir], configformat.RootPath())
	}
}

func TestShellEnvironmentForwardsOnlyContextApproval(t *testing.T) {
	t.Setenv(controlplane.ControlApprovalEnv, "cap_inherited")
	if value := shellEnvironmentMap(context.Background(), nil)[controlplane.ControlApprovalEnv]; value != "" {
		t.Fatalf("unapproved shell inherited capability %q", value)
	}
	ctx := controlguard.WithApproval(context.Background(), controlguard.Approval{
		RequestID: "req_test", Capability: "cap_approved", Invocation: controlguard.Invocation{Program: "cm", Args: []string{"update"}, Command: "cm update"},
	})
	values := shellEnvironmentMap(ctx, nil)
	if values[controlplane.ControlApprovalEnv] != "cap_approved" || values[controlplane.ToolContextEnv] != "1" {
		t.Fatalf("approved shell env = %#v", values)
	}
}

func TestShellEnvironmentInheritsParentAndPrependsConfiguredPath(t *testing.T) {
	t.Setenv("CUSTOM_VISIBLE", "visible")
	configured := t.TempDir()
	values := shellEnvironmentMap(context.Background(), []string{configured})
	if values["CUSTOM_VISIBLE"] != "visible" {
		t.Fatalf("parent environment not inherited: %#v", values)
	}
	pathValue := values["PATH"]
	if pathValue == "" && runtime.GOOS == "windows" {
		pathValue = values["Path"]
	}
	parts := filepath.SplitList(pathValue)
	if len(parts) == 0 || filepath.Clean(parts[0]) != filepath.Clean(configured) {
		t.Fatalf("configured shell path was not prepended: %q", pathValue)
	}
}

func TestShellEnvironmentOutputIsDeterministic(t *testing.T) {
	values := shellEnvironment(context.Background(), nil)
	for index := 1; index < len(values); index++ {
		previous, _, _ := strings.Cut(values[index-1], "=")
		current, _, _ := strings.Cut(values[index], "=")
		if strings.ToUpper(previous) > strings.ToUpper(current) {
			t.Fatalf("environment is not sorted: %#v", values)
		}
	}
}

func TestApprovedControlPlaneCommandUsesCurrentExecutable(t *testing.T) {
	invocation := controlguard.Invocation{Program: "cm", Args: []string{"config", "set", "server.port", "41001"}, Command: "cm config set server.port 41001"}
	ctx := controlguard.WithApproval(context.Background(), controlguard.Approval{RequestID: "req_test", Capability: "cap_test", Invocation: invocation})
	cmd, err := commandForPlatform(ctx, invocation.Command)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(cmd.Path) != filepath.Clean(executable) || len(cmd.Args) != len(invocation.Args)+1 {
		t.Fatalf("approved command = path %q args %#v", cmd.Path, cmd.Args)
	}
	for index := range invocation.Args {
		if cmd.Args[index+1] != invocation.Args[index] {
			t.Fatalf("arg %d = %q want %q", index, cmd.Args[index+1], invocation.Args[index])
		}
	}
	if _, err := commandForPlatform(ctx, "cm config set server.port 41002"); err == nil {
		t.Fatal("changed approved shell command selected current executable")
	}
}

func shellEnvironmentMap(ctx context.Context, shellPath []string) map[string]string {
	values := map[string]string{}
	for _, value := range shellEnvironment(ctx, shellPath) {
		if index := strings.IndexByte(value, '='); index >= 0 {
			values[value[:index]] = value[index+1:]
		}
	}
	return values
}

func newShellTestManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(workspaces, filepath.Join(t.TempDir(), "state")), item.ID, item.Path
}

func TestShellExecReturnsParentCancellationBeforeInternalTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep command test")
	}
	manager, workspaceID, _ := newShellTestManager(t)
	manager.timeout = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := manager.Exec(ctx, workspaceID, "sleep 1; printf done")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v want parent deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("parent cancellation took %s; child process likely kept shell pipes open", elapsed)
	}
	if strings.Contains(err.Error(), "timed out after 2s") {
		t.Fatalf("parent cancellation was misreported as internal timeout: %v", err)
	}
}

func TestShellPersistsCWD(t *testing.T) {
	manager, workspaceID, root := newShellTestManager(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Exec(context.Background(), workspaceID, "cd child")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(result.CWD) != filepath.Clean(child) {
		t.Fatalf("cwd = %q, want %q", result.CWD, child)
	}
	status, err := manager.Status(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(status.CWD) != filepath.Clean(child) {
		t.Fatalf("persisted cwd = %q, want %q", status.CWD, child)
	}

	reloaded := NewManager(manager.workspaces, manager.root)
	status, err = reloaded.Status(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(status.CWD) != filepath.Clean(child) {
		t.Fatalf("disk cwd = %q, want %q", status.CWD, child)
	}
}

func TestMutationUsesPersistentCWD(t *testing.T) {
	manager, workspaceID, root := newShellTestManager(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child, "file.txt")
	moved := filepath.Join(child, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Exec(context.Background(), workspaceID, "cd child"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Exec(context.Background(), workspaceID, "mv file.txt moved.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestMutationAllowsCWDChangeWithinWorkspace(t *testing.T) {
	manager, workspaceID, root := newShellTestManager(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child, "file.txt")
	moved := filepath.Join(child, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Exec(context.Background(), workspaceID, "cd child && mv file.txt moved.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestMutationAllowsCWDChangeIntoExplicitAllowedDirectory(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	workspaces := workspace.NewManagerWithGlobalAllowDirs(filepath.Join(t.TempDir(), "workspaces.json"), []string{allowed})
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(workspaces, filepath.Join(t.TempDir(), "state"))
	file := filepath.Join(allowed, "file.txt")
	moved := filepath.Join(allowed, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Exec(context.Background(), item.ID, "cd "+allowed+" && mv file.txt moved.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestMutationRejectsCWDChangeOutsideAllowedRoots(t *testing.T) {
	manager, workspaceID, _ := newShellTestManager(t)
	outside := t.TempDir()
	_, err := manager.Exec(context.Background(), workspaceID, "cd "+outside+" && touch file.txt")
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("error = %v", err)
	}
}

func TestMutationUsesWorkspaceRootByDefault(t *testing.T) {
	manager, workspaceID, root := newShellTestManager(t)
	file := filepath.Join(root, "file.txt")
	moved := filepath.Join(root, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Exec(context.Background(), workspaceID, "mv file.txt moved.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestShellReset(t *testing.T) {
	manager, workspaceID, root := newShellTestManager(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reset(workspaceID, child); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(status.CWD) != filepath.Clean(child) {
		t.Fatalf("cwd = %q", status.CWD)
	}
}

func TestShellStateUsesCanonicalJSON(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := t.TempDir()
	workspaces := workspace.NewManager(filepath.Join(root, "workspaces.json"))
	item, err := workspaces.Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(workspaces, root)
	if _, err := manager.Status(item.ID); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "workspaces", item.ID, "shell.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("shell state is not JSON: %v", err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != sessionStateVersion {
		t.Fatalf("shell state = %#v err=%v", state, err)
	}
}

func TestShellMarkdownLanguage(t *testing.T) {
	for shell, want := range map[string]string{
		"/bin/bash": "bash", "/usr/bin/zsh": "zsh", "/usr/local/bin/fish": "fish", "/bin/sh": "sh",
		"pwsh.exe": "powershell", "powershell.exe": "powershell", "cmd.exe": "batch", "/opt/custom-shell": "shell",
	} {
		if got := shellMarkdownLanguage(shell); got != want {
			t.Fatalf("shellMarkdownLanguage(%q)=%q want %q", shell, got, want)
		}
	}
}

func TestShellExecBoundsSynchronousOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix output generator")
	}
	manager, workspaceID, _ := newShellTestManager(t)
	result, err := manager.Exec(context.Background(), workspaceID, "head -c 450000 /dev/zero | tr '\\0' x")
	if err != nil {
		t.Fatal(err)
	}
	if !result.StdoutTruncated || result.StderrTruncated || len(result.Stdout) > maxProcessLogChars {
		t.Fatalf("stdout=%d stdout_truncated=%t stderr_truncated=%t", len(result.Stdout), result.StdoutTruncated, result.StderrTruncated)
	}
}
