package tools_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/tools"
)

func TestPromptManagementToolRespectsBoundWorkspaceBeforeMutation(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	runtime.SetPromptProvider(application.NewAgentPromptProvider(runtime.Workspaces))
	allowed, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithBoundWorkspace(context.Background(), allowed.ID)
	definition := map[string]any{
		"version": 1, "name": "review",
		"messages": []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "Hello"}}},
	}
	denied, err := runtime.Call(ctx, "create_prompt", map[string]any{"workspace_id": other.ID, "definition": definition})
	if err == nil && !denied.IsError {
		t.Fatalf("cross-workspace prompt mutation allowed: %#v", denied)
	}
	if _, err := instructioncontext.NewPromptStore(other.Path).GetInScope(instructioncontext.PromptScopeWorkspace, "review"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-workspace mutation persisted: %v", err)
	}
	created, err := runtime.Call(ctx, "create_prompt", map[string]any{"workspace_id": allowed.ID, "definition": definition})
	if err != nil || created.IsError {
		t.Fatalf("authorized prompt mutation failed: %#v %v", created, err)
	}
	if _, err := instructioncontext.NewPromptStore(allowed.Path).GetInScope(instructioncontext.PromptScopeWorkspace, "review"); err != nil {
		t.Fatalf("authorized prompt definition not persisted: %v", err)
	}
}
