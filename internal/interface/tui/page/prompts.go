package page

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/workspace"
)

type promptsLoadedMsg struct {
	workspaces []workspace.Workspace
	prompts    []instructioncontext.ScopedPrompt
	err        error
}

type promptSavedMsg struct {
	err     error
	message string
}

// Prompts uses the same application service and scope rules as CLI and Admin.
type Prompts struct {
	ctx           context.Context
	service       *application.PromptService
	workspaces    []workspace.Workspace
	workspaceID   string
	prompts       []instructioncontext.ScopedPrompt
	selected      int
	editor        *component.TextAreaEditor
	editorMode    string
	targetName    string
	pendingDelete string
	notice        string
	submitting    bool
	width         int
	height        int
}

type PromptCommand string

const (
	PromptCreate PromptCommand = "prompt.create"
	PromptUpdate PromptCommand = "prompt.update"
	PromptDelete PromptCommand = "prompt.delete"
)

type PromptCommandMsg struct{ Command PromptCommand }

func NewPrompts(ctx context.Context) *Prompts {
	if ctx == nil {
		ctx = context.Background()
	}
	manager := workspace.NewManager(workspace.DefaultStorePath())
	return &Prompts{
		ctx: ctx,
		service: &application.PromptService{
			Workspaces:          manager,
			AllowGlobalMutation: func(context.Context) bool { return true },
		},
	}
}

func (p *Prompts) Init() tea.Cmd { return p.reload() }

func (p *Prompts) reload() tea.Cmd {
	return func() tea.Msg {
		workspaces, err := p.service.Workspaces.List()
		if err != nil {
			return promptsLoadedMsg{err: err}
		}
		prompts, err := p.service.List(p.workspaceID)
		return promptsLoadedMsg{workspaces: workspaces, prompts: prompts, err: err}
	}
}

func (p *Prompts) Update(message tea.Msg) (Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
		if p.editor != nil {
			p.editor.Resize(msg.Width, msg.Height)
		}
		return p, nil
	case promptsLoadedMsg:
		if msg.err != nil {
			p.notice = msg.err.Error()
			return p, nil
		}
		p.workspaces, p.prompts = msg.workspaces, msg.prompts
		if p.selected >= len(p.prompts) {
			p.selected = max(0, len(p.prompts)-1)
		}
		return p, nil
	case promptSavedMsg:
		p.submitting = false
		if msg.err != nil {
			p.notice = msg.err.Error()
			return p, nil
		}
		p.editor = nil
		p.pendingDelete = ""
		p.notice = msg.message
		return p, p.reload()
	case component.TextAreaSavedMsg:
		if p.editor == nil {
			return p, nil
		}
		if p.submitting {
			return p, nil
		}
		decoder := json.NewDecoder(bytes.NewBufferString(msg.Value))
		decoder.DisallowUnknownFields()
		var definition instructioncontext.PromptDefinition
		if err := decoder.Decode(&definition); err != nil {
			p.notice = err.Error()
			return p, nil
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			p.notice = "Editor requires one JSON definition"
			return p, nil
		}
		if p.editorMode == "update" && definition.Name != p.targetName {
			p.notice = "Prompt name cannot change during update"
			return p, nil
		}
		scope := instructioncontext.PromptScopeGlobal
		if p.workspaceID != "" {
			scope = instructioncontext.PromptScopeWorkspace
		}
		request := application.PromptWriteRequest{Scope: scope, WorkspaceID: p.workspaceID, Mode: p.editorMode, Definition: definition}
		p.submitting = true
		return p, func() tea.Msg {
			_, err := p.service.Write(p.ctx, request)
			return promptSavedMsg{err: err, message: "Prompt saved"}
		}
	case component.TextAreaCancelledMsg:
		if p.submitting {
			return p, nil
		}
		p.editor = nil
		p.notice = ""
		return p, nil
	}
	if p.editor != nil {
		if p.submitting {
			return p, nil
		}
		updated, command := p.editor.Update(message)
		p.editor = &updated
		return p, command
	}
	if p.submitting {
		return p, nil
	}
	if command, ok := message.(PromptCommandMsg); ok {
		key := map[PromptCommand]string{PromptCreate: "n", PromptUpdate: "e", PromptDelete: "d"}[command.Command]
		if key == "" {
			return p, nil
		}
		return p.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	}
	msg, ok := message.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch msg.String() {
	case "up", "k":
		if p.selected > 0 {
			p.selected--
		}
		p.pendingDelete = ""
	case "down", "j":
		if p.selected+1 < len(p.prompts) {
			p.selected++
		}
		p.pendingDelete = ""
	case "w":
		next := ""
		if p.workspaceID == "" && len(p.workspaces) > 0 {
			next = p.workspaces[0].ID
		} else {
			for i, item := range p.workspaces {
				if item.ID == p.workspaceID && i+1 < len(p.workspaces) {
					next = p.workspaces[i+1].ID
					break
				}
			}
		}
		p.workspaceID, p.selected, p.pendingDelete = next, 0, ""
		p.notice = ""
		return p, p.reload()
	case "r":
		p.notice = ""
		return p, p.reload()
	case "n":
		p.editorMode, p.targetName = "create", ""
		p.openEditor("Create Prompt", "{\n  \"version\": 1,\n  \"name\": \"\",\n  \"description\": \"\",\n  \"messages\": [{\"role\": \"user\", \"content\": {\"type\": \"text\", \"text\": \"\"}}]\n}")
		return p, p.editor.Init()
	case "e":
		if len(p.prompts) == 0 {
			break
		}
		p.pendingDelete = ""
		selected := p.prompts[p.selected]
		p.targetName = selected.Definition.Name
		p.editorMode = "update"
		if p.workspaceID != "" && selected.Scope == instructioncontext.PromptScopeGlobal {
			p.editorMode = "create"
		}
		data, _ := json.MarshalIndent(selected.Definition, "", "  ")
		p.openEditor("Edit Prompt · "+p.targetName, string(data))
		return p, p.editor.Init()
	case "d":
		if len(p.prompts) == 0 {
			break
		}
		selected := p.prompts[p.selected]
		scope := instructioncontext.PromptScopeGlobal
		if p.workspaceID != "" {
			scope = instructioncontext.PromptScopeWorkspace
			if selected.Scope != scope {
				p.notice = "Select Global scope to delete this inherited Prompt"
				break
			}
		}
		name := selected.Definition.Name
		if p.pendingDelete != name {
			p.pendingDelete = name
			p.notice = "Press d again to delete " + name
			break
		}
		p.pendingDelete = ""
		p.submitting = true
		return p, func() tea.Msg {
			err := p.service.Delete(p.ctx, application.PromptDeleteRequest{Scope: scope, WorkspaceID: p.workspaceID, Name: name})
			return promptSavedMsg{err: err, message: "Prompt deleted"}
		}
	default:
		p.pendingDelete = ""
	}
	return p, nil
}

func (p *Prompts) openEditor(title, value string) {
	editor := component.NewTextAreaEditor(title, value)
	editor.Resize(p.width, p.height)
	p.editor = &editor
	p.notice = ""
}

func (p *Prompts) View(width, height int) string {
	if p.editor != nil {
		p.editor.Resize(width, height)
		return prependPageFeedback(p.notice, p.editor.View())
	}
	scope := "Global"
	if p.workspaceID != "" {
		scope = "Workspace " + p.workspaceID
	}
	lines := []string{"Prompts · " + scope, "", "w: scope   j/k: select   n: new   e: edit   d d: delete   r: refresh", ""}
	if len(p.prompts) == 0 {
		lines = append(lines, "No Prompts in this scope")
	}
	for index, value := range p.prompts {
		prefix := "  "
		if index == p.selected {
			prefix = "› "
		}
		lines = append(lines, fmt.Sprintf("%s%s [%s] %s", prefix, value.Definition.Name, value.Scope, value.Definition.Description))
	}
	return prependPageFeedback(p.notice, strings.Join(lines, "\n"))
}

func (p *Prompts) OverlayActive() bool { return p.editor != nil }
func (p *Prompts) InputActive() bool   { return p.editor != nil }
func (p *Prompts) Dirty() bool         { return p.editor != nil && p.editor.Dirty() }
func (p *Prompts) Submitting() bool    { return p.submitting }
