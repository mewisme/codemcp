package page

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/runtimecontrol"
	managed "go.mewis.me/codemcp/internal/service"
	"go.mewis.me/codemcp/internal/tui/component"
	updatepkg "go.mewis.me/codemcp/internal/update"
)

const systemOperationTimeout = 2 * time.Minute

type SystemCommand string

const (
	SystemRefresh        SystemCommand = "system.refresh"
	RuntimeUpUser        SystemCommand = "runtime.up.user"
	RuntimeUpSystem      SystemCommand = "runtime.up.system"
	RuntimeDownUser      SystemCommand = "runtime.down.user"
	RuntimeDownSystem    SystemCommand = "runtime.down.system"
	RuntimeRestartUser   SystemCommand = "runtime.restart.user"
	RuntimeRestartSystem SystemCommand = "runtime.restart.system"
	RuntimeForeground    SystemCommand = "runtime.foreground"
	MCPStdioForeground   SystemCommand = "mcp.stdio.foreground"
	MCPHTTPForeground    SystemCommand = "mcp.http.foreground"
	MCPHTTPEnable        SystemCommand = "transport.mcp-http.enable"
	MCPHTTPDisable       SystemCommand = "transport.mcp-http.disable"
	ConfigInitialize     SystemCommand = "config.initialize.external"
	ConfigUninitialize   SystemCommand = "config.uninitialize.external"
	AuthMCPEnable        SystemCommand = "auth.mcp.enable"
	AuthMCPDisable       SystemCommand = "auth.mcp.disable"
	AuthMCPRotate        SystemCommand = "auth.mcp.rotate"
	AuthAdminEnable      SystemCommand = "auth.admin.enable"
	AuthAdminDisable     SystemCommand = "auth.admin.disable"
	AuthAdminRotate      SystemCommand = "auth.admin.rotate"
	InstallRun           SystemCommand = "install.run"
	UpdateCheck          SystemCommand = "update.check"
	UpdateApply          SystemCommand = "update.apply"
)

type SystemCommandMsg struct{ Command SystemCommand }

type systemOverlay uint8

const (
	systemOverlayNone systemOverlay = iota
	systemOverlayConfirm
	systemOverlayOperation
	systemOverlaySecret
	systemOverlayExternal
)

type systemLoadMsg struct {
	runtime application.RuntimeOverview
	auth    application.AuthStatus
	install application.InstallationOverview
	about   application.AboutInfo
	err     error
}

type systemOperationMsg struct {
	id       uint64
	command  SystemCommand
	token    string
	external *application.ExternalCommand
	update   updatepkg.CheckResult
	notice   string
	err      error
}

type systemCopyMsg struct{ err error }

type runtimeItem struct {
	row         component.Row
	detailTitle string
	detail      string
}

type RuntimePage struct {
	ctx             context.Context
	resourceID      string
	action          string
	browser         component.Browser
	detail          component.DetailPage
	runtime         application.RuntimeOverview
	auth            application.AuthStatus
	install         application.InstallationOverview
	about           application.AboutInfo
	loaded          bool
	loading         bool
	overlay         systemOverlay
	editor          *component.Editor
	installForm     *installFormData
	updateForm      *updateFormData
	confirm         component.ConfirmButtons
	pending         SystemCommand
	progress        *component.Progress
	operationCancel context.CancelFunc
	operationID     uint64
	secret          string
	secretKind      string
	external        *application.ExternalCommand
	notice          string
	err             error
	width           int
	height          int
}

func NewRuntime(ctx context.Context) (*RuntimePage, error) {
	return NewRuntimeRoute(ctx, "")
}

func NewRuntimeRoute(ctx context.Context, resourceID string) (*RuntimePage, error) {
	return NewRuntimeRouteAction(ctx, resourceID, "")
}

func NewRuntimeRouteAction(ctx context.Context, resourceID, action string) (*RuntimePage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	page := &RuntimePage{ctx: ctx, resourceID: strings.TrimSpace(resourceID), action: strings.TrimSpace(action)}
	page.browser = component.NewBrowser(ctx, "Runtime", nil, nil).WithTitleVisible(false).WithHelpBindings(component.Binding([]string{"r"}, "r", "refresh"))
	return page, nil
}

func (page *RuntimePage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	page.loading = true
	return page.loadCmd()
}

func (page *RuntimePage) Close() { page.cancelOperation() }

func (page *RuntimePage) OverlayActive() bool {
	return page != nil && page.overlay != systemOverlayNone
}

func (page *RuntimePage) InputActive() bool {
	return page != nil && (page.editor != nil || page.resourceID == "" && page.browser.InputActive())
}

func (page *RuntimePage) Dirty() bool {
	return page != nil && page.editor != nil && page.editor.Dirty()
}
func (page *RuntimePage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *RuntimePage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *RuntimePage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *RuntimePage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	if msg, ok := message.(systemOperationMsg); ok {
		return page, page.finishOperation(msg)
	}
	if msg, ok := message.(systemCopyMsg); ok {
		if msg.err != nil {
			page.notice = "Clipboard unavailable: " + msg.err.Error()
		} else {
			page.notice = "Copied to clipboard"
		}
		return page, nil
	}
	if page.overlay == systemOverlayOperation {
		if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" {
			page.cancelOperation()
			page.overlay, page.progress = systemOverlayNone, nil
			if page.editor != nil {
				page.editor.SetSubmitting(false)
				page.editor.SetFeedback("Runtime/system operation cancellation requested", nil)
			} else {
				page.notice = "Runtime/system operation cancellation requested"
			}
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
	case systemLoadMsg:
		page.loading = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.loaded, page.err = true, nil
		page.runtime, page.auth, page.install, page.about = msg.runtime, msg.auth, msg.install, msg.about
		if page.action != "" && page.editor == nil {
			if err := page.initRuntimeEditor(); err != nil {
				page.err = err
				return page, nil
			}
			return page, page.editor.Init()
		}
		cmd := page.rebuildBrowser(page.selectedID())
		return page, cmd
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		if page.editor != nil {
			page.resizeRuntimeEditor()
			return page, nil
		}
		var browserCmd tea.Cmd
		if page.resourceID != "" {
			page.detail.Resize(msg.Width, msg.Height)
		} else {
			browserCmd = page.resizeBrowser()
		}
		return page, browserCmd
	case component.EditorSubmitMsg:
		return page, page.submitRuntimeEditor()
	case component.EditorCancelMsg:
		return page, page.runtimeEditorParentNavigation()
	case component.ConfirmChoiceMsg:
		if page.overlay == systemOverlayConfirm {
			page.confirm.Select(msg.Affirmative)
			return page, page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case SystemCommandMsg:
		cmd, err := page.openCommand(msg.Command)
		if err != nil {
			page.err = err
		}
		return page, cmd
	case component.BrowserOpenMsg:
		if page.resourceID == "" && msg.Row.ID != "" {
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"runtime", msg.Row.ID}} }
		}
		return page, nil
	case tea.KeyPressMsg:
		if page.overlay == systemOverlayConfirm {
			return page, page.updateConfirm(msg)
		}
		if page.overlay == systemOverlaySecret || page.overlay == systemOverlayExternal {
			switch msg.String() {
			case "esc":
				page.closeOverlay()
				return page, nil
			case "c":
				return page, page.copyOverlayValue()
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
		if page.resourceID != "" {
			updated, cmd := page.detail.Update(msg)
			page.detail = updated
			return page, cmd
		} else if cmd, handled := page.handleKey(msg); handled {
			return page, cmd
		}
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return page, cmd
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

func (page *RuntimePage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Runtime page unavailable", "")
	}
	page.width, page.height = width, height
	if !page.loaded && page.loading {
		return component.StateView(component.PageLoading, "Loading runtime and system state", "")
	}
	var content string
	if page.editor != nil {
		content = page.runtimeEditorView(width, height)
	} else if page.resourceID != "" {
		page.detail.SetFeedback(page.notice, page.err)
		page.detail.Resize(width, height)
		content = page.detail.View()
	} else {
		title := component.PageTitleNotice("Runtime & System", page.notice, width)
		status := page.statusView(width)
		feedback := ""
		if page.err != nil {
			feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
		}
		browserHeight := max(1, height-lipgloss.Height(title)-lipgloss.Height(status)-pageFeedbackHeight(feedback))
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: browserHeight})
		page.browser = updated.(component.Browser)
		content = title + "\n" + status + "\n" + prependPageFeedback(feedback, page.browser.Content())
	}
	switch page.overlay {
	case systemOverlayConfirm:
		modalWidth := overlayWidth(width, 72)
		body := confirmOverlayBody(page.confirm, page.confirmTitle(), page.confirmDescription(), modalWidth)
		content = component.CenterOverlay(content, component.Modal(body, modalWidth), width, height)
	case systemOverlayOperation:
		body := ""
		if page.progress != nil {
			body = page.progress.View()
		}
		content = component.CenterOverlay(content, component.Modal(body, overlayWidth(width, 64)), width, height)
	case systemOverlaySecret:
		modalWidth := overlayWidth(width, 88)
		body := component.Title(strings.ToUpper(page.secretKind)+" token") + "\n\n" + page.secret + "\n\n" + component.Muted("Shown once · c copy · Esc close")
		content = component.CenterOverlay(content, component.Modal(component.WrapModalBody(body, modalWidth), modalWidth), width, height)
	case systemOverlayExternal:
		modalWidth := overlayWidth(width, 88)
		body := component.Title("Run outside the TUI") + "\n\n" + component.Muted(page.external.Reason) + "\n\n" + component.RenderCodeBlock(page.external.Command, "bash", component.ModalContentWidth(modalWidth)) + "\n\n" + component.Muted("c copy command · Esc close")
		content = component.CenterOverlay(content, component.Modal(component.WrapModalBody(body, modalWidth), modalWidth), width, height)
	}
	return content
}

func (page *RuntimePage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	switch page.overlay {
	case systemOverlayConfirm:
		return confirmOverlayMouseTargets(page.confirm, page.confirmTitle(), page.confirmDescription(), overlayWidth(page.width, 72), page.width, page.height, originX, originY, z+20)
	case systemOverlaySecret:
		body := component.Title(strings.ToUpper(page.secretKind)+" token") + "\n\n" + page.secret + "\n\n" + component.Muted("Shown once · c copy · Esc close")
		return dismissibleOverlayMouseTargets(body, overlayWidth(page.width, 88), page.width, page.height, originX, originY, z+20)
	case systemOverlayExternal:
		if page.external == nil {
			return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
		}
		body := component.Title("Run outside the TUI") + "\n\n" + component.Muted(page.external.Reason) + "\n\n" + component.RenderCodeBlock(page.external.Command, "bash", component.ModalContentWidth(overlayWidth(page.width, 88))) + "\n\n" + component.Muted("c copy command · Esc close")
		return dismissibleOverlayMouseTargets(body, overlayWidth(page.width, 88), page.width, page.height, originX, originY, z+20)
	case systemOverlayOperation:
		return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
	}
	if page.editor != nil {
		return page.runtimeEditorMouseTargets(originX, originY, z)
	}
	if page.resourceID != "" {
		return page.detail.MouseTargets(originX, originY, z)
	}
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, page.width)
	}
	y := originY + lipgloss.Height(component.PageTitleNotice("Runtime & System", page.notice, page.width)) + lipgloss.Height(page.statusView(page.width)) + pageFeedbackHeight(feedback)
	return page.browser.MouseTargets(originX, y, z)
}

func (page *RuntimePage) loadCmd() tea.Cmd {
	ctx := page.ctx
	return func() tea.Msg {
		runtimeState, err := application.LoadRuntimeOverview(ctx)
		if err != nil {
			return systemLoadMsg{err: err}
		}
		auth, err := application.GetAuthStatus()
		if err != nil {
			return systemLoadMsg{err: err}
		}
		installation, err := application.LoadInstallationOverview()
		if err != nil {
			return systemLoadMsg{err: err}
		}
		about, err := application.LoadAbout(ctx)
		return systemLoadMsg{runtime: runtimeState, auth: auth, install: installation, about: about, err: err}
	}
}

func (page *RuntimePage) openCommand(command SystemCommand) (tea.Cmd, error) {
	page.err, page.notice = nil, ""
	switch command {
	case SystemRefresh:
		page.loading = true
		return page.loadCmd(), nil
	case InstallRun:
		return func() tea.Msg { return NavigateMsg{Path: []string{"runtime", "install"}} }, nil
	case UpdateApply:
		return func() tea.Msg { return NavigateMsg{Path: []string{"runtime", "update"}} }, nil
	case RuntimeForeground:
		page.external = &application.ExternalCommand{Command: "cm serve", Reason: "The foreground runtime owns the terminal. Exit the TUI before starting it."}
		page.overlay = systemOverlayExternal
		return nil, nil
	case MCPStdioForeground:
		page.external = &application.ExternalCommand{Command: "cm mcp stdio", Reason: "The stdio MCP server owns stdin/stdout as its protocol transport. Exit the TUI before starting it."}
		page.overlay = systemOverlayExternal
		return nil, nil
	case MCPHTTPForeground:
		page.external = &application.ExternalCommand{Command: "cm mcp http", Reason: "The standalone MCP HTTP server is a foreground process. Exit the TUI before starting it."}
		page.overlay = systemOverlayExternal
		return nil, nil
	case ConfigInitialize:
		page.external = &application.ExternalCommand{Command: "cm init", Reason: "Initialization creates new plaintext MCP/admin tokens. Run it outside the TUI so the CLI can present the one-time credentials directly."}
		page.overlay = systemOverlayExternal
		return nil, nil
	case ConfigUninitialize:
		page.external = &application.ExternalCommand{Command: "cm uninit", Reason: "Uninitialize permanently removes local CodeMCP configuration and state. Run this destructive command explicitly outside the TUI."}
		page.overlay = systemOverlayExternal
		return nil, nil
	case AuthMCPRotate, AuthAdminRotate, RuntimeDownUser, RuntimeDownSystem, RuntimeRestartUser, RuntimeRestartSystem:
		page.pending = command
		page.confirm = component.NewConfirmButtons(page.confirmActionLabel(), "Cancel", false)
		page.overlay = systemOverlayConfirm
		return nil, nil
	case RuntimeUpUser, RuntimeUpSystem, MCPHTTPEnable, MCPHTTPDisable, AuthMCPEnable, AuthMCPDisable, AuthAdminEnable, AuthAdminDisable, UpdateCheck:
		return page.startOperation(command), nil
	default:
		return nil, fmt.Errorf("unsupported system action: %s", command)
	}
}

func (page *RuntimePage) updateConfirm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		page.closeOverlay()
		return nil
	case "enter":
		if !page.confirm.AffirmativeSelected() {
			page.closeOverlay()
			return nil
		}
		command := page.pending
		page.overlay = systemOverlayNone
		return page.startOperation(command)
	default:
		return page.confirm.Update(msg)
	}
}

func (page *RuntimePage) startOperation(command SystemCommand) tea.Cmd {
	page.cancelOperation()
	page.operationID++
	id := page.operationID
	ctx, cancel := context.WithTimeout(page.ctx, systemOperationTimeout)
	page.operationCancel = cancel
	progress := component.NewProgress(systemOperationTitle(command))
	page.progress, page.pending, page.overlay = &progress, command, systemOverlayOperation
	installOptions := application.InstallCurrentOptions{}
	if page.installForm != nil {
		installOptions = page.installForm.Options()
	}
	updateOptions := application.UpdateApplyOptions{}
	if page.updateForm != nil {
		updateOptions = page.updateForm.Options()
	}
	operation := func() tea.Msg {
		msg := systemOperationMsg{id: id, command: command}
		switch command {
		case RuntimeUpUser:
			result, err := application.ManagedRuntimeAction(ctx, "up", managed.ScopeUser)
			msg.err, msg.external = err, result.External
		case RuntimeUpSystem:
			result, err := application.ManagedRuntimeAction(ctx, "up", managed.ScopeSystem)
			msg.err, msg.external = err, result.External
		case RuntimeDownUser:
			result, err := application.ManagedRuntimeAction(ctx, "down", managed.ScopeUser)
			msg.err, msg.external = err, result.External
		case RuntimeDownSystem:
			result, err := application.ManagedRuntimeAction(ctx, "down", managed.ScopeSystem)
			msg.err, msg.external = err, result.External
		case RuntimeRestartUser:
			result, err := application.ManagedRuntimeAction(ctx, "restart", managed.ScopeUser)
			msg.err, msg.external = err, result.External
		case RuntimeRestartSystem:
			result, err := application.ManagedRuntimeAction(ctx, "restart", managed.ScopeSystem)
			msg.err, msg.external = err, result.External
		case MCPHTTPEnable, MCPHTTPDisable:
			enabled := command == MCPHTTPEnable
			result, err := application.SetConfigField(ctx, "server.enabled", fmt.Sprint(enabled))
			msg.err = err
			if msg.err == nil {
				state := "disabled"
				if enabled {
					state = "enabled"
				}
				if result.RuntimeReloaded {
					msg.notice = "MCP HTTP server " + state + " · runtime reloaded"
				} else {
					msg.notice = "MCP HTTP server " + state + " · applies on next runtime start"
				}
			}
		case AuthMCPEnable:
			_, msg.err = application.SetAuthEnabled(ctx, "mcp", true)
		case AuthMCPDisable:
			_, msg.err = application.SetAuthEnabled(ctx, "mcp", false)
		case AuthAdminEnable:
			_, msg.err = application.SetAuthEnabled(ctx, "admin", true)
		case AuthAdminDisable:
			_, msg.err = application.SetAuthEnabled(ctx, "admin", false)
		case AuthMCPRotate:
			msg.token, _, msg.err = application.RotateAuthToken(ctx, "mcp")
		case AuthAdminRotate:
			msg.token, _, msg.err = application.RotateAuthToken(ctx, "admin")
		case InstallRun:
			_, msg.err = application.InstallCurrent(installOptions)
		case UpdateCheck:
			msg.update, msg.err = application.CheckForUpdate(ctx)
		case UpdateApply:
			result, err := application.ApplyUpdate(ctx, updateOptions)
			msg.err, msg.external, msg.notice = err, result.External, result.Notice
		}
		return msg
	}
	return tea.Batch(progress.Init(), operation)
}

func (page *RuntimePage) finishOperation(msg systemOperationMsg) tea.Cmd {
	if msg.id != page.operationID {
		return nil
	}
	page.cancelOperation()
	page.progress = nil
	if msg.err != nil {
		page.overlay = systemOverlayNone
		if page.editor != nil {
			page.editor.SetSubmitting(false)
			page.editor.SetFeedback("", msg.err)
			page.err = nil
		} else {
			page.err = msg.err
		}
		return nil
	}
	if msg.token != "" {
		page.secret = msg.token
		page.secretKind = "admin"
		if msg.command == AuthMCPRotate {
			page.secretKind = "mcp"
		}
		page.overlay = systemOverlaySecret
	} else if msg.external != nil {
		page.external, page.overlay = msg.external, systemOverlayExternal
	} else {
		page.overlay = systemOverlayNone
		page.notice = operationNotice(msg)
	}
	if page.editor != nil {
		page.editor.SetSubmitting(false)
		if msg.external != nil {
			return nil
		}
		page.editor.Accept()
		page.installForm, page.updateForm = nil, nil
		notice := page.notice
		return tea.Batch(page.runtimeEditorParentNavigation(), func() tea.Msg { return ToastMsg{Title: "Runtime", Message: notice, Tone: component.ToneSuccess} })
	}
	page.installForm, page.updateForm = nil, nil
	return page.loadCmd()
}

func (page *RuntimePage) cancelOperation() {
	if page.operationCancel != nil {
		page.operationCancel()
		page.operationCancel = nil
	}
}

func (page *RuntimePage) closeOverlay() {
	page.cancelOperation()
	page.overlay = systemOverlayNone
	page.confirm = component.ConfirmButtons{}
	page.progress = nil
	if page.editor == nil {
		page.pending = ""
		page.installForm, page.updateForm = nil, nil
	} else {
		page.editor.SetSubmitting(false)
	}
	page.secret, page.secretKind = "", ""
	page.external = nil
}

func (page *RuntimePage) copyOverlayValue() tea.Cmd {
	value := page.secret
	if page.overlay == systemOverlayExternal && page.external != nil {
		value = page.external.Command
	}
	return func() tea.Msg { return systemCopyMsg{err: component.CopyText(value)} }
}

func (page *RuntimePage) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if msg.String() == "r" {
		page.loading = true
		return page.loadCmd(), true
	}
	return nil, false
}

func runtimeCommand(action string, scope managed.Scope) SystemCommand {
	system := scope == managed.ScopeSystem
	switch action {
	case "up":
		if system {
			return RuntimeUpSystem
		}
		return RuntimeUpUser
	case "down":
		if system {
			return RuntimeDownSystem
		}
		return RuntimeDownUser
	default:
		if system {
			return RuntimeRestartSystem
		}
		return RuntimeRestartUser
	}
}

func (page *RuntimePage) runtimeScope() managed.Scope {
	if page.runtime.Running && page.runtime.Status.Managed && page.runtime.Status.ServiceScope == string(managed.ScopeSystem) {
		return managed.ScopeSystem
	}
	return managed.ScopeUser
}

func (page *RuntimePage) selectedID() string {
	if page.resourceID != "" {
		return ""
	}
	row, ok := page.browser.Selected()
	if !ok {
		return ""
	}
	return row.ID
}

func (page *RuntimePage) rebuildBrowser(selected string) tea.Cmd {
	items := page.runtimeItems()
	if page.resourceID != "" {
		page.err = page.syncDetail(items)
		return nil
	}
	rows := make([]component.Row, 0, len(items))
	for _, item := range items {
		rows = append(rows, item.row)
	}
	cmd := page.browser.ReplaceRows(rows, selected)
	return cmd
}

func (page *RuntimePage) resizeBrowser() tea.Cmd {
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, page.width)
	}
	height := max(1, page.height-lipgloss.Height(component.PageTitleNotice("Runtime & System", page.notice, page.width))-lipgloss.Height(page.statusView(page.width))-pageFeedbackHeight(feedback))
	updated, cmd := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: height})
	page.browser = updated.(component.Browser)
	return cmd
}

func (page *RuntimePage) runtimeItems() []runtimeItem {
	items := []runtimeItem{page.runtimeItem(), page.mcpHTTPItem(), page.serviceItem(page.runtime.UserService)}
	if page.runtime.SystemService.Supported {
		items = append(items, page.serviceItem(page.runtime.SystemService))
	}
	return append(items, page.authItem("mcp"), page.authItem("admin"), page.installItem(), page.updateItem(), page.aboutItem())
}

func (page *RuntimePage) syncDetail(items []runtimeItem) error {
	var selected runtimeItem
	found := false
	for _, item := range items {
		if item.row.ID == page.resourceID {
			selected, found = item, true
			break
		}
	}
	if !found {
		page.detail = component.NewDetailPage("Unavailable", page.resourceID, component.Muted("Runtime/system item not found.")).WithTitleVisible(false)
		page.detail.SetBindings(component.DetailPageBinding{Key: "r", Desc: "refresh", Message: SystemCommandMsg{Command: SystemRefresh}})
		if page.width > 0 && page.height > 0 {
			page.detail.Resize(page.width, page.height)
		}
		return fmt.Errorf("runtime/system item not found: %s", page.resourceID)
	}
	title := strings.TrimSpace(selected.detailTitle)
	if title == "" {
		title = selected.row.Title
	}
	content := strings.TrimSpace(selected.detail)
	if content == "" {
		content = selected.row.Description
	}
	page.detail = component.NewDetailPage(title, selected.row.Description, content).WithTitleVisible(false)
	page.detail.SetBindings(page.runtimeDetailBindings(selected.row)...)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
	return nil
}

func (page *RuntimePage) runtimeDetailBindings(row component.Row) []component.DetailPageBinding {
	bindings := make([]component.DetailPageBinding, 0, 6)
	add := func(key, desc string, command SystemCommand) {
		bindings = append(bindings, component.DetailPageBinding{Key: key, HelpKey: key, Desc: desc, Message: SystemCommandMsg{Command: command}})
	}
	scope := page.runtimeScope()
	if row.ID == "service.user" {
		scope = managed.ScopeUser
	} else if row.ID == "service.system" {
		scope = managed.ScopeSystem
	}
	switch row.ID {
	case "runtime", "service.user", "service.system":
		add("u", "up", runtimeCommand("up", scope))
		add("x", "restart", runtimeCommand("restart", scope))
		add("d", "down", runtimeCommand("down", scope))
		if row.ID == "runtime" {
			add("f", "foreground", RuntimeForeground)
		}
	case "transport.mcp-http":
		command, label := MCPHTTPEnable, "enable"
		if page.runtime.MCPHTTPEnabled {
			command, label = MCPHTTPDisable, "disable"
		}
		add("space", label, command)
	case "auth.mcp":
		if page.auth.MCPConfigured || page.auth.MCPEnabled {
			command, label := AuthMCPEnable, "enable"
			if page.auth.MCPEnabled {
				command, label = AuthMCPDisable, "disable"
			}
			add("e", label, command)
		}
		add("t", "rotate token", AuthMCPRotate)
	case "auth.admin":
		if page.auth.AdminConfigured || page.auth.AdminEnabled {
			command, label := AuthAdminEnable, "enable"
			if page.auth.AdminEnabled {
				command, label = AuthAdminDisable, "disable"
			}
			add("e", label, command)
		}
		add("t", "rotate token", AuthAdminRotate)
	case "installation":
		add("i", "install", InstallRun)
	case "update":
		add("k", "check", UpdateCheck)
		add("u", "upgrade", UpdateApply)
	}
	add("r", "refresh", SystemRefresh)
	return bindings
}

func (page *RuntimePage) runtimeRow() component.Row {
	return page.runtimeItem().row
}

func (page *RuntimePage) runtimeItem() runtimeItem {
	state := "stopped"
	fields := [][2]string{{"State", state}}
	description := "stopped · no active process"
	if page.runtime.Running {
		status := page.runtime.Status
		mode := "foreground"
		if status.Managed {
			mode = "managed / " + status.ServiceScope
		}
		state = "running"
		if status.Starting {
			state = "starting"
		}
		mcpHTTP := "disabled"
		if status.ServerEnabled {
			mcpHTTP = endpoint(status.ServerPort, "/mcp")
		}
		description = fmt.Sprintf("%s · pid %d · %s", state, status.PID, mode)
		fields = [][2]string{{"State", state}, {"PID", fmt.Sprint(status.PID)}, {"Session", status.RunID}, {"Mode", mode}, {"Service", status.ServiceID}, {"Started", timeLabel(status.StartedAt)}, {"MCP HTTP", mcpHTTP}, {"Admin", adminEndpoint(status)}, {"Exposure", string(status.Exposure)}, {"Tunnel", runtimeTunnelStatus(status)}}
	}
	return runtimeItem{row: component.Row{ID: "runtime", Title: "MCP runtime process", Description: description, Search: "runtime process status service server"}, detailTitle: "MCP runtime process", detail: detailFields(fields...)}
}

func (page *RuntimePage) mcpHTTPRow() component.Row {
	return page.mcpHTTPItem().row
}

func (page *RuntimePage) mcpHTTPItem() runtimeItem {
	configured := "disabled"
	if page.runtime.MCPHTTPEnabled {
		configured = "enabled"
	}
	runtimeState := "stopped"
	description := configured
	endpointValue := ""
	if page.runtime.Running {
		if page.runtime.Status.ServerEnabled {
			runtimeState = "listening"
			endpointValue = endpoint(page.runtime.Status.ServerPort, "/mcp")
			description = configured + " · listening"
		} else {
			runtimeState = "not listening"
			description = configured + " · port closed"
		}
		if page.runtime.Status.ServerEnabled != page.runtime.MCPHTTPEnabled {
			description += " · reload required"
		}
	} else if page.runtime.MCPHTTPEnabled {
		description += " · starts with runtime"
	}
	fallback := "off"
	if page.runtime.TunnelEnabled {
		fallback = "on"
	}
	fields := [][2]string{{"Configured", configured}, {"Runtime", runtimeState}, {"Port", fmt.Sprint(page.runtime.MCPHTTPPort)}, {"Endpoint", endpointValue}, {"Tunnel", fallback}, {"Invariant", "MCP HTTP or OpenAI Secure MCP Tunnel must remain enabled."}}
	return runtimeItem{row: component.Row{ID: "transport.mcp-http", Title: "MCP HTTP server", Description: description, Search: "mcp http server transport listener port enable disable"}, detailTitle: "MCP HTTP server", detail: detailFields(fields...)}
}

func (page *RuntimePage) serviceRow(service application.ServiceOverview) component.Row {
	return page.serviceItem(service).row
}

func (page *RuntimePage) serviceItem(service application.ServiceOverview) runtimeItem {
	state := "not installed"
	if service.Installed {
		state = "installed"
	}
	if service.Running {
		state = "running"
	}
	if service.Err != "" {
		state = "unavailable"
	}
	title := "User managed service"
	if service.Scope == managed.ScopeSystem {
		title = "System managed service"
	}
	description := state
	if service.Backend != "" {
		description += " · " + service.Backend
	}
	if service.PID > 0 {
		description += fmt.Sprintf(" · pid %d", service.PID)
	}
	fields := [][2]string{{"Scope", string(service.Scope)}, {"State", state}, {"Backend", service.Backend}, {"Service", service.ID}, {"PID", valueInt(service.PID)}, {"Config", service.ConfigRoot}, {"Persistence", service.Warning}, {"Error", service.Err}}
	return runtimeItem{row: component.Row{ID: "service." + string(service.Scope), Title: title, Description: description, Search: "managed service " + string(service.Scope) + " " + service.Backend}, detailTitle: title, detail: detailFields(fields...)}
}

func (page *RuntimePage) authItem(kind string) runtimeItem {
	enabled, configured := page.auth.MCPEnabled, page.auth.MCPConfigured
	if kind == "admin" {
		enabled, configured = page.auth.AdminEnabled, page.auth.AdminConfigured
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	configuredText := "missing"
	if configured {
		configuredText = "configured"
	}
	title := "MCP HTTP authentication"
	description := state + " · token " + configuredText
	scope := "Controls authentication only; the MCP HTTP listener is controlled by MCP HTTP server."
	if kind == "admin" {
		title = "Admin UI authentication"
		scope = "Controls authentication for the Admin UI only."
	} else {
		description += " · auth only"
	}
	security := "Token hashes are persisted; plaintext is shown once after rotation."
	legacyBearer := "n/a"
	if kind == "mcp" {
		legacyBearer = "disabled"
		if page.auth.MCPLegacyBearer {
			legacyBearer = "enabled"
		}
	}
	if page.auth.UnauthenticatedLoopback && !enabled {
		security = config.UnauthenticatedLoopbackWarning()
		description += " · UNAUTHENTICATED LOOPBACK"
	} else if page.auth.CleartextHTTP {
		security = config.CleartextHTTPWarning()
		description += " · CLEARTEXT HTTP"
	}
	fields := [][2]string{{"Enabled", fmt.Sprint(enabled)}, {"Token", configuredText}}
	if kind == "mcp" {
		fields = append(fields, [2]string{"OAuth", "canonical for cm mcp http"}, [2]string{"Legacy bearer", legacyBearer})
	}
	fields = append(fields, [2]string{"Scope", scope}, [2]string{"Security", security})
	return runtimeItem{row: component.Row{ID: "auth." + kind, Title: title, Description: description, Search: "auth token " + kind}, detailTitle: title, detail: detailFields(fields...)}
}

func (page *RuntimePage) installItem() runtimeItem {
	method := string(page.install.Detection.Method)
	state := method
	if page.install.Managed {
		state = "managed direct · " + page.install.ManagedVersion
	}
	return runtimeItem{row: component.Row{ID: "installation", Title: "Managed installation", Description: state, Search: "install managed layout"}, detailTitle: "Managed installation", detail: detailFields([2]string{"Method", method}, [2]string{"Executable", page.install.Detection.Executable}, [2]string{"Root", page.install.Detection.Root}, [2]string{"Managed", fmt.Sprint(page.install.Managed)}, [2]string{"Current", page.install.ManagedVersion}, [2]string{"Update policy", string(page.install.Policy.Action)}, [2]string{"Guidance", page.install.Policy.Message})}
}

func (page *RuntimePage) updateItem() runtimeItem {
	status, latest, checked := string(page.install.Policy.Action), "", ""
	if page.install.CachedUpdate != nil {
		status, latest, checked = string(page.install.CachedUpdate.Status), page.install.CachedUpdate.Latest, page.install.CachedUpdate.CheckedAt.Local().Format(time.RFC3339)
	}
	description := status
	if latest != "" {
		description += " · latest " + latest
	}
	return runtimeItem{row: component.Row{ID: "update", Title: "Software upgrade", Description: description, Search: "upgrade update release version latest"}, detailTitle: "Software upgrade", detail: detailFields([2]string{"Current", page.about.Version}, [2]string{"Status", status}, [2]string{"Latest", latest}, [2]string{"Checked", checked}, [2]string{"Policy", page.install.Policy.Message}, [2]string{"External command", page.install.Policy.Command})}
}

func (page *RuntimePage) aboutItem() runtimeItem {
	serverUptime := "stopped"
	if page.about.RuntimeRunning {
		serverUptime = page.about.ServerUptime.String()
	}
	machine := "unavailable"
	if page.about.MachineUptimeOK {
		machine = page.about.MachineUptime.String()
	}
	return runtimeItem{row: component.Row{ID: "about", Title: "Build & environment", Description: page.about.Version + " · " + runtime.GOOS + "/" + runtime.GOARCH, Search: "about version commit build uptime paths environment"}, detailTitle: "Build & environment", detail: detailFields([2]string{"Version", page.about.Version}, [2]string{"Commit", page.about.Commit}, [2]string{"Build time", page.about.BuildTime}, [2]string{"Server uptime", serverUptime}, [2]string{"Machine uptime", machine}, [2]string{"Executable", page.about.Executable}, [2]string{"Config", page.about.ConfigPath}, [2]string{"Config root", page.about.ConfigRoot}, [2]string{"Logs", page.about.LogsPath}, [2]string{"Install method", string(page.about.InstallMethod)}, [2]string{"Install root", page.about.InstallRoot})}
}

func (page *RuntimePage) statusView(width int) string {
	state := component.ToneText("● RUNNING", component.ToneSuccess)
	if page.runtime.Running && page.runtime.Status.Starting {
		state = component.ToneText("· STARTING", component.ToneWarning)
	} else if !page.runtime.Running {
		state = component.Muted("○ STOPPED")
	}
	mode := "no active runtime"
	if page.runtime.Running {
		mode = "foreground"
		if page.runtime.Status.Managed {
			mode = page.runtime.Status.ServiceScope + " · " + page.runtime.Status.ServiceID
		}
	}
	summary := component.TwoColumn(component.KeyValue("Runtime process", state), component.KeyValue("Execution mode", mode), width)
	var banners []string
	if page.auth.UnauthenticatedLoopback {
		banners = append(banners, component.BannerWidth(config.UnauthenticatedLoopbackWarning(), component.ToneWarning, width))
	}
	if page.auth.CleartextHTTP {
		banners = append(banners, component.BannerWidth(config.CleartextHTTPWarning(), component.ToneWarning, width))
	}
	if len(banners) == 0 {
		return summary
	}
	return summary + "\n" + strings.Join(banners, "\n")
}

func (page *RuntimePage) confirmActionLabel() string {
	switch page.pending {
	case AuthMCPRotate, AuthAdminRotate:
		return "Rotate"
	case RuntimeDownUser, RuntimeDownSystem:
		return "Stop & remove"
	case RuntimeRestartUser, RuntimeRestartSystem:
		return "Restart"
	default:
		return "Confirm"
	}
}

func (page *RuntimePage) confirmTitle() string {
	switch page.pending {
	case AuthMCPRotate:
		return "Rotate MCP token?"
	case AuthAdminRotate:
		return "Rotate admin token?"
	case RuntimeDownUser, RuntimeDownSystem:
		return "Stop and remove managed service?"
	case RuntimeRestartUser, RuntimeRestartSystem:
		return "Restart managed service?"
	default:
		return "Confirm action?"
	}
}

func (page *RuntimePage) confirmDescription() string {
	switch page.pending {
	case AuthMCPRotate, AuthAdminRotate:
		return "The previous token stops working immediately. The new plaintext token is shown once and is not persisted by the TUI."
	case RuntimeDownUser, RuntimeDownSystem:
		return "The managed service is stopped and uninstalled. Configuration and runtime logs are preserved."
	case RuntimeRestartUser, RuntimeRestartSystem:
		return "The managed runtime is stopped and started again with the current configuration."
	default:
		return "Review the action before continuing."
	}
}

func operationNotice(msg systemOperationMsg) string {
	if msg.notice != "" {
		return msg.notice
	}
	if msg.command == UpdateCheck {
		return fmt.Sprintf("Update check: %s · latest %s", msg.update.Status, msg.update.Latest)
	}
	switch msg.command {
	case InstallRun:
		return "Managed installation updated"
	case UpdateApply:
		return "Update completed"
	case AuthMCPEnable, AuthAdminEnable:
		return "Authentication enabled"
	case AuthMCPDisable, AuthAdminDisable:
		return "Authentication disabled"
	default:
		return "Runtime/system action completed"
	}
}

func systemOperationTitle(command SystemCommand) string {
	switch command {
	case UpdateCheck:
		return "Checking for updates"
	case UpdateApply:
		return "Verifying and applying update"
	case InstallRun:
		return "Installing managed binary"
	case MCPHTTPEnable, MCPHTTPDisable:
		return "Updating MCP HTTP server"
	case AuthMCPRotate, AuthAdminRotate:
		return "Rotating authentication token"
	default:
		return "Applying system action"
	}
}

func timeLabel(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format(time.RFC3339)
}

func endpoint(port int, path string) string {
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
}

func adminEndpoint(status runtimecontrol.RuntimeStatus) string {
	if !status.AdminEnabled {
		return "disabled"
	}
	return endpoint(status.AdminPort, "/")
}

func valueInt(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprint(value)
}

func runtimeTunnelStatus(status runtimecontrol.RuntimeStatus) string {
	if !status.TunnelEnabled {
		return "disabled"
	}
	if !status.TunnelConfigured {
		return "enabled · not configured"
	}
	switch {
	case status.TunnelReady:
		return "connected"
	case status.TunnelRestarting:
		return "reconnecting"
	case status.TunnelRunning:
		return "connecting"
	default:
		return "starting"
	}
}
