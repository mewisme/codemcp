package application

import (
	"context"
	"errors"
	"fmt"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/workspace"
)

type AgentInstructionAuthoringProvider struct {
	service *InstructionAuthoringService
}

func NewAgentInstructionAuthoringProvider(workspaces *workspace.Manager, streams ...*instructioncontext.ChangeStream) *AgentInstructionAuthoringProvider {
	return &AgentInstructionAuthoringProvider{
		service: NewInstructionAuthoringService(workspaces, nil, streams...),
	}
}

func (p *AgentInstructionAuthoringProvider) AuthorRule(ctx context.Context, args map[string]any) (any, error) {
	const operation = capability.InstructionRuleCreate
	if p == nil || p.service == nil {
		return nil, retryableOperationError(operation, ErrorUnavailable, errors.New("native instruction authoring is unavailable"))
	}
	scope, err := authoringProviderString(args, "scope")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	if InstructionAuthoringScope(scope) != InstructionScopeWorkspace {
		return nil, operationError(operation, ErrorInvalidArgument, errors.New("agent instruction authoring is workspace-scoped"))
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
	content, err := authoringProviderString(args, "content")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	alwaysApply, err := authoringProviderBool(args, "always_apply", false)
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	globs, err := authoringProviderStrings(args, "globs")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	dryRun, err := authoringProviderBool(args, "dry_run", false)
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	result, err := p.service.WriteRule(ctx, RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionAuthoringMode(mode), WorkspaceID: workspaceID,
		Name: name, AlwaysApply: alwaysApply, Globs: globs, Content: content, DryRun: dryRun,
	})
	return result, classifyAgentInstructionAuthoringError(operation, err)
}

func (p *AgentInstructionAuthoringProvider) AuthorSkill(ctx context.Context, args map[string]any) (any, error) {
	const operation = capability.InstructionSkillCreate
	if p == nil || p.service == nil {
		return nil, retryableOperationError(operation, ErrorUnavailable, errors.New("native instruction authoring is unavailable"))
	}
	scope, err := authoringProviderString(args, "scope")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	if InstructionAuthoringScope(scope) != InstructionScopeWorkspace {
		return nil, operationError(operation, ErrorInvalidArgument, errors.New("agent instruction authoring is workspace-scoped"))
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
	description, err := authoringProviderString(args, "description")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	instructions, err := authoringProviderString(args, "instructions")
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	supportingFiles, err := authoringProviderFiles(args["supporting_files"])
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	dryRun, err := authoringProviderBool(args, "dry_run", false)
	if err != nil {
		return nil, operationError(operation, ErrorInvalidArgument, err)
	}
	result, err := p.service.WriteSkill(ctx, SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionAuthoringMode(mode), WorkspaceID: workspaceID,
		Name: name, Description: description, Instructions: instructions, SupportingFiles: supportingFiles, DryRun: dryRun,
	})
	return result, classifyAgentInstructionAuthoringError(operation, err)
}

func classifyAgentInstructionAuthoringError(operation capability.ID, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrInstructionInvalid):
		return operationError(operation, ErrorInvalidArgument, err)
	case errors.Is(err, ErrInstructionNotFound):
		return operationError(operation, ErrorNotFound, err)
	case errors.Is(err, ErrInstructionConflict):
		return operationError(operation, ErrorConflict, err)
	case errors.Is(err, ErrInstructionStale):
		return staleOperationError(operation, err)
	case errors.Is(err, ErrInstructionUnavailable):
		return retryableOperationError(operation, ErrorUnavailable, err)
	default:
		return normalizeOperationError(operation, err)
	}
}

func authoringProviderString(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return value, nil
}

func authoringProviderBool(args map[string]any, key string, fallback bool) (bool, error) {
	value, exists := args[key]
	if !exists {
		return fallback, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return result, nil
}

func authoringProviderStrings(args map[string]any, key string) ([]string, error) {
	value, exists := args[key]
	if !exists {
		return nil, nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		result := make([]string, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a string", key, index)
			}
			result[index] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func authoringProviderFiles(value any) ([]SkillSupportingFile, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]map[string]any); typedOK {
			items = make([]any, len(typed))
			for index := range typed {
				items[index] = typed[index]
			}
		} else {
			return nil, errors.New("supporting_files must be an array")
		}
	}
	if len(items) > maxAuthoredSkillFiles {
		return nil, fmt.Errorf("supporting_files must contain at most %d entries", maxAuthoredSkillFiles)
	}
	result := make([]SkillSupportingFile, 0, len(items))
	for index, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("supporting_files[%d] must be an object", index)
		}
		filePath, err := authoringProviderString(entry, "path")
		if err != nil {
			return nil, fmt.Errorf("supporting_files[%d]: %w", index, err)
		}
		content, err := authoringProviderString(entry, "content")
		if err != nil {
			return nil, fmt.Errorf("supporting_files[%d]: %w", index, err)
		}
		executable, err := authoringProviderBool(entry, "executable", false)
		if err != nil {
			return nil, fmt.Errorf("supporting_files[%d]: %w", index, err)
		}
		result = append(result, SkillSupportingFile{
			Path: filePath, Content: []byte(content), Executable: executable,
		})
	}
	return result, nil
}
