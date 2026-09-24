package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceContainerCRUDAndMembership(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	manager := NewManager(store)
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("Backend")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(container.ID, "wsc_") || len(container.ID) != len("wsc_")+16 || container.Name != "Backend" {
		t.Fatalf("container = %#v", container)
	}
	container, err = manager.AddWorkspacesToContainer(container.ID, []string{second.ID, first.ID, first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(container.WorkspaceIDs) != 2 || container.WorkspaceIDs[0] > container.WorkspaceIDs[1] {
		t.Fatalf("workspace ids = %#v", container.WorkspaceIDs)
	}
	containers, err := manager.ContainersForWorkspace(first.ID)
	if err != nil || len(containers) != 1 || containers[0].ID != container.ID {
		t.Fatalf("containers = %#v err=%v", containers, err)
	}
	workspaces, err := manager.WorkspacesForContainer(container.ID)
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("workspaces = %#v err=%v", workspaces, err)
	}
	container, err = manager.RemoveWorkspaceFromContainer(container.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(container.WorkspaceIDs) != 1 || container.WorkspaceIDs[0] != second.ID {
		t.Fatalf("after remove = %#v", container.WorkspaceIDs)
	}
	container, err = manager.RenameContainer(container.ID, "Services")
	if err != nil || container.Name != "Services" {
		t.Fatalf("renamed = %#v err=%v", container, err)
	}
	reloaded := NewManager(store)
	got, err := reloaded.GetContainer(container.ID)
	if err != nil || got.Name != "Services" || len(got.WorkspaceIDs) != 1 || got.WorkspaceIDs[0] != second.ID {
		t.Fatalf("persisted = %#v err=%v", got, err)
	}
	if err := reloaded.DeleteContainer(container.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.GetContainer(container.ID); !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("deleted container err=%v", err)
	}
}

func TestWorkspaceContainerBatchMutationIsAtomic(t *testing.T) {
	manager := newTestManager(t)
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.CreateContainer("One")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainers(item.ID, []string{first.ID, "wsc_missing"}); !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("expected missing container, got %v", err)
	}
	got, err := manager.GetContainer(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.WorkspaceIDs) != 0 {
		t.Fatalf("partial mutation persisted: %#v", got.WorkspaceIDs)
	}
	if _, err := manager.AddWorkspacesToContainer(first.ID, []string{item.ID, "ws_missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing workspace, got %v", err)
	}
	got, err = manager.GetContainer(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.WorkspaceIDs) != 0 {
		t.Fatalf("partial workspace mutation persisted: %#v", got.WorkspaceIDs)
	}
}

func TestWorkspaceContainerResolveEmptySingleAndMultiple(t *testing.T) {
	manager := newTestManager(t)
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("Product")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := manager.ResolveContainer(container.ID)
	if err != nil || empty.Container.ID != container.ID || len(empty.Workspaces) != 0 {
		t.Fatalf("empty context = %#v err=%v", empty, err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	single, err := manager.ResolveContainer(container.ID)
	if err != nil || len(single.Workspaces) != 1 || single.Workspaces[0].ID != first.ID {
		t.Fatalf("single context = %#v err=%v", single, err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	multiple, err := manager.ResolveContainer(container.ID)
	if err != nil || len(multiple.Workspaces) != 2 {
		t.Fatalf("multiple context = %#v err=%v", multiple, err)
	}
	if multiple.Workspaces[0].Path > multiple.Workspaces[1].Path {
		t.Fatalf("workspaces are not deterministic: %#v", multiple.Workspaces)
	}
}

func TestWorkspaceCanBelongToMultipleContainersWithoutDuplicatedState(t *testing.T) {
	manager := newTestManager(t)
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.CreateContainer("One")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CreateContainer("Two")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainers(item.ID, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	containers, err := manager.ContainersForWorkspace(item.ID)
	if err != nil || len(containers) != 2 {
		t.Fatalf("containers = %#v err=%v", containers, err)
	}
	for _, container := range containers {
		ctx, err := manager.ResolveContainer(container.ID)
		if err != nil || len(ctx.Workspaces) != 1 || ctx.Workspaces[0].ID != item.ID || ctx.Workspaces[0].Path != item.Path {
			t.Fatalf("container %s context = %#v err=%v", container.ID, ctx, err)
		}
	}
}

func TestWorkspaceUnregisterCleansContainerMembership(t *testing.T) {
	manager := newTestManager(t)
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("One")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unregister(item.ID); err != nil {
		t.Fatal(err)
	}
	container, err = manager.GetContainer(container.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(container.WorkspaceIDs) != 0 {
		t.Fatalf("membership survived unregister: %#v", container.WorkspaceIDs)
	}
}

func TestWorkspaceRegistryV3MigratesContainersField(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	legacy := map[string]any{"version": 3, "workspaces": []map[string]any{{"id": workspaceID(root), "path": root}}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, data, 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	if _, err := manager.ListContainers(); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `"version": 5`) {
		t.Fatalf("registry not migrated: %s", updated)
	}
}
