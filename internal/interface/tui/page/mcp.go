package page

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/upstream"
)

type UpstreamCommand string

const (
	UpstreamServerAdd       UpstreamCommand = "upstream.server.add"
	UpstreamServerConfigure UpstreamCommand = "upstream.server.configure"
	UpstreamServerRemove    UpstreamCommand = "upstream.server.remove"
	UpstreamServerEnable    UpstreamCommand = "upstream.server.enable"
	UpstreamServerDisable   UpstreamCommand = "upstream.server.disable"
	UpstreamServerHealth    UpstreamCommand = "upstream.server.status"
	UpstreamServerTools     UpstreamCommand = "upstream.server.tools"
	UpstreamAuthLogin       UpstreamCommand = "upstream.server.auth.login"
	UpstreamAuthLogout      UpstreamCommand = "upstream.server.auth.logout"
)

type UpstreamCommandMsg struct {
	Command    UpstreamCommand
	ResourceID string
}

type mcpOverlayKind uint8

const (
	mcpOverlayNone mcpOverlayKind = iota
	mcpOverlayConfirm
	mcpOverlayOperation
)

type mcpServerEditorMode int

const (
	mcpServerEditorForm mcpServerEditorMode = iota
	mcpServerEditorJSON
)

type mcpServerEditorModeMsg struct{ Mode mcpServerEditorMode }

type mcpHealthMsg struct {
	id       string
	status   upstream.Status
	statuses []upstream.Status
}

type mcpToolsMsg struct {
	id    string
	tools []upstream.Tool
	err   error
}

type mcpOAuthURLMsg struct {
	id  string
	url string
}

type mcpOAuthBrowserErrorMsg struct{ err error }

type mcpOAuthDoneMsg struct {
	id         string
	credential mcpoauth.Credential
	err        error
}

type MCPPage struct {
	ctx                context.Context
	manager            *upstream.Manager
	oauthStore         *mcpoauth.Store
	resourceID         string
	section            string
	action             string
	browser            component.Browser
	detail             component.DetailPage
	overlay            mcpOverlayKind
	editor             *component.Editor
	jsonEditor         *component.TextAreaEditor
	serverEditorMode   mcpServerEditorMode
	initialServerDraft string
	initialJSONDraft   string
	syncedServerDraft  string
	syncedJSONDraft    string
	modeNotice         string
	modeErr            error
	confirm            component.ConfirmButtons
	command            UpstreamCommand
	targetID           string
	serverForm         *mcpServerFormData
	oauthForm          *mcpOAuthFormData
	progress           *component.Progress
	operationCancel    context.CancelFunc
	operationCancelled bool
	operationURL       string
	oauthEventCh       <-chan tea.Msg
	status             map[string]upstream.Status
	tools              map[string][]upstream.Tool
	notice             string
	err                error
	width              int
	height             int
	openBrowser        func(string) error
	oauthLogin         func(context.Context, mcpoauth.LoginConfig, mcpoauth.LoginOptions) (mcpoauth.Credential, error)
}

func NewMCP(ctx context.Context, resourceID string) (*MCPPage, error) {
	return NewMCPRoute(ctx, resourceID, "")
}

func NewMCPRoute(ctx context.Context, resourceID, section string) (*MCPPage, error) {
	return NewMCPRouteAction(ctx, resourceID, section, "")
}

func NewMCPRouteAction(ctx context.Context, resourceID, section, action string) (*MCPPage, error) {
	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		return nil, err
	}
	store := mcpoauth.NewStore(mcpoauth.Path())
	return newMCPRoutePageAction(ctx, resourceID, section, action, manager, store)
}

func newMCPPage(ctx context.Context, resourceID string, manager *upstream.Manager, store *mcpoauth.Store) (*MCPPage, error) {
	return newMCPRoutePage(ctx, resourceID, "", manager, store)
}

func newMCPRoutePage(ctx context.Context, resourceID, section string, manager *upstream.Manager, store *mcpoauth.Store) (*MCPPage, error) {
	return newMCPRoutePageAction(ctx, resourceID, section, "", manager, store)
}

func newMCPRoutePageAction(ctx context.Context, resourceID, section, action string, manager *upstream.Manager, store *mcpoauth.Store) (*MCPPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if manager == nil {
		return nil, fmt.Errorf("MCP manager is required")
	}
	if store == nil {
		store = mcpoauth.NewStore(mcpoauth.Path())
	}
	page := &MCPPage{
		ctx: ctx, manager: manager, oauthStore: store, resourceID: strings.TrimSpace(resourceID), section: strings.TrimSpace(section), action: strings.TrimSpace(action),
		status: map[string]upstream.Status{}, tools: map[string][]upstream.Tool{}, openBrowser: application.OpenBrowser,
	}
	page.oauthLogin = store.Login
	if err := page.reload(); err != nil {
		return nil, err
	}
	if page.action != "" {
		if err := page.initEditorRoute(); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func (page *MCPPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	if page.serverEditorMode == mcpServerEditorJSON && page.jsonEditor != nil {
		return page.jsonEditor.Init()
	}
	if page.editor != nil {
		return page.editor.Init()
	}
	return nil
}

func (page *MCPPage) OverlayActive() bool {
	return page != nil && page.overlay != mcpOverlayNone
}

func (page *MCPPage) InputActive() bool {
	return page != nil && (page.serverEditorActive() || page.resourceID == "" && page.browser.InputActive())
}

func (page *MCPPage) Dirty() bool {
	if page == nil || !page.serverEditorActive() {
		return false
	}
	if page.command == UpstreamAuthLogin {
		return page.editor != nil && page.editor.Dirty()
	}
	if mcpServerFormSnapshot(page.serverForm) != page.initialServerDraft {
		return true
	}
	return page.jsonEditor != nil && page.jsonEditor.Value() != page.initialJSONDraft
}

func (page *MCPPage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *MCPPage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *MCPPage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *MCPPage) serverEditorActive() bool {
	return page != nil && (page.editor != nil || page.jsonEditor != nil)
}

func (page *MCPPage) formDraftChangedSinceSync() bool {
	return page != nil && mcpServerFormSnapshot(page.serverForm) != page.syncedServerDraft
}

func (page *MCPPage) jsonDraftChangedSinceSync() bool {
	return page != nil && page.jsonEditor != nil && page.jsonEditor.Value() != page.syncedJSONDraft
}

func (page *MCPPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case mcpHealthMsg:
		return page, page.finishHealth(msg)
	case mcpToolsMsg:
		return page, page.finishTools(msg)
	case mcpOAuthURLMsg:
		page.operationURL = msg.url
		return page, waitMCPEvent(page.oauthEventCh)
	case mcpOAuthBrowserErrorMsg:
		page.notice = "Could not open browser automatically: " + msg.err.Error()
		return page, waitMCPEvent(page.oauthEventCh)
	case mcpOAuthDoneMsg:
		return page, page.finishOAuth(msg)
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		var cmd tea.Cmd
		if page.serverEditorActive() {
			page.resizeEditor()
		} else if page.resourceID != "" {
			page.detail.Resize(msg.Width, msg.Height)
		} else {
			updated, browserCmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			cmd = browserCmd
		}
		return page, cmd
	}

	if page.overlay == mcpOverlayOperation {
		if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" {
			page.cancelOperation()
			return page, nil
		}
		if page.progress != nil {
			updated, cmd := page.progress.Update(message)
			page.progress = &updated
			return page, cmd
		}
		return page, nil
	}

	switch msg := message.(type) {
	case component.EditorSubmitMsg:
		if page.editor == nil {
			return page, nil
		}
		if page.command == UpstreamAuthLogin {
			return page, page.submitOAuthEditor()
		}
		if page.serverEditorMode == mcpServerEditorForm {
			return page, page.submitServerEditor()
		}
		return page, nil
	case component.TextAreaSavedMsg:
		if page.serverEditorMode == mcpServerEditorJSON && page.jsonEditor != nil {
			return page, page.submitJSONEditor()
		}
		return page, nil
	case component.EditorCancelMsg, component.TextAreaCancelledMsg:
		if page.serverEditorActive() {
			return page, page.editorParentNavigation()
		}
		return page, nil
	case mcpServerEditorModeMsg:
		return page, page.switchServerEditorMode(msg.Mode)
	case component.FormMouseMsg:
		if page.editor != nil {
			return page, page.updateServerEditor(msg)
		}
		return page, nil
	case component.ConfirmChoiceMsg:
		if page.overlay == mcpOverlayConfirm {
			page.confirm.Select(msg.Affirmative)
			return page, page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case UpstreamCommandMsg:
		cmd, err := page.openCommand(msg.Command, msg.ResourceID)
		if err != nil {
			page.err = err
		}
		return page, cmd
	case component.BrowserOpenMsg:
		if page.resourceID == "" && msg.Row.ID != "" {
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"mcp", msg.Row.ID}} }
		}
		return page, nil
	case tea.KeyPressMsg:
		if page.serverEditorActive() {
			switch msg.Keystroke() {
			case "alt+1":
				return page, page.switchServerEditorMode(mcpServerEditorForm)
			case "alt+2":
				return page, page.switchServerEditorMode(mcpServerEditorJSON)
			default:
				return page, page.updateServerEditor(msg)
			}
		}
		if page.overlay == mcpOverlayConfirm {
			return page, page.updateConfirm(msg)
		}
		if page.resourceID == "" && page.browser.InputActive() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		if cmd, handled := page.handleKey(msg); handled {
			return page, cmd
		}
	}
	if page.serverEditorActive() {
		return page, page.updateServerEditor(message)
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

func (page *MCPPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "MCP page unavailable", "")
	}
	page.width, page.height = width, height
	var content string
	if page.serverEditorActive() {
		content = page.editorView(width, height)
	} else if page.resourceID != "" {
		page.detail.SetFeedback(page.notice, page.err)
		page.detail.Resize(width, height)
		content = page.detail.View()
	} else {
		page.browser.SetTitleNotice(page.notice)
		feedback := ""
		if page.err != nil {
			feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
		}
		browserHeight := max(1, height-pageFeedbackHeight(feedback))
		if width > 0 && browserHeight > 0 {
			updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: browserHeight})
			page.browser = updated.(component.Browser)
		}
		content = prependPageFeedback(feedback, page.browser.Content())
	}
	switch page.overlay {
	case mcpOverlayConfirm:
		modalWidth := overlayWidth(width, 68)
		body := confirmOverlayBody(page.confirm, page.confirmTitle(), page.confirmDescription(), modalWidth)
		content = component.CenterOverlay(content, component.Modal(body, modalWidth), width, height)
	case mcpOverlayOperation:
		modalWidth := overlayWidth(width, 82)
		body := ""
		if page.progress != nil {
			body = page.progress.View()
		}
		if page.operationURL != "" {
			body += "\n\n" + component.Label("Authorization URL") + "\n" + page.operationURL
		}
		body += "\n\n" + component.Muted("Esc cancel")
		content = component.CenterOverlay(content, component.Modal(component.WrapModalBody(body, modalWidth), modalWidth), width, height)
	}
	return content
}

func (page *MCPPage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	if page.serverEditorActive() {
		y := originY
		if page.jsonEditor == nil {
			if page.editor == nil {
				return nil
			}
			return page.editor.MouseTargets(originX, y, z)
		}
		header, spans := page.serverEditorModeHeader(page.width)
		targets := make([]component.MouseTarget, 0, len(spans)+8)
		for _, span := range spans {
			index := span.Index
			targets = append(targets, component.MouseTarget{
				ID: "mcp.editor.mode", Rect: component.Rect{X: originX + span.X, Y: y, Width: span.Width, Height: 1}, Z: z,
				Handle: func(event component.MouseEvent) tea.Msg {
					if event.Button != tea.MouseLeft {
						return nil
					}
					return mcpServerEditorModeMsg{Mode: mcpServerEditorMode(index)}
				},
			})
		}
		y += lipgloss.Height(header) + 2
		if feedback := page.serverEditorFeedback(page.width); feedback != "" {
			y += lipgloss.Height(feedback) + 1
		}
		if page.serverEditorMode == mcpServerEditorForm && page.editor != nil {
			targets = append(targets, page.editor.MouseTargets(originX, y, z)...)
		}
		return targets
	}
	switch page.overlay {
	case mcpOverlayConfirm:
		return confirmOverlayMouseTargets(page.confirm, page.confirmTitle(), page.confirmDescription(), overlayWidth(page.width, 68), page.width, page.height, originX, originY, z+20)
	case mcpOverlayOperation:
		return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
	default:
		if page.resourceID != "" {
			return page.detail.MouseTargets(originX, originY, z)
		}
		page.browser.SetTitleNotice(page.notice)
		feedback := ""
		if page.err != nil {
			feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, page.width)
		}
		return page.browser.MouseTargets(originX, originY+pageFeedbackHeight(feedback), z)
	}
}

func (page *MCPPage) updateServerEditor(message tea.Msg) tea.Cmd {
	if page == nil || !page.serverEditorActive() {
		return nil
	}
	if background, ok := message.(tea.BackgroundColorMsg); ok {
		var cmds []tea.Cmd
		if page.editor != nil {
			updated, cmd := page.editor.Update(background)
			page.editor = &updated
			cmds = append(cmds, cmd)
		}
		if page.jsonEditor != nil {
			updated, cmd := page.jsonEditor.Update(background)
			page.jsonEditor = &updated
			cmds = append(cmds, cmd)
		}
		return tea.Batch(cmds...)
	}
	if page.serverEditorMode == mcpServerEditorJSON && page.jsonEditor != nil {
		updated, cmd := page.jsonEditor.Update(message)
		page.jsonEditor = &updated
		return cmd
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return cmd
	}
	return nil
}

func (page *MCPPage) switchServerEditorMode(mode mcpServerEditorMode) tea.Cmd {
	if page == nil || page.command != UpstreamServerAdd || page.jsonEditor == nil || (mode != mcpServerEditorForm && mode != mcpServerEditorJSON) || mode == page.serverEditorMode {
		return nil
	}
	page.modeNotice, page.modeErr = "", nil
	switch mode {
	case mcpServerEditorJSON:
		sourceChanged, targetChanged := page.formDraftChangedSinceSync(), page.jsonDraftChangedSinceSync()
		if sourceChanged && targetChanged {
			page.modeNotice = "Form and JSON drafts both changed; keeping the existing JSON draft without overwriting it."
		} else if sourceChanged {
			if page.editor != nil {
				if err := page.editor.Validate(); err != nil {
					page.editor.SetFeedback("", err)
					return nil
				}
			}
			server, err := serverFromMCPForm(page.serverForm, upstream.Server{}, true)
			if err != nil {
				page.editor.SetFeedback("", err)
				return nil
			}
			encoded, err := upstream.MarshalMCPServersJSON([]upstream.Server{server})
			if err != nil {
				page.editor.SetFeedback("", err)
				return nil
			}
			page.jsonEditor.SetValue(string(encoded))
			page.syncedServerDraft = mcpServerFormSnapshot(page.serverForm)
			page.syncedJSONDraft = page.jsonEditor.Value()
		}
		page.serverEditorMode = mode
		page.resizeEditor()
		return page.jsonEditor.Init()
	case mcpServerEditorForm:
		sourceChanged, targetChanged := page.jsonDraftChangedSinceSync(), page.formDraftChangedSinceSync()
		if sourceChanged && targetChanged {
			page.modeNotice = "JSON and Form drafts both changed; keeping the existing Form draft without overwriting it."
		} else if sourceChanged {
			servers, err := upstream.ParseMCPServersJSON([]byte(page.jsonEditor.Value()))
			if err != nil {
				page.modeErr = err
				return nil
			}
			if len(servers) != 1 {
				page.modeNotice = fmt.Sprintf("JSON contains %d servers; Form mode supports exactly one server, so the JSON draft remains authoritative.", len(servers))
				return nil
			}
			editor, data := newUpstreamServerEditor(servers[0], true)
			page.editor, page.serverForm = &editor, data
			page.syncedServerDraft = mcpServerFormSnapshot(data)
			page.syncedJSONDraft = page.jsonEditor.Value()
		}
		page.serverEditorMode = mode
		page.resizeEditor()
		if page.editor != nil {
			return page.editor.Init()
		}
	}
	return nil
}

func (page *MCPPage) submitJSONEditor() tea.Cmd {
	if page == nil || page.command != UpstreamServerAdd || page.jsonEditor == nil {
		return nil
	}
	page.modeNotice, page.modeErr = "", nil
	servers, err := upstream.ParseMCPServersJSON([]byte(page.jsonEditor.Value()))
	if err != nil {
		page.modeErr = err
		return nil
	}
	if err := page.manager.CreateBatch(servers); err != nil {
		page.modeErr = err
		return nil
	}
	page.acceptServerDrafts()
	message := "MCP server added"
	path := []string{"mcp"}
	if len(servers) == 1 {
		path = []string{"mcp", servers[0].ID}
	} else {
		message = fmt.Sprintf("Added %d MCP servers", len(servers))
	}
	navigation := func() tea.Msg { return NavigateMsg{Path: path} }
	return tea.Batch(navigation, func() tea.Msg { return ToastMsg{Title: "MCP", Message: message, Tone: component.ToneSuccess} })
}

func (page *MCPPage) acceptServerDrafts() {
	if page == nil {
		return
	}
	page.initialServerDraft = mcpServerFormSnapshot(page.serverForm)
	page.syncedServerDraft = page.initialServerDraft
	if page.jsonEditor != nil {
		page.initialJSONDraft = page.jsonEditor.Value()
		page.syncedJSONDraft = page.initialJSONDraft
	}
}

func (page *MCPPage) serverEditorModeHeader(width int) (string, []component.TabSpan) {
	if page == nil || page.jsonEditor == nil {
		return "", nil
	}
	return component.PageTabsLayout([]string{"Form", "JSON"}, int(page.serverEditorMode), "Alt+1 Form · Alt+2 JSON", width)
}

func (page *MCPPage) serverEditorFeedback(width int) string {
	if page == nil {
		return ""
	}
	if page.modeErr != nil {
		return component.BannerWidth(page.modeErr.Error(), component.ToneDanger, width)
	}
	if strings.TrimSpace(page.modeNotice) != "" {
		return component.BannerWidth(page.modeNotice, component.ToneWarning, width)
	}
	return ""
}

func (page *MCPPage) initEditorRoute() error {
	if page == nil {
		return nil
	}
	switch page.action {
	case "create":
		if page.resourceID != "" || page.section != "" {
			return fmt.Errorf("MCP create editor does not accept a resource or section")
		}
		editor, data := newUpstreamServerEditor(upstream.Server{}, true)
		jsonEditor := component.NewTextAreaEditorAction("", "", "create")
		page.editor, page.jsonEditor, page.serverForm = &editor, &jsonEditor, data
		page.serverEditorMode = mcpServerEditorForm
		page.initialServerDraft = mcpServerFormSnapshot(data)
		page.initialJSONDraft = ""
		page.syncedServerDraft, page.syncedJSONDraft = page.initialServerDraft, page.initialJSONDraft
		page.command, page.targetID = UpstreamServerAdd, ""
	case "edit":
		if page.resourceID == "" || page.section != "" {
			return fmt.Errorf("MCP edit editor requires a server resource")
		}
		server, ok := page.manager.Get(page.resourceID)
		if !ok {
			return fmt.Errorf("unknown upstream server: %s", page.resourceID)
		}
		editor, data := newUpstreamServerEditor(server, false)
		page.editor, page.serverForm = &editor, data
		page.serverEditorMode = mcpServerEditorForm
		page.initialServerDraft = mcpServerFormSnapshot(data)
		page.syncedServerDraft = page.initialServerDraft
		page.command, page.targetID = UpstreamServerConfigure, server.ID
	case "login":
		if page.resourceID == "" || page.section != "oauth" {
			return fmt.Errorf("MCP OAuth login editor requires an OAuth server route")
		}
		server, ok := page.manager.Get(page.resourceID)
		if !ok {
			return fmt.Errorf("unknown upstream server: %s", page.resourceID)
		}
		if server.Transport != "http" {
			return fmt.Errorf("OAuth login requires an HTTP upstream server")
		}
		if server.Auth.Type == "none" {
			return fmt.Errorf("OAuth is disabled for %s", server.ID)
		}
		editor, data := newMCPOAuthEditor()
		page.editor, page.oauthForm = &editor, data
		page.command, page.targetID = UpstreamAuthLogin, server.ID
	default:
		return fmt.Errorf("unsupported MCP editor action: %s", page.action)
	}
	page.resizeEditor()
	return nil
}

func (page *MCPPage) resizeEditor() {
	if page == nil || !page.serverEditorActive() || page.width <= 0 || page.height <= 0 {
		return
	}
	bodyHeight := page.height
	if page.jsonEditor == nil {
		if page.editor != nil {
			page.editor.Resize(page.width, bodyHeight)
		}
		return
	}
	header, _ := page.serverEditorModeHeader(page.width)
	overhead := lipgloss.Height(header) + 2
	if feedback := page.serverEditorFeedback(page.width); feedback != "" {
		overhead += lipgloss.Height(feedback) + 1
	}
	childHeight := max(1, bodyHeight-overhead)
	if page.serverEditorMode == mcpServerEditorJSON {
		page.jsonEditor.Resize(page.width, childHeight)
	} else if page.editor != nil {
		page.editor.Resize(page.width, childHeight)
	}
}

func (page *MCPPage) editorView(width, height int) string {
	if !page.serverEditorActive() {
		return component.StateView(component.PageError, "MCP editor unavailable", "")
	}
	page.width, page.height = width, height
	page.resizeEditor()
	if page.jsonEditor == nil {
		if page.editor == nil {
			return component.StateView(component.PageError, "MCP editor unavailable", "")
		}
		return page.editor.View()
	}
	header, _ := page.serverEditorModeHeader(width)
	parts := []string{header, ""}
	if feedback := page.serverEditorFeedback(width); feedback != "" {
		parts = append(parts, feedback)
	}
	if page.serverEditorMode == mcpServerEditorJSON {
		parts = append(parts, page.jsonEditor.View())
	} else if page.editor != nil {
		parts = append(parts, page.editor.View())
	}
	return strings.Join(parts, "\n")
}

func (page *MCPPage) editorParentNavigation() tea.Cmd {
	if page == nil {
		return nil
	}
	path := []string{"mcp"}
	switch page.command {
	case UpstreamServerConfigure:
		if page.targetID != "" {
			path = []string{"mcp", page.targetID}
		}
	case UpstreamAuthLogin:
		if page.targetID != "" {
			path = []string{"mcp", page.targetID, "oauth"}
		}
	}
	return func() tea.Msg { return NavigateMsg{Path: path} }
}

func (page *MCPPage) submitOAuthEditor() tea.Cmd {
	if page == nil || page.editor == nil || page.oauthForm == nil {
		return nil
	}
	if err := page.editor.Validate(); err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	page.editor.SetFeedback("", nil)
	return page.startOAuthLogin()
}

func (page *MCPPage) submitServerEditor() tea.Cmd {
	if page == nil || page.editor == nil || page.serverForm == nil {
		return nil
	}
	page.modeNotice, page.modeErr = "", nil
	if err := page.editor.Validate(); err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	create := page.command == UpstreamServerAdd
	existing := upstream.Server{}
	if !create {
		var ok bool
		existing, ok = page.manager.Get(page.targetID)
		if !ok {
			page.editor.SetFeedback("", fmt.Errorf("unknown upstream server: %s", page.targetID))
			return nil
		}
	}
	server, err := serverFromMCPForm(page.serverForm, existing, create)
	if err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	if create {
		err = page.manager.CreateBatch([]upstream.Server{server})
	} else {
		err = page.manager.Add(server)
	}
	if err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	editor, data := newUpstreamServerEditor(server, false)
	page.editor, page.serverForm = &editor, data
	page.editor.SetFeedback("", nil)
	page.err = nil
	page.acceptServerDrafts()
	message := "MCP server updated"
	if create {
		message = "MCP server added"
	}
	navigation := func() tea.Msg { return NavigateMsg{Path: []string{"mcp", server.ID}} }
	return tea.Batch(navigation, func() tea.Msg { return ToastMsg{Title: "MCP", Message: message, Tone: component.ToneSuccess} })
}

func (page *MCPPage) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "a":
		return func() tea.Msg { return NavigateMsg{Path: []string{"mcp", "create"}} }, true
	}
	return nil, false
}

func (page *MCPPage) openCommand(command UpstreamCommand, resourceID string) (tea.Cmd, error) {
	page.err, page.notice = nil, ""
	page.command, page.targetID = command, strings.TrimSpace(resourceID)
	switch command {
	case UpstreamServerAdd:
		return func() tea.Msg { return NavigateMsg{Path: []string{"mcp", "create"}} }, nil
	case UpstreamServerConfigure:
		if _, ok := page.manager.Get(page.targetID); !ok {
			return nil, fmt.Errorf("unknown upstream server: %s", page.targetID)
		}
		return func() tea.Msg { return NavigateMsg{Path: []string{"mcp", page.targetID, "edit"}} }, nil
	case UpstreamServerRemove:
		if _, ok := page.manager.Get(page.targetID); !ok {
			return nil, fmt.Errorf("unknown upstream server: %s", page.targetID)
		}
		page.confirm = component.NewConfirmButtons("Remove", "Cancel", false)
		page.overlay = mcpOverlayConfirm
		return nil, nil
	case UpstreamServerEnable, UpstreamServerDisable:
		return nil, page.toggleServer(command == UpstreamServerEnable)
	case UpstreamServerHealth:
		return page.startHealth(page.targetID), nil
	case UpstreamServerTools:
		if _, ok := page.manager.Get(page.targetID); !ok {
			return nil, fmt.Errorf("unknown upstream server: %s", page.targetID)
		}
		return page.startTools(page.targetID), nil
	case UpstreamAuthLogin:
		server, ok := page.manager.Get(page.targetID)
		if !ok {
			return nil, fmt.Errorf("unknown upstream server: %s", page.targetID)
		}
		if server.Transport != "http" {
			return nil, fmt.Errorf("OAuth login requires an HTTP upstream server")
		}
		if server.Auth.Type == "none" {
			return nil, fmt.Errorf("OAuth is disabled for %s", server.ID)
		}
		return func() tea.Msg { return NavigateMsg{Path: []string{"mcp", server.ID, "oauth", "login"}} }, nil
	case UpstreamAuthLogout:
		if _, ok := page.manager.Get(page.targetID); !ok {
			return nil, fmt.Errorf("unknown upstream server: %s", page.targetID)
		}
		page.confirm = component.NewConfirmButtons("Logout", "Cancel", false)
		page.overlay = mcpOverlayConfirm
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported MCP action: %s", command)
	}
}

func (page *MCPPage) updateConfirm(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		page.closeOverlay()
		return nil
	}
	if msg.String() != "enter" {
		return page.confirm.Update(msg)
	}
	if !page.confirm.AffirmativeSelected() {
		page.closeOverlay()
		return nil
	}
	target := page.targetID
	switch page.command {
	case UpstreamServerRemove:
		if err := page.manager.Remove(target); err != nil {
			page.err = err
			return nil
		}
		delete(page.status, target)
		delete(page.tools, target)
		page.notice = "MCP server removed"
		page.closeOverlay()
		return func() tea.Msg { return NavigateMsg{Path: []string{"mcp"}, Replace: true} }
	case UpstreamAuthLogout:
		if err := page.oauthStore.Delete(target); err != nil {
			page.err = err
			return nil
		}
		page.notice = "OAuth authorization removed"
		page.closeOverlay()
		_ = page.reload()
		return nil
	default:
		page.err = fmt.Errorf("unsupported MCP confirmation: %s", page.command)
		return nil
	}
}

func (page *MCPPage) toggleServer(enabled bool) error {
	server, ok := page.manager.Get(page.targetID)
	if !ok {
		return fmt.Errorf("unknown upstream server: %s", page.targetID)
	}
	server.Enabled = enabled
	if err := page.manager.Add(server); err != nil {
		return err
	}
	page.notice = "MCP server disabled"
	if enabled {
		page.notice = "MCP server enabled"
	}
	return page.reload()
}

func (page *MCPPage) startHealth(id string) tea.Cmd {
	ctx, cancel := context.WithTimeout(page.ctx, 15*time.Second)
	page.beginOperation(UpstreamServerHealth, id, "Refreshing MCP health", cancel)
	if id == "" {
		return func() tea.Msg { return mcpHealthMsg{statuses: page.manager.ListStatuses(ctx, true)} }
	}
	return func() tea.Msg { return mcpHealthMsg{id: id, status: page.manager.CheckHealth(ctx, id, true)} }
}

func (page *MCPPage) finishHealth(msg mcpHealthMsg) tea.Cmd {
	if page.operationCancelled {
		page.finishCancelledOperation()
		return nil
	}
	if msg.id != "" {
		page.status[msg.id] = msg.status
	} else {
		for _, status := range msg.statuses {
			page.status[status.ID] = status
		}
	}
	page.finishOperation("MCP health refreshed", nil)
	_ = page.reload()
	return nil
}

func (page *MCPPage) startTools(id string) tea.Cmd {
	ctx, cancel := context.WithTimeout(page.ctx, 15*time.Second)
	page.beginOperation(UpstreamServerTools, id, "Loading MCP tools", cancel)
	return func() tea.Msg {
		values, err := page.manager.Tools(ctx, id, true)
		return mcpToolsMsg{id: id, tools: values, err: err}
	}
}

func (page *MCPPage) finishTools(msg mcpToolsMsg) tea.Cmd {
	if page.operationCancelled {
		page.finishCancelledOperation()
		return nil
	}
	if msg.err != nil {
		page.finishOperation("", msg.err)
		return nil
	}
	page.tools[msg.id] = append([]upstream.Tool(nil), msg.tools...)
	page.finishOperation(fmt.Sprintf("Loaded %d MCP tools", len(msg.tools)), nil)
	_ = page.reload()
	return nil
}

func (page *MCPPage) startOAuthLogin() tea.Cmd {
	server, ok := page.manager.Get(page.targetID)
	if !ok {
		err := fmt.Errorf("unknown upstream server: %s", page.targetID)
		if page.editor != nil {
			page.editor.SetFeedback("", err)
		}
		return nil
	}
	data := page.oauthForm
	if data == nil {
		err := fmt.Errorf("OAuth editor is unavailable")
		if page.editor != nil {
			page.editor.SetFeedback("", err)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(page.ctx, 5*time.Minute)
	page.beginOperation(UpstreamAuthLogin, server.ID, "Waiting for OAuth authorization", cancel)
	events := make(chan tea.Msg, 8)
	page.oauthEventCh = events
	login := page.oauthLogin
	openBrowser := page.openBrowser
	go func() {
		credential, err := login(ctx, mcpoauth.LoginConfig{
			ServerID: server.ID, ServerURL: server.URL, Scope: server.Auth.Scope, Issuer: data.Issuer,
			ClientID: data.ClientID, ClientSecretEnvVar: data.ClientSecretEnvVar, ClientMetadataURL: data.ClientMetadataURL,
		}, mcpoauth.LoginOptions{ExtraScope: data.ExtraScope, OnURL: func(raw string) error {
			events <- mcpOAuthURLMsg{id: server.ID, url: raw}
			if data.OpenBrowser && openBrowser != nil {
				if err := openBrowser(raw); err != nil {
					events <- mcpOAuthBrowserErrorMsg{err: err}
				}
			}
			return nil
		}})
		events <- mcpOAuthDoneMsg{id: server.ID, credential: credential, err: err}
		close(events)
	}()
	return waitMCPEvent(events)
}

func (page *MCPPage) finishOAuth(msg mcpOAuthDoneMsg) tea.Cmd {
	page.oauthEventCh = nil
	if page.operationCancelled {
		page.finishCancelledOperation()
		return nil
	}
	if msg.err != nil {
		page.finishOperation("", msg.err)
		if page.editor != nil {
			page.editor.SetFeedback("", msg.err)
		}
		return nil
	}
	page.finishOperation("", nil)
	if page.editor != nil {
		page.editor.Accept()
	}
	page.oauthForm = nil
	message := "OAuth authorization stored"
	return tea.Batch(
		func() tea.Msg { return NavigateMsg{Path: []string{"mcp", msg.id, "oauth"}} },
		func() tea.Msg { return ToastMsg{Title: "MCP", Message: message, Tone: component.ToneSuccess} },
	)
}

func (page *MCPPage) beginOperation(command UpstreamCommand, targetID, title string, cancel context.CancelFunc) {
	page.command, page.targetID = command, targetID
	page.operationCancel = cancel
	page.operationCancelled = false
	page.operationURL = ""
	progress := component.NewProgress(title)
	page.progress = &progress
	page.overlay = mcpOverlayOperation
	page.err = nil
}

func (page *MCPPage) cancelOperation() {
	if page.operationCancel != nil {
		page.operationCancel()
	}
	page.operationCancelled = true
	page.overlay = mcpOverlayNone
	page.progress = nil
	page.operationURL = ""
	page.notice = "Operation cancellation requested"
}

func (page *MCPPage) finishCancelledOperation() {
	page.finishOperation("Operation cancelled", nil)
	page.operationCancelled = false
}

func (page *MCPPage) finishOperation(notice string, err error) {
	if page.operationCancel != nil {
		page.operationCancel()
	}
	page.operationCancel = nil
	page.overlay = mcpOverlayNone
	page.progress = nil
	page.operationURL = ""
	page.err = err
	if err == nil && notice != "" {
		page.notice = notice
	}
}

func (page *MCPPage) closeOverlay() {
	page.overlay = mcpOverlayNone
	page.confirm = component.ConfirmButtons{}
}

func (page *MCPPage) reload() error {
	if page.resourceID != "" {
		return page.syncDetail()
	}
	helpExpanded := page.browser.HelpExpanded()
	rows, err := page.rows()
	if err != nil {
		return err
	}
	page.browser = component.NewBrowser(page.ctx, "Upstream MCP servers", rows, nil).WithHelpBindings(component.Binding([]string{"a"}, "a", "add"))
	page.browser.SetHelpExpanded(helpExpanded)
	if page.width > 0 && page.height > 0 {
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: page.height})
		page.browser = updated.(component.Browser)
	}
	return nil
}

func (page *MCPPage) rows() ([]component.Row, error) {
	servers := page.manager.List()
	rows := make([]component.Row, 0, len(servers))
	for _, server := range servers {
		redacted := upstream.RedactServer(server)
		endpoint := redacted.URL
		if redacted.Transport == "stdio" {
			endpoint = redacted.Command
		}
		state := "disabled"
		if redacted.Enabled {
			state = "enabled"
		}
		rows = append(rows, component.Row{
			ID: server.ID, Title: redacted.Name, Description: server.ID + " · " + endpoint, Meta: redacted.Transport + " · " + state,
			Search: strings.Join([]string{server.ID, redacted.Name, endpoint, redacted.ToolPrefix, redacted.Expose}, " "),
		})
	}
	return rows, nil
}

func (page *MCPPage) syncDetail() error {
	server, ok := page.manager.Get(page.resourceID)
	if !ok {
		return fmt.Errorf("MCP server not found: %s", page.resourceID)
	}
	redacted := upstream.RedactServer(server)
	content := ""
	switch page.section {
	case "":
		content = page.serverOverview(redacted)
	case "health":
		content = page.serverHealth(server)
	case "tools":
		content = page.serverTools(server)
	case "oauth":
		content = page.serverOAuth(server)
	default:
		return fmt.Errorf("unsupported MCP child section: %s", page.section)
	}
	state := "disabled"
	if redacted.Enabled {
		state = "enabled"
	}
	detailTitle := "Overview"
	switch page.section {
	case "health":
		detailTitle = "Health"
	case "tools":
		detailTitle = "Tools"
	case "oauth":
		detailTitle = "OAuth"
	}
	page.detail = component.NewDetailPage(detailTitle, redacted.Transport+" · "+state, content).WithTitleVisible(false)
	bindings := make([]component.DetailPageBinding, 0, 10)
	if page.section == "" {
		bindings = append(bindings,
			component.DetailPageBinding{Key: "h", Desc: "health", Message: NavigateMsg{Path: []string{"mcp", server.ID, "health"}}},
			component.DetailPageBinding{Key: "v", Desc: "tools", Message: NavigateMsg{Path: []string{"mcp", server.ID, "tools"}}},
			component.DetailPageBinding{Key: "u", Desc: "oauth", Message: NavigateMsg{Path: []string{"mcp", server.ID, "oauth"}}},
		)
	}
	toggle := UpstreamServerEnable
	if server.Enabled {
		toggle = UpstreamServerDisable
	}
	bindings = append(bindings,
		component.DetailPageBinding{Key: "e", Desc: "configure", Message: NavigateMsg{Path: []string{"mcp", server.ID, "edit"}}},
		component.DetailPageBinding{Key: "space", HelpKey: "space", Desc: "toggle", Message: UpstreamCommandMsg{Command: toggle, ResourceID: server.ID}},
		component.DetailPageBinding{Key: "r", Desc: "health", Message: UpstreamCommandMsg{Command: UpstreamServerHealth, ResourceID: server.ID}},
		component.DetailPageBinding{Key: "t", Desc: "tools", Message: UpstreamCommandMsg{Command: UpstreamServerTools, ResourceID: server.ID}},
	)
	if server.Transport == "http" && server.Auth.Type != "none" {
		bindings = append(bindings, component.DetailPageBinding{Key: "o", Desc: "login", Message: NavigateMsg{Path: []string{"mcp", server.ID, "oauth", "login"}}})
	}
	if status, err := page.oauthStore.Status(server.ID); err == nil && status.Configured {
		bindings = append(bindings, component.DetailPageBinding{Key: "l", Desc: "logout", Message: UpstreamCommandMsg{Command: UpstreamAuthLogout, ResourceID: server.ID}})
	}
	bindings = append(bindings, component.DetailPageBinding{Key: "d", Desc: "remove", Message: UpstreamCommandMsg{Command: UpstreamServerRemove, ResourceID: server.ID}})
	page.detail.SetBindings(bindings...)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
	return nil
}

func (page *MCPPage) serverOverview(server upstream.Server) string {
	endpoint := server.URL
	if server.Transport == "stdio" {
		endpoint = server.Command
	}
	fields := [][2]string{
		{"ID", server.ID}, {"Name", server.Name}, {"Transport", server.Transport}, {"Endpoint", endpoint}, {"Enabled", fmt.Sprint(server.Enabled)},
		{"Auth", server.Auth.Type}, {"Auth scope", server.Auth.Scope}, {"Expose", server.Expose}, {"Tool prefix", server.ToolPrefix}, {"Idle timeout", fmt.Sprintf("%ds", server.IdleTimeoutSec)},
		{"Bearer env", server.BearerTokenEnvVar}, {"CWD", server.CWD}, {"Args", joinedOrNone(server.Args)}, {"Headers", assignmentText(server.Headers)}, {"Environment", assignmentText(server.Env)},
		{"Allowlisted tools", joinedOrNone(server.Tools)}, {"Disabled tools", joinedOrNone(server.DisabledTools)},
	}
	return detailFields(fields...)
}

func (page *MCPPage) serverHealth(server upstream.Server) string {
	status, ok := page.status[server.ID]
	if !ok {
		if !server.Enabled {
			return detailFields([2]string{"Health", string(upstream.HealthDisabled)}, [2]string{"Enabled", "false"})
		}
		return component.Muted("Not checked yet. Use r or Command Palette -> MCP: Refresh health.")
	}
	pid := ""
	if status.PID != nil {
		pid = fmt.Sprint(*status.PID)
	}
	return detailFields(
		[2]string{"Health", string(status.Health)}, [2]string{"Connected", fmt.Sprint(status.Connected)}, [2]string{"Tools", fmt.Sprint(status.ToolCount)},
		[2]string{"Auth", status.Auth}, [2]string{"Expose", status.Expose}, [2]string{"PID", pid}, [2]string{"Error", status.LastError},
	)
}

func (page *MCPPage) serverTools(server upstream.Server) string {
	tools, ok := page.tools[server.ID]
	if !ok {
		return component.Muted("Tools not loaded yet. Use t or Command Palette -> MCP: View tools.")
	}
	if len(tools) == 0 {
		return component.Muted("No tools exposed by the upstream server.")
	}
	proxied := map[string]bool{}
	for _, name := range page.manager.ProxiedToolNames(server, tools) {
		proxied[name] = true
	}
	lines := make([]string, 0, len(tools))
	for _, tool := range tools {
		proxy := upstream.ProxyName(server.ToolPrefix, tool.Name)
		state := "hidden"
		if proxied[proxy] {
			state = proxy
		}
		line := tool.Name + " · " + state
		if strings.TrimSpace(tool.Description) != "" {
			line += "\n  " + tool.Description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n\n")
}

func (page *MCPPage) serverOAuth(server upstream.Server) string {
	if server.Transport != "http" {
		return component.Muted("OAuth is only available for HTTP upstream servers.")
	}
	status, err := page.oauthStore.Status(server.ID)
	if err != nil {
		return component.Banner(err.Error(), component.ToneDanger)
	}
	if !status.Configured {
		return detailFields([2]string{"Configured", "false"}, [2]string{"Auth mode", server.Auth.Type}, [2]string{"Scope", server.Auth.Scope})
	}
	expires := ""
	if status.ExpiresAt != nil {
		expires = status.ExpiresAt.Format(time.RFC3339)
	}
	return detailFields(
		[2]string{"Configured", "true"}, [2]string{"Issuer", status.Issuer}, [2]string{"Registration", status.Registration}, [2]string{"Client ID", status.ClientID},
		[2]string{"Scopes", strings.Join(status.Scopes, " ")}, [2]string{"Refresh token", fmt.Sprint(status.HasRefreshToken)}, [2]string{"Expires", expires}, [2]string{"Expired", fmt.Sprint(status.Expired)},
	)
}

func (page *MCPPage) confirmTitle() string {
	if page.command == UpstreamAuthLogout {
		return "Remove OAuth authorization for " + page.targetID + "?"
	}
	return "Remove MCP server " + page.targetID + "?"
}

func (page *MCPPage) confirmDescription() string {
	if page.command == UpstreamAuthLogout {
		return "Stored OAuth credentials will be deleted. The MCP server configuration is preserved."
	}
	return "The MCP server configuration and its managed OAuth credentials will be removed. External server data is unchanged."
}

func waitMCPEvent(events <-chan tea.Msg) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		message, ok := <-events
		if !ok {
			return nil
		}
		return message
	}
}
