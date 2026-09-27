package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/workspace"
)

type AgentPromptProvider struct{ service *PromptService }

func NewAgentPromptProvider(workspaces *workspace.Manager) *AgentPromptProvider {
	return &AgentPromptProvider{service: &PromptService{Workspaces: workspaces}}
}

func (p *AgentPromptProvider) ListPromptDefinitions(_ context.Context, workspaceID string) (any, error) {
	return p.service.List(workspaceID)
}

func (p *AgentPromptProvider) GetPromptDefinition(_ context.Context, workspaceID, name string, arguments map[string]string) (any, error) {
	prompt, messages, err := p.service.Get(workspaceID, name, arguments)
	if err != nil {
		return nil, err
	}
	return map[string]any{"scope": prompt.Scope, "definition": prompt.Definition, "messages": messages}, nil
}

func (p *AgentPromptProvider) WritePromptDefinition(ctx context.Context, workspaceID, mode string, value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var definition instructioncontext.PromptDefinition
	if err := decoder.Decode(&definition); err != nil {
		return nil, err
	}
	if definition.Name == "" {
		return nil, errors.New("prompt name is required")
	}
	return p.service.Write(ctx, PromptWriteRequest{Scope: instructioncontext.PromptScopeWorkspace, WorkspaceID: workspaceID, Mode: mode, Definition: definition})
}

func (p *AgentPromptProvider) DeletePromptDefinition(ctx context.Context, workspaceID, name string) (any, error) {
	err := p.service.Delete(ctx, PromptDeleteRequest{Scope: instructioncontext.PromptScopeWorkspace, WorkspaceID: workspaceID, Name: name})
	if err != nil {
		return nil, err
	}
	return map[string]any{"scope": "workspace", "workspace_id": workspaceID, "name": name, "deleted": true}, nil
}
