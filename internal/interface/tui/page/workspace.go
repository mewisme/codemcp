package page

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/workspace"
)

type WorkspaceCommand string

const (
	WorkspaceRegister         WorkspaceCommand = "workspace.register"
	WorkspaceRelocate         WorkspaceCommand = "workspace.relocate"
	WorkspaceUnregister       WorkspaceCommand = "workspace.unregister"
	WorkspacePurge            WorkspaceCommand = "workspace.purge"
	WorkspaceAccessAdd        WorkspaceCommand = "workspace.access.add"
	WorkspaceAccessRemove     WorkspaceCommand = "workspace.access.remove"
	WorkspaceContainerCreate  WorkspaceCommand = "workspace.container.create"
	WorkspaceContainerRename  WorkspaceCommand = "workspace.container.rename"
	WorkspaceContainerDelete  WorkspaceCommand = "workspace.container.delete"
	WorkspaceContainerMembers WorkspaceCommand = "workspace.container.members"
)

type WorkspaceCommandMsg struct {
	Command    WorkspaceCommand
	ResourceID string
}

type workspaceTab int

const (
	workspaceTabWorkspaces workspaceTab = iota
	workspaceTabContainers
)

var workspaceTabLabels = []string{"Workspaces", "Containers"}

type workspaceOverlayKind uint8

const (
	workspaceOverlayNone workspaceOverlayKind = iota
	workspaceOverlayConfirm
)

type WorkspacePage struct {
	ctx             context.Context
	manager         *workspace.Manager
	operations      *application.WorkspaceService
	containers      bool
	resourceID      string
	section         string
	action          string
	browser         component.Browser
	detail          component.DetailPage
	overlay         workspaceOverlayKind
	editor          *component.Editor
	confirm         component.ConfirmButtons
	command         WorkspaceCommand
	targetID        string
	value           string
	members         []string
	contextSession  *WorkspaceContextSession
	contextEditor   *component.Editor
	contextData     *workspaceContextFormData
	contextBuild    workspaceContextBuildFunc
	contextBuilding bool
	contextBuildID  uint64
	contextCancel   context.CancelFunc
	contextProgress *component.Progress
	contextPreview  *workspaceContextPreviewState
	notice          string
	err             error
	width           int
	height          int
}

type workspaceCopyIDMsg struct{ ID string }
type workspaceRefreshDetailMsg struct{}

var copyWorkspaceID = component.CopyText
var reloadWorkspaceRuntime = application.ReloadWorkspaces

func NewWorkspaces(ctx context.Context, resourceID string) (*WorkspacePage, error) {
	return NewWorkspacesRoute(ctx, resourceID, "")
}

func NewContainers(ctx context.Context, resourceID string) (*WorkspacePage, error) {
	return NewContainersRoute(ctx, resourceID, "")
}

func NewWorkspacesRoute(ctx context.Context, resourceID, section string) (*WorkspacePage, error) {
	return newWorkspacePage(ctx, false, resourceID, section, "", nil)
}

func NewWorkspacesRouteWithContextSession(ctx context.Context, resourceID, section string, session *WorkspaceContextSession) (*WorkspacePage, error) {
	return newWorkspacePage(ctx, false, resourceID, section, "", session)
}

func NewWorkspacesRouteAction(ctx context.Context, resourceID, section, action string) (*WorkspacePage, error) {
	return newWorkspacePage(ctx, false, resourceID, section, action, nil)
}

func NewWorkspacesRouteWithContextSessionAction(ctx context.Context, resourceID, section, action string, session *WorkspaceContextSession) (*WorkspacePage, error) {
	return newWorkspacePage(ctx, false, resourceID, section, action, session)
}

func NewContainersRoute(ctx context.Context, resourceID, section string) (*WorkspacePage, error) {
	return newWorkspacePage(ctx, true, resourceID, section, "", nil)
}

func NewContainersRouteAction(ctx context.Context, resourceID, section, action string) (*WorkspacePage, error) {
	return newWorkspacePage(ctx, true, resourceID, section, action, nil)
}

func newWorkspacePage(ctx context.Context, containers bool, resourceID, section, action string, session *WorkspaceContextSession) (*WorkspacePage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	manager := workspace.NewManager(workspace.DefaultStorePath())
	page := &WorkspacePage{ctx: ctx, manager: manager, containers: containers, resourceID: strings.TrimSpace(resourceID), section: strings.TrimSpace(section), action: strings.TrimSpace(action), contextSession: session}
	page.workspaceOperations()
	if err := page.reload(); err != nil {
		return nil, err
	}
	if page.action != "" {
		if err := page.initWorkspaceEditor(); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func (page *WorkspacePage) Init() tea.Cmd {
	if page != nil && page.editor != nil {
		return page.editor.Init()
	}
	if page != nil && page.contextEditor != nil && !page.contextBuilding {
		return page.contextEditor.Init()
	}
	return nil
}

func (page *WorkspacePage) Close() {
	if page == nil || !page.contextBuilding {
		return
	}
	page.cancelWorkspaceContextBuild()
}

func (page *WorkspacePage) OverlayActive() bool {
	return page != nil && (page.overlay != workspaceOverlayNone || page.contextBuilding)
}

func (page *WorkspacePage) InputActive() bool {
	return page != nil && (page.editor != nil || page.resourceID == "" && page.browser.InputActive() || page.contextEditor != nil && !page.contextBuilding || !page.containers && page.resourceID != "" && page.section == "context-preview" && page.contextPreview != nil && page.contextPreview.sourceViewer != nil)
}

func (page *WorkspacePage) Dirty() bool {
	if page == nil {
		return false
	}
	if page.editor != nil {
		return page.editor.Dirty()
	}
	return !page.contextBuilding && page.contextEditor != nil && page.contextEditor.Dirty()
}

func (page *WorkspacePage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *WorkspacePage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *WorkspacePage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *WorkspacePage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		if page.editor != nil {
			page.resizeWorkspaceEditor()
			return page, nil
		}
		var cmd tea.Cmd
		if page.resourceID != "" && page.section == "context" {
			if !page.contextBuilding {
				page.resizeWorkspaceContextEditor()
			}
		} else if page.resourceID != "" && page.section == "context-preview" && page.contextPreview != nil {
			cmd = page.resizeWorkspaceContextPreview(msg.Width, msg.Height)
		} else if page.resourceID != "" {
			page.detail.Resize(msg.Width, msg.Height)
		} else {
			cmd = page.resizeBrowser()
		}
		return page, cmd
	case component.EditorSubmitMsg:
		if page.editor != nil {
			return page, page.submitWorkspaceEditor()
		}
		if page.contextEditor != nil && !page.contextBuilding && page.overlay == workspaceOverlayNone {
			return page, page.submitWorkspaceContext()
		}
		return page, nil
	case component.EditorCancelMsg:
		if page.editor != nil {
			return page, page.workspaceEditorParentNavigation()
		}
		if page.contextEditor != nil && !page.contextBuilding && page.overlay == workspaceOverlayNone {
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"workspaces", page.resourceID}} }
		}
		return page, nil
	case component.FormMouseMsg:
		if page.contextEditor != nil && page.overlay == workspaceOverlayNone && !page.contextBuilding {
			updated, cmd := page.contextEditor.Update(msg)
			page.contextEditor = &updated
			return page, cmd
		}
		if page.editor != nil {
			updated, cmd := page.editor.Update(msg)
			page.editor = &updated
			return page, cmd
		}
		return page, nil
	case component.ConfirmChoiceMsg:
		if page.overlay == workspaceOverlayConfirm {
			page.confirm.Select(msg.Affirmative)
			return page, page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case WorkspaceCommandMsg:
		cmd, err := page.openCommand(msg.Command, msg.ResourceID)
		if err != nil {
			page.err = err
		}
		return page, cmd
	case component.BrowserOpenMsg:
		if page.resourceID == "" && msg.Row.ID != "" {
			return page, page.navigateResource(msg.Row.ID)
		}
		return page, nil
	case workspaceCopyIDMsg:
		if err := copyWorkspaceID(msg.ID); err != nil {
			page.err = err
		} else {
			page.err = nil
			page.notice = "Copied " + msg.ID
		}
		return page, nil
	case workspaceRefreshDetailMsg:
		page.err = page.syncDetail()
		if page.err == nil {
			page.notice = "Refreshed"
		}
		return page, nil
	case workspaceContextBuildMsg:
		return page, page.finishWorkspaceContextBuild(msg)
	case tea.KeyPressMsg:
		if page.editor != nil {
			updated, cmd := page.editor.Update(msg)
			page.editor = &updated
			return page, cmd
		}
		if page.resourceID != "" && page.section == "context" && page.overlay == workspaceOverlayNone {
			if page.contextBuilding {
				if msg.String() == "esc" {
					page.cancelWorkspaceContextBuild()
				}
				return page, nil
			}
			if page.contextEditor != nil {
				updated, cmd := page.contextEditor.Update(msg)
				page.contextEditor = &updated
				return page, cmd
			}
		}
		if page.resourceID != "" && page.section == "context-preview" && page.contextPreview != nil && page.overlay == workspaceOverlayNone {
			if cmd, handled := page.handleWorkspaceContextPreviewKey(msg); handled {
				return page, cmd
			}
		}
		if page.overlay == workspaceOverlayConfirm {
			return page, page.updateConfirm(msg)
		}
		if page.resourceID == "" && page.browser.InputActive() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		if page.resourceID == "" {
			if cmd, handled := page.handleTabKey(msg); handled {
				return page, cmd
			}
			if cmd, handled := page.handleListKey(msg); handled {
				return page, cmd
			}
		}
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return page, cmd
	}
	if page.resourceID != "" && page.section == "context" {
		if page.contextBuilding && page.contextProgress != nil {
			updated, cmd := page.contextProgress.Update(message)
			page.contextProgress = &updated
			return page, cmd
		}
		if page.contextEditor != nil {
			updated, cmd := page.contextEditor.Update(message)
			page.contextEditor = &updated
			return page, cmd
		}
	}
	if page.resourceID != "" && page.section == "context-preview" && page.contextPreview != nil {
		return page, page.updateWorkspaceContextPreview(message)
	}
	if page.resourceID != "" {
		updated, cmd := page.detail.Update(message)
		page.detail = updated
		return page, cmd
	}
	updated, cmd := page.browser.Update(message)
	page.browser = updated.(component.Browser)
	return page, cmd
}

func (page *WorkspacePage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Workspace page unavailable", "")
	}
	page.width, page.height = width, height
	if page.editor != nil {
		return page.workspaceEditorView(width, height)
	}
	content := page.baseView(width, height)
	switch page.overlay {
	case workspaceOverlayConfirm:
		modalWidth := overlayWidth(width, 64)
		body := confirmOverlayBody(page.confirm, page.confirmTitle(), page.confirmDescription(), modalWidth)
		content = component.CenterOverlay(content, component.Modal(body, modalWidth), width, height)
	}
	return content
}

func (page *WorkspacePage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	if page.editor != nil {
		return page.workspaceEditorMouseTargets(originX, originY, z)
	}
	switch page.overlay {
	case workspaceOverlayConfirm:
		return confirmOverlayMouseTargets(page.confirm, page.confirmTitle(), page.confirmDescription(), overlayWidth(page.width, 64), page.width, page.height, originX, originY, z+20)
	default:
		if page.resourceID != "" {
			if !page.containers && page.section == "context" {
				if page.contextBuilding || page.contextEditor == nil {
					return nil
				}
				return page.contextEditor.MouseTargets(originX, originY, z)
			}
			if !page.containers && page.section == "context-preview" && page.contextPreview != nil {
				return page.workspaceContextPreviewMouseTargets(originX, originY, z)
			}
			return page.detail.MouseTargets(originX, originY, z)
		}
		feedback := page.listFeedback(page.width)
		tabs := component.PageTabsNotice(workspaceTabLabels, int(page.activeTab()), page.notice, page.width)
		targets := page.workspaceTabMouseTargets(originX, originY, z+2)
		bodyHeight := max(1, page.height-lipgloss.Height(tabs))
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("", "", feedback, page.width, bodyHeight, lipgloss.Height(help))
		browserY := originY + lipgloss.Height(tabs) + layout.BodyY
		targets = append(targets, page.browser.MouseTargets(originX, browserY, z)...)
		helpY := originY + lipgloss.Height(tabs) + bodyHeight - lipgloss.Height(help)
		return append(targets, page.browser.HelpMouseTargets(originX, helpY, z+2)...)
	}
}

func (page *WorkspacePage) activeTab() workspaceTab {
	if page != nil && page.containers {
		return workspaceTabContainers
	}
	return workspaceTabWorkspaces
}

func (page *WorkspacePage) handleTabKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "1":
		return page.switchWorkspaceTab(workspaceTabWorkspaces), true
	case "2":
		return page.switchWorkspaceTab(workspaceTabContainers), true
	}
	delta, ok := component.TabDelta(msg)
	if !ok {
		return nil, false
	}
	next := component.MoveTab(int(page.activeTab()), len(workspaceTabLabels), delta)
	return page.switchWorkspaceTab(workspaceTab(next)), true
}

func (page *WorkspacePage) switchWorkspaceTab(tab workspaceTab) tea.Cmd {
	if page == nil || tab == page.activeTab() {
		return nil
	}
	path := []string{"workspaces"}
	if tab == workspaceTabContainers {
		path = []string{"containers"}
	}
	return func() tea.Msg { return NavigateMsg{Path: path, Replace: true} }
}

func (page *WorkspacePage) workspaceTabMouseTargets(originX, originY, z int) []component.MouseTarget {
	_, spans := component.PageTabsLayout(workspaceTabLabels, int(page.activeTab()), page.notice, page.width)
	targets := make([]component.MouseTarget, 0, len(spans))
	for _, span := range spans {
		tab := workspaceTab(span.Index)
		targets = append(targets, component.MouseTarget{
			ID: "workspace.tab", Rect: component.Rect{X: originX + span.X, Y: originY, Width: span.Width, Height: 1}, Z: z,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				if tab == workspaceTabContainers {
					return tea.KeyPressMsg{Code: '2'}
				}
				return tea.KeyPressMsg{Code: '1'}
			},
		})
	}
	return targets
}

func (page *WorkspacePage) handleListKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "a":
		command := WorkspaceRegister
		if page.containers {
			command = WorkspaceContainerCreate
		}
		cmd, err := page.openCommand(command, "")
		if err != nil {
			page.err = err
		}
		return cmd, true
	}
	return nil, false
}

func (page *WorkspacePage) openCommand(command WorkspaceCommand, resourceID string) (tea.Cmd, error) {
	page.err, page.notice = nil, ""
	page.command, page.targetID, page.value, page.members = command, strings.TrimSpace(resourceID), "", nil
	switch command {
	case WorkspaceRegister, WorkspaceRelocate, WorkspaceAccessAdd, WorkspaceAccessRemove, WorkspaceContainerCreate, WorkspaceContainerRename, WorkspaceContainerMembers:
		return page.workspaceEditorNavigation(command, page.targetID), nil
	case WorkspaceUnregister, WorkspacePurge, WorkspaceContainerDelete:
		if command == WorkspaceUnregister || command == WorkspacePurge {
			if _, err := page.manager.Get(page.targetID); err != nil {
				return nil, err
			}
		} else if _, err := page.manager.GetContainer(page.targetID); err != nil {
			return nil, err
		}
		affirmative := "Delete"
		if command == WorkspaceUnregister {
			affirmative = "Unregister"
		} else if command == WorkspacePurge {
			affirmative = "Purge"
		}
		page.confirm = component.NewConfirmButtons(affirmative, "Cancel", false)
		page.overlay = workspaceOverlayConfirm
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported workspace action: %s", command)
	}
}

func (page *WorkspacePage) updateConfirm(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		page.closeOverlay()
		return nil
	}
	if msg.String() == "enter" {
		if !page.confirm.AffirmativeSelected() {
			page.closeOverlay()
			return nil
		}
		var err error
		if page.command == WorkspaceUnregister {
			_, err = page.workspaceOperations().Unregister(page.ctx, page.targetID)
		} else if page.command == WorkspacePurge {
			_, err = page.workspaceOperations().Purge(page.ctx, page.targetID, true)
		} else {
			_, err = page.workspaceOperations().DeleteContainer(page.ctx, page.targetID)
		}
		if err != nil {
			page.err = err
			return nil
		}
		page.notice = workspaceSuccess(page.command)
		deletedID := page.targetID
		page.closeOverlay()
		if deletedID != "" {
			path := []string{"workspaces"}
			if page.containers {
				path = []string{"containers"}
			}
			return func() tea.Msg { return NavigateMsg{Path: path, Replace: true} }
		}
		return nil
	}
	return page.confirm.Update(msg)
}

func (page *WorkspacePage) updateMembers() error {
	container, err := page.manager.GetContainer(page.targetID)
	if err != nil {
		return err
	}
	before, after := stringSet(container.WorkspaceIDs), stringSet(page.members)
	add, remove := []string{}, []string{}
	for id := range after {
		if !before[id] {
			add = append(add, id)
		}
	}
	for id := range before {
		if !after[id] {
			remove = append(remove, id)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	if len(add) > 0 {
		if _, err := page.workspaceOperations().AddWorkspacesToContainer(page.ctx, page.targetID, add); err != nil {
			return err
		}
	}
	if len(remove) > 0 {
		if _, err := page.workspaceOperations().RemoveWorkspacesFromContainer(page.ctx, page.targetID, remove); err != nil {
			return err
		}
	}
	return nil
}

func (page *WorkspacePage) workspaceOperations() *application.WorkspaceService {
	if page == nil {
		return application.NewWorkspaceService(nil, nil)
	}
	if page.operations != nil {
		return page.operations
	}
	if page.manager == nil {
		page.manager = workspace.NewManager(workspace.DefaultStorePath())
	}
	page.operations = application.NewWorkspaceService(page.manager, func(reloadCtx context.Context) error {
		if reloadCtx == nil {
			reloadCtx = context.Background()
		}
		reloadCtx, cancel := context.WithTimeout(reloadCtx, 5*time.Second)
		defer cancel()
		_, _, err := reloadWorkspaceRuntime(reloadCtx)
		return err
	})
	return page.operations
}

func (page *WorkspacePage) reload() error {
	if page.resourceID != "" {
		return page.syncDetail()
	}
	helpExpanded := page.browser.HelpExpanded()
	selected, _ := page.browser.Selected()
	rows, err := page.listRows()
	if err != nil {
		return err
	}
	refresh := func(context.Context) ([]component.Row, error) { return page.listRows() }
	page.browser = component.NewBrowser(page.ctx, page.listTitle(), rows, refresh).WithTitleVisible(false).WithExternalHelp(true)
	page.browser.SetHelpExpanded(helpExpanded)
	if page.containers {
		page.browser.SetHelpBindings(component.Binding([]string{"h", "l", "left", "right"}, "←/→", "tabs"), component.Binding([]string{"a"}, "a", "create"))
	} else {
		page.browser.SetHelpBindings(component.Binding([]string{"h", "l", "left", "right"}, "←/→", "tabs"), component.Binding([]string{"a"}, "a", "register"))
	}
	if selected.ID != "" {
		page.browser.SelectID(selected.ID)
	}
	if page.width > 0 && page.height > 0 {
		page.resizeBrowser()
	}
	return nil
}

func (page *WorkspacePage) workspaceRows() ([]component.Row, error) {
	items, err := page.manager.List()
	if err != nil {
		return nil, err
	}
	rows := make([]component.Row, 0, len(items))
	for _, item := range items {
		rows = append(rows, component.Row{ID: item.ID, Title: item.ID, Description: item.Path, Meta: fmt.Sprintf("%d extra roots", len(item.AllowDirs)), Search: strings.Join(append(append([]string{item.Path}, item.AllowDirs...), item.LegacyIDs...), " ")})
	}
	return rows, nil
}

func (page *WorkspacePage) containerRows() ([]component.Row, error) {
	items, err := page.manager.ListContainers()
	if err != nil {
		return nil, err
	}
	rows := make([]component.Row, 0, len(items))
	for _, item := range items {
		rows = append(rows, component.Row{ID: item.ID, Title: item.Name, Description: item.ID, Meta: fmt.Sprintf("%d workspaces", len(item.WorkspaceIDs)), Search: strings.Join(item.WorkspaceIDs, " ")})
	}
	return rows, nil
}

func (page *WorkspacePage) listRows() ([]component.Row, error) {
	if page.containers {
		return page.containerRows()
	}
	return page.workspaceRows()
}

func (page *WorkspacePage) listTitle() string {
	if page.containers {
		return "Containers"
	}
	return "Workspaces"
}

func (page *WorkspacePage) listFeedback(width int) string {
	if page.err == nil {
		return ""
	}
	return component.BannerWidth(page.err.Error(), component.ToneDanger, width)
}

func (page *WorkspacePage) baseView(width, height int) string {
	if page.resourceID != "" {
		if !page.containers && page.section == "context" {
			return page.workspaceContextView(width, height)
		}
		if !page.containers && page.section == "context-preview" && page.contextPreview != nil {
			return page.workspaceContextPreviewView(width, height)
		}
		page.detail.SetFeedback(page.notice, page.err)
		page.detail.Resize(width, height)
		return page.detail.View()
	}
	feedback := page.listFeedback(width)
	tabs := component.PageTabsNotice(workspaceTabLabels, int(page.activeTab()), page.notice, width)
	bodyHeight := max(1, height-lipgloss.Height(tabs))
	help := page.browser.HelpView()
	layout := component.NewSectionLayout("", "", feedback, width, bodyHeight, lipgloss.Height(help))
	updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: layout.BodyHeight})
	page.browser = updated.(component.Browser)
	return tabs + "\n" + component.BottomHelp(layout.View(page.browser.BodyContent()), help, width, bodyHeight)
}

func (page *WorkspacePage) resizeBrowser() tea.Cmd {
	if page.resourceID != "" || page.width <= 0 || page.height <= 0 {
		return nil
	}
	tabs := component.PageTabsNotice(workspaceTabLabels, int(page.activeTab()), page.notice, page.width)
	bodyHeight := max(1, page.height-lipgloss.Height(tabs))
	help := page.browser.HelpView()
	layout := component.NewSectionLayout("", "", page.listFeedback(page.width), page.width, bodyHeight, lipgloss.Height(help))
	updated, cmd := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: layout.BodyHeight})
	page.browser = updated.(component.Browser)
	return cmd
}

func (page *WorkspacePage) navigateResource(id string) tea.Cmd {
	path := []string{"workspaces", id}
	if page.containers {
		path = []string{"containers", id}
	}
	return func() tea.Msg { return NavigateMsg{Path: path} }
}

func (page *WorkspacePage) syncDetail() error {
	if page.resourceID == "" {
		return nil
	}
	if page.containers {
		return page.syncContainerDetail()
	}
	return page.syncWorkspaceDetail()
}

func (page *WorkspacePage) syncWorkspaceDetail() error {
	item, err := page.manager.Get(page.resourceID)
	if err != nil {
		return err
	}
	content := ""
	switch page.section {
	case "":
		content = detailFields([2]string{"Root", item.Path}, [2]string{"Legacy IDs", joinedOrNone(item.LegacyIDs)})
	case "context":
		page.initWorkspaceContext()
		return nil
	case "context-preview":
		page.initWorkspaceContext()
		page.syncWorkspaceContextPreview()
		return nil
	case "access":
		content = detailList(item.AllowDirs)
	case "containers":
		containers, err := page.manager.ContainersForWorkspace(item.ID)
		if err != nil {
			return err
		}
		values := make([]string, 0, len(containers))
		for _, container := range containers {
			values = append(values, container.Name+" · "+container.ID)
		}
		content = detailList(values)
	default:
		return fmt.Errorf("unsupported workspace child section: %s", page.section)
	}
	detailTitle := "Overview"
	if page.section == "access" {
		detailTitle = "Access"
	} else if page.section == "containers" {
		detailTitle = "Containers"
	}
	page.detail = component.NewDetailPage(detailTitle, fmt.Sprintf("%d extra roots", len(item.AllowDirs)), content).WithTitleVisible(false)
	bindings := []component.DetailPageBinding{}
	if page.section == "" {
		bindings = append(bindings,
			component.DetailPageBinding{Key: "p", Desc: "context", Message: NavigateMsg{Path: []string{"workspaces", item.ID, "context"}}},
			component.DetailPageBinding{Key: "a", Desc: "access", Message: NavigateMsg{Path: []string{"workspaces", item.ID, "access"}}},
			component.DetailPageBinding{Key: "v", Desc: "containers", Message: NavigateMsg{Path: []string{"workspaces", item.ID, "containers"}}},
		)
	}
	bindings = append(bindings,
		component.DetailPageBinding{Key: "c", Desc: "copy ID", Message: workspaceCopyIDMsg{ID: item.ID}},
		component.DetailPageBinding{Key: "m", Desc: "relocate", Message: WorkspaceCommandMsg{Command: WorkspaceRelocate, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "+", Desc: "add access", Message: WorkspaceCommandMsg{Command: WorkspaceAccessAdd, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "-", Desc: "remove access", Message: WorkspaceCommandMsg{Command: WorkspaceAccessRemove, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "d", Desc: "unregister", Message: WorkspaceCommandMsg{Command: WorkspaceUnregister, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "x", Desc: "purge", Message: WorkspaceCommandMsg{Command: WorkspacePurge, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "r", Desc: "refresh", Message: workspaceRefreshDetailMsg{}},
	)
	page.detail.SetBindings(bindings...)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
	return nil
}

func (page *WorkspacePage) syncContainerDetail() error {
	item, err := page.manager.GetContainer(page.resourceID)
	if err != nil {
		return err
	}
	workspaces, err := page.manager.WorkspacesForContainer(item.ID)
	if err != nil {
		return err
	}
	content := ""
	switch page.section {
	case "":
		content = detailFields([2]string{"ID", item.ID}, [2]string{"Name", item.Name})
		if len(workspaces) > 0 {
			content += "\n\n" + component.Label("Members") + "\n" + detailList(containerWorkspaceDetails(workspaces))
		}
	case "workspaces":
		values := make([]string, 0, len(workspaces))
		for _, workspaceItem := range workspaces {
			values = append(values, workspaceItem.ID+" · "+workspaceItem.Path)
		}
		content = detailList(values)
	default:
		return fmt.Errorf("unsupported container child section: %s", page.section)
	}
	detailTitle := item.Name
	if page.section == "workspaces" {
		detailTitle = "Workspaces"
	}
	page.detail = component.NewDetailPage(detailTitle, fmt.Sprintf("%d workspaces", len(item.WorkspaceIDs)), content).WithTitleVisible(false)
	bindings := []component.DetailPageBinding{}
	if page.section == "" {
		bindings = append(bindings, component.DetailPageBinding{Key: "w", Desc: "workspaces", Message: NavigateMsg{Path: []string{"containers", item.ID, "workspaces"}}})
	}
	bindings = append(bindings,
		component.DetailPageBinding{Key: "c", Desc: "copy ID", Message: workspaceCopyIDMsg{ID: item.ID}},
		component.DetailPageBinding{Key: "e", Desc: "rename", Message: WorkspaceCommandMsg{Command: WorkspaceContainerRename, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "m", Desc: "members", Message: WorkspaceCommandMsg{Command: WorkspaceContainerMembers, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "d", Desc: "delete", Message: WorkspaceCommandMsg{Command: WorkspaceContainerDelete, ResourceID: item.ID}},
		component.DetailPageBinding{Key: "r", Desc: "refresh", Message: workspaceRefreshDetailMsg{}},
	)
	page.detail.SetBindings(bindings...)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
	return nil
}

func (page *WorkspacePage) closeOverlay() {
	page.overlay = workspaceOverlayNone
	page.confirm = component.ConfirmButtons{}
	page.command, page.targetID = "", ""
	page.value, page.members = "", nil
}

func workspaceMemberPickerHeight(count, pageHeight int) int {
	limit := 14
	if pageHeight > 0 {
		limit = max(6, min(16, pageHeight-8))
	}
	return max(6, min(limit, count+3))
}

func workspaceMemberLabel(item workspace.Workspace) string {
	name := strings.TrimSpace(filepath.Base(filepath.Clean(item.Path)))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return item.ID
	}
	return name + " · " + item.ID
}

func (page *WorkspacePage) confirmTitle() string {
	if page.command == WorkspaceUnregister {
		return "Unregister workspace " + page.targetID + "?"
	} else if page.command == WorkspacePurge {
		return "Purge workspace local state " + page.targetID + "?"
	}
	return "Delete container " + page.targetID + "?"
}

func (page *WorkspacePage) confirmDescription() string {
	if page.command == WorkspaceUnregister {
		return "The workspace handle will be removed. Project files and local .cm state are unchanged."
	} else if page.command == WorkspacePurge {
		return "This deletes local .cm state after confirmation. Project files are unchanged."
	}
	return "The container record will be removed. Registered workspaces and project files are unchanged."
}

func workspaceSuccess(command WorkspaceCommand) string {
	switch command {
	case WorkspaceRegister:
		return "Workspace registered"
	case WorkspaceRelocate:
		return "Workspace relocated"
	case WorkspaceUnregister:
		return "Workspace unregistered"
	case WorkspacePurge:
		return "Workspace local state deleted"
	case WorkspaceAccessAdd:
		return "Access directory added"
	case WorkspaceAccessRemove:
		return "Access directory removed"
	case WorkspaceContainerCreate:
		return "Container created"
	case WorkspaceContainerRename:
		return "Container renamed"
	case WorkspaceContainerDelete:
		return "Container deleted"
	case WorkspaceContainerMembers:
		return "Container membership updated"
	default:
		return "Workspace updated"
	}
}

func requiredValue(label string) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", label)
		}
		return nil
	}
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = true
		}
	}
	return result
}

func containerWorkspaceDetails(items []workspace.Workspace) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, workspaceMemberLabel(item)+" · "+item.Path)
	}
	return values
}

func detailFields(fields ...[2]string) string {
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		value := strings.TrimSpace(field[1])
		if value == "" {
			value = "None"
		}
		lines = append(lines, component.Label(field[0])+"  "+value)
	}
	return strings.Join(lines, "\n")
}

func detailList(values []string) string {
	if len(values) == 0 {
		return component.Muted("None")
	}
	return strings.Join(values, "\n")
}

func joinedOrNone(values []string) string {
	if len(values) == 0 {
		return "None"
	}
	return strings.Join(values, ", ")
}
