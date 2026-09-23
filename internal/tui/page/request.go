package page

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/tui/component"
)

const requestRefreshInterval = time.Second
const requestOperationTimeout = 5 * time.Second

var requestTabLabels = []string{"Pending", "History", "All"}

type RequestCommand string

const (
	RequestRefresh     RequestCommand = "request.refresh"
	RequestCreateTest  RequestCommand = "request.create.test"
	RequestApprove     RequestCommand = "request.approve"
	RequestDeny        RequestCommand = "request.deny"
	RequestGrantList   RequestCommand = "request.grant.list"
	RequestGrantRevoke RequestCommand = "request.grant.revoke"
	RequestShowPending RequestCommand = "request.show.pending"
	RequestShowHistory RequestCommand = "request.show.history"
	RequestShowAll     RequestCommand = "request.show.all"
)

type RequestCommandMsg struct {
	Command    RequestCommand
	ResourceID string
}

type requestMode int

const (
	requestModePending requestMode = iota
	requestModeHistory
	requestModeAll
)

type requestOverlay uint8

const (
	requestOverlayNone requestOverlay = iota
	requestOverlayOperation
)

type requestTickMsg time.Time

type requestListMsg struct {
	requests    []approval.Request
	resource    approval.Request
	resourceOK  bool
	resourceErr error
	err         error
}

type requestResolveMsg struct {
	request approval.Request
	approve bool
	err     error
}

type requestCreateMsg struct {
	request approval.Request
	err     error
}

type RequestsPage struct {
	ctx                context.Context
	requests           []approval.Request
	browser            component.Browser
	mode               requestMode
	resourceID         string
	section            string
	action             string
	resourceErr        error
	detail             component.DetailPage
	detailReady        bool
	codeViewer         *component.CodeViewer
	codeViewerSection  string
	codeHelp           component.HelpFooter
	loading            bool
	overlay            requestOverlay
	editor             *component.Editor
	createForm         *requestCreateFormData
	resolveForm        *requestResolveFormData
	resolveApprove     bool
	resolveID          string
	operationCancel    context.CancelFunc
	operationCancelled bool
	progress           *component.Progress
	notice             string
	err                error
	width              int
	height             int
}

func NewRequests(ctx context.Context, resourceID string) (*RequestsPage, error) {
	return NewRequestsRoute(ctx, resourceID, "")
}

func NewRequestsRoute(ctx context.Context, resourceID, section string) (*RequestsPage, error) {
	mode := "pending"
	if strings.TrimSpace(resourceID) != "" {
		mode = "all"
	}
	return NewRequestsRouteMode(ctx, mode, resourceID, section)
}

func NewRequestsRouteMode(ctx context.Context, modeValue, resourceID, section string) (*RequestsPage, error) {
	return NewRequestsRouteAction(ctx, modeValue, resourceID, section, "")
}

func NewRequestsRouteAction(ctx context.Context, modeValue, resourceID, section, action string) (*RequestsPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resourceID = strings.TrimSpace(resourceID)
	mode := parseRequestMode(modeValue, resourceID != "")
	page := &RequestsPage{ctx: ctx, mode: mode, resourceID: resourceID, section: strings.TrimSpace(section), action: strings.TrimSpace(action)}
	page.codeHelp = component.NewHelpFooter(component.Binding([]string{"j", "k", "up", "down", "pgup", "pgdown"}, "j/k", "scroll"), component.Binding([]string{"r"}, "r", "refresh"))
	page.rebuildBrowser("")
	if page.action == "create-test" {
		page.initCreateEditor()
	}
	return page, nil
}

func (page *RequestsPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	page.loading = true
	commands := []tea.Cmd{requestTickCmd(), page.refreshCmd()}
	if page.editor != nil {
		commands = append(commands, page.editor.Init())
	}
	return tea.Batch(commands...)
}

func (page *RequestsPage) OverlayActive() bool {
	return page != nil && page.overlay != requestOverlayNone
}

func (page *RequestsPage) InputActive() bool {
	return page != nil && (page.editor != nil || page.resourceID == "" && page.browser.InputActive())
}
func (page *RequestsPage) Dirty() bool {
	return page != nil && page.editor != nil && page.editor.Dirty()
}
func (page *RequestsPage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *RequestsPage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *RequestsPage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *RequestsPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case requestTickMsg:
		commands := []tea.Cmd{requestTickCmd()}
		if !page.loading && page.overlay != requestOverlayOperation {
			page.loading = true
			commands = append(commands, page.refreshCmd())
		}
		return page, tea.Batch(commands...)
	case requestListMsg:
		page.loading = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.err = nil
		page.resourceErr = msg.resourceErr
		page.requests = append([]approval.Request(nil), msg.requests...)
		if msg.resourceOK {
			page.upsertRequest(msg.resource)
			page.resourceID = msg.resource.ID
		}
		if page.editor == nil && (page.action == "approve" || page.action == "deny") && msg.resourceOK {
			if err := page.initResolveEditor(msg.resource, page.action == "approve"); err != nil {
				page.err = err
				return page, nil
			}
			return page, page.editor.Init()
		}
		selectedID := page.selectedID()
		if page.resourceID != "" {
			selectedID = page.resourceID
		}
		page.rebuildBrowser(selectedID)
		return page, nil
	case requestResolveMsg:
		if page.operationCancel != nil {
			page.operationCancel()
		}
		page.operationCancel = nil
		page.overlay = requestOverlayNone
		page.progress = nil
		if page.operationCancelled {
			page.operationCancelled = false
			page.notice = "Approval operation cancelled"
			return page, nil
		}
		if msg.err != nil {
			requestEditorError(page.editor, msg.err)
			page.err = nil
			return page, nil
		}
		page.err = nil
		page.upsertRequest(msg.request)
		notice := requestResolveNotice(msg.approve, msg.request)
		page.editor, page.resolveForm, page.resolveID, page.action = nil, nil, "", ""
		return page, tea.Batch(requestNavigateCmd(page.mode, msg.request.ID, "", true), func() tea.Msg { return ToastMsg{Title: "Requests", Message: notice, Tone: component.ToneSuccess} })
	case requestCreateMsg:
		if page.operationCancel != nil {
			page.operationCancel()
		}
		page.operationCancel = nil
		page.overlay = requestOverlayNone
		page.progress = nil
		if page.operationCancelled {
			page.operationCancelled = false
			page.notice = "Test request creation cancelled"
			return page, nil
		}
		if msg.err != nil {
			requestEditorError(page.editor, msg.err)
			page.err = nil
			return page, nil
		}
		page.err = nil
		page.upsertRequest(msg.request)
		notice := "Created test request " + msg.request.ID
		page.editor, page.createForm, page.action = nil, nil, ""
		return page, tea.Batch(requestNavigateCmd(requestModePending, msg.request.ID, "", true), func() tea.Msg { return ToastMsg{Title: "Requests", Message: notice, Tone: component.ToneSuccess} })
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		if page.editor != nil {
			page.resizeRequestEditor()
			return page, nil
		}
		var cmd tea.Cmd
		if page.resourceID != "" && page.requestCodeSection() {
			page.resizeCodeViewer(msg.Width, msg.Height)
		} else if page.resourceID != "" {
			page.detail.Resize(msg.Width, msg.Height)
		} else {
			updated, browserCmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			cmd = browserCmd
		}
		return page, cmd
	case component.EditorSubmitMsg:
		if page.createForm != nil {
			return page, page.submitCreateTestForm()
		}
		return page, page.submitResolveForm()
	case component.EditorCancelMsg:
		return page, page.closeRequestEditor()
	case RequestCommandMsg:
		return page, page.handleCommand(msg.Command, msg.ResourceID)
	case component.BrowserOpenMsg:
		if page.resourceID == "" && msg.Row.ID != "" {
			return page, requestNavigateCmd(page.mode, msg.Row.ID, "", false)
		}
		return page, nil
	case tea.KeyPressMsg:
		if page.overlay == requestOverlayOperation {
			if msg.String() == "esc" {
				page.cancelOperation()
				return page, nil
			}
			if page.progress != nil {
				updated, cmd := page.progress.Update(msg)
				page.progress = &updated
				return page, cmd
			}
			return page, nil
		}
		if page.editor != nil {
			updated, cmd := page.editor.Update(msg)
			page.editor = &updated
			return page, cmd
		}
		if page.resourceID == "" && page.browser.InputActive() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		if page.resourceID != "" && page.requestCodeSection() {
			if page.codeHelp.Update(msg) {
				page.resizeCodeViewer(page.width, page.height)
				return page, nil
			}
			if msg.String() == "r" {
				return page, page.manualRefreshCmd()
			}
			if page.codeViewer != nil {
				updated, cmd := page.codeViewer.Update(msg)
				page.codeViewer = &updated
				return page, cmd
			}
			return page, nil
		}
		if page.resourceID != "" {
			updated, cmd := page.detail.Update(msg)
			page.detail = updated
			return page, cmd
		}
		if delta, ok := component.TabDelta(msg); ok {
			mode := requestMode(component.MoveTab(int(page.mode), len(requestTabLabels), delta))
			return page, requestNavigateCmd(mode, "", "", true)
		}
		switch msg.String() {
		case "t":
			return page, page.handleCommand(RequestCreateTest, "")
		case "r":
			return page, page.manualRefreshCmd()
		}
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return page, cmd
	}
	if page.resourceID != "" && page.requestCodeSection() {
		if page.codeViewer != nil {
			updated, cmd := page.codeViewer.Update(message)
			page.codeViewer = &updated
			return page, cmd
		}
		return page, nil
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

func (page *RequestsPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Approval inbox unavailable", "")
	}
	page.width, page.height = width, height
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
	}
	var content string
	if page.editor != nil {
		content = page.requestEditorView(width, height)
	} else if page.resourceID != "" && page.requestCodeSection() {
		content = page.requestCodeView(width, height)
	} else if page.resourceID != "" {
		page.detail.SetFeedback(page.notice, page.err)
		page.detail.Resize(width, height)
		content = page.detail.View()
	} else {
		tabs := component.PageTabsNotice(requestTabLabels, int(page.mode), page.notice, width)
		bodyHeight := max(1, height-lipgloss.Height(tabs))
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("", fmt.Sprintf("%d requests", len(page.requestRows())), feedback, width, bodyHeight, lipgloss.Height(help))
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: layout.BodyHeight})
		page.browser = updated.(component.Browser)
		content = tabs + "\n" + component.BottomHelp(layout.View(page.browser.BodyContent()), help, width, bodyHeight)
	}
	if page.overlay == requestOverlayOperation {
		body := ""
		if page.progress != nil {
			body = page.progress.View()
		}
		body += "\n\n" + component.Muted("Esc cancel")
		content = component.CenterOverlay(content, component.Modal(body, overlayWidth(width, 68)), width, height)
	}
	return content
}

func (page *RequestsPage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	switch page.overlay {
	case requestOverlayOperation:
		return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
	default:
		if page.editor != nil {
			return page.requestEditorMouseTargets(originX, originY, z)
		}
		if page.resourceID != "" && page.requestCodeSection() {
			return page.requestCodeMouseTargets(originX, originY, z)
		}
		if page.resourceID != "" {
			return page.detail.MouseTargets(originX, originY, z)
		}
		feedback := ""
		if page.err != nil {
			feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, page.width)
		}
		_, spans := component.PageTabsLayout(requestTabLabels, int(page.mode), page.notice, page.width)
		header := component.PageTabsNotice(requestTabLabels, int(page.mode), page.notice, page.width)
		targets := make([]component.MouseTarget, 0, len(spans)+8)
		for _, span := range spans {
			mode := requestMode(span.Index)
			targets = append(targets, component.MouseTarget{
				ID: "requests.tab", Rect: component.Rect{X: originX + span.X, Y: originY, Width: span.Width, Height: 1}, Z: z + 1,
				Handle: func(event component.MouseEvent) tea.Msg {
					if event.Button != tea.MouseLeft {
						return nil
					}
					return NavigateMsg{Path: requestRoutePath(mode, "", ""), Replace: true}
				},
			})
		}
		bodyHeight := max(1, page.height-lipgloss.Height(header))
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("", fmt.Sprintf("%d requests", len(page.requestRows())), feedback, page.width, bodyHeight, lipgloss.Height(help))
		offsetY := lipgloss.Height(header) + 1 + layout.BodyY
		targets = append(targets, page.browser.MouseTargets(originX, originY+offsetY, z)...)
		helpY := originY + lipgloss.Height(header) + 1 + bodyHeight - lipgloss.Height(help)
		return append(targets, page.browser.HelpMouseTargets(originX, helpY, z+2)...)
	}
}

func (page *RequestsPage) handleCommand(command RequestCommand, resourceID string) tea.Cmd {
	page.err, page.notice = nil, ""
	switch command {
	case RequestRefresh:
		return page.manualRefreshCmd()
	case RequestCreateTest:
		return func() tea.Msg { return NavigateMsg{Path: []string{"requests", "create-test"}} }
	case RequestShowPending:
		return requestNavigateCmd(requestModePending, "", "", true)
	case RequestShowHistory:
		return requestNavigateCmd(requestModeHistory, "", "", true)
	case RequestShowAll:
		return requestNavigateCmd(requestModeAll, "", "", true)
	case RequestApprove, RequestDeny:
		id := strings.TrimSpace(resourceID)
		if id == "" {
			id = page.selectedID()
		}
		request, ok := page.findRequest(id)
		if !ok {
			page.err = fmt.Errorf("approval request not found: %s", id)
			return nil
		}
		if err := validateResolvableRequest(request, time.Now()); err != nil {
			page.err = err
			return nil
		}
		return requestResolveRoute(page.mode, request.ID, command == RequestApprove)
	case RequestGrantList:
		page.loading = true
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(page.ctx, requestOperationTimeout)
			defer cancel()
			grants, err := application.ListRuntimeGrants(ctx, "")
			if err != nil {
				return requestListMsg{err: err}
			}
			return requestListMsg{requests: grants}
		}
	case RequestGrantRevoke:
		id := strings.TrimSpace(resourceID)
		if id == "" {
			id = page.selectedID()
		}
		if id == "" {
			page.err = errors.New("select a runtime grant request to revoke")
			return nil
		}
		page.loading = true
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(page.ctx, requestOperationTimeout)
			defer cancel()
			request, err := application.RevokeRuntimeGrant(ctx, id)
			if err != nil {
				return requestResolveMsg{request: request, err: err}
			}
			return requestResolveMsg{request: request, approve: false, err: nil}
		}
	default:
		page.err = fmt.Errorf("unsupported request action: %s", command)
		return nil
	}
}

func (page *RequestsPage) submitCreateTestForm() tea.Cmd {
	if page.createForm == nil {
		requestEditorError(page.editor, fmt.Errorf("test request form is unavailable"))
		return nil
	}
	data := *page.createForm
	page.editor.SetSubmitting(true)
	ctx, cancel := context.WithTimeout(page.ctx, requestOperationTimeout)
	page.operationCancel = cancel
	page.operationCancelled = false
	progress := component.NewProgress("Creating test approval request")
	page.progress = &progress
	page.overlay = requestOverlayOperation
	return func() tea.Msg {
		request, err := application.CreateDummyApprovalRequest(ctx, data.WorkspaceID, data.Title, data.Command)
		return requestCreateMsg{request: request, err: err}
	}
}

func (page *RequestsPage) submitResolveForm() tea.Cmd {
	if page.resolveForm == nil || page.resolveID == "" {
		requestEditorError(page.editor, fmt.Errorf("approval resolution form is unavailable"))
		return nil
	}
	id, approve, reason := page.resolveID, page.resolveApprove, requestReason(page.resolveForm)
	page.editor.SetSubmitting(true)
	ctx, cancel := context.WithTimeout(page.ctx, requestOperationTimeout)
	page.operationCancel = cancel
	page.operationCancelled = false
	progress := component.NewProgress(requestProgressTitle(approve))
	page.progress = &progress
	page.overlay = requestOverlayOperation
	return func() tea.Msg {
		current, err := application.GetApprovalRequest(ctx, id)
		if err == nil {
			err = validateResolvableRequest(current, time.Now())
		}
		if err != nil {
			return requestResolveMsg{request: current, approve: approve, err: err}
		}
		request, err := application.ResolveApprovalRequest(ctx, id, approve, reason)
		return requestResolveMsg{request: request, approve: approve, err: err}
	}
}

func (page *RequestsPage) manualRefreshCmd() tea.Cmd {
	if page.loading {
		return nil
	}
	page.loading = true
	return page.refreshCmd()
}

func (page *RequestsPage) refreshCmd() tea.Cmd {
	resourceID := page.resourceID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(page.ctx, requestOperationTimeout)
		defer cancel()
		requests, err := application.ListApprovalRequests(ctx)
		if err != nil {
			return requestListMsg{err: err}
		}
		result := requestListMsg{requests: requests}
		if resourceID != "" {
			request, err := application.GetApprovalRequest(ctx, resourceID)
			if err != nil {
				result.resourceErr = err
				return result
			}
			result.resource, result.resourceOK = request, true
		}
		return result
	}
}

func (page *RequestsPage) cancelOperation() {
	if page.operationCancel != nil {
		page.operationCancel()
	}
	page.operationCancelled = true
	page.overlay = requestOverlayNone
	page.progress = nil
	if page.editor != nil {
		page.editor.SetSubmitting(false)
		page.editor.SetFeedback("Approval operation cancellation requested", nil)
	} else {
		page.notice = "Approval operation cancellation requested"
	}
}

func (page *RequestsPage) setMode(mode requestMode) {
	if page.mode == mode {
		return
	}
	selected := page.selectedID()
	page.mode = mode
	page.resourceID = ""
	page.rebuildBrowser(selected)
}

func (page *RequestsPage) rebuildBrowser(selectedID string) {
	if page.resourceID != "" {
		page.syncDetail()
		return
	}
	helpExpanded := page.browser.HelpExpanded()
	if selectedID == "" {
		selectedID = page.selectedID()
	}
	rows := page.requestRows()
	browser := component.NewBrowser(page.ctx, "Approval requests", rows, nil).WithTitleVisible(false).WithExternalHelp(true)
	browser = browser.WithHelpBindings(component.Binding([]string{"h", "l", "left", "right"}, "←/→", "tabs"), component.Binding([]string{"t"}, "t", "test request"), component.Binding([]string{"r"}, "r", "refresh"))
	page.browser = browser
	page.browser.SetHelpExpanded(helpExpanded)
	if page.width > 0 && page.height > 0 {
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: page.height})
		page.browser = updated.(component.Browser)
	}
	if selectedID != "" {
		page.browser.SelectID(selectedID)
	}
}

func (page *RequestsPage) requestRows() []component.Row {
	now := time.Now()
	rows := make([]component.Row, 0, len(page.requests))
	for _, request := range page.requests {
		if !page.modeIncludes(request.Status) {
			continue
		}
		title := strings.TrimSpace(request.Title)
		if title == "" {
			title = request.ID
		}
		meta := strings.ToUpper(string(request.Status))
		if countdown := requestCountdownLabel(request, now); countdown != "" {
			meta += " · " + countdown
		}
		rows = append(rows, component.Row{
			ID: request.ID, Title: title, Description: strings.Join(nonEmptyRequestStrings(shortApprovalRequestID(request.ID), request.WorkspaceID, request.TargetTool), " · "), Meta: meta,
			Search: strings.Join([]string{request.ID, string(request.Status), request.WorkspaceID, request.TargetTool, request.Source, request.Title, request.Command}, " "),
		})
	}
	return rows
}

func (page *RequestsPage) syncDetail() {
	request, ok := page.findRequest(page.resourceID)
	if !ok {
		body := component.Muted("Loading approval request...")
		if page.resourceErr != nil {
			body = component.Muted("The approval request is no longer available.")
		}
		page.detail = component.NewDetailPage(page.requestDetailTitle(), "", body).WithTitleVisible(false)
		page.detailReady = true
		page.detail.SetBindings(component.DetailPageBinding{Key: "r", Desc: "refresh", Message: RequestCommandMsg{Command: RequestRefresh, ResourceID: page.resourceID}})
		if page.width > 0 && page.height > 0 {
			page.detail.Resize(page.width, page.height)
		}
		return
	}
	if page.requestCodeSection() {
		page.syncCodeViewer(request)
		return
	}
	content := ""
	switch page.section {
	case "":
		content = requestOverview(request, page.width)
	case "guard":
		content = requestGuard(request)
	default:
		content = component.Muted("Unsupported approval request child section: " + page.section)
	}
	meta := strings.ToUpper(string(request.Status))
	if countdown := requestCountdownLabel(request, time.Now()); countdown != "" {
		meta += " · " + countdown
	}
	if page.detailReady {
		page.detail.SetTitle(page.requestDetailTitle())
		page.detail.SetMeta(meta)
		page.detail.SetContentPreserveScroll(content)
	} else {
		page.detail = component.NewDetailPage(page.requestDetailTitle(), meta, content).WithTitleVisible(false)
		page.detailReady = true
	}
	bindings := make([]component.DetailPageBinding, 0, 6)
	if page.section == "" {
		bindings = append(bindings,
			component.DetailPageBinding{Key: "c", Desc: "command", Message: NavigateMsg{Path: requestRoutePath(page.mode, request.ID, "command")}},
			component.DetailPageBinding{Key: "v", Desc: "arguments", Message: NavigateMsg{Path: requestRoutePath(page.mode, request.ID, "arguments")}},
			component.DetailPageBinding{Key: "g", Desc: "guard", Message: NavigateMsg{Path: requestRoutePath(page.mode, request.ID, "guard")}},
		)
	}
	if request.Status == approval.StatusPending {
		bindings = append(bindings,
			component.DetailPageBinding{Key: "a", Desc: "approve", Message: RequestCommandMsg{Command: RequestApprove, ResourceID: request.ID}},
			component.DetailPageBinding{Key: "d", Desc: "deny", Message: RequestCommandMsg{Command: RequestDeny, ResourceID: request.ID}},
		)
	}
	bindings = append(bindings, component.DetailPageBinding{Key: "r", Desc: "refresh", Message: RequestCommandMsg{Command: RequestRefresh, ResourceID: request.ID}})
	page.detail.SetBindings(bindings...)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
}

func (page *RequestsPage) requestCodeSection() bool {
	return page != nil && (page.section == "command" || page.section == "arguments")
}

func (page *RequestsPage) syncCodeViewer(request approval.Request) {
	if page == nil || !page.requestCodeSection() {
		return
	}
	language, content := "text", ""
	switch page.section {
	case "command":
		language, content = "bash", strings.TrimSpace(request.Command)
		if content == "" {
			content, language = "No command", "text"
		}
	case "arguments":
		language, content = "json", requestArguments(request)
	}
	if page.codeViewer == nil || page.codeViewerSection != page.section {
		viewer := component.NewCodeViewerLanguage(content, language)
		page.codeViewer = &viewer
		page.codeViewerSection = page.section
	} else {
		page.codeViewer.SetContent(content)
	}
	page.resizeCodeViewer(page.width, page.height)
}

func (page *RequestsPage) requestCodeView(width, height int) string {
	request, ok := page.findRequest(page.resourceID)
	if !ok {
		return component.StateView(component.PageError, "Approval request unavailable", page.resourceID)
	}
	page.syncCodeViewer(request)
	meta := strings.ToUpper(string(request.Status))
	if countdown := requestCountdownLabel(request, time.Now()); countdown != "" {
		meta += " · " + countdown
	}
	feedback := requestCodeFeedback(page.notice, page.err, width)
	help := page.codeHelp.View(width)
	layout := component.NewSectionLayout("", meta, feedback, width, height, lipgloss.Height(help))
	body := component.Muted("No content")
	if page.codeViewer != nil {
		page.codeViewer.Resize(width, layout.BodyHeight)
		body = page.codeViewer.View()
	}
	return component.BottomHelp(layout.View(body), help, width, height)
}

func (page *RequestsPage) resizeCodeViewer(width, height int) {
	if page == nil || page.codeViewer == nil {
		return
	}
	help := page.codeHelp.View(width)
	layout := component.NewSectionLayout("", "", requestCodeFeedback(page.notice, page.err, width), width, height, lipgloss.Height(help))
	page.codeViewer.Resize(width, layout.BodyHeight)
}

func (page *RequestsPage) requestCodeMouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil || page.codeViewer == nil {
		return nil
	}
	help := page.codeHelp.View(page.width)
	layout := component.NewSectionLayout("", "", requestCodeFeedback(page.notice, page.err, page.width), page.width, page.height, lipgloss.Height(help))
	return page.codeViewer.MouseTargets(originX, originY+layout.BodyY, z)
}

func (page *RequestsPage) requestDetailTitle() string {
	if page == nil {
		return "Overview"
	}
	switch page.section {
	case "command":
		return "Command"
	case "arguments":
		return "Arguments"
	case "guard":
		return "Guard"
	default:
		return "Overview"
	}
}

func requestCodeFeedback(notice string, err error, width int) string {
	if err != nil {
		return component.BannerWidth(err.Error(), component.ToneDanger, width)
	}
	if strings.TrimSpace(notice) != "" {
		return component.BannerWidth(notice, component.ToneSuccess, width)
	}
	return ""
}

func (page *RequestsPage) modeIncludes(status approval.Status) bool {
	switch page.mode {
	case requestModePending:
		return status == approval.StatusPending
	case requestModeHistory:
		return status != approval.StatusPending
	default:
		return true
	}
}

func parseRequestMode(value string, detail bool) requestMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "history":
		return requestModeHistory
	case "all":
		return requestModeAll
	case "pending":
		return requestModePending
	default:
		if detail {
			return requestModeAll
		}
		return requestModePending
	}
}

func requestModePath(mode requestMode) string {
	switch mode {
	case requestModeHistory:
		return "history"
	case requestModeAll:
		return "all"
	default:
		return "pending"
	}
}

func requestRoutePath(mode requestMode, resourceID, section string) []string {
	path := []string{"requests", requestModePath(mode)}
	if resourceID != "" {
		path = append(path, resourceID)
	}
	if section != "" {
		path = append(path, section)
	}
	return path
}

func requestNavigateCmd(mode requestMode, resourceID, section string, replace bool) tea.Cmd {
	return func() tea.Msg {
		return NavigateMsg{Path: requestRoutePath(mode, resourceID, section), Replace: replace}
	}
}

func (page *RequestsPage) selectedID() string {
	selected, ok := page.browser.Selected()
	if !ok {
		return ""
	}
	return selected.ID
}

func (page *RequestsPage) findRequest(id string) (approval.Request, bool) {
	id = strings.TrimSpace(id)
	for _, request := range page.requests {
		if request.ID == id {
			return request, true
		}
	}
	return approval.Request{}, false
}

func (page *RequestsPage) upsertRequest(value approval.Request) {
	for index := range page.requests {
		if page.requests[index].ID == value.ID {
			page.requests[index] = value
			return
		}
	}
	page.requests = append(page.requests, value)
}

func requestTickCmd() tea.Cmd {
	return tea.Tick(requestRefreshInterval, func(now time.Time) tea.Msg { return requestTickMsg(now) })
}

func requestOverview(request approval.Request, _ int) string {
	return detailFields(
		[2]string{"Status", string(request.Status)}, [2]string{"Title", request.Title}, [2]string{"Workspace", request.WorkspaceID}, [2]string{"Tool", request.TargetTool},
		[2]string{"Source", request.Source}, [2]string{"Session", request.SessionHash}, [2]string{"Created", requestTime(request.CreatedAt)}, [2]string{"Expires", requestTime(request.ExpiresAt)},
		[2]string{"Resolved", requestTime(request.ResolvedAt)}, [2]string{"Resolved by", request.ResolvedBy}, [2]string{"Reason", request.Reason}, [2]string{"Retry until", requestTime(request.RetryUntil)}, [2]string{"Consumed", requestTime(request.ConsumedAt)},
	)
}

func requestArguments(request approval.Request) string {
	if len(request.Arguments) == 0 {
		return component.Muted("No arguments")
	}
	var value any
	if json.Unmarshal(request.Arguments, &value) == nil {
		if data, err := json.MarshalIndent(value, "", "  "); err == nil {
			return string(data)
		}
	}
	return string(request.Arguments)
}

func requestGuard(request approval.Request) string {
	guard := strings.TrimSpace(string(request.GuardCode))
	if guard == "" {
		guard = "control-plane mutation"
	}
	return detailFields([2]string{"Guard", guard}, [2]string{"Reason", request.GuardReason})
}

func requestTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func requestCountdownLabel(request approval.Request, now time.Time) string {
	switch request.Status {
	case approval.StatusPending:
		remaining := request.ExpiresAt.Sub(now)
		if remaining <= 0 {
			return "expired"
		}
		return fmt.Sprintf("%ds", int(remaining.Round(time.Second)/time.Second))
	case approval.StatusApproved:
		if request.RetryUntil.IsZero() {
			return ""
		}
		remaining := request.RetryUntil.Sub(now)
		if remaining <= 0 {
			return "expired"
		}
		return fmt.Sprintf("retry %ds", int(remaining.Round(time.Second)/time.Second))
	default:
		return ""
	}
}

func shortApprovalRequestID(id string) string {
	if len(id) <= 14 {
		return id
	}
	return id[:14]
}

func nonEmptyRequestStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func requestProgressTitle(approve bool) string {
	if approve {
		return "Approving control request"
	}
	return "Denying control request"
}

func requestResolveNotice(approve bool, request approval.Request) string {
	if approve {
		return "Approved " + request.ID
	}
	return "Denied " + request.ID
}
