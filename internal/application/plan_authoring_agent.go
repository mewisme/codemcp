package application

import (
	"context"
	"errors"
	"fmt"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/workspace"
)

type AgentPlanAuthoringProvider struct {
	service *PlanAuthoringService
}

func NewAgentPlanAuthoringProvider(workspaces *workspace.Manager, streams ...*instructioncontext.ChangeStream) *AgentPlanAuthoringProvider {
	return &AgentPlanAuthoringProvider{service: NewPlanAuthoringService(workspaces, streams...)}
}

func (p *AgentPlanAuthoringProvider) AuthorPlan(ctx context.Context, args map[string]any) (any, error) {
	const operation = capability.PlanCreate
	if p == nil || p.service == nil {
		return nil, retryableOperationError(operation, ErrorUnavailable, errors.New("plan authoring is unavailable"))
	}
	workspaceID, err := authoringProviderString(args, "workspace_id")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	mode, err := authoringProviderString(args, "mode")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	name, err := authoringProviderString(args, "name")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	planContent, err := authoringProviderString(args, "plan_content")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	implementationOrder, err := authoringProviderString(args, "implementation_order")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	expectedContentID, err := planAuthoringOptionalString(args, "expected_content_id")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	dryRun, err := authoringProviderBool(args, "dry_run", false)
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	result, err := p.service.Write(ctx, PlanAuthoringRequest{
		WorkspaceID: workspaceID, Mode: PlanAuthoringMode(mode), Name: name,
		PlanContent: planContent, ImplementationOrder: implementationOrder,
		ExpectedContentID: expectedContentID, DryRun: dryRun,
	})
	return result, classifyAgentPlanAuthoringError(operation, err)
}

func classifyAgentPlanAuthoringError(operation capability.ID, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrPlanInvalid):
		return operationError(operation, ErrorInvalidArgument, err)
	case errors.Is(err, ErrPlanNotFound):
		return operationError(operation, ErrorNotFound, err)
	case errors.Is(err, ErrPlanConflict):
		return operationError(operation, ErrorConflict, err)
	case errors.Is(err, ErrPlanStale):
		return staleOperationError(operation, err)
	case errors.Is(err, ErrPlanUnavailable):
		return retryableOperationError(operation, ErrorUnavailable, err)
	default:
		return normalizeOperationError(operation, err)
	}
}

func planAuthoringOptionalString(args map[string]any, key string) (string, error) {
	value, exists := args[key]
	if !exists {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}
