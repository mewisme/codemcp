package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestWorkspaceRegisterSynchronizesRunningRuntime(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	runtimeManager := workspace.NewManager(workspace.DefaultStorePath())
	if items, err := runtimeManager.List(); err != nil || len(items) != 0 {
		t.Fatalf("initial items=%#v err=%v", items, err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{
		Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil },
		ReloadWorkspaces: func() (workspaceReloadResult, error) {
			if err := runtimeManager.Reload(); err != nil {
				return workspaceReloadResult{}, err
			}
			items, err := runtimeManager.List()
			if err != nil {
				return workspaceReloadResult{}, err
			}
			return workspaceReloadResult{PID: os.Getpid(), Count: len(items)}, nil
		},
		Status:   func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} },
		Shutdown: func() {}, ClearLogs: func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	workspaceRoot := t.TempDir()
	executeRequestCommand(t, root, []string{"workspace", "register", workspaceRoot})
	persistedManager := workspace.NewManager(workspace.DefaultStorePath())
	persisted, err := persistedManager.List()
	if err != nil || len(persisted) != 1 {
		t.Fatalf("persisted workspaces=%#v err=%v", persisted, err)
	}
	if _, err := runtimeManager.Get(persisted[0].ID); err != nil {
		t.Fatalf("running runtime did not see registered workspace %s: %v", persisted[0].ID, err)
	}
}

func TestWorkspaceContainerMutationIsImmediatelyVisibleToRunningMCPRuntime(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	runtimeManager := workspace.NewManager(workspace.DefaultStorePath())
	if values, err := runtimeManager.ListContainers(); err != nil || len(values) != 0 {
		t.Fatalf("initial containers=%#v err=%v", values, err)
	}
	registry := tools.NewRegistry()
	tools.RegisterWorkspaceTools(registry, runtimeManager)
	tools.RegisterWorkspaceContainerTools(registry, runtimeManager)
	runtime := &tools.Runtime{Registry: registry, Workspaces: runtimeManager, Checkpoints: checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoints")), SessionAccess: tools.NewSessionWorkspaceAccessManager()}
	control, err := startRuntimeControl(runtimeControlOptions{
		Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil },
		ReloadWorkspaces: func() (workspaceReloadResult, error) {
			if err := runtime.ReloadWorkspaces(); err != nil {
				return workspaceReloadResult{}, err
			}
			items, err := runtimeManager.List()
			if err != nil {
				return workspaceReloadResult{}, err
			}
			return workspaceReloadResult{PID: os.Getpid(), Count: len(items)}, nil
		},
		Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	executeRequestCommand(t, root, []string{"workspace", "container", "create", "Product"})
	persisted := workspace.NewManager(workspace.DefaultStorePath())
	containers, err := persisted.ListContainers()
	if err != nil || len(containers) != 1 {
		t.Fatalf("persisted containers=%#v err=%v", containers, err)
	}
	containerID := containers[0].ID
	ctx := tools.WithMCPSessionID(context.Background(), "container-live-session")
	status, err := runtime.Call(ctx, "workspace_container_status", map[string]any{"container_id": containerID})
	if err != nil || status.IsError {
		t.Fatalf("status immediately after create = %#v err=%v", status, err)
	}
	statusValue := status.StructuredContent.(tools.WorkspaceContainerStatusResult)
	if statusValue.Name != "Product" || statusValue.WorkspaceCount != 0 {
		t.Fatalf("status immediately after create = %#v", statusValue)
	}
	list, err := runtime.Call(ctx, "workspace_container_list", map[string]any{})
	if err != nil || list.IsError {
		t.Fatalf("list immediately after create = %#v err=%v", list, err)
	}
	listValue := list.StructuredContent.(tools.WorkspaceContainerListResult)
	if listValue.Count != 1 || listValue.Containers[0].ContainerID != containerID {
		t.Fatalf("list immediately after create = %#v", listValue)
	}
	if _, ok := runtime.SessionAccess.Lookup("container-live-session"); ok {
		t.Fatal("container reads created fake workspace session access")
	}

	executeRequestCommand(t, root, []string{"workspace", "container", "rename", containerID, "Renamed"})
	status, err = runtime.Call(ctx, "workspace_container_status", map[string]any{"container_id": containerID})
	if err != nil || status.IsError || status.StructuredContent.(tools.WorkspaceContainerStatusResult).Name != "Renamed" {
		t.Fatalf("status immediately after rename = %#v err=%v", status, err)
	}

	workspaceRoot := t.TempDir()
	executeRequestCommand(t, root, []string{"workspace", "register", workspaceRoot})
	workspaces, err := workspace.NewManager(workspace.DefaultStorePath()).List()
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("persisted workspaces=%#v err=%v", workspaces, err)
	}
	workspaceID := workspaces[0].ID
	workspaceStatus, err := runtime.Call(ctx, "workspace_status", map[string]any{"workspace_id": workspaceID})
	if err != nil || workspaceStatus.IsError {
		t.Fatalf("workspace status immediately after register = %#v err=%v", workspaceStatus, err)
	}
	extra := t.TempDir()
	canonicalExtra, err := filepath.EvalSymlinks(extra)
	if err != nil {
		t.Fatal(err)
	}
	executeRequestCommand(t, root, []string{"workspace", "access", "add", workspaceID, extra})
	workspaceStatus, err = runtime.Call(ctx, "workspace_status", map[string]any{"workspace_id": workspaceID})
	if err != nil || workspaceStatus.IsError || !slices.Contains(workspaceStatus.StructuredContent.(tools.WorkspaceStatusResult).AllowedDirectories, canonicalExtra) {
		t.Fatalf("workspace status immediately after access add = %#v err=%v", workspaceStatus, err)
	}
	executeRequestCommand(t, root, []string{"workspace", "access", "remove", workspaceID, extra})
	workspaceStatus, err = runtime.Call(ctx, "workspace_status", map[string]any{"workspace_id": workspaceID})
	if err != nil || workspaceStatus.IsError || slices.Contains(workspaceStatus.StructuredContent.(tools.WorkspaceStatusResult).AllowedDirectories, canonicalExtra) {
		t.Fatalf("workspace status immediately after access remove = %#v err=%v", workspaceStatus, err)
	}
	executeRequestCommand(t, root, []string{"workspace", "container", "add", containerID, workspaceID})
	containerContext, err := runtime.Call(ctx, "workspace_container_context", map[string]any{"container_id": containerID})
	if err != nil || containerContext.IsError {
		t.Fatalf("context immediately after membership add = %#v err=%v", containerContext, err)
	}
	contextValue := containerContext.StructuredContent.(tools.WorkspaceContainerContextResult)
	if contextValue.WorkspaceCount != 1 || contextValue.Workspaces[0].WorkspaceID != workspaceID {
		t.Fatalf("context immediately after membership add = %#v", contextValue)
	}

	executeRequestCommand(t, root, []string{"workspace", "container", "remove", containerID, workspaceID})
	containerContext, err = runtime.Call(ctx, "workspace_container_context", map[string]any{"container_id": containerID})
	if err != nil || containerContext.IsError || containerContext.StructuredContent.(tools.WorkspaceContainerContextResult).WorkspaceCount != 0 {
		t.Fatalf("context immediately after membership remove = %#v err=%v", containerContext, err)
	}

	executeRequestCommand(t, root, []string{"workspace", "container", "delete", containerID})
	deleted, err := runtime.Call(ctx, "workspace_container_status", map[string]any{"container_id": containerID})
	if err != nil || !deleted.IsError || len(deleted.Content) == 0 || !strings.Contains(deleted.Content[0].Text, "workspace container not found") {
		t.Fatalf("status immediately after delete = %#v err=%v", deleted, err)
	}
	executeRequestCommand(t, root, []string{"workspace", "unregister", workspaceID})
	workspaceStatus, err = runtime.Call(ctx, "workspace_status", map[string]any{"workspace_id": workspaceID})
	if err != nil || !workspaceStatus.IsError || len(workspaceStatus.Content) == 0 || !strings.Contains(workspaceStatus.Content[0].Text, "workspace not found") {
		t.Fatalf("workspace status immediately after unregister = %#v err=%v", workspaceStatus, err)
	}
}

func TestWorkspaceContainerMutationReportsRuntimeSynchronizationFailure(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	runtimeManager := workspace.NewManager(workspace.DefaultStorePath())
	if values, err := runtimeManager.ListContainers(); err != nil || len(values) != 0 {
		t.Fatalf("initial containers=%#v err=%v", values, err)
	}
	control, err := startRuntimeControl(runtimeControlOptions{
		Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil },
		ReloadWorkspaces: func() (workspaceReloadResult, error) {
			return workspaceReloadResult{}, errors.New("sentinel workspace reload failure")
		},
		Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	output, commandErr := executeRequestCommandError(root, []string{"workspace", "container", "create", "Product"})
	if commandErr == nil || !strings.Contains(commandErr.Error(), "running runtime reload failed") || !strings.Contains(commandErr.Error(), "sentinel workspace reload failure") {
		t.Fatalf("command error=%v output=%q", commandErr, output)
	}
	persisted, err := workspace.NewManager(workspace.DefaultStorePath()).ListContainers()
	if err != nil || len(persisted) != 1 || persisted[0].Name != "Product" {
		t.Fatalf("persisted containers=%#v err=%v", persisted, err)
	}
	if _, err := runtimeManager.GetContainer(persisted[0].ID); !errors.Is(err, workspace.ErrContainerNotFound) {
		t.Fatalf("runtime unexpectedly synchronized failed mutation: %v", err)
	}
}
