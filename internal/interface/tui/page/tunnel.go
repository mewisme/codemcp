package page

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/tunnel"
)

const tunnelOperationTimeout = 30 * time.Second

type TunnelCommand string

const (
	TunnelConfigure        TunnelCommand = "tunnel.configure"
	TunnelEnable           TunnelCommand = "tunnel.enable"
	TunnelDisable          TunnelCommand = "tunnel.disable"
	TunnelForeground       TunnelCommand = "tunnel.foreground"
	TunnelSync             TunnelCommand = "tunnel.sync"
	TunnelAdminKeySet      TunnelCommand = "tunnel.admin.key.set"
	TunnelAdminKeyVerify   TunnelCommand = "tunnel.admin.key.verify"
	TunnelAdminKeyRemove   TunnelCommand = "tunnel.admin.key.remove"
	TunnelManagedRefresh   TunnelCommand = "tunnel.managed.refresh"
	TunnelManagedCreate    TunnelCommand = "tunnel.managed.create"
	TunnelManagedUpdate    TunnelCommand = "tunnel.managed.update"
	TunnelManagedDelete    TunnelCommand = "tunnel.managed.delete"
	TunnelManagedConfigure TunnelCommand = "tunnel.managed.configure"
)

type TunnelCommandMsg struct {
	Command    TunnelCommand
	ResourceID string
}

type tunnelPageKind uint8

const (
	tunnelPageRuntime tunnelPageKind = iota
	tunnelPageManaged
)

type tunnelOverlayKind uint8

const (
	tunnelOverlayNone tunnelOverlayKind = iota
	tunnelOverlayConfirm
	tunnelOverlayOperation
	tunnelOverlayExternal
)

type tunnelCopyMsg struct{ err error }

type tunnelOperationMsg struct {
	command   TunnelCommand
	targetID  string
	dashboard application.TunnelDashboard
	metadata  tunnel.Metadata
	items     []tunnel.Metadata
	result    application.ManagedTunnelResult
	count     int
	scope     tunnel.AdminScope
	err       error
}

type TunnelPage struct {
	ctx                context.Context
	kind               tunnelPageKind
	resourceID         string
	section            string
	action             string
	dashboard          application.TunnelDashboard
	adminStatus        application.TunnelAdminStatus
	items              []tunnel.Metadata
	browser            component.Browser
	detail             component.DetailPage
	runtimeHelp        component.HelpFooter
	overlay            tunnelOverlayKind
	editor             *component.Editor
	confirm            component.ConfirmButtons
	progress           *component.Progress
	command            TunnelCommand
	targetID           string
	operationCancel    context.CancelFunc
	operationCancelled bool
	runtimeForm        *tunnelRuntimeFormData
	adminForm          *tunnelAdminFormData
	managedForm        *managedTunnelFormData
	managedUpdateFetch bool
	pendingInit        tea.Cmd
	configureForm      *managedConfigureFormData
	deleteClear        bool
	deleteOptions      bool
	external           *application.ExternalCommand
	notice             string
	err                error
	width              int
	height             int
}

func NewTunnelDashboard(ctx context.Context) (*TunnelPage, error) {
	return NewTunnelDashboardRoute(ctx, "", "")
}

func NewTunnelDashboardRoute(ctx context.Context, section, action string) (*TunnelPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dashboard, err := application.TunnelStatus()
	if err != nil {
		return nil, err
	}
	adminStatus, err := application.TunnelAdminKeyStatus()
	if err != nil {
		return nil, err
	}
	page := &TunnelPage{ctx: ctx, kind: tunnelPageRuntime, section: strings.TrimSpace(section), action: strings.TrimSpace(action), dashboard: dashboard, adminStatus: adminStatus}
	page.runtimeHelp = component.NewHelpFooter(page.runtimeHelpBindings()...)
	if page.action != "" {
		if err := page.initRuntimeEditor(); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func NewManagedTunnels(ctx context.Context, resourceID string) (*TunnelPage, error) {
	return NewManagedTunnelsRouteAction(ctx, resourceID, "", "")
}

func NewManagedTunnelsRoute(ctx context.Context, resourceID, section string) (*TunnelPage, error) {
	return NewManagedTunnelsRouteAction(ctx, resourceID, section, "")
}

func NewManagedTunnelsRouteAction(ctx context.Context, resourceID, section, action string) (*TunnelPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := config.ListTunnelMetadata()
	if err != nil {
		return nil, err
	}
	adminStatus, err := application.TunnelAdminKeyStatus()
	if err != nil {
		return nil, err
	}
	dashboard, err := application.TunnelStatus()
	if err != nil {
		return nil, err
	}
	page := &TunnelPage{ctx: ctx, kind: tunnelPageManaged, resourceID: strings.TrimSpace(resourceID), section: strings.TrimSpace(section), action: strings.TrimSpace(action), items: items, adminStatus: adminStatus, dashboard: dashboard}
	if (page.action == "create" || page.action == "edit") && !page.adminStatus.Access.Manage {
		return nil, fmt.Errorf("tunnel admin key does not have verified Manage access")
	}
	if page.action == "configure" && !page.adminStatus.Access.Read && !page.adminStatus.Access.Manage {
		return nil, fmt.Errorf("tunnel admin key does not have verified Read access")
	}
	if page.action == "" {
		if err := page.reloadManagedBrowser(); err != nil {
			return nil, err
		}
	}
	if page.action != "" {
		if err := page.initManagedEditorRoute(); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func (page *TunnelPage) Init() tea.Cmd {
	if page != nil && page.pendingInit != nil {
		cmd := page.pendingInit
		page.pendingInit = nil
		return cmd
	}
	if page != nil && page.editor != nil {
		return page.editor.Init()
	}
	return nil
}

func (page *TunnelPage) OverlayActive() bool {
	return page != nil && page.overlay != tunnelOverlayNone
}

func (page *TunnelPage) InputActive() bool {
	return page != nil && (page.editor != nil || page.kind == tunnelPageManaged && page.resourceID == "" && page.browser.InputActive())
}

func (page *TunnelPage) Dirty() bool { return page != nil && page.editor != nil && page.editor.Dirty() }
func (page *TunnelPage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *TunnelPage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *TunnelPage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *TunnelPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	if msg, ok := message.(tunnelOperationMsg); ok {
		return page, page.finishOperation(msg)
	}
	if msg, ok := message.(tunnelCopyMsg); ok {
		if msg.err != nil {
			page.notice = "Clipboard unavailable: " + msg.err.Error()
		} else {
			page.notice = "Copied command to clipboard"
		}
		return page, nil
	}
	if page.overlay == tunnelOverlayOperation {
		if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" {
			prefetch := page.kind == tunnelPageManaged && page.action == "edit" && page.editor == nil && page.managedUpdateFetch
			page.cancelOperation()
			if prefetch {
				return page, page.editorParentNavigation()
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
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		if page.editor != nil {
			page.resizeEditor()
			return page, nil
		}
		if page.kind == tunnelPageManaged {
			var cmd tea.Cmd
			if page.resourceID != "" {
				page.detail.Resize(msg.Width, msg.Height)
			} else {
				updated, browserCmd := page.browser.Update(msg)
				page.browser = updated.(component.Browser)
				cmd = browserCmd
			}
			return page, cmd
		}
		return page, nil
	case component.EditorSubmitMsg:
		if page.editor != nil {
			return page, page.submitEditor()
		}
		return page, nil
	case component.EditorCancelMsg:
		if page.editor != nil {
			return page, page.editorParentNavigation()
		}
		return page, nil
	case component.FormMouseMsg:
		if page.editor != nil {
			updated, cmd := page.editor.Update(msg)
			page.editor = &updated
			return page, cmd
		}
		return page, nil
	case component.ConfirmChoiceMsg:
		if page.overlay == tunnelOverlayConfirm {
			page.confirm.Select(msg.Affirmative)
			return page, page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case TunnelCommandMsg:
		cmd, err := page.openCommand(msg.Command, msg.ResourceID)
		if err != nil {
			page.err = err
		}
		return page, cmd
	case component.BrowserOpenMsg:
		if page.kind == tunnelPageManaged && page.resourceID == "" && msg.Row.ID != "" {
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "managed", msg.Row.ID}} }
		}
		return page, nil
	case tea.KeyPressMsg:
		if page.editor != nil {
			updated, cmd := page.editor.Update(msg)
			page.editor = &updated
			return page, cmd
		}
		if page.overlay == tunnelOverlayConfirm {
			return page, page.updateConfirm(msg)
		}
		if page.overlay == tunnelOverlayExternal {
			switch msg.String() {
			case "esc":
				page.closeOverlay()
				return page, nil
			case "c":
				value := ""
				if page.external != nil {
					value = page.external.Command
				}
				return page, func() tea.Msg { return tunnelCopyMsg{err: component.CopyText(value)} }
			}
			return page, nil
		}
		if page.kind == tunnelPageManaged && page.resourceID == "" && page.browser.InputActive() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		if page.kind == tunnelPageRuntime {
			page.runtimeHelp.SetBindings(page.runtimeHelpBindings()...)
			if page.runtimeHelp.Update(msg) {
				return page, nil
			}
		}
		if cmd, handled := page.handleKey(msg); handled {
			return page, cmd
		}
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return page, cmd
	}
	if page.kind == tunnelPageManaged {
		if page.resourceID != "" {
			updated, cmd := page.detail.Update(message)
			page.detail = updated
			return page, cmd
		}
		updated, cmd := page.browser.Update(message)
		page.browser = updated.(component.Browser)
		return page, cmd
	}
	return page, nil
}

func (page *TunnelPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Tunnel page unavailable", "")
	}
	page.width, page.height = width, height
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
	}
	content := page.runtimeViewWithFeedback(width, feedback)
	if page.editor != nil {
		content = page.editorView(width, height)
	} else if page.kind == tunnelPageManaged {
		if page.action == "edit" && page.resourceID != "" {
			state := component.StateView(component.PageLoading, "Loading managed tunnel", page.resourceID)
			if page.err != nil && page.overlay != tunnelOverlayOperation {
				state = component.StateView(component.PageError, "Unable to load managed tunnel", page.err.Error())
			}
			content = component.WrapContent(state, width)
		} else if page.resourceID != "" {
			page.detail.SetFeedback(page.notice, page.err)
			page.detail.Resize(width, height)
			content = page.detail.View()
		} else {
			feedback = page.managedFeedback(width, feedback)
			browserHeight := max(1, height-pageFeedbackHeight(feedback))
			updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: browserHeight})
			page.browser = updated.(component.Browser)
			content = prependPageFeedback(feedback, page.browser.Content())
		}
	}
	switch page.overlay {
	case tunnelOverlayConfirm:
		modalWidth := overlayWidth(width, 72)
		body := confirmOverlayBody(page.confirm, page.confirmTitle(), page.confirmDescription(), modalWidth)
		content = component.CenterOverlay(content, component.Modal(body, modalWidth), width, height)
	case tunnelOverlayOperation:
		body := ""
		if page.progress != nil {
			body = page.progress.View()
		}
		body += "\n\n" + component.Muted("Esc cancel")
		content = component.CenterOverlay(content, component.Modal(body, overlayWidth(width, 72)), width, height)
	case tunnelOverlayExternal:
		modalWidth := overlayWidth(width, 88)
		body := component.Title("Run outside the TUI")
		if page.external != nil {
			body += "\n\n" + component.Muted(page.external.Reason) + "\n\n" + component.RenderCodeBlock(page.external.Command, "bash", component.ModalContentWidth(modalWidth))
		}
		body += "\n\n" + component.Muted("c copy command · Esc close")
		content = component.CenterOverlay(content, component.Modal(component.WrapModalBody(body, modalWidth), modalWidth), width, height)
	}
	return content
}

func (page *TunnelPage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	switch page.overlay {
	case tunnelOverlayConfirm:
		return confirmOverlayMouseTargets(page.confirm, page.confirmTitle(), page.confirmDescription(), overlayWidth(page.width, 72), page.width, page.height, originX, originY, z+20)
	case tunnelOverlayExternal:
		body := component.Title("Run outside the TUI")
		if page.external != nil {
			body += "\n\n" + component.Muted(page.external.Reason) + "\n\n" + component.RenderCodeBlock(page.external.Command, "bash", component.ModalContentWidth(overlayWidth(page.width, 88)))
		}
		body += "\n\n" + component.Muted("c copy command · Esc close")
		return dismissibleOverlayMouseTargets(body, overlayWidth(page.width, 88), page.width, page.height, originX, originY, z+20)
	case tunnelOverlayOperation:
		return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
	}
	if page.editor != nil {
		return page.editor.MouseTargets(originX, originY, z)
	}
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, page.width)
	}
	if page.kind == tunnelPageManaged {
		if page.resourceID != "" {
			return page.detail.MouseTargets(originX, originY, z)
		}
		feedback = page.managedFeedback(page.width, feedback)
		return page.browser.MouseTargets(originX, originY+pageFeedbackHeight(feedback), z)
	}
	view := page.runtimeViewWithFeedback(page.width, feedback)
	return keyHintMouseTargets(view, map[string]string{
		"configure": "e", "toggle": "space", "sync": "s", "foreground": "f", "admin key": "a", "verify": "v", "remove admin": "d",
	}, originX, originY, z)
}

func (page *TunnelPage) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if page.kind == tunnelPageRuntime {
		switch msg.String() {
		case "e":
			cmd, err := page.openCommand(TunnelConfigure, "")
			page.err = err
			return cmd, true
		case "space":
			if !page.dashboard.Config.Enabled && !tunnel.Configured(page.dashboard.Config) {
				return nil, true
			}
			if page.dashboard.Config.Enabled && !page.dashboard.MCPHTTPEnabled {
				page.err = fmt.Errorf("tunnel must remain enabled while MCP HTTP is disabled")
				return nil, true
			}
			command := TunnelEnable
			if page.dashboard.Config.Enabled {
				command = TunnelDisable
			}
			cmd, err := page.openCommand(command, "")
			page.err = err
			return cmd, true
		case "s":
			if !tunnel.Configured(page.dashboard.Config) {
				return nil, true
			}
			cmd, err := page.openCommand(TunnelSync, "")
			page.err = err
			return cmd, true
		case "f":
			cmd, err := page.openCommand(TunnelForeground, "")
			page.err = err
			return cmd, true
		case "a":
			cmd, err := page.openCommand(TunnelAdminKeySet, "")
			page.err = err
			return cmd, true
		case "v":
			if !page.adminStatus.Configured {
				return nil, true
			}
			cmd, err := page.openCommand(TunnelAdminKeyVerify, "")
			page.err = err
			return cmd, true
		case "d":
			if !page.adminStatus.Configured {
				return nil, true
			}
			cmd, err := page.openCommand(TunnelAdminKeyRemove, "")
			page.err = err
			return cmd, true
		}
		return nil, false
	}

	switch msg.String() {
	case "r":
		if !page.adminStatus.Access.Manage {
			return nil, true
		}
		cmd, err := page.openCommand(TunnelManagedRefresh, "")
		page.err = err
		return cmd, true
	case "a":
		if !page.adminStatus.Access.Manage {
			return nil, true
		}
		cmd, err := page.openCommand(TunnelManagedCreate, "")
		page.err = err
		return cmd, true
	case "u":
		if !page.adminStatus.Access.Read && !page.adminStatus.Access.Manage {
			return nil, true
		}
		if page.resourceID != "" {
			return nil, false
		}
		row, ok := page.browser.Selected()
		if !ok || strings.TrimSpace(row.ID) == "" {
			return nil, true
		}
		cmd, err := page.openCommand(TunnelManagedConfigure, row.ID)
		page.err = err
		return cmd, true
	}
	return nil, false
}

func (page *TunnelPage) openCommand(command TunnelCommand, resourceID string) (tea.Cmd, error) {
	page.err, page.notice = nil, ""
	page.command, page.targetID = command, strings.TrimSpace(resourceID)
	switch command {
	case TunnelConfigure:
		return func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "edit"}} }, nil
	case TunnelEnable, TunnelDisable:
		enabled := command == TunnelEnable
		return page.startOperation(command, "", "Updating tunnel state", func(ctx context.Context) tunnelOperationMsg {
			dashboard, err := application.SetTunnelEnabled(ctx, enabled)
			return tunnelOperationMsg{command: command, dashboard: dashboard, err: err}
		}), nil
	case TunnelForeground:
		page.external = &application.ExternalCommand{Command: "cm tunnel run", Reason: "The foreground tunnel owns the terminal. Exit the TUI before starting it."}
		page.overlay = tunnelOverlayExternal
		return nil, nil
	case TunnelSync:
		return page.startOperation(command, "", "Syncing tunnel metadata", func(ctx context.Context) tunnelOperationMsg {
			metadata, _, err := application.SyncConfiguredTunnel(ctx)
			return tunnelOperationMsg{command: command, metadata: metadata, err: err}
		}), nil
	case TunnelAdminKeySet:
		return func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "admin-key", "edit"}} }, nil
	case TunnelAdminKeyVerify:
		return page.startOperation(command, "", "Verifying tunnel admin key", func(ctx context.Context) tunnelOperationMsg {
			count, scope, err := application.VerifyTunnelAdminKey(ctx)
			return tunnelOperationMsg{command: command, count: count, scope: scope, err: err}
		}), nil
	case TunnelAdminKeyRemove:
		if !page.adminStatus.Configured {
			return nil, fmt.Errorf("tunnel admin key is not configured")
		}
		page.confirm = component.NewConfirmButtons("Remove", "Cancel", false)
		page.overlay = tunnelOverlayConfirm
		return nil, nil
	case TunnelManagedRefresh:
		if page.targetID == "" {
			return page.startOperation(command, "", "Refreshing managed tunnels", func(ctx context.Context) tunnelOperationMsg {
				items, err := application.RefreshManagedTunnels(ctx)
				return tunnelOperationMsg{command: command, items: items, err: err}
			}), nil
		}
		return page.startOperation(command, page.targetID, "Refreshing managed tunnel", func(ctx context.Context) tunnelOperationMsg {
			result, err := application.GetManagedTunnel(ctx, page.targetID, application.ManagedTunnelOptions{})
			return tunnelOperationMsg{command: command, targetID: page.targetID, result: result, err: err}
		}), nil
	case TunnelManagedCreate:
		return func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "managed", "create"}} }, nil
	case TunnelManagedUpdate:
		if page.targetID == "" {
			return nil, fmt.Errorf("managed tunnel id is required")
		}
		return func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "managed", page.targetID, "edit"}} }, nil
	case TunnelManagedConfigure:
		if page.targetID == "" {
			return nil, fmt.Errorf("managed tunnel id is required")
		}
		return func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "managed", page.targetID, "configure"}} }, nil
	case TunnelManagedDelete:
		if page.targetID == "" {
			return nil, fmt.Errorf("managed tunnel id is required")
		}
		page.deleteClear = page.dashboard.Config.ID == page.targetID
		if dashboard, err := application.TunnelStatus(); err == nil {
			page.dashboard = dashboard
			page.deleteClear = dashboard.Config.ID == page.targetID
		}
		if page.deleteClear && !page.dashboard.MCPHTTPEnabled {
			return nil, fmt.Errorf("cannot delete the configured tunnel while MCP HTTP is disabled")
		}
		if page.deleteClear {
			page.deleteOptions = true
			page.confirm = component.NewConfirmButtons("Clear runtime", "Keep runtime", false)
			page.overlay = tunnelOverlayConfirm
			return nil, nil
		}
		page.confirm = component.NewConfirmButtons("Delete", "Cancel", false)
		page.overlay = tunnelOverlayConfirm
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported tunnel action: %s", command)
	}
}

func (page *TunnelPage) updateConfirm(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		page.closeOverlay()
		return nil
	}
	if msg.String() != "enter" {
		return page.confirm.Update(msg)
	}
	if page.command == TunnelManagedDelete && page.deleteOptions {
		page.deleteClear = page.confirm.AffirmativeSelected()
		page.deleteOptions = false
		page.confirm = component.NewConfirmButtons("Delete", "Cancel", false)
		page.overlay = tunnelOverlayConfirm
		return nil
	}
	if !page.confirm.AffirmativeSelected() {
		page.closeOverlay()
		return nil
	}
	switch page.command {
	case TunnelAdminKeyRemove:
		if err := application.RemoveTunnelAdminKey(page.ctx); err != nil {
			page.err = err
			return nil
		}
		page.notice = "Tunnel admin key removed"
		page.closeOverlay()
		page.reloadDashboard()
		return nil
	case TunnelManagedDelete:
		id, clearConfig := page.targetID, page.deleteClear
		return page.startOperation(page.command, id, "Deleting managed tunnel", func(ctx context.Context) tunnelOperationMsg {
			result, err := application.DeleteManagedTunnel(ctx, id, clearConfig)
			return tunnelOperationMsg{command: TunnelManagedDelete, targetID: id, result: result, err: err}
		})
	default:
		page.err = fmt.Errorf("unsupported tunnel confirmation: %s", page.command)
		return nil
	}
}

func (page *TunnelPage) startOperation(command TunnelCommand, targetID, title string, run func(context.Context) tunnelOperationMsg) tea.Cmd {
	ctx, cancel := context.WithTimeout(page.ctx, tunnelOperationTimeout)
	page.command, page.targetID = command, targetID
	page.operationCancel = cancel
	page.operationCancelled = false
	progress := component.NewProgress(title)
	page.progress = &progress
	page.overlay = tunnelOverlayOperation
	page.err = nil
	return func() tea.Msg { return run(ctx) }
}

func (page *TunnelPage) finishOperation(msg tunnelOperationMsg) tea.Cmd {
	if page.operationCancel != nil {
		page.operationCancel()
	}
	page.operationCancel = nil
	if page.operationCancelled {
		page.operationCancelled = false
		page.overlay = tunnelOverlayNone
		page.progress = nil
		page.notice = "Operation cancelled"
		return nil
	}
	page.overlay = tunnelOverlayNone
	page.progress = nil
	if msg.err != nil {
		if msg.command == TunnelManagedUpdate && page.managedUpdateFetch {
			page.managedUpdateFetch = false
		}
		page.err = msg.err
		if page.editor != nil {
			page.editor.SetFeedback("", msg.err)
		}
		return nil
	}
	switch msg.command {
	case TunnelConfigure, TunnelEnable, TunnelDisable:
		page.dashboard = msg.dashboard
		page.reloadDashboard()
		page.notice = "Tunnel configuration updated"
		if msg.command == TunnelConfigure && page.editor != nil && page.action != "" {
			page.acceptRuntimeEditorSuccess()
			return page.runtimeEditorSuccess(page.notice)
		}
	case TunnelSync:
		page.reloadDashboard()
		page.notice = "Tunnel metadata synced"
	case TunnelAdminKeySet, TunnelAdminKeyVerify:
		page.reloadDashboard()
		page.notice = fmt.Sprintf("Admin key verified for %d tunnel(s) · %s", msg.count, tunnelScopeLabel(msg.scope))
		if msg.command == TunnelAdminKeySet && page.editor != nil && page.action != "" {
			page.acceptRuntimeEditorSuccess()
			return page.runtimeEditorSuccess(page.notice)
		}
	case TunnelManagedRefresh:
		if msg.targetID == "" {
			page.items = append([]tunnel.Metadata(nil), msg.items...)
			page.notice = fmt.Sprintf("Refreshed %d managed tunnel(s)", len(page.items))
		} else {
			page.upsertMetadata(msg.result.Metadata)
			page.notice = "Managed tunnel refreshed"
		}
		_ = page.reloadManagedBrowser()
	case TunnelManagedUpdate:
		if page.managedUpdateFetch {
			page.managedUpdateFetch = false
			editor, data := newManagedTunnelEditor(msg.result.Metadata, false)
			page.editor, page.managedForm = &editor, data
			page.overlay = tunnelOverlayNone
			page.resizeEditor()
			return page.editor.Init()
		}
		page.upsertMetadata(msg.result.Metadata)
		_ = page.reloadManagedBrowser()
		page.notice = "Managed tunnel updated"
		if page.editor != nil && page.action == "edit" {
			page.acceptManagedEditorSuccess(msg.result.Metadata)
			return page.managedEditorSuccess(page.notice, msg.result.Metadata.ID)
		}
	case TunnelManagedCreate:
		page.upsertMetadata(msg.result.Metadata)
		_ = page.reloadManagedBrowser()
		page.notice = "Managed tunnel created"
		if page.editor != nil && page.action == "create" {
			page.acceptManagedEditorSuccess(msg.result.Metadata)
			return page.managedEditorSuccess(page.notice, msg.result.Metadata.ID)
		}
		return func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "managed", msg.result.Metadata.ID}} }
	case TunnelManagedConfigure:
		page.upsertMetadata(msg.result.Metadata)
		_ = page.reloadManagedBrowser()
		page.notice = "Managed tunnel selected for runtime"
		if page.editor != nil && page.action == "configure" {
			page.acceptManagedConfigureSuccess()
			return page.managedEditorSuccess(page.notice, msg.result.Metadata.ID)
		}
	case TunnelManagedDelete:
		page.removeMetadata(msg.targetID)
		page.resourceID = ""
		_ = page.reloadManagedBrowser()
		page.notice = "Managed tunnel deleted"
		return func() tea.Msg { return NavigateMsg{Path: []string{"tunnel", "managed"}, Replace: true} }
	}
	return nil
}

func (page *TunnelPage) cancelOperation() {
	if page.operationCancel != nil {
		page.operationCancel()
	}
	page.operationCancelled = true
	page.managedUpdateFetch = false
	page.overlay = tunnelOverlayNone
	page.progress = nil
	page.notice = "Operation cancellation requested"
}

func (page *TunnelPage) closeOverlay() {
	page.overlay = tunnelOverlayNone
	page.confirm = component.ConfirmButtons{}
	page.managedUpdateFetch = false
	page.deleteOptions = false
	page.external = nil
}

func (page *TunnelPage) reloadDashboard() {
	if dashboard, err := application.TunnelStatus(); err == nil {
		page.dashboard = dashboard
	} else {
		page.err = err
	}
	if status, err := application.TunnelAdminKeyStatus(); err == nil {
		page.adminStatus = status
	} else if page.err == nil {
		page.err = err
	}
}

func (page *TunnelPage) reloadManagedBrowser() error {
	if page.resourceID != "" {
		return page.syncManagedDetail()
	}
	helpExpanded := page.browser.HelpExpanded()
	rows := page.managedRows()
	bindings := make([]key.Binding, 0, 3)
	if page.adminStatus.Access.Read || page.adminStatus.Access.Manage {
		bindings = append(bindings, component.Binding([]string{"u"}, "u", "use"))
	}
	if page.adminStatus.Access.Manage {
		bindings = append(bindings, component.Binding([]string{"r"}, "r", "refresh all"), component.Binding([]string{"a"}, "a", "add"))
	}
	page.browser = component.NewBrowser(page.ctx, "Managed tunnels", rows, nil).WithTitleVisible(false).WithHelpBindings(bindings...)
	page.browser.SetHelpExpanded(helpExpanded)
	if page.width > 0 && page.height > 0 {
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: page.height})
		page.browser = updated.(component.Browser)
	}
	return nil
}

func (page *TunnelPage) managedFeedback(width int, feedback string) string {
	if page == nil || strings.TrimSpace(page.notice) == "" {
		return feedback
	}
	return prependPageFeedback(feedback, component.BannerWidth(page.notice, component.ToneSuccess, width))
}

func (page *TunnelPage) managedRows() []component.Row {
	dashboard, _ := application.TunnelStatus()
	rows := make([]component.Row, 0, len(page.items))
	for _, item := range page.items {
		selected := ""
		if dashboard.Config.ID == item.ID {
			selected = "selected runtime"
		}
		rows = append(rows, component.Row{
			ID: item.ID, Title: item.Name, Description: item.ID + optionalTunnelDescription(item.Description), Meta: selected,
			Search: strings.Join(append(append(append([]string{item.ID, item.Name, item.Description}, item.OrganizationIDs...), item.WorkspaceIDs...), item.TenantIDs...), " "),
		})
	}
	return rows
}

func (page *TunnelPage) syncManagedDetail() error {
	var item *tunnel.Metadata
	for index := range page.items {
		if page.items[index].ID == page.resourceID {
			item = &page.items[index]
			break
		}
	}
	if item == nil {
		return fmt.Errorf("managed tunnel not found in local cache: %s", page.resourceID)
	}
	content := ""
	switch page.section {
	case "":
		content = detailFields([2]string{"ID", item.ID}, [2]string{"Name", item.Name}, [2]string{"Description", item.Description}, [2]string{"Creator", item.Creator}, [2]string{"Fetched", formatTunnelTime(item.FetchedAt)})
	case "scope":
		content = detailFields([2]string{"Organizations", joinedOrNone(item.OrganizationIDs)}, [2]string{"Workspaces", joinedOrNone(item.WorkspaceIDs)}, [2]string{"Tenants", joinedOrNone(item.TenantIDs)})
	default:
		return fmt.Errorf("unsupported managed tunnel child section: %s", page.section)
	}
	dashboard, _ := application.TunnelStatus()
	meta := ""
	if dashboard.Config.ID == item.ID {
		meta = "selected runtime"
	}
	detailTitle := item.Name
	if strings.TrimSpace(detailTitle) == "" {
		detailTitle = "Overview"
	}
	if page.section == "scope" {
		detailTitle = "Scope"
	}
	page.detail = component.NewDetailPage(detailTitle, meta, content).WithTitleVisible(false)
	bindings := make([]component.DetailPageBinding, 0, 5)
	if page.section == "" {
		bindings = append(bindings, component.DetailPageBinding{Key: "s", Desc: "scope", Message: NavigateMsg{Path: []string{"tunnel", "managed", item.ID, "scope"}}})
	}
	if page.adminStatus.Access.Read || page.adminStatus.Access.Manage {
		bindings = append(bindings, component.DetailPageBinding{Key: "r", Desc: "refresh", Message: TunnelCommandMsg{Command: TunnelManagedRefresh, ResourceID: item.ID}}, component.DetailPageBinding{Key: "u", Desc: "use", Message: TunnelCommandMsg{Command: TunnelManagedConfigure, ResourceID: item.ID}})
	}
	if page.adminStatus.Access.Manage {
		bindings = append(bindings, component.DetailPageBinding{Key: "e", Desc: "update", Message: TunnelCommandMsg{Command: TunnelManagedUpdate, ResourceID: item.ID}}, component.DetailPageBinding{Key: "d", Desc: "delete", Message: TunnelCommandMsg{Command: TunnelManagedDelete, ResourceID: item.ID}})
	}
	page.detail.SetBindings(bindings...)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
	return nil
}

func (page *TunnelPage) runtimeView(width int) string {
	return page.runtimeViewWithFeedback(width, "")
}

func (page *TunnelPage) runtimeViewWithFeedback(width int, feedback string) string {
	cfg, status := page.dashboard.Config, page.dashboard.Status
	configured := tunnel.Configured(cfg)
	statusSection := tunnelSection("Status",
		[2]string{"Enabled", tunnelEnabledIndicator(cfg.Enabled)},
		[2]string{"MCP HTTP", tunnelEnabledIndicator(page.dashboard.MCPHTTPEnabled)},
		[2]string{"Configured", tunnelYesNo(configured)},
		[2]string{"Runtime key", tunnelConfiguredIndicator(cfg.APIKey != "")},
	)
	tunnelSectionView := tunnelSection("Tunnel",
		[2]string{"ID", valueOrNone(cfg.ID)},
		[2]string{"Control plane", defaultLabel(cfg.ControlPlaneBaseURL)},
		[2]string{"Organization", valueOrNone(cfg.OrganizationID)},
	)
	adminSection := tunnelSection("Admin",
		[2]string{"State", tunnelConfiguredIndicator(page.adminStatus.Configured)},
		[2]string{"Access", tunnelAdminAccessLabel(page.adminStatus.Access)},
		[2]string{"Scope", tunnelScopeLabel(page.adminStatus.Scope)},
	)
	metadataSection := tunnelMetadataSection(status.Metadata, status.MetadataError)
	page.runtimeHelp.SetBindings(page.runtimeHelpBindings()...)
	actions := page.runtimeHelp.View(width)
	lines := []string{
		component.PageTitleNotice("OpenAI Secure MCP Tunnel", page.notice, width),
		tunnelSectionPair(statusSection, tunnelSectionView, width),
		"",
		tunnelSectionPair(adminSection, metadataSection, width),
		"",
		component.Muted("At least one MCP transport must remain enabled. Live process state is handled by Runtime."),
	}
	return component.BottomHelp(prependPageFeedback(feedback, strings.Join(lines, "\n")), actions, width, page.height)
}

func (page *TunnelPage) runtimeHelpBindings() []key.Binding {
	cfg := page.dashboard.Config
	configured := tunnel.Configured(cfg)
	bindings := []key.Binding{component.Binding([]string{"e"}, "e", "configure")}
	if !cfg.Enabled && configured || cfg.Enabled && page.dashboard.MCPHTTPEnabled {
		bindings = append(bindings, component.Binding([]string{"space"}, "space", "toggle"))
	}
	if configured {
		bindings = append(bindings, component.Binding([]string{"s"}, "s", "sync"), component.Binding([]string{"f"}, "f", "foreground"))
	}
	bindings = append(bindings, component.Binding([]string{"a"}, "a", "admin key"))
	if page.adminStatus.Configured {
		bindings = append(bindings, component.Binding([]string{"v"}, "v", "verify"), component.Binding([]string{"d"}, "d", "remove admin"))
	}
	return bindings
}

func tunnelSection(title string, fields ...[2]string) string {
	lines := []string{component.Title(title)}
	for _, field := range fields {
		label := strings.TrimSpace(field[0])
		value := field[1]
		if strings.TrimSpace(value) == "" {
			value = component.Muted("None")
		}
		lines = append(lines, component.Label(fmt.Sprintf("%-14s", label))+value)
	}
	return strings.Join(lines, "\n")
}

func tunnelSectionPair(left, right string, width int) string {
	if width < 84 {
		return left + "\n\n" + right
	}
	gap := 4
	leftWidth := (width - gap) / 2
	rightWidth := width - gap - leftWidth
	leftView := lipgloss.NewStyle().Width(leftWidth).Render(left)
	rightView := lipgloss.NewStyle().Width(rightWidth).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftView, strings.Repeat(" ", gap), rightView)
}

func tunnelMetadataSection(metadata *tunnel.Metadata, metadataError string) string {
	if metadata == nil {
		state := component.Muted("not loaded")
		if strings.TrimSpace(metadataError) != "" {
			state = component.ToneText("unavailable", component.ToneWarning)
		}
		return tunnelSection("Metadata", [2]string{"State", state}, [2]string{"Error", valueOrNone(metadataError)})
	}
	return tunnelSection("Metadata",
		[2]string{"Name", valueOrNone(metadata.Name)},
		[2]string{"ID", valueOrNone(metadata.ID)},
		[2]string{"Fetched", formatTunnelTime(metadata.FetchedAt)},
	)
}

func tunnelEnabledIndicator(enabled bool) string {
	if enabled {
		return component.ToneText("● ON", component.ToneSuccess)
	}
	return component.Muted("○ OFF")
}

func tunnelYesNo(value bool) string {
	if value {
		return component.ToneText("yes", component.ToneSuccess)
	}
	return component.Muted("no")
}

func tunnelConfiguredIndicator(value bool) string {
	if value {
		return component.ToneText("configured", component.ToneSuccess)
	}
	return component.Muted("not configured")
}

func valueOrNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return component.Muted("None")
	}
	return value
}

func (page *TunnelPage) confirmTitle() string {
	if page.command == TunnelAdminKeyRemove {
		return "Remove stored tunnel admin key?"
	}
	if page.command == TunnelManagedDelete && page.deleteOptions {
		return "Clear selected runtime configuration too?"
	}
	return "Delete managed tunnel " + page.targetID + "?"
}

func (page *TunnelPage) confirmDescription() string {
	if page.command == TunnelAdminKeyRemove {
		return "The admin key and verification scope will be removed. Runtime tunnel configuration is unchanged."
	}
	if page.command == TunnelManagedDelete && page.deleteOptions {
		return "Choose whether deleting this remote tunnel should also clear the local runtime tunnel selection. A final delete confirmation follows."
	}
	if page.deleteClear {
		return "The remote tunnel will be permanently deleted and the selected local runtime tunnel configuration will also be cleared."
	}
	return "The remote tunnel will be permanently deleted. Local runtime tunnel configuration will be preserved."
}

func (page *TunnelPage) upsertMetadata(value tunnel.Metadata) {
	for index := range page.items {
		if page.items[index].ID == value.ID {
			page.items[index] = value
			return
		}
	}
	page.items = append(page.items, value)
}

func (page *TunnelPage) removeMetadata(id string) {
	result := page.items[:0]
	for _, item := range page.items {
		if item.ID != id {
			result = append(result, item)
		}
	}
	page.items = result
}

func tunnelAdminAccessLabel(access tunnel.AdminAccess) string {
	if access.Manage {
		return "full management"
	}
	if access.Read {
		return "read only"
	}
	return "not verified"
}

func tunnelScopeLabel(scope tunnel.AdminScope) string {
	switch {
	case scope.OrganizationID != "":
		return "organization:" + scope.OrganizationID
	case scope.WorkspaceID != "":
		return "workspace:" + scope.WorkspaceID
	case scope.TenantID != "":
		return "tenant:" + scope.TenantID
	default:
		return "none"
	}
}

func defaultLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "default"
	}
	return value
}

func optionalTunnelDescription(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return " · " + value
}

func formatTunnelTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format(time.RFC3339)
}
