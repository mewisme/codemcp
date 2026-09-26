package application

import (
	"errors"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestWorkspaceRelocateConflictReadModelAndExplicitResolution(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}

	_, err = service.Relocate(t.Context(), WorkspaceRelocateRequest{ID: registered.Value.ID, Path: destination})
	if ErrorCodeOf(err) != ErrorConflict {
		t.Fatalf("duplicate error=%v code=%s", err, ErrorCodeOf(err))
	}
	conflict, ok := WorkspaceRelocationConflictOf(err)
	if !ok {
		t.Fatalf("missing relocation conflict model: %T %v", err, err)
	}
	if conflict.WorkspaceID != registered.Value.ID ||
		conflict.RegisteredRoot != filepath.Clean(source) ||
		conflict.DestinationRoot != filepath.Clean(destination) ||
		len(conflict.Resolutions) != 3 {
		t.Fatalf("conflict=%#v", conflict)
	}

	result, err := service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID:         registered.Value.ID,
		Path:       destination,
		Resolution: workspace.RelocationResolutionDestination,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Value.After.ID != registered.Value.ID || result.Value.After.Path != filepath.Clean(destination) {
		t.Fatalf("relocation=%#v", result.Value)
	}
}

func TestWorkspaceRelocateResolutionValidation(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}

	_, err = service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolution("invalid"),
	})
	if ErrorCodeOf(err) != ErrorInvalidArgument || !errors.Is(err, workspace.ErrInvalidRelocationResolution) {
		t.Fatalf("invalid resolution err=%v code=%s", err, ErrorCodeOf(err))
	}

	merged, err := service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.Value.After.ID != registered.Value.ID || merged.Value.After.Path != filepath.Clean(destination) {
		t.Fatalf("merged relocation=%#v", merged.Value)
	}
}

func TestWorkspaceRelocateDispatcherUsesCanonicalRequest(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	registered, err := service.Register(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}

	dispatcher := NewDispatcher()
	if err := BindWorkspaceOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Dispatch(t.Context(), DispatchRequest{
		Operation: capability.WorkspaceRelocate,
		Input: WorkspaceRelocateRequest{
			ID:         registered.Value.ID,
			Path:       destination,
			Resolution: workspace.RelocationResolutionDestination,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	relocated, ok := result.Value.(WorkspaceRelocation)
	if !ok || relocated.After.Path != filepath.Clean(destination) {
		t.Fatalf("dispatch result=%#v", result.Value)
	}
}
