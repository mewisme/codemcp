package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/capability"
	managed "go.mewis.me/codemcp/internal/service"
)

type RuntimeActionInput struct {
	Scope managed.Scope
}

type PromptListInput struct {
	WorkspaceID string
}

type PromptGetInput struct {
	WorkspaceID string
	Name        string
}

type SystemOperationServices struct {
	Instructions   *InstructionSettingsService
	ProjectContext *ProjectContextService
	Tools          *ToolInventoryService
	Prompts        *PromptService
}

func BindSystemOperations(dispatcher *Dispatcher, services SystemOperationServices) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if services.Instructions == nil {
		services.Instructions = NewInstructionSettingsService(nil)
	}
	if services.ProjectContext == nil {
		services.ProjectContext = NewDefaultProjectContextService()
	}
	if services.Tools == nil {
		services.Tools = NewToolInventoryService()
	}
	if services.Prompts == nil {
		services.Prompts = &PromptService{}
	}

	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.VersionAbout, func(ctx context.Context, _ any) (any, error) { return LoadAbout(ctx) }},
		{capability.RuntimeUp, typedOperation[RuntimeActionInput](capability.RuntimeUp, func(ctx context.Context, input RuntimeActionInput) (any, error) {
			return ManagedRuntimeAction(ctx, "up", input.Scope)
		})},
		{capability.RuntimeDown, typedOperation[RuntimeActionInput](capability.RuntimeDown, func(ctx context.Context, input RuntimeActionInput) (any, error) {
			return ManagedRuntimeAction(ctx, "down", input.Scope)
		})},
		{capability.RuntimeRestart, typedOperation[RuntimeActionInput](capability.RuntimeRestart, func(ctx context.Context, input RuntimeActionInput) (any, error) {
			return ManagedRuntimeAction(ctx, "restart", input.Scope)
		})},
		{capability.UpdateCheck, func(ctx context.Context, _ any) (any, error) { return CheckForUpdate(ctx) }},
		{capability.UpdateApply, typedOperation[UpdateApplyOptions](capability.UpdateApply, func(ctx context.Context, input UpdateApplyOptions) (any, error) {
			return ApplyUpdate(ctx, input)
		})},
		{capability.InstallRun, typedOperation[InstallCurrentOptions](capability.InstallRun, func(ctx context.Context, input InstallCurrentOptions) (any, error) {
			return InstallCurrentContext(ctx, input)
		})},
		{capability.InstructionSettingsRead, func(ctx context.Context, _ any) (any, error) {
			result, err := services.Instructions.Read(ctx)
			return result.Value, err
		}},
		{capability.InstructionSettingsWrite, typedOperation[InstructionSettingsPatch](capability.InstructionSettingsWrite, func(ctx context.Context, input InstructionSettingsPatch) (any, error) {
			result, err := services.Instructions.Write(ctx, input)
			return result.Value, err
		})},
		{capability.ProjectContextRead, typedOperation[ProjectContextInput](capability.ProjectContextRead, func(ctx context.Context, input ProjectContextInput) (any, error) {
			result, err := services.ProjectContext.Read(ctx, input)
			return result.Value, err
		})},
		{capability.ToolInventoryRead, func(ctx context.Context, _ any) (any, error) {
			result, err := services.Tools.List(ctx)
			return result.Value, err
		}},
		{capability.PromptList, typedOperation[PromptListInput](capability.PromptList, func(_ context.Context, input PromptListInput) (any, error) {
			return services.Prompts.List(input.WorkspaceID)
		})},
		{capability.PromptGet, typedOperation[PromptGetInput](capability.PromptGet, func(_ context.Context, input PromptGetInput) (any, error) {
			return services.Prompts.Definition(input.WorkspaceID, input.Name)
		})},
		{capability.PromptCreate, typedOperation[PromptWriteRequest](capability.PromptCreate, func(ctx context.Context, input PromptWriteRequest) (any, error) {
			input.Mode = "create"
			return services.Prompts.Write(ctx, input)
		})},
		{capability.PromptUpdate, typedOperation[PromptWriteRequest](capability.PromptUpdate, func(ctx context.Context, input PromptWriteRequest) (any, error) {
			input.Mode = "update"
			return services.Prompts.Write(ctx, input)
		})},
		{capability.PromptDelete, typedOperation[PromptDeleteRequest](capability.PromptDelete, func(ctx context.Context, input PromptDeleteRequest) (any, error) {
			if err := services.Prompts.Delete(ctx, input); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true, "name": input.Name, "scope": input.Scope}, nil
		})},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
