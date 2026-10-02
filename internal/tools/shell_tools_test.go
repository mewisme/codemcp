package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/checkpoint"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

func newShellToolTestRuntime(t *testing.T) (*Runtime, string, string) {
	t.Helper()
	root := t.TempDir()
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoint-state"))
	registry := NewRegistry()
	shell := shellruntime.NewManager(workspaces, filepath.Join(t.TempDir(), "shell-state"))
	RegisterWorkspaceTools(registry, workspaces, shell)
	RegisterFilesystemTools(registry, workspaces, checkpoints)
	processes := shellruntime.NewProcessManager(workspaces, shell)
	RegisterShellTools(registry, workspaces, shell, processes)
	return &Runtime{Registry: registry, Workspaces: workspaces, Checkpoints: checkpoints, Executions: shell.Executions()}, item.ID, item.Path
}

func TestRunCommandExecutionCarriesSafeCallAttribution(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	runtime, workspaceID, _ := newShellToolTestRuntime(t)
	command := "printf attribution"
	const sessionID = "raw-session-secret"
	ctx := WithCallSource(WithMCPSessionID(context.Background(), sessionID), "tunnel")
	result, err := runtime.Call(ctx, "run_command", map[string]any{"workspace_id": workspaceID, "command": command})
	if err != nil || result.IsError {
		t.Fatalf("run_command failed: result=%#v err=%v", result, err)
	}
	executions := runtime.Executions.List(workspaceID, 1)
	if len(executions) != 1 {
		t.Fatalf("executions=%#v", executions)
	}
	info := executions[0]
	if info.Source != "tunnel" || info.CallID == "" || info.SessionHash != MCPSessionFingerprint(sessionID) || info.ReceivedByInstanceID == "" || info.ExecutedByInstanceID == "" {
		t.Fatalf("execution attribution=%#v", info)
	}
	if strings.Contains(info.SessionHash, sessionID) || info.SessionHash == sessionID {
		t.Fatalf("raw session leaked: %#v", info)
	}
}

func TestShellToolsPersistCWD(t *testing.T) {
	runtime, workspaceID, root := newShellToolTestRuntime(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(context.Background(), "run_command", map[string]any{
		"workspace_id": workspaceID,
		"command":      "cd child",
	})
	if err != nil || result.IsError {
		t.Fatalf("run_command failed: result=%#v err=%v", result, err)
	}
	statusResult, err := runtime.Call(context.Background(), "shell_status", map[string]any{"workspace_id": workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	status := statusResult.StructuredContent.(shellruntime.Status)
	if filepath.Clean(status.CWD) != filepath.Clean(child) {
		t.Fatalf("cwd = %q, want %q", status.CWD, child)
	}
	workspaceStatusResult, err := runtime.Call(context.Background(), "workspace_status", map[string]any{"workspace_id": workspaceID})
	if err != nil || workspaceStatusResult.IsError {
		t.Fatalf("workspace_status failed: result=%#v err=%v", workspaceStatusResult, err)
	}
	workspaceStatus := workspaceStatusResult.StructuredContent.(WorkspaceStatusResult)
	if filepath.Clean(workspaceStatus.WorkspaceRoot) != filepath.Clean(root) || filepath.Clean(workspaceStatus.ShellCWD) != filepath.Clean(child) {
		t.Fatalf("workspace status = %#v", workspaceStatus)
	}
	if len(workspaceStatus.AllowedDirectories) == 0 || filepath.Clean(workspaceStatus.AllowedDirectories[0]) != filepath.Clean(root) {
		t.Fatalf("allowed directories = %#v", workspaceStatus.AllowedDirectories)
	}
}

func TestShellMutationUsesPersistentCWD(t *testing.T) {
	runtime, workspaceID, root := newShellToolTestRuntime(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child, "file.txt")
	moved := filepath.Join(child, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _ = runtime.Call(context.Background(), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "cd child",
	})
	result, err := runtime.Call(context.Background(), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "mv file.txt moved.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("mutation failed: %#v", result)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestShellMutationAllowsCWDDirective(t *testing.T) {
	runtime, workspaceID, root := newShellToolTestRuntime(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child, "file.txt")
	moved := filepath.Join(child, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(context.Background(), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "cd child && mv file.txt moved.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("mutation failed: %#v", result)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestShellMutationAllowsCWDDirectiveIntoAllowedDirectory(t *testing.T) {
	runtime, workspaceID, _ := newShellToolTestRuntime(t)
	allowed := t.TempDir()
	if _, err := runtime.Workspaces.AddAllowDir(workspaceID, allowed); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(allowed, "file.txt")
	moved := filepath.Join(allowed, "moved.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(context.Background(), "run_command", map[string]any{
		"workspace_id": workspaceID, "command": "cd " + allowed + " && mv file.txt moved.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("allowed-dir mutation failed: %#v", result)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func backgroundLifecycleCommand() string {
	return "printf ready; sleep 0.1"
}

func TestStartProcessResultUsesCapabilitySpecificNoPollGuidance(t *testing.T) {
	value := shellruntime.StartResult{ID: "proc_test", PID: 123, Command: "example", CWD: "/tmp", StartedAt: "2026-09-26T00:00:00Z"}
	tests := []struct {
		name         string
		capabilities BackgroundCapabilities
		want         string
		forbid       string
	}{
		{name: "unproven", want: "No model continuation is proven"},
		{name: "tasks-only", capabilities: BackgroundCapabilities{TaskObservation: true}, want: "Tasks provide observation only", forbid: "client proves model continuation"},
		{name: "continuation", capabilities: BackgroundCapabilities{ModelContinuation: true}, want: "client proves model continuation", forbid: "return control after starting background work"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithBackgroundCapabilities(context.Background(), tc.capabilities)
			result := startProcessResult(ctx, value)
			if got, ok := result.StructuredContent.(shellruntime.StartResult); !ok || got.ID != value.ID {
				t.Fatalf("structured content=%T %#v", result.StructuredContent, result.StructuredContent)
			}
			if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, tc.want) || !strings.Contains(result.Content[0].Text, "Do not poll process_status/process_output") {
				t.Fatalf("content=%#v", result.Content)
			}
			if tc.forbid != "" && strings.Contains(result.Content[0].Text, tc.forbid) {
				t.Fatalf("content contains forbidden guidance %q: %s", tc.forbid, result.Content[0].Text)
			}
		})
	}
}

func TestBackgroundProcessLifecycle(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	runtime, workspaceID, _ := newShellToolTestRuntime(t)
	startResult, err := runtime.Call(context.Background(), "start_process", map[string]any{
		"workspace_id": workspaceID,
		"command":      backgroundLifecycleCommand(),
	})
	if err != nil || startResult.IsError {
		t.Fatalf("start_process failed: result=%#v err=%v", startResult, err)
	}
	started := startResult.StructuredContent.(shellruntime.StartResult)
	if started.ID == "" || started.PID <= 0 {
		t.Fatalf("bad process result: %#v", started)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statusResult, err := runtime.Call(context.Background(), "process_status", map[string]any{"workspace_id": workspaceID, "id": started.ID})
		if err != nil {
			t.Fatal(err)
		}
		status := statusResult.StructuredContent.(ProcessStatusResult)
		if len(status.Processes) == 1 && !status.Processes[0].Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	outputResult, err := runtime.Call(context.Background(), "process_output", map[string]any{"workspace_id": workspaceID, "id": started.ID})
	if err != nil {
		t.Fatal(err)
	}
	output := outputResult.StructuredContent.(shellruntime.OutputResult)
	if !strings.Contains(output.Stdout, "ready") {
		t.Fatalf("stdout = %q", output.Stdout)
	}

	clearResult, err := runtime.Call(context.Background(), "clear_processes", map[string]any{"workspace_id": workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if clearResult.StructuredContent.(ClearProcessesResult).Cleared != 1 {
		t.Fatalf("clear result = %#v", clearResult.StructuredContent)
	}
}

func TestRuntimeOwnsShellToolProcessManager(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	runtime := NewRuntime()
	if runtime.Processes == nil {
		t.Fatal("runtime process manager is nil")
	}
	root := t.TempDir()
	item, err := runtime.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(context.Background(), "start_process", map[string]any{"workspace_id": item.ID, "command": backgroundLifecycleCommand()})
	if err != nil || result.IsError {
		t.Fatalf("start_process failed: result=%#v err=%v", result, err)
	}
	started := result.StructuredContent.(shellruntime.StartResult)
	if started.ExecutionID == "" {
		t.Fatal("start_process did not expose execution id")
	}
	processes, err := runtime.Processes.Status(item.ID, started.ID)
	if err != nil || len(processes) != 1 || processes[0].ID != started.ID || processes[0].ExecutionID != started.ExecutionID {
		t.Fatalf("runtime process lookup=%#v err=%v", processes, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, snapshotErr := runtime.Executions.Get(item.ID, started.ExecutionID)
		if snapshotErr == nil && snapshot.Execution.Status != shellruntime.ExecutionStatusRunning {
			if !strings.Contains(snapshot.Stdout, "ready") || snapshot.Execution.Tool != "start_process" {
				t.Fatalf("process execution snapshot=%#v", snapshot)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("process execution did not complete in execution hub")
}

func TestBackgroundMutationUsesPersistedCWDAndRejectsOutside(t *testing.T) {
	runtime, workspaceID, _ := newShellToolTestRuntime(t)
	result, err := runtime.Call(context.Background(), "start_process", map[string]any{
		"workspace_id": workspaceID,
		"command":      "touch file.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("background mutation failed: %#v", result)
	}
	started := result.StructuredContent.(shellruntime.StartResult)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statusResult, statusErr := runtime.Call(context.Background(), "process_status", map[string]any{"workspace_id": workspaceID, "id": started.ID})
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		status := statusResult.StructuredContent.(ProcessStatusResult)
		if len(status.Processes) == 1 && !status.Processes[0].Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := runtime.Call(context.Background(), "clear_processes", map[string]any{"workspace_id": workspaceID}); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	result, err = runtime.Call(context.Background(), "start_process", map[string]any{
		"workspace_id": workspaceID,
		"command":      "rm " + outside,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("outside background mutation was not denied: %#v", result)
	}
}
