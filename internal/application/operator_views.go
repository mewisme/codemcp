package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

type ToolInventoryService struct {
	Runtime    *tools.Runtime
	LoadConfig func() (config.Config, error)
}

func NewToolInventoryService() *ToolInventoryService {
	return &ToolInventoryService{LoadConfig: config.Load}
}

func NewToolInventoryServiceWithRuntime(runtime *tools.Runtime) *ToolInventoryService {
	return &ToolInventoryService{Runtime: runtime, LoadConfig: config.Load}
}

func (s *ToolInventoryService) List(ctx context.Context) (Result[[]tools.Schema], error) {
	return runOperation(ctx, "TOOLS", capability.ToolInventoryRead, "Reading tool inventory", nil, func() ([]tools.Schema, error) {
		runtime, err := s.runtime()
		if err != nil {
			return nil, err
		}
		return runtime.ListTools(), nil
	})
}

func (s *ToolInventoryService) runtime() (*tools.Runtime, error) {
	if s != nil && s.Runtime != nil {
		return s.Runtime, nil
	}
	load := config.Load
	if s != nil && s.LoadConfig != nil {
		load = s.LoadConfig
	}
	cfg, err := load()
	if err != nil {
		return nil, err
	}
	runtime := tools.NewRuntimeWithAccess(cfg.Integrations, cfg.Permissions.AllowDirs, ProjectContextEnvironment)
	runtime.SetShellPath(cfg.Shell.Path)
	return runtime, nil
}

type ProjectContextInput struct {
	WorkspaceID string
	Options     projectcontext.Options
}

type ProjectContextService struct {
	Workspaces *workspace.Manager
	Runtime    *tools.Runtime
}

func NewDefaultProjectContextService() *ProjectContextService {
	return &ProjectContextService{Workspaces: workspace.NewManager(workspace.DefaultStorePath())}
}

func NewApplicationProjectContextService(workspaces *workspace.Manager, runtimes ...*tools.Runtime) *ProjectContextService {
	service := &ProjectContextService{Workspaces: workspaces}
	if len(runtimes) > 0 {
		service.Runtime = runtimes[0]
	}
	return service
}

func (s *ProjectContextService) Read(ctx context.Context, input ProjectContextInput) (Result[projectcontext.Result], error) {
	return runOperation(ctx, "PROJECT_CONTEXT", capability.ProjectContextRead, "Building project context", nil, func() (projectcontext.Result, error) {
		if s == nil || s.Workspaces == nil {
			return projectcontext.Result{}, errors.New("workspace manager is unavailable")
		}
		workspaceID := strings.TrimSpace(input.WorkspaceID)
		if workspaceID == "" {
			return projectcontext.Result{}, errors.New("workspace_id is required")
		}
		service := NewProjectContextService(ctx, s.Workspaces)
		if s.Runtime != nil {
			service.ToolInventory = func(context.Context) instructioncontext.ToolInventory {
				return tools.InstructionToolInventory(tools.EffectiveToolSnapshot{Profile: "full", Schemas: s.Runtime.ListTools()})
			}
		}
		value, err := service.Build(ctx, workspaceID, input.Options)
		if errors.Is(err, projectcontext.ErrPlanNotFound) {
			return projectcontext.Result{}, operationError(capability.ProjectContextRead, ErrorNotFound, err)
		}
		return value, err
	})
}

type RuntimeInspectionService struct{}

func NewRuntimeInspectionService() *RuntimeInspectionService { return &RuntimeInspectionService{} }

func (s *RuntimeInspectionService) ListExecutions(ctx context.Context, workspaceID string) (Result[[]shellruntime.ExecutionInfo], error) {
	return runOperation(ctx, "EXECUTION", capability.ExecutionList, "Listing workspace executions", nil, func() ([]shellruntime.ExecutionInfo, error) {
		workspaceID = strings.TrimSpace(workspaceID)
		if workspaceID == "" {
			return nil, errors.New("workspace_id is required")
		}
		values, err := runtimecontrol.ListExecutions(ctx)
		if err != nil {
			return nil, err
		}
		filtered := make([]shellruntime.ExecutionInfo, 0, len(values))
		for _, value := range values {
			if value.WorkspaceID == workspaceID {
				filtered = append(filtered, value)
			}
		}
		return filtered, nil
	})
}

func (s *RuntimeInspectionService) ViewExecution(ctx context.Context, workspaceID, executionID string) (Result[shellruntime.ExecutionSnapshot], error) {
	return runOperation(ctx, "EXECUTION", capability.ExecutionView, "Reading workspace execution", nil, func() (shellruntime.ExecutionSnapshot, error) {
		workspaceID, executionID = strings.TrimSpace(workspaceID), strings.TrimSpace(executionID)
		if workspaceID == "" || executionID == "" {
			return shellruntime.ExecutionSnapshot{}, errors.New("workspace_id and execution_id are required")
		}
		value, err := runtimecontrol.GetExecution(ctx, executionID)
		if err != nil {
			return shellruntime.ExecutionSnapshot{}, err
		}
		if value.Execution.WorkspaceID != workspaceID {
			return shellruntime.ExecutionSnapshot{}, fmt.Errorf("execution not found in workspace %s: %s", workspaceID, executionID)
		}
		return value, nil
	})
}

func (s *RuntimeInspectionService) ListProcesses(ctx context.Context, workspaceID string) (Result[[]shellruntime.ProcessInfo], error) {
	return runOperation(ctx, "PROCESS", capability.ProcessList, "Listing workspace processes", nil, func() ([]shellruntime.ProcessInfo, error) {
		workspaceID = strings.TrimSpace(workspaceID)
		if workspaceID == "" {
			return nil, errors.New("workspace_id is required")
		}
		return runtimecontrol.ListProcesses(ctx, workspaceID)
	})
}

func (s *RuntimeInspectionService) ViewProcess(ctx context.Context, workspaceID, processID string) (Result[shellruntime.ProcessInfo], error) {
	return runOperation(ctx, "PROCESS", capability.ProcessView, "Reading workspace process", nil, func() (shellruntime.ProcessInfo, error) {
		workspaceID, processID = strings.TrimSpace(workspaceID), strings.TrimSpace(processID)
		if workspaceID == "" || processID == "" {
			return shellruntime.ProcessInfo{}, errors.New("workspace_id and process_id are required")
		}
		values, err := runtimecontrol.ListProcesses(ctx, workspaceID)
		if err != nil {
			return shellruntime.ProcessInfo{}, err
		}
		for _, value := range values {
			if value.ID == processID {
				return value, nil
			}
		}
		return shellruntime.ProcessInfo{}, fmt.Errorf("process not found in workspace %s: %s", workspaceID, processID)
	})
}

func (s *RuntimeInspectionService) ClearProcess(ctx context.Context, workspaceID, processID string) (Result[ProcessClearResult], error) {
	return runOperation(ctx, "PROCESS", capability.ProcessClear, "Clearing finished workspace process", nil, func() (ProcessClearResult, error) {
		workspaceID, processID = strings.TrimSpace(workspaceID), strings.TrimSpace(processID)
		if workspaceID == "" || processID == "" {
			return ProcessClearResult{}, errors.New("workspace_id and process_id are required")
		}
		if err := runtimecontrol.DeleteFinishedProcess(ctx, workspaceID, processID); err != nil {
			return ProcessClearResult{}, err
		}
		return ProcessClearResult{Deleted: true}, nil
	})
}
