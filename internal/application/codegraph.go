package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type CodeGraphService struct {
	LoadConfig  func() (config.Config, error)
	ManagedRoot string
	HTTPClient  *http.Client
	Workspaces  *workspace.Manager
	Now         func() time.Time
}

type CodeGraphWorkspaceInput struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path,omitempty"`
}

type CodeGraphWorkspaceActionResult struct {
	Status  codegraph.WorkspaceStatus `json:"status"`
	Output  string                    `json:"output,omitempty"`
	Skipped bool                      `json:"skipped,omitempty"`
	Reason  string                    `json:"reason,omitempty"`
}

func NewCodeGraphService(workspaces ...*workspace.Manager) *CodeGraphService {
	service := &CodeGraphService{LoadConfig: config.Load, Now: time.Now}
	if len(workspaces) > 0 {
		service.Workspaces = workspaces[0]
	}
	return service
}

func (s *CodeGraphService) Status(context.Context) (codegraph.Status, error) {
	runtime, err := s.runtime()
	if err != nil {
		return codegraph.Status{}, err
	}
	return runtime.Status()
}

func (s *CodeGraphService) Probe(ctx context.Context) (codegraph.ProbeResult, error) {
	runtime, err := s.runtime()
	if err != nil {
		return codegraph.ProbeResult{}, err
	}
	return runtime.Probe(ctx)
}

func (s *CodeGraphService) Install(ctx context.Context) (codegraph.InstallResult, error) {
	runtime, err := s.runtime()
	if err != nil {
		return codegraph.InstallResult{}, err
	}
	return runtime.Install(ctx)
}

func (s *CodeGraphService) WorkspaceStatus(ctx context.Context, input CodeGraphWorkspaceInput) (codegraph.WorkspaceStatus, error) {
	runtime, item, projectRoot, relative, store, err := s.workspaceTarget(input)
	if err != nil {
		return codegraph.WorkspaceStatus{}, err
	}
	status, err := runtime.Status()
	if err != nil {
		return codegraph.WorkspaceStatus{}, err
	}
	_ = ctx
	return codegraph.InspectWorkspace(status, store, item.ID, projectRoot, relative), nil
}

func (s *CodeGraphService) InitWorkspace(ctx context.Context, input CodeGraphWorkspaceInput) (CodeGraphWorkspaceActionResult, error) {
	runtime, item, projectRoot, relative, store, err := s.workspaceTarget(input)
	if err != nil {
		return CodeGraphWorkspaceActionResult{}, err
	}
	lock, err := codegraph.AcquireWorkspaceMutationLock(store)
	if err != nil {
		return CodeGraphWorkspaceActionResult{}, err
	}
	defer lock.Release()

	status, err := runtime.Status()
	if err != nil {
		return CodeGraphWorkspaceActionResult{}, err
	}
	before := codegraph.InspectWorkspace(status, store, item.ID, projectRoot, relative)
	if err := validateCodeGraphWorkspaceRuntime(before.Runtime); err != nil {
		return CodeGraphWorkspaceActionResult{Status: before}, err
	}
	if before.IndexState == codegraph.IndexIndexed {
		return CodeGraphWorkspaceActionResult{Status: before, Skipped: true, Reason: "workspace project is already indexed"}, nil
	}
	result, err := runtime.ExecuteInDir(ctx, projectRoot, codegraph.InitArgs(projectRoot), codegraph.InitTimeout, codegraph.MaxOutputBytes)
	if err != nil {
		return CodeGraphWorkspaceActionResult{Status: before}, fmt.Errorf("codegraph init failed: %w", err)
	}
	if !codegraph.InspectIndex(projectRoot) {
		return CodeGraphWorkspaceActionResult{Status: before}, errors.New("codegraph init completed without creating a workspace index")
	}
	if err := codegraph.RecordWorkspaceLifecycle(store, item.ID, projectRoot, relative, "init", s.now()); err != nil {
		return CodeGraphWorkspaceActionResult{Status: before}, fmt.Errorf("record codegraph init state: %w", err)
	}
	after := codegraph.InspectWorkspace(status, store, item.ID, projectRoot, relative)
	return CodeGraphWorkspaceActionResult{Status: after, Output: codeGraphCommandOutput(result)}, nil
}

func (s *CodeGraphService) SyncWorkspace(ctx context.Context, input CodeGraphWorkspaceInput) (CodeGraphWorkspaceActionResult, error) {
	runtime, item, projectRoot, relative, store, err := s.workspaceTarget(input)
	if err != nil {
		return CodeGraphWorkspaceActionResult{}, err
	}
	lock, err := codegraph.AcquireWorkspaceMutationLock(store)
	if err != nil {
		return CodeGraphWorkspaceActionResult{}, err
	}
	defer lock.Release()

	status, err := runtime.Status()
	if err != nil {
		return CodeGraphWorkspaceActionResult{}, err
	}
	before := codegraph.InspectWorkspace(status, store, item.ID, projectRoot, relative)
	if err := validateCodeGraphWorkspaceRuntime(before.Runtime); err != nil {
		return CodeGraphWorkspaceActionResult{Status: before}, err
	}
	if before.IndexState != codegraph.IndexIndexed {
		return CodeGraphWorkspaceActionResult{Status: before}, errors.New("codegraph workspace project is not indexed; initialize it first")
	}
	if before.Freshness == codegraph.FreshnessFresh {
		return CodeGraphWorkspaceActionResult{Status: before, Skipped: true, Reason: "workspace project index is already fresh"}, nil
	}
	result, err := runtime.ExecuteInDir(ctx, projectRoot, codegraph.SyncArgs(projectRoot), codegraph.SyncTimeout, codegraph.MaxOutputBytes)
	if err != nil {
		return CodeGraphWorkspaceActionResult{Status: before}, fmt.Errorf("codegraph sync failed: %w", err)
	}
	if err := codegraph.RecordWorkspaceLifecycle(store, item.ID, projectRoot, relative, "sync", s.now()); err != nil {
		return CodeGraphWorkspaceActionResult{Status: before}, fmt.Errorf("record codegraph sync state: %w", err)
	}
	after := codegraph.InspectWorkspace(status, store, item.ID, projectRoot, relative)
	return CodeGraphWorkspaceActionResult{Status: after, Output: codeGraphCommandOutput(result)}, nil
}

func (s *CodeGraphService) workspaceTarget(input CodeGraphWorkspaceInput) (*codegraph.Runtime, workspace.Workspace, string, string, workspacestate.Store, error) {
	if s == nil || s.Workspaces == nil {
		return nil, workspace.Workspace{}, "", "", workspacestate.Store{}, errors.New("workspace manager is unavailable")
	}
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID == "" {
		return nil, workspace.Workspace{}, "", "", workspacestate.Store{}, errors.New("workspace_id is required")
	}
	item, projectRoot, err := s.Workspaces.ResolveDirectory(workspaceID, strings.TrimSpace(input.Path))
	if err != nil {
		return nil, workspace.Workspace{}, "", "", workspacestate.Store{}, err
	}
	relative, err := filepath.Rel(item.Path, projectRoot)
	if err != nil {
		return nil, workspace.Workspace{}, "", "", workspacestate.Store{}, err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, workspace.Workspace{}, "", "", workspacestate.Store{}, fmt.Errorf("codegraph project path escapes registered workspace root: %s", projectRoot)
	}
	store, err := s.Workspaces.LocalState(item.ID)
	if err != nil {
		return nil, workspace.Workspace{}, "", "", workspacestate.Store{}, err
	}
	runtime, err := s.runtime()
	if err != nil {
		return nil, workspace.Workspace{}, "", "", workspacestate.Store{}, err
	}
	return runtime, item, projectRoot, relative, store, nil
}

func (s *CodeGraphService) runtime() (*codegraph.Runtime, error) {
	if s == nil || s.LoadConfig == nil {
		return nil, errors.New("CodeGraph config loader is unavailable")
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}
	return codegraph.New(codegraph.Options{
		Enabled:        cfg.Integrations.CodeGraph.Enabled,
		ConfiguredPath: cfg.Integrations.CodeGraph.Path,
		ManagedRoot:    s.ManagedRoot,
		HTTPClient:     s.HTTPClient,
	}), nil
}

func (s *CodeGraphService) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func validateCodeGraphWorkspaceRuntime(status codegraph.Status) error {
	if !status.Enabled || status.Resolution.Source == codegraph.ExecutableDisabled {
		return errors.New("codegraph integration is disabled")
	}
	if status.Resolution.Source == codegraph.ExecutableUnavailable || strings.TrimSpace(status.Resolution.Path) == "" {
		return errors.New("codegraph executable is unavailable")
	}
	return nil
}

func codeGraphCommandOutput(result codegraph.CommandResult) string {
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		output = strings.TrimSpace(result.Stderr)
	}
	if len(output) > codegraph.MaxOutputBytes {
		output = output[:codegraph.MaxOutputBytes]
	}
	return output
}

func BindCodeGraphOperations(dispatcher *Dispatcher, service *CodeGraphService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("CodeGraph service is nil")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.IntegrationCodeGraphStatus, func(ctx context.Context, _ any) (any, error) { return service.Status(ctx) }},
		{capability.IntegrationCodeGraphProbe, func(ctx context.Context, _ any) (any, error) { return service.Probe(ctx) }},
		{capability.IntegrationCodeGraphInstall, func(ctx context.Context, _ any) (any, error) { return service.Install(ctx) }},
		{capability.IntegrationCodeGraphWorkspaceStatus, typedOperation[CodeGraphWorkspaceInput](capability.IntegrationCodeGraphWorkspaceStatus, func(ctx context.Context, input CodeGraphWorkspaceInput) (any, error) {
			return service.WorkspaceStatus(ctx, input)
		})},
		{capability.IntegrationCodeGraphWorkspaceInit, typedOperation[CodeGraphWorkspaceInput](capability.IntegrationCodeGraphWorkspaceInit, func(ctx context.Context, input CodeGraphWorkspaceInput) (any, error) {
			return service.InitWorkspace(ctx, input)
		})},
		{capability.IntegrationCodeGraphWorkspaceSync, typedOperation[CodeGraphWorkspaceInput](capability.IntegrationCodeGraphWorkspaceSync, func(ctx context.Context, input CodeGraphWorkspaceInput) (any, error) {
			return service.SyncWorkspace(ctx, input)
		})},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
