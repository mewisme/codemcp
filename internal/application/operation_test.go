package application

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestDispatcherReturnsCanonicalMetadataAndTypedErrors(t *testing.T) {
	dispatcher := NewDispatcher()
	if err := dispatcher.Register(capability.WorkspaceList, func(context.Context, any) (any, error) {
		return []string{"ws_a"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.WorkspaceList})
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation != capability.WorkspaceList || result.Metadata.ID != capability.WorkspaceList {
		t.Fatalf("result=%#v", result)
	}
	if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.WorkspaceRegister}); ErrorCodeOf(err) != ErrorUnsupported {
		t.Fatalf("unbound error=%v code=%s", err, ErrorCodeOf(err))
	}
	if err := dispatcher.Register(capability.ID("missing.operation"), func(context.Context, any) (any, error) { return nil, nil }); err == nil {
		t.Fatal("unknown operation registered")
	}
}

func TestDispatcherMetadataCannotBeOverriddenByAdapterInput(t *testing.T) {
	dispatcher := NewDispatcher()
	if err := dispatcher.Register(capability.WorkspaceRegister, func(_ context.Context, input any) (any, error) {
		return input, nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := capability.Spec{
		ID:            capability.WorkspaceRegister,
		Authorization: capability.AuthorizationProtocol,
		Confirmation:  capability.ConfirmationPolicy{Mode: capability.ConfirmationNone, ControlApproval: false},
		Effects:       capability.SemanticEffects{ReadOnly: true, Idempotent: true},
	}
	result, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.WorkspaceRegister, Input: fake})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := capability.Lookup(capability.WorkspaceRegister)
	if result.Metadata.Authorization != canonical.Authorization || result.Metadata.Confirmation != canonical.Confirmation || result.Metadata.Effects != canonical.Effects {
		t.Fatalf("adapter input changed canonical metadata: got=%#v want=%#v", result.Metadata, canonical)
	}
}

func TestWorkspaceServiceMutationReconcilesExactlyOnceAndUsesCanonicalTrace(t *testing.T) {
	var events []tracepkg.Event
	observer := func(event tracepkg.Event) { events = append(events, event) }
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json")).SetTraceObserver(observer)
	var reconciles atomic.Int32
	service := NewWorkspaceService(manager, func(context.Context) error {
		reconciles.Add(1)
		return nil
	})
	ctx := tracepkg.WithObserver(t.Context(), observer)
	root := t.TempDir()

	registered, err := service.Register(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if registered.Operation != capability.WorkspaceRegister || registered.Value.ID == "" || reconciles.Load() != 1 {
		t.Fatalf("registered=%#v reconciles=%d", registered, reconciles.Load())
	}
	if got := traceEventCount(events, "workspace.registry.persist.completed"); got != 1 {
		t.Fatalf("register persistence count=%d want 1", got)
	}
	if _, err := service.Get(ctx, registered.Value.ID); err != nil {
		t.Fatal(err)
	}
	if reconciles.Load() != 1 {
		t.Fatalf("read reconciled runtime: %d", reconciles.Load())
	}
	if _, err := service.CreateContainer(ctx, "Primary"); err != nil {
		t.Fatal(err)
	}
	if reconciles.Load() != 2 {
		t.Fatalf("container mutation reconciles=%d", reconciles.Load())
	}
	if got := traceEventCount(events, "workspace.registry.persist.completed"); got != 2 {
		t.Fatalf("total persistence count=%d want 2", got)
	}

	foundStart, foundEnd := false, false
	for _, event := range events {
		switch event.Name {
		case string(capability.WorkspaceRegister) + ".started":
			foundStart = true
		case string(capability.WorkspaceRegister) + ".completed":
			foundEnd = true
		}
	}
	if !foundStart || !foundEnd {
		t.Fatalf("canonical operation trace missing: %#v", events)
	}
}

func traceEventCount(events []tracepkg.Event, name string) int {
	count := 0
	for _, event := range events {
		if event.Name == name {
			count++
		}
	}
	return count
}

func TestWorkspaceServiceClassifiesValidationAndLookupErrors(t *testing.T) {
	service := NewWorkspaceService(workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json")), nil)
	if _, err := service.Register(t.Context(), " "); ErrorCodeOf(err) != ErrorInvalidArgument {
		t.Fatalf("register error=%v code=%s", err, ErrorCodeOf(err))
	}
	if _, err := service.Get(t.Context(), "ws_missing"); ErrorCodeOf(err) != ErrorNotFound {
		t.Fatalf("get error=%v code=%s", err, ErrorCodeOf(err))
	}
	var operationErr *OperationError
	_, err := service.Get(t.Context(), "ws_missing")
	if !errors.As(err, &operationErr) || operationErr.Operation != capability.WorkspaceShow {
		t.Fatalf("typed error=%#v", operationErr)
	}
}

func TestWorkspaceDispatcherBindingUsesSameServiceOwner(t *testing.T) {
	var events []tracepkg.Event
	observer := func(event tracepkg.Event) { events = append(events, event) }
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	var reconciles atomic.Int32
	service := NewWorkspaceService(manager, func(context.Context) error { reconciles.Add(1); return nil })
	dispatcher := NewDispatcher()
	if err := BindWorkspaceOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx := tracepkg.WithObserver(t.Context(), observer)
	result, err := dispatcher.Dispatch(ctx, DispatchRequest{Operation: capability.WorkspaceRegister, Input: WorkspaceRegisterInput{Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := result.Value.(WorkspaceView)
	if !ok || view.ID == "" || reconciles.Load() != 1 {
		t.Fatalf("result=%#v reconciles=%d", result, reconciles.Load())
	}
	if got := traceEventCount(events, string(capability.WorkspaceRegister)+".started"); got != 1 {
		t.Fatalf("workspace register start spans=%d want 1", got)
	}
	list, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.WorkspaceList})
	if err != nil {
		t.Fatal(err)
	}
	values, ok := list.Value.([]WorkspaceView)
	if !ok || len(values) != 1 || values[0].ID != view.ID {
		t.Fatalf("list=%#v", list.Value)
	}
}

func TestWorkspaceMembershipRoutesShareOneCanonicalDispatcherOwner(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	dispatcher := NewDispatcher()
	if err := BindWorkspaceOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	first, err := service.Register(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Register(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	primary, err := service.CreateContainer(t.Context(), "Primary")
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := service.CreateContainer(t.Context(), "Secondary")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{
		Operation: capability.WorkspaceContainerAdd,
		Input: WorkspaceContainerMembershipInput{
			ContainerID:  primary.Value.ID,
			WorkspaceIDs: []string{first.Value.ID, second.Value.ID},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{
		Operation: capability.WorkspaceContainerAdd,
		Input: WorkspaceContainerMembershipInput{
			WorkspaceID:  first.Value.ID,
			ContainerIDs: []string{secondary.Value.ID},
		},
	}); err != nil {
		t.Fatal(err)
	}
	containers, err := service.ContainersForWorkspace(t.Context(), first.Value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers.Value) != 2 {
		t.Fatalf("container membership=%#v", containers.Value)
	}
}

func TestWorkspaceServiceExposesRuntimeDiagnosticsAndConflicts(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	registered, err := service.Register(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer manager.Deactivate()
	diagnostics := service.RuntimeDiagnostics()
	if !diagnostics.Active || diagnostics.Owned != 1 || len(diagnostics.Workspaces) != 1 || diagnostics.Workspaces[0].WorkspaceID != registered.Value.ID {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if code := ErrorCodeOf(classifyWorkspaceError(capability.WorkspaceRelocate, workspace.ErrAlreadyActive)); code != ErrorConflict {
		t.Fatalf("active conflict code=%s", code)
	}
	if code := ErrorCodeOf(classifyWorkspaceError(capability.WorkspaceShow, workspace.ErrStateLost)); code != ErrorConflict {
		t.Fatalf("state lost conflict code=%s", code)
	}
	if code := ErrorCodeOf(classifyWorkspaceError(capability.WorkspaceRegister, workspace.ErrRegistryBusy)); code != ErrorConflict {
		t.Fatalf("registry busy conflict code=%s", code)
	}
}
