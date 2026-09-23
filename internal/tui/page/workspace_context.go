package page

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/tui/component"
)

type WorkspaceContextSession struct {
	Options    projectcontext.Options
	Result     *projectcontext.Result
	PreviewTab string
}

type workspaceContextFormData struct {
	Path                string
	MemoryQuery         string
	MaxMemoryEntries    string
	MaxMemoryBytes      string
	MaxInstructionBytes string
	MaxSectionBytes     string
	MaxLinesPerSection  string
	IncludeGit          bool
	IncludeMemory       bool
	IncludeSkills       bool
}

type workspaceContextBuildMsg struct {
	ID     uint64
	Result projectcontext.Result
	Err    error
}

type workspaceContextBuildFunc func(context.Context, string, projectcontext.Options) (projectcontext.Result, error)

func NewWorkspaceContextSession() *WorkspaceContextSession {
	return &WorkspaceContextSession{Options: defaultWorkspaceContextOptions(), PreviewTab: "rendered"}
}

func defaultWorkspaceContextOptions() projectcontext.Options {
	return projectcontext.DefaultOptions()
}

func newWorkspaceContextEditor(options projectcontext.Options) (component.Editor, *workspaceContextFormData) {
	data := &workspaceContextFormData{
		Path: strings.TrimSpace(options.Path), MemoryQuery: strings.TrimSpace(options.MemoryQuery), MaxMemoryEntries: strconv.Itoa(options.MaxMemoryEntries), MaxMemoryBytes: strconv.Itoa(options.MaxMemoryBytes),
		MaxInstructionBytes: strconv.Itoa(options.MaxInstructionBytes), MaxSectionBytes: strconv.Itoa(options.MaxSectionBytes), MaxLinesPerSection: strconv.Itoa(options.MaxLinesPerSection),
		IncludeGit: options.IncludeGit, IncludeMemory: options.IncludeMemory, IncludeSkills: options.IncludeSkills,
	}
	editor := component.NewEditor("build",
		component.EditorSection{ID: "scope", Title: "Scope", Description: "Choose the workspace-relative path and optional memory relevance query.", Form: component.NewEditorForm(component.Group(
			component.Input("Path (optional, inside workspace root)", &data.Path),
			component.Input("Memory query (optional relevance query)", &data.MemoryQuery),
		))},
		component.EditorSection{ID: "budgets", Title: "Budgets", Description: "Limit memory and instruction context size for this build.", Form: component.NewEditorForm(component.Group(
			workspaceContextIntInput("Max memory entries", &data.MaxMemoryEntries, projectcontext.MinMemoryEntries, projectcontext.MaxMemoryEntries),
			workspaceContextIntInput("Max memory bytes", &data.MaxMemoryBytes, projectcontext.MinMemoryBytes, projectcontext.MaxMemoryBytes),
			workspaceContextIntInput("Max instruction bytes", &data.MaxInstructionBytes, projectcontext.MinInstructionBytes, projectcontext.MaxInstructionBytes),
			workspaceContextIntInput("Max section bytes", &data.MaxSectionBytes, projectcontext.MinSectionBytes, projectcontext.MaxSectionBytes),
			workspaceContextIntInput("Max lines per section", &data.MaxLinesPerSection, projectcontext.MinLinesPerSection, projectcontext.MaxLinesPerSection),
		))},
		component.EditorSection{ID: "include", Title: "Include", Description: "Choose optional context sources for this build.", Form: component.NewEditorForm(component.Group(
			component.Switch("Git", &data.IncludeGit, "INCLUDE", "EXCLUDE"), component.Switch("Memory", &data.IncludeMemory, "INCLUDE", "EXCLUDE"), component.Switch("Skills", &data.IncludeSkills, "INCLUDE", "EXCLUDE"),
		))},
	)
	return editor, data
}

func workspaceContextIntInput(title string, value *string, minValue, maxValue int) huh.Field {
	return component.Input(title, value).Validate(func(raw string) error {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || parsed < minValue || parsed > maxValue {
			return fmt.Errorf("%s must be between %d and %d", strings.ToLower(title), minValue, maxValue)
		}
		return nil
	})
}

func (data *workspaceContextFormData) Options() (projectcontext.Options, error) {
	if data == nil {
		return projectcontext.Options{}, fmt.Errorf("project context form is unavailable")
	}
	parse := func(label, value string, minValue, maxValue int) (int, error) {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < minValue || parsed > maxValue {
			return 0, fmt.Errorf("%s must be between %d and %d", label, minValue, maxValue)
		}
		return parsed, nil
	}
	maxMemoryEntries, err := parse("max memory entries", data.MaxMemoryEntries, projectcontext.MinMemoryEntries, projectcontext.MaxMemoryEntries)
	if err != nil {
		return projectcontext.Options{}, err
	}
	maxMemoryBytes, err := parse("max memory bytes", data.MaxMemoryBytes, projectcontext.MinMemoryBytes, projectcontext.MaxMemoryBytes)
	if err != nil {
		return projectcontext.Options{}, err
	}
	maxInstructionBytes, err := parse("max instruction bytes", data.MaxInstructionBytes, projectcontext.MinInstructionBytes, projectcontext.MaxInstructionBytes)
	if err != nil {
		return projectcontext.Options{}, err
	}
	maxSectionBytes, err := parse("max section bytes", data.MaxSectionBytes, projectcontext.MinSectionBytes, projectcontext.MaxSectionBytes)
	if err != nil {
		return projectcontext.Options{}, err
	}
	maxLinesPerSection, err := parse("max lines per section", data.MaxLinesPerSection, projectcontext.MinLinesPerSection, projectcontext.MaxLinesPerSection)
	if err != nil {
		return projectcontext.Options{}, err
	}
	return projectcontext.Options{
		Path: strings.TrimSpace(data.Path), MemoryQuery: strings.TrimSpace(data.MemoryQuery), MaxMemoryEntries: maxMemoryEntries, MaxMemoryBytes: maxMemoryBytes,
		MaxInstructionBytes: maxInstructionBytes, MaxSectionBytes: maxSectionBytes, MaxLinesPerSection: maxLinesPerSection,
		IncludeGit: data.IncludeGit, IncludeMemory: data.IncludeMemory, IncludeSkills: data.IncludeSkills,
	}, nil
}

func (page *WorkspacePage) initWorkspaceContext() {
	if page.contextSession == nil {
		page.contextSession = NewWorkspaceContextSession()
	}
	if page.contextSession.Options.MaxInstructionBytes <= 0 {
		page.contextSession.Options = defaultWorkspaceContextOptions()
	}
	editor, data := newWorkspaceContextEditor(page.contextSession.Options)
	page.contextEditor, page.contextData = &editor, data
	if page.contextBuild == nil {
		manager := page.manager
		page.contextBuild = func(ctx context.Context, workspaceID string, options projectcontext.Options) (projectcontext.Result, error) {
			profile := application.ProjectContextToolProfile(ctx)
			service := projectcontext.New(manager, func() instructioncontext.ToolProfile { return profile })
			service.Environment = application.ProjectContextEnvironment
			return service.Build(ctx, workspaceID, options)
		}
	}
}

func (page *WorkspacePage) submitWorkspaceContext() tea.Cmd {
	if page.contextEditor == nil || page.contextBuilding {
		return nil
	}
	if err := page.contextEditor.Validate(); err != nil {
		page.contextEditor.SetFeedback("", err)
		return nil
	}
	options, err := page.contextData.Options()
	if err != nil {
		page.contextEditor.SetFeedback("", err)
		return nil
	}
	page.contextBuildID++
	id := page.contextBuildID
	ctx, cancel := context.WithCancel(page.ctx)
	page.contextCancel = cancel
	page.contextBuilding = true
	page.err, page.notice = nil, ""
	page.contextEditor.SetFeedback("", nil)
	progress := component.NewProgress("Building project context")
	page.contextProgress = &progress
	build, workspaceID := page.contextBuild, page.resourceID
	return tea.Batch(progress.Init(), func() tea.Msg {
		result, err := build(ctx, workspaceID, options)
		return workspaceContextBuildMsg{ID: id, Result: result, Err: err}
	})
}

func (page *WorkspacePage) finishWorkspaceContextBuild(msg workspaceContextBuildMsg) tea.Cmd {
	if msg.ID != page.contextBuildID {
		return nil
	}
	if page.contextCancel != nil {
		page.contextCancel()
	}
	page.contextCancel = nil
	page.contextBuilding = false
	if msg.Err != nil {
		page.err = nil
		page.contextProgress = nil
		if page.contextEditor != nil {
			page.contextEditor.SetFeedback("", msg.Err)
		}
		return nil
	}
	options, err := page.contextData.Options()
	if err != nil {
		page.err = nil
		page.contextProgress = nil
		if page.contextEditor != nil {
			page.contextEditor.SetFeedback("", err)
		}
		return nil
	}
	page.contextSession.Options = options
	editor, data := newWorkspaceContextEditor(options)
	page.contextEditor, page.contextData = &editor, data
	result := msg.Result
	page.contextSession.Result = &result
	page.contextProgress = nil
	page.notice = "Project Context built"
	return func() tea.Msg { return NavigateMsg{Path: []string{"workspaces", page.resourceID, "context-preview"}} }
}

func (page *WorkspacePage) cancelWorkspaceContextBuild() {
	if page.contextCancel != nil {
		page.contextCancel()
	}
	page.contextCancel = nil
	page.contextBuildID++
	page.contextBuilding = false
	page.contextProgress = nil
	page.notice = "Project Context build cancelled"
}

func (page *WorkspacePage) resizeWorkspaceContextEditor() {
	if page == nil || page.contextEditor == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	page.contextEditor.Resize(page.width, page.height)
}

func (page *WorkspacePage) workspaceContextView(width, height int) string {
	if page.contextBuilding && page.contextProgress != nil {
		return component.CenterLayout(page.contextProgress.View()+"\n\n"+component.Muted("Esc cancel"), width, height)
	}
	if page.contextEditor == nil {
		return component.StateView(component.PageError, "Project Context editor unavailable", "")
	}
	page.contextEditor.Resize(width, height)
	return page.contextEditor.View()
}
