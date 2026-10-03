package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/workspace"
)

type ManagedAgentService struct {
	Manager    *managedagent.Manager
	Workspaces *workspace.Manager
}

type ManagedAgentSpawnInput struct {
	WorkspaceID     string `json:"workspace_id"`
	Prompt          string `json:"prompt"`
	Backend         string `json:"backend,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type ManagedAgentListInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Backend     string `json:"backend,omitempty"`
	State       string `json:"state,omitempty"`
}

type ManagedAgentIDInput struct {
	AgentID string `json:"agent_id"`
}

type ManagedAgentWaitInput struct {
	AgentID       string `json:"agent_id"`
	AfterRevision uint64 `json:"after_revision,omitempty"`
	TimeoutMS     int    `json:"timeout_ms,omitempty"`
}

type ManagedAgentSendInput struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

func NewManagedAgentService(manager *managedagent.Manager, workspaces *workspace.Manager) *ManagedAgentService {
	return &ManagedAgentService{Manager: manager, Workspaces: workspaces}
}

func (service *ManagedAgentService) Spawn(ctx context.Context, input ManagedAgentSpawnInput) (managedagent.Snapshot, error) {
	if service == nil || service.Manager == nil || service.Workspaces == nil {
		return managedagent.Snapshot{}, errors.New("managed agent runtime is unavailable")
	}
	workspaceID, err := service.Workspaces.CanonicalID(input.WorkspaceID)
	if err != nil {
		return managedagent.Snapshot{}, err
	}
	return service.Manager.Spawn(ctx, managedagent.OperatorController(), managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: workspaceID, Prompt: input.Prompt, Backend: managedagent.BackendID(input.Backend),
		Model: input.Model, ReasoningEffort: input.ReasoningEffort,
	}})
}

func (service *ManagedAgentService) List(ctx context.Context, input ManagedAgentListInput) ([]managedagent.Snapshot, error) {
	if service == nil || service.Manager == nil {
		return nil, errors.New("managed agent runtime is unavailable")
	}
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID != "" {
		if service.Workspaces == nil {
			return nil, errors.New("managed agent workspace runtime is unavailable")
		}
		canonical, err := service.Workspaces.CanonicalID(workspaceID)
		if err != nil {
			return nil, err
		}
		workspaceID = canonical
	}
	backendRaw := strings.TrimSpace(input.Backend)
	stateRaw := strings.TrimSpace(input.State)
	var backend managedagent.BackendID
	var err error
	if backendRaw != "" {
		backend, err = managedagent.NormalizeBackendID(backendRaw)
		if err != nil {
			return nil, err
		}
	}
	state := managedagent.State(stateRaw)
	if stateRaw != "" && !state.Valid() {
		return nil, fmt.Errorf("invalid managed agent state %q", stateRaw)
	}
	items, err := service.Manager.List(ctx, managedagent.OperatorController())
	if err != nil {
		return nil, err
	}
	filtered := make([]managedagent.Snapshot, 0, len(items))
	for _, item := range items {
		if workspaceID != "" && item.WorkspaceID != workspaceID {
			continue
		}
		if backend != "" && item.Backend != backend {
			continue
		}
		if state != "" && item.State != state {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}

func (service *ManagedAgentService) Get(ctx context.Context, input ManagedAgentIDInput) (managedagent.Snapshot, error) {
	id, err := managedAgentID(input.AgentID)
	if err != nil {
		return managedagent.Snapshot{}, err
	}
	if service == nil || service.Manager == nil {
		return managedagent.Snapshot{}, errors.New("managed agent runtime is unavailable")
	}
	return service.Manager.Get(ctx, managedagent.OperatorController(), id)
}

func (service *ManagedAgentService) Wait(ctx context.Context, input ManagedAgentWaitInput) (managedagent.Snapshot, error) {
	id, err := managedAgentID(input.AgentID)
	if err != nil {
		return managedagent.Snapshot{}, err
	}
	if input.TimeoutMS < 0 || input.TimeoutMS > int(managedagent.MaxWaitDuration/time.Millisecond) {
		return managedagent.Snapshot{}, fmt.Errorf("timeout_ms must be between 0 and %d", managedagent.MaxWaitDuration/time.Millisecond)
	}
	if service == nil || service.Manager == nil {
		return managedagent.Snapshot{}, errors.New("managed agent runtime is unavailable")
	}
	return service.Manager.Wait(ctx, managedagent.OperatorController(), id, input.AfterRevision, time.Duration(input.TimeoutMS)*time.Millisecond)
}

func (service *ManagedAgentService) Send(ctx context.Context, input ManagedAgentSendInput) (managedagent.Snapshot, error) {
	id, err := managedAgentID(input.AgentID)
	if err != nil {
		return managedagent.Snapshot{}, err
	}
	if service == nil || service.Manager == nil {
		return managedagent.Snapshot{}, errors.New("managed agent runtime is unavailable")
	}
	return service.Manager.Send(ctx, managedagent.OperatorController(), id, managedagent.Message{Content: input.Message})
}

func (service *ManagedAgentService) Cancel(ctx context.Context, input ManagedAgentIDInput) (managedagent.Snapshot, error) {
	id, err := managedAgentID(input.AgentID)
	if err != nil {
		return managedagent.Snapshot{}, err
	}
	if service == nil || service.Manager == nil {
		return managedagent.Snapshot{}, errors.New("managed agent runtime is unavailable")
	}
	return service.Manager.Cancel(ctx, managedagent.OperatorController(), id)
}

func BindManagedAgentOperations(dispatcher *Dispatcher, service *ManagedAgentService) error {
	if dispatcher == nil || service == nil {
		return errors.New("managed agent operation dependencies are required")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.ManagedAgentSpawn, typedOperation(capability.ManagedAgentSpawn, func(ctx context.Context, input ManagedAgentSpawnInput) (any, error) { return service.Spawn(ctx, input) })},
		{capability.ManagedAgentList, typedOperation(capability.ManagedAgentList, func(ctx context.Context, input ManagedAgentListInput) (any, error) { return service.List(ctx, input) })},
		{capability.ManagedAgentGet, typedOperation(capability.ManagedAgentGet, func(ctx context.Context, input ManagedAgentIDInput) (any, error) { return service.Get(ctx, input) })},
		{capability.ManagedAgentWait, typedOperation(capability.ManagedAgentWait, func(ctx context.Context, input ManagedAgentWaitInput) (any, error) { return service.Wait(ctx, input) })},
		{capability.ManagedAgentSend, typedOperation(capability.ManagedAgentSend, func(ctx context.Context, input ManagedAgentSendInput) (any, error) { return service.Send(ctx, input) })},
		{capability.ManagedAgentCancel, typedOperation(capability.ManagedAgentCancel, func(ctx context.Context, input ManagedAgentIDInput) (any, error) { return service.Cancel(ctx, input) })},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}

func managedAgentID(raw string) (managedagent.ID, error) {
	id := managedagent.ID(strings.TrimSpace(raw))
	if err := managedagent.ValidateID(id); err != nil {
		return "", err
	}
	return id, nil
}
