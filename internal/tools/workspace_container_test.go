package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/memory"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/workspace"
)

func newWorkspaceContainerToolFixture(t *testing.T) (*workspace.Manager, *Registry, workspace.WorkspaceContainer, workspace.Workspace, workspace.Workspace) {
	t.Helper()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
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
	if _, err := manager.AddWorkspacesToContainer(container.ID, []string{second.ID, first.ID}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterWorkspaceContainerTools(registry, manager)
	return manager, registry, container, first, second
}

func TestWorkspaceContainerToolSchemasAreOrchestrationScoped(t *testing.T) {
	_, registry, _, _, _ := newWorkspaceContainerToolFixture(t)
	for _, name := range []string{"workspace_container_list", "workspace_container_status", "workspace_container_context"} {
		schema, ok := registry.Schema(name)
		if !ok {
			t.Fatalf("missing %s schema", name)
		}
		if strings.Contains(string(schema.InputSchema), `"workspace_id"`) {
			t.Fatalf("%s accepts workspace_id: %s", name, schema.InputSchema)
		}
		workspaceScoped, err := registry.WorkspaceScoped(name)
		if err != nil || workspaceScoped {
			t.Fatalf("%s workspace scoped=%t err=%v", name, workspaceScoped, err)
		}
	}
}

func TestNormalRuntimeRegistersWorkspaceContainerTools(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	for _, name := range []string{"workspace_container_list", "workspace_container_status", "workspace_container_context"} {
		if _, ok := runtime.Registry.Schema(name); !ok {
			t.Fatalf("normal runtime missing %s", name)
		}
	}
}

func TestWorkspaceContainerListStatusAndContext(t *testing.T) {
	_, registry, container, first, second := newWorkspaceContainerToolFixture(t)
	listResult, err := registry.Call(context.Background(), "workspace_container_list", map[string]any{})
	if err != nil || listResult.IsError {
		t.Fatalf("list = %#v err=%v", listResult, err)
	}
	list, ok := listResult.StructuredContent.(WorkspaceContainerListResult)
	if !ok || list.Count != 1 || list.Containers[0].ContainerID != container.ID || list.Containers[0].WorkspaceCount != 2 {
		t.Fatalf("list = %#v", listResult.StructuredContent)
	}

	statusResult, err := registry.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": container.ID})
	if err != nil || statusResult.IsError {
		t.Fatalf("status = %#v err=%v", statusResult, err)
	}
	status, ok := statusResult.StructuredContent.(WorkspaceContainerStatusResult)
	if !ok || status.ContainerID != container.ID || status.Name != "Product" || status.WorkspaceCount != 2 {
		t.Fatalf("status = %#v", statusResult.StructuredContent)
	}
	want := map[string]string{first.ID: first.Path, second.ID: second.Path}
	for _, member := range status.Workspaces {
		if want[member.WorkspaceID] != member.WorkspaceRoot {
			t.Fatalf("unexpected member = %#v", member)
		}
		delete(want, member.WorkspaceID)
	}
	if len(want) != 0 {
		t.Fatalf("missing members = %#v", want)
	}
	if len(status.Workspaces) == 2 && status.Workspaces[0].WorkspaceRoot > status.Workspaces[1].WorkspaceRoot {
		t.Fatalf("member order is unstable: %#v", status.Workspaces)
	}

	contextResult, err := registry.Call(context.Background(), "workspace_container_context", map[string]any{"container_id": container.ID})
	if err != nil || contextResult.IsError {
		t.Fatalf("context = %#v err=%v", contextResult, err)
	}
	containerContext, ok := contextResult.StructuredContent.(WorkspaceContainerContextResult)
	if !ok || containerContext.WorkspaceCount != 2 {
		t.Fatalf("context = %#v", contextResult.StructuredContent)
	}
	for _, expected := range []string{container.ID, first.ID, second.ID, "orchestration scope", "project_context", "Do not pass this wsc_* container_id as workspace_id"} {
		if !strings.Contains(containerContext.Instructions, expected) {
			t.Fatalf("instructions missing %q: %s", expected, containerContext.Instructions)
		}
	}
}

func TestWorkspaceContainerContextEmptyDoesNotInventWorkspace(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	container, err := manager.CreateContainer("Empty")
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterWorkspaceContainerTools(registry, manager)
	result, err := registry.Call(context.Background(), "workspace_container_context", map[string]any{"container_id": container.ID})
	if err != nil || result.IsError {
		t.Fatalf("context = %#v err=%v", result, err)
	}
	value := result.StructuredContent.(WorkspaceContainerContextResult)
	if value.WorkspaceCount != 0 || len(value.Workspaces) != 0 || !strings.Contains(value.Instructions, "no member workspaces") || !strings.Contains(value.Instructions, "Do not invent") {
		t.Fatalf("empty context = %#v", value)
	}
}

func TestWorkspaceContainerContextDoesNotLeakMemberContent(t *testing.T) {
	_, registry, container, first, second := newWorkspaceContainerToolFixture(t)
	for _, root := range []string{first.Path, second.Path} {
		if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("SENTINEL_CONTAINER_RULE_SECRET"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("SENTINEL_CONTAINER_MEMORY_SECRET"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := registry.Call(context.Background(), "workspace_container_context", map[string]any{"container_id": container.ID})
	if err != nil || result.IsError || len(result.Content) == 0 {
		t.Fatalf("context = %#v err=%v", result, err)
	}
	text := result.Content[0].Text
	for _, forbidden := range []string{"SENTINEL_CONTAINER_RULE_SECRET", "SENTINEL_CONTAINER_MEMORY_SECRET"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("context leaked %q: %s", forbidden, text)
		}
	}
}

func TestWorkspaceContainerContextDoesNotGrantSessionWorkspaceAccess(t *testing.T) {
	manager, registry, container, _, _ := newWorkspaceContainerToolFixture(t)
	runtime := &Runtime{Registry: registry, Workspaces: manager, SessionAccess: NewSessionWorkspaceAccessManager()}
	observed := make(chan CallObservation, 2)
	runtime.SetCallObserver(func(value CallObservation) { observed <- value })
	ctx := WithMCPSessionID(context.Background(), "container-session")
	result, err := runtime.Call(ctx, "workspace_container_context", map[string]any{"container_id": container.ID})
	if err != nil || result.IsError {
		t.Fatalf("context = %#v err=%v", result, err)
	}
	if _, ok := runtime.SessionAccess.Lookup("container-session"); ok {
		t.Fatal("container context created fake workspace access")
	}
	start, finish := <-observed, <-observed
	if start.WorkspaceID != "" || finish.WorkspaceID != "" || start.SessionWorkspaceCount != 0 || finish.SessionWorkspaceCount != 0 || start.SessionAccess != "" || finish.SessionAccess != "" {
		t.Fatalf("container telemetry used workspace scope: start=%#v finish=%#v", start, finish)
	}
}

func TestWorkspaceOnlyToolsRejectExistingContainerWithActionableError(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	container, err := manager.CreateContainer("Product")
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoints"))
	RegisterCore(registry, manager, checkpoints)
	RegisterWorkspaceContainerTools(registry, manager)
	runtime := &Runtime{Registry: registry, Workspaces: manager, Checkpoints: checkpoints, SessionAccess: NewSessionWorkspaceAccessManager()}
	cases := []struct {
		name string
		args map[string]any
	}{
		{name: "read_files", args: map[string]any{"workspace_id": container.ID, "paths": []string{"README.md"}}},
		{name: "run_command", args: map[string]any{"workspace_id": container.ID, "command": "pwd"}},
		{name: "project_context", args: map[string]any{"workspace_id": container.ID, "include_git": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := runtime.Call(WithMCPSessionID(context.Background(), "misuse-session"), tc.name, tc.args)
			if err != nil || !result.IsError || len(result.Content) == 0 {
				t.Fatalf("result = %#v err=%v", result, err)
			}
			message := result.Content[0].Text
			for _, expected := range []string{"workspace container", "workspace_container_context", "member ws_* workspace_id"} {
				if !strings.Contains(message, expected) {
					t.Fatalf("error missing %q: %s", expected, message)
				}
			}
		})
	}
	if _, ok := runtime.SessionAccess.Lookup("misuse-session"); ok {
		t.Fatal("container misuse created session workspace access")
	}
}

func TestWorkspaceOnlyToolsDistinguishUnknownContainer(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	registry := NewRegistry()
	registry.MustRegister("workspace_probe", Schema{Name: "workspace_probe", InputSchema: []byte(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"]}`)}, func(context.Context, map[string]any) (Result, error) {
		return TextResult("unexpected"), nil
	})
	runtime := &Runtime{Registry: registry, Workspaces: manager, SessionAccess: NewSessionWorkspaceAccessManager()}
	result, err := runtime.Call(context.Background(), "workspace_probe", map[string]any{"workspace_id": "wsc_missing"})
	if err != nil || !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "workspace container not found") {
		t.Fatalf("unknown container = %#v err=%v", result, err)
	}
}

func TestWorkspaceContainerContextTraceIncludesSafeOperationalFacts(t *testing.T) {
	_, registry, container, first, second := newWorkspaceContainerToolFixture(t)
	for _, root := range []string{first.Path, second.Path} {
		if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("SENTINEL_CONTAINER_RULE_SECRET"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("SENTINEL_CONTAINER_MEMORY_SECRET"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "workspace_container_list", args: map[string]any{}},
		{name: "workspace_container_status", args: map[string]any{"container_id": container.ID}},
		{name: "workspace_container_context", args: map[string]any{"container_id": container.ID}},
	} {
		result, err := registry.Call(ctx, call.name, call.args)
		if err != nil || result.IsError {
			t.Fatalf("%s = %#v err=%v", call.name, result, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string]bool{
		"workspace.container.list.completed":    false,
		"workspace.container.status.completed":  false,
		"workspace.container.context.completed": false,
	}
	for _, event := range events {
		if _, ok := want[event.Name]; !ok {
			continue
		}
		fields := map[string]any{}
		for _, field := range event.Fields {
			fields[field.Key] = field.Value
		}
		if fields["duration_ms"] == nil {
			t.Fatalf("%s missing duration: %#v", event.Name, fields)
		}
		if event.Name == "workspace.container.list.completed" {
			if fields["count"] != 1 {
				t.Fatalf("list trace fields = %#v", fields)
			}
		} else if fields["container_id"] != container.ID || fields["workspace_count"] != 2 {
			t.Fatalf("%s trace fields = %#v", event.Name, fields)
		}
		want[event.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing %s trace: %#v", name, events)
		}
	}
	encoded := strings.Builder{}
	for _, event := range events {
		encoded.WriteString(event.Message)
		for _, field := range event.Fields {
			encoded.WriteString(field.Key)
			encoded.WriteString("=")
			encoded.WriteString(fmt.Sprint(field.Value))
		}
	}
	for _, forbidden := range []string{"SENTINEL_CONTAINER_RULE_SECRET", "SENTINEL_CONTAINER_MEMORY_SECRET"} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("trace leaked %q", forbidden)
		}
	}
}

func TestWorkspaceContainerPermissionBoundaryDoesNotTransferAllowDirs(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	extra := t.TempDir()
	if _, err := manager.AddAllowDir(first.ID, extra); err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("Product")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspacesToContainer(container.ID, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterWorkspaceContainerTools(registry, manager)
	if result, err := registry.Call(context.Background(), "workspace_container_context", map[string]any{"container_id": container.ID}); err != nil || result.IsError {
		t.Fatalf("container context = %#v err=%v", result, err)
	}
	firstRoots, err := manager.EffectiveRoots(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondRoots, err := manager.EffectiveRoots(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(firstRoots, extra) || containsPath(secondRoots, extra) {
		t.Fatalf("permission transfer first=%#v second=%#v extra=%s", firstRoots, secondRoots, extra)
	}
	if _, err := manager.ResolvePath(second.ID, second.Path, filepath.Join(extra, "secret.txt"), false); err == nil {
		t.Fatal("container membership transferred first workspace allow-dir to second workspace")
	}
}

func TestWorkspaceContainerDoesNotMergeCWDCheckpointsOrMemory(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
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
	if _, err := manager.AddWorkspacesToContainer(container.ID, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterWorkspaceContainerTools(registry, manager)

	shell := shellruntime.NewManager(manager, filepath.Join(t.TempDir(), "shell-state"))
	child := filepath.Join(first.Path, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := shell.Reset(first.ID, child); err != nil {
		t.Fatal(err)
	}
	secondStatus, err := shell.Status(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(secondStatus.CWD) != filepath.Clean(second.Path) {
		t.Fatalf("second cwd = %q, want %q", secondStatus.CWD, second.Path)
	}

	checkpoints := checkpoint.NewStore(filepath.Join(t.TempDir(), "checkpoint-state"))
	firstFile := filepath.Join(first.Path, "first.txt")
	if err := os.WriteFile(firstFile, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoints.Before(first.ID, first.Path, "edit_file", []string{firstFile}, false); err != nil {
		t.Fatal(err)
	}
	firstCheckpoints, err := checkpoints.List(first.ID, 10)
	if err != nil || len(firstCheckpoints) != 1 {
		t.Fatalf("first checkpoints = %#v err=%v", firstCheckpoints, err)
	}
	secondCheckpoints, err := checkpoints.List(second.ID, 10)
	if err != nil || len(secondCheckpoints) != 0 {
		t.Fatalf("second checkpoints = %#v err=%v", secondCheckpoints, err)
	}

	memoryStore := memory.NewStore(filepath.Join(t.TempDir(), "memory-state"))
	if _, err := memoryStore.Upsert(first.ID, "project", "note", "alpha only"); err != nil {
		t.Fatal(err)
	}
	firstMemory, err := memoryStore.Get(first.ID, "project", "note")
	if err != nil || len(firstMemory) != 1 || firstMemory[0].Note != "alpha only" {
		t.Fatalf("first memory = %#v err=%v", firstMemory, err)
	}
	secondMemory, err := memoryStore.Get(second.ID, "project", "note")
	if err != nil || len(secondMemory) != 0 {
		t.Fatalf("second memory = %#v err=%v", secondMemory, err)
	}

	if result, err := registry.Call(context.Background(), "workspace_container_context", map[string]any{"container_id": container.ID}); err != nil || result.IsError {
		t.Fatalf("container context = %#v err=%v", result, err)
	}
	firstStatus, err := shell.Status(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondStatus, err = shell.Status(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(firstStatus.CWD) != filepath.Clean(child) || filepath.Clean(secondStatus.CWD) != filepath.Clean(second.Path) {
		t.Fatalf("container context changed cwd first=%q second=%q", firstStatus.CWD, secondStatus.CWD)
	}
	secondMemory, err = memoryStore.Get(second.ID, "project", "note")
	if err != nil || len(secondMemory) != 0 {
		t.Fatalf("container context changed second memory = %#v err=%v", secondMemory, err)
	}
}

func TestWorkspaceContainerToolsFollowRuntimeReload(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	writer := workspace.NewManager(store)
	runtimeManager := workspace.NewManager(store)
	if values, err := runtimeManager.ListContainers(); err != nil || len(values) != 0 {
		t.Fatalf("initial containers = %#v err=%v", values, err)
	}
	registry := NewRegistry()
	RegisterWorkspaceContainerTools(registry, runtimeManager)
	runtime := &Runtime{Registry: registry, Workspaces: runtimeManager, SessionAccess: NewSessionWorkspaceAccessManager()}

	container, err := writer.CreateContainer("Product")
	if err != nil {
		t.Fatal(err)
	}
	if stale, err := runtime.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": container.ID}); err != nil || !stale.IsError {
		t.Fatalf("runtime unexpectedly saw un-reloaded container = %#v err=%v", stale, err)
	}
	if err := runtime.ReloadWorkspaces(); err != nil {
		t.Fatal(err)
	}
	assertContainerStatusName(t, runtime, container.ID, "Product")

	if _, err := writer.RenameContainer(container.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReloadWorkspaces(); err != nil {
		t.Fatal(err)
	}
	assertContainerStatusName(t, runtime, container.ID, "Renamed")

	member, err := writer.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AddWorkspaceToContainer(container.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReloadWorkspaces(); err != nil {
		t.Fatal(err)
	}
	contextResult, err := runtime.Call(context.Background(), "workspace_container_context", map[string]any{"container_id": container.ID})
	if err != nil || contextResult.IsError {
		t.Fatalf("context after membership = %#v err=%v", contextResult, err)
	}
	contextValue := contextResult.StructuredContent.(WorkspaceContainerContextResult)
	if contextValue.WorkspaceCount != 1 || contextValue.Workspaces[0].WorkspaceID != member.ID {
		t.Fatalf("context after membership = %#v", contextValue)
	}

	if err := writer.DeleteContainer(container.ID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReloadWorkspaces(); err != nil {
		t.Fatal(err)
	}
	deleted, err := runtime.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": container.ID})
	if err != nil || !deleted.IsError || len(deleted.Content) == 0 || !strings.Contains(deleted.Content[0].Text, "workspace container not found") {
		t.Fatalf("deleted container = %#v err=%v", deleted, err)
	}
}

func TestWorkspaceContainerReadsAreStableImmediatelyAfterReload(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	writer := workspace.NewManager(store)
	runtimeManager := workspace.NewManager(store)
	if _, err := runtimeManager.ListContainers(); err != nil {
		t.Fatal(err)
	}
	container, err := writer.CreateContainer("Product")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeManager.Reload(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterWorkspaceContainerTools(registry, runtimeManager)
	runtime := &Runtime{Registry: registry, Workspaces: runtimeManager, SessionAccess: NewSessionWorkspaceAccessManager()}
	const readers = 32
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := runtime.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": container.ID})
			if err != nil {
				errs <- err
				return
			}
			if result.IsError {
				errs <- errors.New(result.Content[0].Text)
				return
			}
			value := result.StructuredContent.(WorkspaceContainerStatusResult)
			if value.ContainerID != container.ID || value.Name != "Product" {
				errs <- errors.New("unexpected container snapshot")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func assertContainerStatusName(t *testing.T, runtime *Runtime, containerID, name string) {
	t.Helper()
	result, err := runtime.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": containerID})
	if err != nil || result.IsError {
		t.Fatalf("status = %#v err=%v", result, err)
	}
	value := result.StructuredContent.(WorkspaceContainerStatusResult)
	if value.Name != name {
		t.Fatalf("status name = %q, want %q", value.Name, name)
	}
}

func containsPath(values []string, target string) bool {
	target = canonicalTestPath(target)
	for _, value := range values {
		if canonicalTestPath(value) == target {
			return true
		}
	}
	return false
}

func canonicalTestPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func TestWorkspaceContainerUnknownStatusPreservesSentinelError(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	registry := NewRegistry()
	RegisterWorkspaceContainerTools(registry, manager)
	_, err := registry.Call(context.Background(), "workspace_container_status", map[string]any{"container_id": "wsc_missing"})
	if !errors.Is(err, workspace.ErrContainerNotFound) {
		t.Fatalf("error = %v", err)
	}
}
