package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/workspace"
)

// PromptService is the shared Prompt read, render, and mutation authority for interfaces.
type PromptService struct {
	Workspaces          *workspace.Manager
	AllowGlobalMutation func(context.Context) bool
}

type PromptWriteRequest struct {
	Scope       instructioncontext.PromptScope      `json:"scope"`
	WorkspaceID string                              `json:"workspace_id,omitempty"`
	Mode        string                              `json:"mode"`
	Definition  instructioncontext.PromptDefinition `json:"definition"`
}

type PromptDeleteRequest struct {
	Scope       instructioncontext.PromptScope `json:"scope"`
	WorkspaceID string                         `json:"workspace_id,omitempty"`
	Name        string                         `json:"name"`
}

func (s *PromptService) store(scope instructioncontext.PromptScope, workspaceID string) (*instructioncontext.PromptStore, error) {
	if s == nil {
		return nil, errors.New("prompt service is unavailable")
	}
	switch scope {
	case instructioncontext.PromptScopeGlobal:
		if strings.TrimSpace(workspaceID) != "" {
			return nil, errors.New("global prompt scope cannot select a workspace")
		}
		return instructioncontext.NewPromptStore(""), nil
	case instructioncontext.PromptScopeWorkspace:
		if s.Workspaces == nil {
			return nil, errors.New("workspace manager is unavailable")
		}
		if strings.TrimSpace(workspaceID) == "" {
			return nil, errors.New("workspace_id is required")
		}
		selected, err := s.Workspaces.Get(workspaceID)
		if err != nil {
			return nil, err
		}
		return instructioncontext.NewPromptStore(selected.Path), nil
	default:
		return nil, fmt.Errorf("unsupported prompt scope %q", scope)
	}
}

// List resolves workspace definitions over global definitions; global-only reads use an empty workspace ID.
func (s *PromptService) List(workspaceID string) ([]instructioncontext.ScopedPrompt, error) {
	scope := instructioncontext.PromptScopeGlobal
	if workspaceID != "" {
		scope = instructioncontext.PromptScopeWorkspace
	}
	store, err := s.store(scope, workspaceID)
	if err != nil {
		return nil, err
	}
	return store.List()
}

func (s *PromptService) Get(workspaceID, name string, arguments map[string]string) (instructioncontext.ScopedPrompt, []instructioncontext.PromptMessage, error) {
	scope := instructioncontext.PromptScopeGlobal
	if workspaceID != "" {
		scope = instructioncontext.PromptScopeWorkspace
	}
	store, err := s.store(scope, workspaceID)
	if err != nil {
		return instructioncontext.ScopedPrompt{}, nil, err
	}
	prompt, err := store.Get(name)
	if err != nil {
		return instructioncontext.ScopedPrompt{}, nil, err
	}
	messages, err := instructioncontext.RenderPrompt(prompt.Definition, arguments)
	return prompt, messages, err
}

func (s *PromptService) Definition(workspaceID, name string) (instructioncontext.ScopedPrompt, error) {
	scope := instructioncontext.PromptScopeGlobal
	if workspaceID != "" {
		scope = instructioncontext.PromptScopeWorkspace
	}
	store, err := s.store(scope, workspaceID)
	if err != nil {
		return instructioncontext.ScopedPrompt{}, err
	}
	return store.Get(name)
}

func (s *PromptService) Write(ctx context.Context, request PromptWriteRequest) (instructioncontext.ScopedPrompt, error) {
	if request.Scope == instructioncontext.PromptScopeGlobal && (s == nil || s.AllowGlobalMutation == nil || !s.AllowGlobalMutation(ctx)) {
		return instructioncontext.ScopedPrompt{}, errors.New("global prompt mutation requires operator authorization")
	}
	store, err := s.store(request.Scope, request.WorkspaceID)
	if err != nil {
		return instructioncontext.ScopedPrompt{}, err
	}
	return store.Put(request.Scope, request.Mode, request.Definition)
}

func (s *PromptService) Delete(ctx context.Context, request PromptDeleteRequest) error {
	if request.Scope == instructioncontext.PromptScopeGlobal && (s == nil || s.AllowGlobalMutation == nil || !s.AllowGlobalMutation(ctx)) {
		return errors.New("global prompt mutation requires operator authorization")
	}
	store, err := s.store(request.Scope, request.WorkspaceID)
	if err != nil {
		return err
	}
	if err := store.Delete(request.Scope, request.Name); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("prompt %q not found in %s scope: %w", request.Name, request.Scope, err)
	} else {
		return err
	}
}
