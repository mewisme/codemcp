package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/workspace"
	workspacereconcile "go.mewis.me/codemcp/internal/workspace/reconcile"
)

type WorkspaceView struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	AllowDirs []string `json:"allow_dirs,omitempty"`
	LegacyIDs []string `json:"legacy_ids,omitempty"`
	Available bool     `json:"available"`
	Error     string   `json:"error,omitempty"`
}

type WorkspaceContainerView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	WorkspaceIDs []string `json:"workspace_ids,omitempty"`
}

type WorkspaceRelocation struct {
	Before     WorkspaceView                  `json:"before"`
	After      WorkspaceView                  `json:"after"`
	Resolution workspace.RelocationResolution `json:"resolution,omitempty"`
}

type WorkspaceRelocateRequest struct {
	ID         string                         `json:"workspace_id"`
	Path       string                         `json:"path"`
	Resolution workspace.RelocationResolution `json:"resolution,omitempty"`
}

type WorkspaceRelocationConflict struct {
	WorkspaceID     string                           `json:"workspace_id"`
	RegisteredRoot  string                           `json:"registered_root"`
	DestinationRoot string                           `json:"destination_root"`
	Resolutions     []workspace.RelocationResolution `json:"resolutions"`
}

type WorkspaceOperations interface {
	List(context.Context) (Result[[]WorkspaceView], error)
	Get(context.Context, string) (Result[WorkspaceView], error)
	AccessList(context.Context, string) (Result[[]string], error)
	Register(context.Context, string) (Result[WorkspaceView], error)
	Relocate(context.Context, WorkspaceRelocateRequest) (Result[WorkspaceRelocation], error)
	Unregister(context.Context, string) (Result[WorkspaceView], error)
	Purge(context.Context, string, bool) (Result[WorkspaceView], error)
	AddAllowDir(context.Context, string, string) (Result[WorkspaceView], error)
	RemoveAllowDir(context.Context, string, string) (Result[WorkspaceView], error)
	ListContainers(context.Context) (Result[[]WorkspaceContainerView], error)
	GetContainer(context.Context, string) (Result[WorkspaceContainerView], error)
	CreateContainer(context.Context, string) (Result[WorkspaceContainerView], error)
	RenameContainer(context.Context, string, string) (Result[WorkspaceContainerView], error)
	DeleteContainer(context.Context, string) (Result[WorkspaceContainerView], error)
	AddWorkspacesToContainer(context.Context, string, []string) (Result[WorkspaceContainerView], error)
	RemoveWorkspacesFromContainer(context.Context, string, []string) (Result[WorkspaceContainerView], error)
	ContainersForWorkspace(context.Context, string) (Result[[]WorkspaceContainerView], error)
	WorkspacesForContainer(context.Context, string) (Result[[]WorkspaceView], error)
}

type WorkspaceService struct {
	manager   *workspace.Manager
	reconcile func(context.Context) error
}

func NewWorkspaceService(manager *workspace.Manager, reconcile func(context.Context) error) *WorkspaceService {
	return &WorkspaceService{manager: manager, reconcile: reconcile}
}

func NewDefaultWorkspaceService(manager *workspace.Manager) *WorkspaceService {
	return NewWorkspaceService(manager, func(ctx context.Context) error {
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		span := tracepkg.Start(ctx, "WORKSPACE", "workspace.runtime.reload", "Synchronizing workspace registry with runtime")
		result, running, err := ReloadWorkspaces(ctx)
		if err != nil {
			span.FailMessage("Workspace runtime synchronization failed", err, tracepkg.Bool("runtime_running", running))
			return err
		}
		span.EndMessage("Workspace runtime synchronization completed", tracepkg.Bool("runtime_running", running), tracepkg.Bool("runtime_reloaded", running), tracepkg.Int("count", result.Count), tracepkg.Int("pid", result.PID))
		return nil
	})
}

func (service *WorkspaceService) Manager() *workspace.Manager {
	if service == nil {
		return nil
	}
	return service.manager
}

func (service *WorkspaceService) RuntimeDiagnostics() workspace.RuntimeDiagnostics {
	if service == nil || service.manager == nil {
		return workspace.RuntimeDiagnostics{}
	}
	return service.manager.RuntimeDiagnostics()
}

func (service *WorkspaceService) require(id capability.ID) error {
	if service == nil || service.manager == nil {
		return operationError(id, ErrorUnavailable, errors.New("workspace manager is unavailable"))
	}
	return nil
}

func (service *WorkspaceService) List(ctx context.Context) (Result[[]WorkspaceView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceList, "Listing registered workspaces", nil, func() ([]WorkspaceView, error) {
		if err := service.require(capability.WorkspaceList); err != nil {
			return nil, err
		}
		values, err := service.manager.List()
		if err != nil {
			return nil, classifyWorkspaceError(capability.WorkspaceList, err)
		}
		return workspaceViews(values), nil
	})
}

func (service *WorkspaceService) Get(ctx context.Context, id string) (Result[WorkspaceView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceShow, "Loading registered workspace", []tracepkg.Field{tracepkg.String("workspace_id", strings.TrimSpace(id))}, func() (WorkspaceView, error) {
		if err := service.require(capability.WorkspaceShow); err != nil {
			return WorkspaceView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceShow, "workspace id", id); err != nil {
			return WorkspaceView{}, err
		}
		value, err := service.manager.Get(id)
		if err != nil {
			return WorkspaceView{}, classifyWorkspaceError(capability.WorkspaceShow, err)
		}
		return workspaceView(value), nil
	})
}

func (service *WorkspaceService) AccessList(ctx context.Context, id string) (Result[[]string], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceAccessList, "Listing workspace allowed directories", []tracepkg.Field{tracepkg.String("workspace_id", strings.TrimSpace(id))}, func() ([]string, error) {
		if err := service.require(capability.WorkspaceAccessList); err != nil {
			return nil, err
		}
		if err := requireApplicationText(capability.WorkspaceAccessList, "workspace id", id); err != nil {
			return nil, err
		}
		value, err := service.manager.Get(id)
		if err != nil {
			return nil, classifyWorkspaceError(capability.WorkspaceAccessList, err)
		}
		return append([]string(nil), value.AllowDirs...), nil
	})
}

func (service *WorkspaceService) Register(ctx context.Context, path string) (Result[WorkspaceView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceRegister, "Registering workspace", []tracepkg.Field{tracepkg.String("input_path", path)}, func() (WorkspaceView, error) {
		if err := service.require(capability.WorkspaceRegister); err != nil {
			return WorkspaceView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceRegister, "path", path); err != nil {
			return WorkspaceView{}, err
		}
		value, err := service.manager.Register(path)
		if err != nil {
			return WorkspaceView{}, classifyWorkspaceError(capability.WorkspaceRegister, err)
		}
		if err := service.reconcileOnce(ctx, capability.WorkspaceRegister); err != nil {
			return WorkspaceView{}, err
		}
		return workspaceView(value), nil
	})
}

func (service *WorkspaceService) Relocate(ctx context.Context, request WorkspaceRelocateRequest) (Result[WorkspaceRelocation], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceRelocate, "Relocating workspace", []tracepkg.Field{tracepkg.String("workspace_id", strings.TrimSpace(request.ID)), tracepkg.String("input_path", request.Path), tracepkg.String("resolution", string(request.Resolution))}, func() (WorkspaceRelocation, error) {
		if err := service.require(capability.WorkspaceRelocate); err != nil {
			return WorkspaceRelocation{}, err
		}
		if err := requireApplicationText(capability.WorkspaceRelocate, "workspace id", request.ID); err != nil {
			return WorkspaceRelocation{}, err
		}
		if err := requireApplicationText(capability.WorkspaceRelocate, "path", request.Path); err != nil {
			return WorkspaceRelocation{}, err
		}
		resolution, err := workspace.ParseRelocationResolution(string(request.Resolution))
		if err != nil {
			return WorkspaceRelocation{}, classifyWorkspaceError(capability.WorkspaceRelocate, err)
		}
		before, err := service.manager.Get(request.ID)
		if err != nil {
			return WorkspaceRelocation{}, classifyWorkspaceError(capability.WorkspaceRelocate, err)
		}
		var after workspace.Workspace
		if resolution == "" {
			after, err = service.manager.Relocate(request.ID, request.Path)
		} else if resolution == workspace.RelocationResolutionMerge {
			after, err = service.manager.ResolveDuplicateRelocationWithMerge(request.ID, request.Path, resolution, workspacereconcile.MergeDuplicateState)
		} else {
			after, err = service.manager.ResolveDuplicateRelocation(request.ID, request.Path, resolution)
		}
		if err != nil {
			return WorkspaceRelocation{}, classifyWorkspaceError(capability.WorkspaceRelocate, err)
		}
		if err := service.reconcileOnce(ctx, capability.WorkspaceRelocate); err != nil {
			return WorkspaceRelocation{}, err
		}
		return WorkspaceRelocation{Before: workspaceView(before), After: workspaceView(after), Resolution: resolution}, nil
	})
}

func WorkspaceRelocationConflictOf(err error) (WorkspaceRelocationConflict, bool) {
	var duplicate *workspace.DuplicateWorkspaceIdentityError
	if !errors.As(err, &duplicate) || duplicate == nil {
		return WorkspaceRelocationConflict{}, false
	}
	return WorkspaceRelocationConflict{
		WorkspaceID:     duplicate.WorkspaceID,
		RegisteredRoot:  duplicate.RegisteredRoot,
		DestinationRoot: duplicate.DestinationRoot,
		Resolutions:     duplicate.Resolutions(),
	}, true
}

func (service *WorkspaceService) Unregister(ctx context.Context, id string) (Result[WorkspaceView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceUnregister, "Unregistering workspace", []tracepkg.Field{tracepkg.String("workspace_id", strings.TrimSpace(id))}, func() (WorkspaceView, error) {
		if err := service.require(capability.WorkspaceUnregister); err != nil {
			return WorkspaceView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceUnregister, "workspace id", id); err != nil {
			return WorkspaceView{}, err
		}
		value, err := service.manager.Get(id)
		if err != nil {
			return WorkspaceView{}, classifyWorkspaceError(capability.WorkspaceUnregister, err)
		}
		if err := service.manager.Unregister(id); err != nil {
			return WorkspaceView{}, classifyWorkspaceError(capability.WorkspaceUnregister, err)
		}
		if err := service.reconcileOnce(ctx, capability.WorkspaceUnregister); err != nil {
			return WorkspaceView{}, err
		}
		return workspaceView(value), nil
	})
}

func (service *WorkspaceService) Purge(ctx context.Context, target string, confirm bool) (Result[WorkspaceView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspacePurge, "Purging workspace local state", []tracepkg.Field{tracepkg.String("target", strings.TrimSpace(target)), tracepkg.Bool("confirmed", confirm)}, func() (WorkspaceView, error) {
		if err := service.require(capability.WorkspacePurge); err != nil {
			return WorkspaceView{}, err
		}
		if err := requireApplicationText(capability.WorkspacePurge, "workspace id or path", target); err != nil {
			return WorkspaceView{}, err
		}
		if !confirm {
			return WorkspaceView{}, operationError(capability.WorkspacePurge, ErrorInvalidArgument, workspace.ErrPurgeNotConfirmed)
		}
		value, err := service.manager.DeleteState(target)
		if err != nil {
			return WorkspaceView{}, classifyWorkspaceError(capability.WorkspacePurge, err)
		}
		if err := service.reconcileOnce(ctx, capability.WorkspacePurge); err != nil {
			return WorkspaceView{}, err
		}
		return workspaceView(value), nil
	})
}

func (service *WorkspaceService) AddAllowDir(ctx context.Context, id, path string) (Result[WorkspaceView], error) {
	return service.mutateWorkspaceAccess(ctx, capability.WorkspaceAccessAdd, id, path, true)
}

func (service *WorkspaceService) RemoveAllowDir(ctx context.Context, id, path string) (Result[WorkspaceView], error) {
	return service.mutateWorkspaceAccess(ctx, capability.WorkspaceAccessRemove, id, path, false)
}

func (service *WorkspaceService) mutateWorkspaceAccess(ctx context.Context, operation capability.ID, id, path string, add bool) (Result[WorkspaceView], error) {
	return runOperation(ctx, "WORKSPACE", operation, "Updating workspace allowed directory", []tracepkg.Field{tracepkg.String("workspace_id", strings.TrimSpace(id)), tracepkg.String("input_path", path)}, func() (WorkspaceView, error) {
		if err := service.require(operation); err != nil {
			return WorkspaceView{}, err
		}
		if err := requireApplicationText(operation, "workspace id", id); err != nil {
			return WorkspaceView{}, err
		}
		if err := requireApplicationText(operation, "path", path); err != nil {
			return WorkspaceView{}, err
		}
		var value workspace.Workspace
		var err error
		if add {
			value, err = service.manager.AddAllowDir(id, path)
		} else {
			value, err = service.manager.RemoveAllowDir(id, path)
		}
		if err != nil {
			return WorkspaceView{}, classifyWorkspaceError(operation, err)
		}
		if err := service.reconcileOnce(ctx, operation); err != nil {
			return WorkspaceView{}, err
		}
		return workspaceView(value), nil
	})
}

func (service *WorkspaceService) ListContainers(ctx context.Context) (Result[[]WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceContainerList, "Listing workspace containers", nil, func() ([]WorkspaceContainerView, error) {
		if err := service.require(capability.WorkspaceContainerList); err != nil {
			return nil, err
		}
		values, err := service.manager.ListContainers()
		if err != nil {
			return nil, classifyWorkspaceError(capability.WorkspaceContainerList, err)
		}
		return workspaceContainerViews(values), nil
	})
}

func (service *WorkspaceService) GetContainer(ctx context.Context, id string) (Result[WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceContainerShow, "Loading workspace container", []tracepkg.Field{tracepkg.String("container_id", strings.TrimSpace(id))}, func() (WorkspaceContainerView, error) {
		if err := service.require(capability.WorkspaceContainerShow); err != nil {
			return WorkspaceContainerView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceContainerShow, "container id", id); err != nil {
			return WorkspaceContainerView{}, err
		}
		value, err := service.manager.GetContainer(id)
		if err != nil {
			return WorkspaceContainerView{}, classifyWorkspaceError(capability.WorkspaceContainerShow, err)
		}
		return workspaceContainerView(value), nil
	})
}

func (service *WorkspaceService) CreateContainer(ctx context.Context, name string) (Result[WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceContainerCreate, "Creating workspace container", []tracepkg.Field{tracepkg.String("name", strings.TrimSpace(name))}, func() (WorkspaceContainerView, error) {
		if err := service.require(capability.WorkspaceContainerCreate); err != nil {
			return WorkspaceContainerView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceContainerCreate, "container name", name); err != nil {
			return WorkspaceContainerView{}, err
		}
		value, err := service.manager.CreateContainer(name)
		if err != nil {
			return WorkspaceContainerView{}, classifyWorkspaceError(capability.WorkspaceContainerCreate, err)
		}
		if err := service.reconcileOnce(ctx, capability.WorkspaceContainerCreate); err != nil {
			return WorkspaceContainerView{}, err
		}
		return workspaceContainerView(value), nil
	})
}

func (service *WorkspaceService) RenameContainer(ctx context.Context, id, name string) (Result[WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceContainerRename, "Renaming workspace container", []tracepkg.Field{tracepkg.String("container_id", strings.TrimSpace(id)), tracepkg.String("name", strings.TrimSpace(name))}, func() (WorkspaceContainerView, error) {
		if err := service.require(capability.WorkspaceContainerRename); err != nil {
			return WorkspaceContainerView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceContainerRename, "container id", id); err != nil {
			return WorkspaceContainerView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceContainerRename, "container name", name); err != nil {
			return WorkspaceContainerView{}, err
		}
		value, err := service.manager.RenameContainer(id, name)
		if err != nil {
			return WorkspaceContainerView{}, classifyWorkspaceError(capability.WorkspaceContainerRename, err)
		}
		if err := service.reconcileOnce(ctx, capability.WorkspaceContainerRename); err != nil {
			return WorkspaceContainerView{}, err
		}
		return workspaceContainerView(value), nil
	})
}

func (service *WorkspaceService) DeleteContainer(ctx context.Context, id string) (Result[WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceContainerDelete, "Deleting workspace container", []tracepkg.Field{tracepkg.String("container_id", strings.TrimSpace(id))}, func() (WorkspaceContainerView, error) {
		if err := service.require(capability.WorkspaceContainerDelete); err != nil {
			return WorkspaceContainerView{}, err
		}
		if err := requireApplicationText(capability.WorkspaceContainerDelete, "container id", id); err != nil {
			return WorkspaceContainerView{}, err
		}
		value, err := service.manager.GetContainer(id)
		if err != nil {
			return WorkspaceContainerView{}, classifyWorkspaceError(capability.WorkspaceContainerDelete, err)
		}
		if err := service.manager.DeleteContainer(id); err != nil {
			return WorkspaceContainerView{}, classifyWorkspaceError(capability.WorkspaceContainerDelete, err)
		}
		if err := service.reconcileOnce(ctx, capability.WorkspaceContainerDelete); err != nil {
			return WorkspaceContainerView{}, err
		}
		return workspaceContainerView(value), nil
	})
}

func (service *WorkspaceService) AddWorkspacesToContainer(ctx context.Context, id string, workspaceIDs []string) (Result[WorkspaceContainerView], error) {
	return service.mutateContainerMembership(ctx, capability.WorkspaceContainerAdd, id, workspaceIDs, true)
}

func (service *WorkspaceService) RemoveWorkspacesFromContainer(ctx context.Context, id string, workspaceIDs []string) (Result[WorkspaceContainerView], error) {
	return service.mutateContainerMembership(ctx, capability.WorkspaceContainerRemove, id, workspaceIDs, false)
}

func (service *WorkspaceService) mutateContainerMembership(ctx context.Context, operation capability.ID, id string, workspaceIDs []string, add bool) (Result[WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", operation, "Updating workspace container membership", []tracepkg.Field{tracepkg.String("container_id", strings.TrimSpace(id)), tracepkg.Int("workspace_count", len(workspaceIDs))}, func() (WorkspaceContainerView, error) {
		if err := service.require(operation); err != nil {
			return WorkspaceContainerView{}, err
		}
		if err := requireApplicationText(operation, "container id", id); err != nil {
			return WorkspaceContainerView{}, err
		}
		if len(workspaceIDs) == 0 {
			return WorkspaceContainerView{}, operationError(operation, ErrorInvalidArgument, errors.New("at least one workspace id is required"))
		}
		var value workspace.WorkspaceContainer
		var err error
		if add {
			value, err = service.manager.AddWorkspacesToContainer(id, workspaceIDs)
		} else {
			value, err = service.manager.RemoveWorkspacesFromContainer(id, workspaceIDs)
		}
		if err != nil {
			return WorkspaceContainerView{}, classifyWorkspaceError(operation, err)
		}
		if err := service.reconcileOnce(ctx, operation); err != nil {
			return WorkspaceContainerView{}, err
		}
		return workspaceContainerView(value), nil
	})
}

func (service *WorkspaceService) ContainersForWorkspace(ctx context.Context, id string) (Result[[]WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceContainerMembershipList, "Listing containers for workspace", []tracepkg.Field{tracepkg.String("workspace_id", strings.TrimSpace(id))}, func() ([]WorkspaceContainerView, error) {
		if err := service.require(capability.WorkspaceContainerMembershipList); err != nil {
			return nil, err
		}
		values, err := service.manager.ContainersForWorkspace(id)
		if err != nil {
			return nil, classifyWorkspaceError(capability.WorkspaceContainerMembershipList, err)
		}
		return workspaceContainerViews(values), nil
	})
}

func (service *WorkspaceService) WorkspacesForContainer(ctx context.Context, id string) (Result[[]WorkspaceView], error) {
	return runOperation(ctx, "WORKSPACE", capability.WorkspaceContainerMembershipList, "Listing workspaces for container", []tracepkg.Field{tracepkg.String("container_id", strings.TrimSpace(id))}, func() ([]WorkspaceView, error) {
		if err := service.require(capability.WorkspaceContainerMembershipList); err != nil {
			return nil, err
		}
		values, err := service.manager.WorkspacesForContainer(id)
		if err != nil {
			return nil, classifyWorkspaceError(capability.WorkspaceContainerMembershipList, err)
		}
		return workspaceViews(values), nil
	})
}

func (service *WorkspaceService) AddWorkspaceToContainers(ctx context.Context, workspaceID string, containerIDs []string) (Result[[]WorkspaceContainerView], error) {
	return service.mutateWorkspaceContainers(ctx, capability.WorkspaceContainerAdd, workspaceID, containerIDs, true)
}

func (service *WorkspaceService) RemoveWorkspaceFromContainers(ctx context.Context, workspaceID string, containerIDs []string) (Result[[]WorkspaceContainerView], error) {
	return service.mutateWorkspaceContainers(ctx, capability.WorkspaceContainerRemove, workspaceID, containerIDs, false)
}

func (service *WorkspaceService) mutateWorkspaceContainers(ctx context.Context, operation capability.ID, workspaceID string, containerIDs []string, add bool) (Result[[]WorkspaceContainerView], error) {
	return runOperation(ctx, "WORKSPACE", operation, "Updating workspace membership across containers", []tracepkg.Field{tracepkg.String("workspace_id", strings.TrimSpace(workspaceID)), tracepkg.Int("container_count", len(containerIDs))}, func() ([]WorkspaceContainerView, error) {
		if err := service.require(operation); err != nil {
			return nil, err
		}
		if err := requireApplicationText(operation, "workspace id", workspaceID); err != nil {
			return nil, err
		}
		if len(containerIDs) == 0 {
			return nil, operationError(operation, ErrorInvalidArgument, errors.New("at least one container id is required"))
		}
		var values []workspace.WorkspaceContainer
		var err error
		if add {
			values, err = service.manager.AddWorkspaceToContainers(workspaceID, containerIDs)
		} else {
			values, err = service.manager.RemoveWorkspaceFromContainers(workspaceID, containerIDs)
		}
		if err != nil {
			return nil, classifyWorkspaceError(operation, err)
		}
		if err := service.reconcileOnce(ctx, operation); err != nil {
			return nil, err
		}
		return workspaceContainerViews(values), nil
	})
}

func (service *WorkspaceService) reconcileOnce(ctx context.Context, operation capability.ID) error {
	if service == nil || service.reconcile == nil {
		return nil
	}
	if err := service.reconcile(ctx); err != nil {
		return operationError(operation, ErrorUnavailable, fmt.Errorf("workspace registry saved but running runtime reload failed: %w", err))
	}
	return nil
}

func requireApplicationText(operation capability.ID, label, value string) error {
	if strings.TrimSpace(value) == "" {
		return operationError(operation, ErrorInvalidArgument, fmt.Errorf("%s is required", label))
	}
	return nil
}

func classifyWorkspaceError(operation capability.ID, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, workspace.ErrContainerNotFound):
		return operationError(operation, ErrorNotFound, err)
	case errors.Is(err, workspace.ErrStateLost):
		return staleOperationError(operation, err)
	case errors.Is(err, workspace.ErrRegistryBusy):
		return retryableOperationError(operation, ErrorUnavailable, err)
	case errors.Is(err, workspace.ErrAlreadyActive),
		errors.Is(err, workspace.ErrDuplicateWorkspaceIdentity),
		errors.Is(err, workspace.ErrWorkspaceReconnectConflict),
		errors.Is(err, workspace.ErrRelocationMergeUnavailable):
		return operationError(operation, ErrorConflict, err)
	case errors.Is(err, workspace.ErrInvalidRelocationResolution):
		return operationError(operation, ErrorInvalidArgument, err)
	case errors.Is(err, workspacereconcile.ErrDurableStateConflict),
		errors.Is(err, workspacereconcile.ErrUnsupportedWorkspaceState):
		return operationError(operation, ErrorConflict, err)
	case errors.Is(err, workspace.ErrUnavailable):
		return operationError(operation, ErrorUnavailable, err)
	case errors.Is(err, workspace.ErrPurgeNotConfirmed):
		return operationError(operation, ErrorInvalidArgument, err)
	case errors.Is(err, os.ErrNotExist):
		return operationError(operation, ErrorInvalidArgument, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return operationError(operation, ErrorUnavailable, err)
	default:
		return operationError(operation, ErrorInternal, err)
	}
}

func workspaceView(value workspace.Workspace) WorkspaceView {
	return WorkspaceView{ID: value.ID, Path: value.Path, AllowDirs: append([]string(nil), value.AllowDirs...), LegacyIDs: append([]string(nil), value.LegacyIDs...), Available: value.Available(), Error: value.Error}
}

func workspaceViews(values []workspace.Workspace) []WorkspaceView {
	result := make([]WorkspaceView, 0, len(values))
	for _, value := range values {
		result = append(result, workspaceView(value))
	}
	return result
}

func workspaceContainerView(value workspace.WorkspaceContainer) WorkspaceContainerView {
	return WorkspaceContainerView{ID: value.ID, Name: value.Name, WorkspaceIDs: append([]string(nil), value.WorkspaceIDs...)}
}

func workspaceContainerViews(values []workspace.WorkspaceContainer) []WorkspaceContainerView {
	result := make([]WorkspaceContainerView, 0, len(values))
	for _, value := range values {
		result = append(result, workspaceContainerView(value))
	}
	return result
}

type WorkspaceIDInput struct {
	ID string
}

type WorkspaceRegisterInput struct {
	Path string
}

type WorkspacePurgeInput struct {
	Target  string
	Confirm bool
}

type WorkspaceContainerInput struct {
	ID   string
	Name string
}

type WorkspaceContainerMembershipInput struct {
	ContainerID  string
	WorkspaceIDs []string
	WorkspaceID  string
	ContainerIDs []string
}

type WorkspaceMembershipQueryInput struct {
	WorkspaceID string
	ContainerID string
}

type WorkspaceAccessInput struct {
	ID   string
	Path string
}

func BindWorkspaceOperations(dispatcher *Dispatcher, service *WorkspaceService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("workspace service is nil")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.WorkspaceList, func(ctx context.Context, _ any) (any, error) {
			result, err := service.List(ctx)
			return result.Value, err
		}},
		{capability.WorkspaceShow, typedOperation[WorkspaceIDInput](capability.WorkspaceShow, func(ctx context.Context, input WorkspaceIDInput) (any, error) {
			result, err := service.Get(ctx, input.ID)
			return result.Value, err
		})},
		{capability.WorkspaceRegister, typedOperation[WorkspaceRegisterInput](capability.WorkspaceRegister, func(ctx context.Context, input WorkspaceRegisterInput) (any, error) {
			result, err := service.Register(ctx, input.Path)
			return result.Value, err
		})},
		{capability.WorkspaceRelocate, typedOperation[WorkspaceRelocateRequest](capability.WorkspaceRelocate, func(ctx context.Context, input WorkspaceRelocateRequest) (any, error) {
			result, err := service.Relocate(ctx, input)
			return result.Value, err
		})},
		{capability.WorkspaceUnregister, typedOperation[WorkspaceIDInput](capability.WorkspaceUnregister, func(ctx context.Context, input WorkspaceIDInput) (any, error) {
			result, err := service.Unregister(ctx, input.ID)
			return result.Value, err
		})},
		{capability.WorkspacePurge, typedOperation[WorkspacePurgeInput](capability.WorkspacePurge, func(ctx context.Context, input WorkspacePurgeInput) (any, error) {
			result, err := service.Purge(ctx, input.Target, input.Confirm)
			return result.Value, err
		})},
		{capability.WorkspaceAccessList, typedOperation[WorkspaceIDInput](capability.WorkspaceAccessList, func(ctx context.Context, input WorkspaceIDInput) (any, error) {
			result, err := service.AccessList(ctx, input.ID)
			return result.Value, err
		})},
		{capability.WorkspaceAccessAdd, typedOperation[WorkspaceAccessInput](capability.WorkspaceAccessAdd, func(ctx context.Context, input WorkspaceAccessInput) (any, error) {
			result, err := service.AddAllowDir(ctx, input.ID, input.Path)
			return result.Value, err
		})},
		{capability.WorkspaceAccessRemove, typedOperation[WorkspaceAccessInput](capability.WorkspaceAccessRemove, func(ctx context.Context, input WorkspaceAccessInput) (any, error) {
			result, err := service.RemoveAllowDir(ctx, input.ID, input.Path)
			return result.Value, err
		})},
		{capability.WorkspaceContainerList, func(ctx context.Context, _ any) (any, error) {
			result, err := service.ListContainers(ctx)
			return result.Value, err
		}},
		{capability.WorkspaceContainerShow, typedOperation[WorkspaceIDInput](capability.WorkspaceContainerShow, func(ctx context.Context, input WorkspaceIDInput) (any, error) {
			result, err := service.GetContainer(ctx, input.ID)
			return result.Value, err
		})},
		{capability.WorkspaceContainerCreate, typedOperation[WorkspaceContainerInput](capability.WorkspaceContainerCreate, func(ctx context.Context, input WorkspaceContainerInput) (any, error) {
			result, err := service.CreateContainer(ctx, input.Name)
			return result.Value, err
		})},
		{capability.WorkspaceContainerRename, typedOperation[WorkspaceContainerInput](capability.WorkspaceContainerRename, func(ctx context.Context, input WorkspaceContainerInput) (any, error) {
			result, err := service.RenameContainer(ctx, input.ID, input.Name)
			return result.Value, err
		})},
		{capability.WorkspaceContainerDelete, typedOperation[WorkspaceIDInput](capability.WorkspaceContainerDelete, func(ctx context.Context, input WorkspaceIDInput) (any, error) {
			result, err := service.DeleteContainer(ctx, input.ID)
			return result.Value, err
		})},
		{capability.WorkspaceContainerMembershipList, typedOperation[WorkspaceMembershipQueryInput](capability.WorkspaceContainerMembershipList, func(ctx context.Context, input WorkspaceMembershipQueryInput) (any, error) {
			if strings.TrimSpace(input.ContainerID) != "" {
				result, err := service.WorkspacesForContainer(ctx, input.ContainerID)
				return result.Value, err
			}
			result, err := service.ContainersForWorkspace(ctx, input.WorkspaceID)
			return result.Value, err
		})},
		{capability.WorkspaceContainerAdd, typedOperation[WorkspaceContainerMembershipInput](capability.WorkspaceContainerAdd, func(ctx context.Context, input WorkspaceContainerMembershipInput) (any, error) {
			if strings.TrimSpace(input.WorkspaceID) != "" {
				result, err := service.AddWorkspaceToContainers(ctx, input.WorkspaceID, input.ContainerIDs)
				return result.Value, err
			}
			result, err := service.AddWorkspacesToContainer(ctx, input.ContainerID, input.WorkspaceIDs)
			return result.Value, err
		})},
		{capability.WorkspaceContainerRemove, typedOperation[WorkspaceContainerMembershipInput](capability.WorkspaceContainerRemove, func(ctx context.Context, input WorkspaceContainerMembershipInput) (any, error) {
			if strings.TrimSpace(input.WorkspaceID) != "" {
				result, err := service.RemoveWorkspaceFromContainers(ctx, input.WorkspaceID, input.ContainerIDs)
				return result.Value, err
			}
			result, err := service.RemoveWorkspacesFromContainer(ctx, input.ContainerID, input.WorkspaceIDs)
			return result.Value, err
		})},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}

func typedOperation[T any](operation capability.ID, handler func(context.Context, T) (any, error)) OperationHandler {
	return func(ctx context.Context, raw any) (any, error) {
		input, ok := raw.(T)
		if !ok {
			return nil, operationError(operation, ErrorInvalidArgument, fmt.Errorf("invalid input type for %s", operation))
		}
		return handler(ctx, input)
	}
}
