package page

import (
	"context"
	"errors"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestPromptsPageInheritedDefinitionRequiresLocalOverrideAndScopedDelete(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	registered, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	global := instructioncontext.NewPromptStore("")
	definition := instructioncontext.PromptDefinition{
		Version: 1, Name: "review", Messages: []instructioncontext.PromptMessage{{
			Role: "user", Content: instructioncontext.PromptTextContent{Type: "text", Text: "Global"},
		}},
	}
	if _, err := global.Save(instructioncontext.PromptScopeGlobal, definition); err != nil {
		t.Fatal(err)
	}
	page := NewPrompts(context.Background())
	page.Update(page.Init()())
	if len(page.prompts) != 1 || page.prompts[0].Scope != instructioncontext.PromptScopeGlobal {
		t.Fatalf("global view: %#v", page.prompts)
	}
	page.workspaceID = registered.ID
	page.Update(page.reload()())
	if page.workspaceID != registered.ID || len(page.prompts) != 1 {
		t.Fatalf("workspace view: id=%q expected=%q prompts=%#v", page.workspaceID, registered.ID, page.prompts)
	}
	page.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if _, err := global.GetInScope(instructioncontext.PromptScopeGlobal, "review"); err != nil {
		t.Fatalf("inherited global definition was deleted: %v", err)
	}
	page.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if page.editorMode != "create" {
		t.Fatalf("inherited definition must create a workspace override: %q", page.editorMode)
	}
	_, cmd := page.Update(component.TextAreaSavedMsg{Value: `{"version":1,"name":"review","messages":[{"role":"user","content":{"type":"text","text":"Local"}}]}`})
	page.Update(cmd())
	page.Update(page.reload()())
	if page.prompts[0].Scope != instructioncontext.PromptScopeWorkspace {
		t.Fatalf("override not selected: %#v", page.prompts)
	}
	page.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	_, cmd = page.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	page.Update(cmd())
	page.Update(page.reload()())
	if page.prompts[0].Scope != instructioncontext.PromptScopeGlobal {
		t.Fatalf("global definition not restored after local delete: %#v", page.prompts)
	}
	if _, err := instructioncontext.NewPromptStore(registered.Path).GetInScope(instructioncontext.PromptScopeWorkspace, "review"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local definition still exists: %v", err)
	}
}
