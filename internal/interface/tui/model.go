package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/interface/tui/action"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	tuipage "go.mewis.me/codemcp/internal/interface/tui/page"
	"go.mewis.me/codemcp/internal/interface/tui/palette"
	"go.mewis.me/codemcp/internal/interface/tui/quickopen"
	tuistate "go.mewis.me/codemcp/internal/interface/tui/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type overlayKind uint8

const (
	overlayNone overlayKind = iota
	overlayCommands
)

const navbarMinHeight = 9
const approvalClockInterval = time.Second
const approvalReconnectDelay = time.Second
const toastDuration = 3 * time.Second

type approvalStage uint8

const (
	approvalStageNone approvalStage = iota
	approvalStageChoice
	approvalStageResolving
)

type approvalSnapshotMsg struct {
	requests []approval.Request
	err      error
}

type approvalClockTickMsg struct{}

type approvalSubscribedMsg struct {
	subscription approvalEventSubscription
	snapshot     application.ApprovalStateSnapshot
	err          error
}

type approvalEventMsg struct {
	event approval.Event
	err   error
}

type approvalReconnectMsg struct{}

type approvalEventSubscription interface {
	Next() (approval.Event, error)
	Close() error
}

type approvalResolvedMsg struct {
	id      string
	approve bool
	err     error
}

type toastDismissMsg struct {
	id    uint64
	timer uint64
}

type toastCloseMsg struct{}

type toastHoverMsg struct {
	id      uint64
	hovered bool
}

type toastState struct {
	id      uint64
	timer   uint64
	title   string
	message string
	tone    component.Tone
	hovered bool
}

type navigationIntent struct {
	route             Route
	replace           bool
	restoreRemembered bool
	quit              bool
}

type Model struct {
	ctx                    context.Context
	router                 Router
	actions                *action.Registry
	palette                *palette.Model
	homeCommands           *palette.Model
	overlay                overlayKind
	commandResources       map[string]quickopen.Resource
	workspaceContexts      map[string]*tuipage.WorkspaceContextSession
	pageViewStates         map[RouteKind]any
	lastRoutes             map[RouteKind]Route
	stateRoot              string
	state                  tuistate.State
	notice                 string
	currentPage            tuipage.Model
	theme                  theme
	width                  int
	height                 int
	approvals              []approval.Request
	approvalStage          approvalStage
	approvalChoice         component.ConfirmButtons
	approvalApprove        bool
	approvalSimilar        bool
	approvalErr            error
	approvalViewport       viewport.Model
	approvalList           func(context.Context) ([]approval.Request, error)
	approvalSubscribe      func(context.Context) (approvalEventSubscription, application.ApprovalStateSnapshot, error)
	approvalSubscription   approvalEventSubscription
	approvalResolve        func(context.Context, string, bool, string) (approval.Request, error)
	approvalResolveSimilar func(context.Context, string, bool, bool, string) (approval.Request, error)
	approvalNow            func() time.Time
	toast                  toastState
	toastSeq               uint64
	pendingNavigation      *navigationIntent
	navigationConfirm      component.ConfirmButtons
}

func NewModel(initial Route) Model {
	return NewModelWithContext(context.Background(), initial)
}

func NewModelWithContext(ctx context.Context, initial Route) Model {
	return NewModelWithState(ctx, initial, "")
}

func NewModelWithState(ctx context.Context, initial Route, root string) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = tracepkg.WithoutObserver(ctx)
	state := tuistate.Default()
	if root != "" {
		if loaded, err := tuistate.Load(root); err == nil {
			state = loaded
		}
	}
	approvalView := viewport.New(viewport.WithWidth(72), viewport.WithHeight(12))
	approvalView.SoftWrap = false
	approvalView.FillHeight = false
	model := Model{ctx: ctx, router: NewRouter(initial), actions: defaultActionRegistry(), workspaceContexts: map[string]*tuipage.WorkspaceContextSession{}, pageViewStates: map[RouteKind]any{}, lastRoutes: map[RouteKind]Route{}, stateRoot: root, state: state, theme: newTheme(true), approvalViewport: approvalView, approvalList: application.ListApprovalRequests, approvalSubscribe: func(ctx context.Context) (approvalEventSubscription, application.ApprovalStateSnapshot, error) {
		return application.SubscribeApprovalRequests(ctx)
	}, approvalResolve: application.ResolveApprovalRequest, approvalResolveSimilar: application.ResolveApprovalRequestWithRuntimeGrant, approvalNow: time.Now}
	model.loadPage(initial)
	model.rememberStableRoute(initial)
	return model
}

func (model Model) Init() tea.Cmd {
	commands := []tea.Cmd{model.subscribeApprovalsCmd()}
	if model.currentPage != nil {
		commands = append(commands, model.initCurrentPage())
	}
	return tea.Batch(commands...)
}

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tuipage.ToastMsg:
		return model, model.showToast(msg.Title, msg.Message, msg.Tone)
	case toastCloseMsg:
		model.dismissToast()
		return model, nil
	case toastHoverMsg:
		if msg.id != model.toast.id || model.toast.id == 0 || msg.hovered == model.toast.hovered {
			return model, nil
		}
		model.toast.hovered = msg.hovered
		model.toast.timer++
		if msg.hovered {
			return model, nil
		}
		return model, model.toastTimerCmd()
	case toastDismissMsg:
		if msg.id == model.toast.id && msg.timer == model.toast.timer && !model.toast.hovered {
			model.dismissToast()
		}
		return model, nil
	case approvalSnapshotMsg:
		wasActive := model.approvalActive()
		model.applyApprovalSnapshot(msg)
		if !wasActive && model.approvalActive() {
			return model, model.approvalTickCmd()
		}
		return model, nil
	case approvalClockTickMsg:
		model.expireElapsedApprovals(model.approvalTime())
		if model.approvalActive() {
			return model, model.approvalTickCmd()
		}
		return model, nil
	case approvalSubscribedMsg:
		if msg.err != nil {
			return model, model.approvalReconnectCmd()
		}
		if model.approvalSubscription != nil && model.approvalSubscription != msg.subscription {
			_ = model.approvalSubscription.Close()
		}
		model.approvalSubscription = msg.subscription
		model.applyApprovalSnapshot(approvalSnapshotMsg{requests: msg.snapshot.Requests})
		commands := []tea.Cmd{model.waitApprovalEventCmd()}
		if model.approvalActive() {
			commands = append(commands, model.approvalTickCmd())
		}
		return model, tea.Batch(commands...)
	case approvalEventMsg:
		if msg.err != nil {
			if model.approvalSubscription != nil {
				_ = model.approvalSubscription.Close()
				model.approvalSubscription = nil
			}
			return model, model.approvalReconnectCmd()
		}
		model.applyApprovalEvent(msg.event)
		return model, tea.Batch(model.refreshApprovalsCmd(), model.waitApprovalEventCmd())
	case approvalReconnectMsg:
		return model, model.subscribeApprovalsCmd()
	case approvalResolvedMsg:
		return model.finishApprovalResolution(msg)
	case tea.BackgroundColorMsg:
		model.theme = newTheme(msg.IsDark())
		component.SetDarkBackground(msg.IsDark())
		if model.homeCommands != nil {
			updated, _ := model.homeCommands.Update(msg)
			model.homeCommands = &updated
		}
		if model.currentPage != nil {
			updated, cmd := model.currentPage.Update(msg)
			model.currentPage = updated
			if model.palette == nil && cmd != nil {
				return model, cmd
			}
		}
		if model.palette != nil {
			updated, cmd := model.palette.Update(msg)
			model.palette = &updated
			return model, cmd
		}
	case tea.WindowSizeMsg:
		model.width, model.height = msg.Width, msg.Height
		if model.approvalActive() {
			model.syncApprovalViewport(false)
		}
		if model.currentPage != nil {
			metrics := model.frameMetrics(msg.Width, msg.Height)
			updated, cmd := model.currentPage.Update(tea.WindowSizeMsg{Width: metrics.contentWidth, Height: metrics.bodyHeight})
			model.currentPage = updated
			return model, cmd
		}
		return model, nil
	case palette.ClosedMsg:
		model.closeOverlay()
		return model, nil
	case component.ConfirmChoiceMsg:
		if model.approvalActive() {
			return model.updateApprovalChoice(msg)
		}
		if model.navigationGuardActive() {
			model.navigationConfirm.Select(msg.Affirmative)
			return model.resolveNavigationChoice(model.navigationConfirm.AffirmativeSelected())
		}
		return model.updatePage(msg)
	case palette.SelectedMsg:
		if resource, ok := model.commandResources[msg.ID]; ok {
			model.closeOverlay()
			route, err := ParseRoute(resource.Path)
			if err != nil {
				return model, model.showToast("Commands", err.Error(), component.ToneDanger)
			}
			return model.requestNavigation(navigationIntent{route: route})
		}
		homeSelection := model.router.Current().Kind == RouteHome && model.overlay == overlayNone && model.homeCommands != nil
		if !homeSelection {
			model.closeOverlay()
		}
		recentCmd := model.recordRecent(msg.ID)
		if homeSelection {
			model.resetHomeCommands()
		}
		cmd, err := model.actions.Execute(model.ctx, msg.ID, actionContext(model.router.Current()))
		if err != nil {
			return model, tea.Batch(recentCmd, model.showToast("Command", err.Error(), component.ToneDanger))
		}
		return model, tea.Batch(recentCmd, cmd)
	case palette.MouseScrollMsg:
		if model.palette != nil {
			updated, cmd := model.palette.Update(msg)
			model.palette = &updated
			return model, cmd
		}
		if model.router.Current().Kind == RouteHome && model.homeCommands != nil {
			updated, cmd := model.homeCommands.Update(msg)
			model.homeCommands = &updated
			return model, cmd
		}
		return model, nil
	case navigateMsg:
		return model.requestNavigation(navigationIntent{route: msg.route, replace: msg.sibling, restoreRemembered: msg.sibling})
	case tuipage.NavigateMsg:
		route, err := ParseRoute(msg.Path)
		if err != nil {
			return model, model.showToast("Navigation", err.Error(), component.ToneDanger)
		}
		if msg.PreservePage {
			if msg.Replace {
				model.router.Switch(route)
			} else {
				model.router.Navigate(route)
			}
			model.rememberStableRoute(route)
			if model.currentPage != nil && model.width > 0 && model.height > 0 {
				metrics := model.frameMetrics(model.width, model.height)
				updated, cmd := model.currentPage.Update(tea.WindowSizeMsg{Width: metrics.contentWidth, Height: metrics.bodyHeight})
				model.currentPage = updated
				return model, cmd
			}
			return model, nil
		}
		return model.requestNavigation(navigationIntent{route: route, replace: msg.Replace})
	case tuipage.WorkspaceCommandMsg:
		if err := model.ensureWorkspacePage(msg.Command, msg.ResourceID); err != nil {
			return model, model.showToast("Workspaces", err.Error(), component.ToneDanger)
		}
		return model.updatePage(msg)
	case tuipage.UpstreamCommandMsg:
		if err := model.ensureMCPPage(msg.ResourceID); err != nil {
			return model, model.showToast("Upstream", err.Error(), component.ToneDanger)
		}
		return model.updatePage(msg)
	case tuipage.TunnelCommandMsg:
		if err := model.ensureTunnelPage(msg.Command, msg.ResourceID); err != nil {
			return model, model.showToast("Tunnel", err.Error(), component.ToneDanger)
		}
		return model.updatePage(msg)
	case tuipage.RequestCommandMsg:
		if err := model.ensureRequestPage(msg.ResourceID); err != nil {
			return model, model.showToast("Requests", err.Error(), component.ToneDanger)
		}
		return model.updatePage(msg)
	case tuipage.LogsCommandMsg:
		if err := model.ensureLogsPage(); err != nil {
			return model, model.showToast("Logs", err.Error(), component.ToneDanger)
		}
		return model.updatePage(msg)
	case tuipage.SystemCommandMsg:
		if err := model.ensureRuntimePage(); err != nil {
			return model, model.showToast("Runtime", err.Error(), component.ToneDanger)
		}
		return model.updatePage(msg)
	case tuipage.ConfigCommandMsg:
		if err := model.ensureConfigPage(); err != nil {
			return model, model.showToast("Config", err.Error(), component.ToneDanger)
		}
		return model.updatePage(msg)
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
		return model, nil
	case tea.KeyPressMsg:
		if model.toast.id != 0 {
			switch msg.String() {
			case "enter", "esc":
				model.dismissToast()
			}
			return model, nil
		}
		if model.approvalActive() {
			return model.updateApprovalKey(msg)
		}
		if model.navigationGuardActive() {
			return model.updateNavigationGuardKey(msg)
		}
		if model.palette != nil {
			updated, cmd := model.palette.Update(msg)
			model.palette = &updated
			return model, cmd
		}
		if msg.String() == "backspace" && model.currentPage != nil && model.currentPage.InputActive() {
			return model.updatePage(msg)
		}
		_, guardedPage := model.currentPage.(tuipage.NavigationGuardModel)
		if model.currentPage != nil && (model.currentPage.OverlayActive() || model.currentPage.InputActive() && !guardedPage) {
			return model.updatePage(msg)
		}
		if model.router.Current().Kind == RouteHome && model.homeCommands != nil {
			switch msg.String() {
			case "alt+left":
				return model.requestNavigation(navigationIntent{route: cycleHeaderRoute(model.router.Current(), -1), replace: true, restoreRemembered: true})
			case "alt+right":
				return model.requestNavigation(navigationIntent{route: cycleHeaderRoute(model.router.Current(), 1), replace: true, restoreRemembered: true})
			case "esc":
				return model, tea.Quit
			case "ctrl+k":
				return model, nil
			default:
				updated, cmd := model.homeCommands.Update(msg)
				model.homeCommands = &updated
				return model, cmd
			}
		}
		if isCommandsKey(msg) {
			return model, model.openCommands()
		}
		switch msg.String() {
		case "alt+left":
			return model.requestNavigation(navigationIntent{route: cycleHeaderRoute(model.router.Current(), -1), replace: true, restoreRemembered: true})
		case "alt+right":
			return model.requestNavigation(navigationIntent{route: cycleHeaderRoute(model.router.Current(), 1), replace: true, restoreRemembered: true})
		case "esc":
			if intent, ok := model.backNavigationIntent(); ok {
				return model.requestNavigation(intent)
			}
			if model.router.Current().Kind == RouteHome {
				return model, tea.Quit
			}
			return model.requestNavigation(navigationIntent{route: Route{Kind: RouteHome}, replace: true})
		case "backspace":
			if intent, ok := model.backNavigationIntent(); ok {
				return model.requestNavigation(intent)
			}
		default:
			if selected, ok := model.actions.MatchShortcut(msg, actionContext(model.router.Current())); ok {
				cmd, err := model.actions.Execute(model.ctx, selected.ID, actionContext(model.router.Current()))
				if err == nil {
					return model, cmd
				}
			}
		}
	}
	if model.currentPage != nil {
		return model.updatePage(message)
	}
	return model, nil
}

func (model Model) View() tea.View {
	content, targets := model.render()
	if model.palette != nil {
		width, height := model.layoutSize()
		paletteWidth := max(1, min(78, width-4))
		foreground := model.palette.View(paletteWidth)
		x, y := max(0, (width-lipgloss.Width(foreground))/2), max(0, (height-lipgloss.Height(foreground))/2)
		content = centerOverlay(content, foreground, width, height)
		targets = append(targets, model.palette.MouseTargets(x, y, 100, paletteWidth)...)
	}
	if model.navigationGuardActive() && !model.approvalActive() {
		width, height := model.layoutSize()
		modalWidth := max(1, min(64, width-4))
		bodyWidth := component.ModalContentWidth(modalWidth)
		body := strings.Join([]string{
			component.WrapContent(component.Title("Discard changes?"), bodyWidth), "",
			component.WrapContent(component.Muted("Unsaved changes in this editor will be lost."), bodyWidth), "",
			model.navigationConfirm.View(),
			component.WrapContent(component.Muted("Enter confirm · Esc keep editing"), bodyWidth),
		}, "\n")
		foreground := component.Modal(body, modalWidth)
		overlayTargets, x, y := component.CenteredOverlayTargets(foreground, width, height, 0, 0, 149, tea.KeyPressMsg{Code: tea.KeyEscape})
		content = centerOverlay(content, foreground, width, height)
		targets = append(targets, overlayTargets...)
		if rect, ok := component.FindRenderedRect(foreground, model.navigationConfirm.View()); ok {
			targets = append(targets, model.navigationConfirm.MouseTargets(x+rect.X, y+rect.Y, 151)...)
		}
	}
	if model.approvalActive() {
		width, height := model.layoutSize()
		modalWidth, body, viewportHeight := model.approvalDialogViewport(width, height)
		foreground := component.Modal(body, modalWidth)
		overlayTargets, x, y := component.CenteredOverlayTargets(foreground, width, height, 0, 0, 199, tea.KeyPressMsg{Code: tea.KeyEscape})
		content = centerOverlay(content, foreground, width, height)
		targets = append(targets, overlayTargets...)
		buttons := model.approvalButtonsView()
		if buttons != "" {
			if rect, ok := component.FindRenderedRect(foreground, buttons); ok {
				var buttonTargets []component.MouseTarget
				if model.approvalStage == approvalStageChoice {
					buttonTargets = model.approvalChoice.MouseTargets(x+rect.X, y+rect.Y, 201)
				}
				targets = append(targets, buttonTargets...)
			}
		}
		contentWidth := component.ModalContentWidth(modalWidth)
		viewportY := y + 2
		viewportX := x + 3
		targets = append(targets, component.MouseTarget{
			ID: "approval.scroll", Rect: component.Rect{X: viewportX, Y: viewportY, Width: contentWidth, Height: viewportHeight}, Z: 202,
			Handle: func(event component.MouseEvent) tea.Msg {
				switch event.Button {
				case tea.MouseWheelUp:
					return tea.KeyPressMsg{Code: tea.KeyUp}
				case tea.MouseWheelDown:
					return tea.KeyPressMsg{Code: tea.KeyDown}
				default:
					return nil
				}
			},
		})
	}
	if model.toast.id != 0 {
		width, height := model.layoutSize()
		dialog := component.NewToastDialog(model.toast.title, model.toast.message, model.toast.tone)
		modalWidth := max(1, min(72, width-4))
		foreground := component.Modal(dialog.ViewWidth(component.ModalContentWidth(modalWidth)), modalWidth)
		overlayTargets, x, y := component.CenteredOverlayTargets(foreground, width, height, 0, 0, 299, toastCloseMsg{})
		id := model.toast.id
		overlayTargets[0].Handle = func(event component.MouseEvent) tea.Msg {
			if event.Motion {
				return toastHoverMsg{id: id, hovered: false}
			}
			if event.Button == tea.MouseLeft {
				return toastCloseMsg{}
			}
			return nil
		}
		overlayTargets[1].Handle = func(event component.MouseEvent) tea.Msg {
			if event.Motion {
				return toastHoverMsg{id: id, hovered: true}
			}
			return nil
		}
		content = centerOverlay(content, foreground, width, height)
		targets = append(targets, overlayTargets...)
		if rect, ok := component.FindRenderedRect(foreground, dialog.CloseButtonView()); ok {
			targets = append(targets, component.MouseTarget{
				ID: "toast.close", Rect: component.Rect{X: x + rect.X, Y: y + rect.Y, Width: rect.Width, Height: rect.Height}, Z: 301,
				Handle: func(event component.MouseEvent) tea.Msg {
					if event.Motion {
						return toastHoverMsg{id: id, hovered: true}
					}
					if event.Button == tea.MouseLeft {
						return toastCloseMsg{}
					}
					return nil
				},
			})
		}
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "CodeMCP · " + model.router.Current().Title()
	view.MouseMode = tea.MouseModeCellMotion
	view.OnMouse = func(message tea.MouseMsg) tea.Cmd { return component.DispatchMouse(targets, message) }
	return view
}

func (model Model) refreshApprovalsCmd() tea.Cmd {
	list := model.approvalList
	ctx := model.ctx
	if list == nil {
		return nil
	}
	return func() tea.Msg {
		requests, err := list(ctx)
		return approvalSnapshotMsg{requests: requests, err: err}
	}
}

func (model Model) subscribeApprovalsCmd() tea.Cmd {
	subscribe := model.approvalSubscribe
	ctx := model.ctx
	if subscribe == nil {
		return nil
	}
	return func() tea.Msg {
		subscription, snapshot, err := subscribe(ctx)
		return approvalSubscribedMsg{subscription: subscription, snapshot: snapshot, err: err}
	}
}

func (model Model) waitApprovalEventCmd() tea.Cmd {
	subscription := model.approvalSubscription
	if subscription == nil {
		return nil
	}
	return func() tea.Msg {
		event, err := subscription.Next()
		return approvalEventMsg{event: event, err: err}
	}
}

func (model Model) approvalReconnectCmd() tea.Cmd {
	return tea.Tick(approvalReconnectDelay, func(time.Time) tea.Msg { return approvalReconnectMsg{} })
}

func (model Model) approvalTickCmd() tea.Cmd {
	return tea.Tick(approvalClockInterval, func(time.Time) tea.Msg { return approvalClockTickMsg{} })
}

func (model *Model) applyApprovalSnapshot(msg approvalSnapshotMsg) {
	if model == nil || msg.err != nil {
		return
	}
	now := model.approvalTime()
	pending := make([]approval.Request, 0, len(msg.requests))
	for _, request := range msg.requests {
		if request.Status == approval.StatusPending && !approvalRequestExpired(request, now) {
			pending = append(pending, request)
		}
	}
	activeID := model.activeApprovalID()
	if activeID != "" {
		pending = moveApprovalFirst(pending, activeID)
	}
	model.approvals = pending
	if len(pending) == 0 {
		model.resetApprovalDialog()
		return
	}
	if activeID == "" || pending[0].ID != activeID {
		model.openApprovalChoice()
	}
}

func (model *Model) applyApprovalEvent(event approval.Event) {
	if model == nil || event.RequestID == "" {
		return
	}
	switch event.Name {
	case approval.EventApproved, approval.EventDenied, approval.EventExpired, approval.EventCancelled, approval.EventClaimed:
		model.expireApproval(event.RequestID)
	}
}

func (model Model) approvalActive() bool {
	return len(model.approvals) > 0 && model.approvalStage != approvalStageNone
}

func (model Model) activeApproval() (approval.Request, bool) {
	if len(model.approvals) == 0 {
		return approval.Request{}, false
	}
	return model.approvals[0], true
}

func (model Model) activeApprovalID() string {
	request, ok := model.activeApproval()
	if !ok {
		return ""
	}
	return request.ID
}

func (model *Model) openApprovalChoice() {
	if model == nil || len(model.approvals) == 0 {
		return
	}
	model.approvalStage = approvalStageChoice
	model.approvalChoice = component.NewConfirmButtons("Approve", "Deny", false)
	model.approvalApprove = false
	model.approvalSimilar = false
	model.approvalErr = nil
	model.syncApprovalViewport(true)
}

func (model *Model) resetApprovalDialog() {
	if model == nil {
		return
	}
	model.approvalStage = approvalStageNone
	model.approvalChoice = component.ConfirmButtons{}
	model.approvalApprove = false
	model.approvalSimilar = false
	model.approvalErr = nil
	model.approvalViewport.GotoTop()
}

func (model Model) updateApprovalChoice(msg component.ConfirmChoiceMsg) (tea.Model, tea.Cmd) {
	if model.approvalStage == approvalStageChoice {
		model.approvalChoice.Select(msg.Affirmative)
		return model.resolveApprovalSelection(model.approvalChoice.AffirmativeSelected(), false)
	}
	return model, nil
}

func (model Model) updateApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch model.approvalStage {
	case approvalStageChoice:
		switch msg.String() {
		case "a":
			return model.resolveApprovalSelection(true, false)
		case "s":
			request, ok := model.activeApproval()
			if ok && strings.TrimSpace(request.SimilarCommandPattern) != "" {
				return model.resolveApprovalSelection(true, true)
			}
			return model, nil
		case "d":
			return model.resolveApprovalSelection(false, false)
		case "enter":
			return model.resolveApprovalSelection(model.approvalChoice.AffirmativeSelected(), false)
		case "esc":
			return model, nil
		case "j", "down", "k", "up", "pgdown", "pgup":
			updated, cmd := model.approvalViewport.Update(msg)
			model.approvalViewport = updated
			return model, cmd
		default:
			return model, model.approvalChoice.Update(msg)
		}
	case approvalStageResolving:
		return model, nil
	default:
		return model, nil
	}
}

func (model Model) resolveApprovalSelection(approve, similar bool) (tea.Model, tea.Cmd) {
	request, ok := model.activeApproval()
	if !ok || model.approvalResolve == nil {
		model.resetApprovalDialog()
		return model, nil
	}
	if approvalRequestExpired(request, model.approvalTime()) {
		model.expireApproval(request.ID)
		return model, model.refreshApprovalsCmd()
	}
	id, resolve, resolveSimilar, ctx := request.ID, model.approvalResolve, model.approvalResolveSimilar, model.ctx
	model.approvalApprove = approve
	model.approvalSimilar = similar
	model.approvalStage = approvalStageResolving
	model.approvalErr = nil
	return model, func() tea.Msg {
		var err error
		if similar && resolveSimilar != nil {
			_, err = resolveSimilar(ctx, id, approve, true, "")
		} else {
			_, err = resolve(ctx, id, approve, "")
		}
		return approvalResolvedMsg{id: id, approve: approve, err: err}
	}
}

func (model Model) finishApprovalResolution(msg approvalResolvedMsg) (tea.Model, tea.Cmd) {
	if msg.id != model.activeApprovalID() {
		return model, nil
	}
	if msg.err != nil {
		model.openApprovalChoice()
		model.approvalErr = msg.err
		return model, nil
	}
	model.approvals = removeApprovalRequest(model.approvals, msg.id)
	action := "Denied"
	if msg.approve {
		action = "Approved"
		if model.approvalSimilar {
			action = "Approved similar commands for all MCP sessions (1h)"
		}
	}
	if len(model.approvals) == 0 {
		model.resetApprovalDialog()
	} else {
		model.openApprovalChoice()
	}
	return model, model.showToast("Approval request", action+" "+msg.id, component.ToneSuccess)
}

func (model Model) approvalButtonsView() string {
	if model.approvalStage == approvalStageChoice {
		return model.approvalChoice.View()
	}
	return ""
}

func (model Model) approvalDialogView(width int) string {
	content := model.approvalDialogContent(width)
	footer := model.approvalDialogFooter(width)
	if footer == "" {
		return content
	}
	return content + "\n\n" + footer
}

func (model Model) approvalDialogContent(width int) string {
	request, ok := model.activeApproval()
	if !ok {
		return ""
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = request.ID
	}
	lines := []string{
		component.WrapContent(component.Title("Approval request"), width), "",
		component.WrapKeyValue("Title", title, width), component.WrapKeyValue("Request", request.ID, width), component.WrapKeyValue("Workspace", request.WorkspaceID, width), component.WrapKeyValue("Tool", request.TargetTool, width),
	}
	if request.Source != "" {
		lines = append(lines, component.WrapKeyValue("Source", request.Source, width))
	}
	if request.GuardCode != "" {
		lines = append(lines, component.WrapKeyValue("Guard", string(request.GuardCode), width))
	}
	if !request.ExpiresAt.IsZero() {
		expires := request.ExpiresAt.Local().Format("15:04:05")
		countdown := approvalCountdown(request.ExpiresAt, model.approvalTime())
		lines = append(lines, component.WrapKeyValue("Expires in", countdown+" · "+expires, width))
	}
	if model.approvalErr != nil {
		lines = append(lines, "", component.BannerWidth(model.approvalErr.Error(), component.ToneDanger, width))
	}
	if command := strings.TrimSpace(request.Command); command != "" {
		lines = append(lines, "", component.Label("Command"), component.RenderCodeBlock(command, "bash", width))
	}
	if pattern := strings.TrimSpace(request.SimilarCommandPattern); pattern != "" {
		lines = append(lines, "", component.WrapKeyValue("Similar pattern", pattern, width))
		ttl := approval.DefaultRuntimeGrantTTL.String()
		lines = append(lines, component.WrapKeyValue("Similar grant", "all MCP sessions for "+ttl+" (revocable)", width))
	}
	lines = append(lines, "", component.Label("Arguments"), component.RenderCodeBlock(approvalArguments(request.Arguments), "json", width))
	return strings.Join(lines, "\n")
}

func (model Model) approvalDialogFooter(width int) string {
	lines := []string{}
	switch model.approvalStage {
	case approvalStageChoice:
		hint := "j/k scroll · a approve · d deny · ←/→ choose · Enter submit"
		if request, ok := model.activeApproval(); ok && strings.TrimSpace(request.SimilarCommandPattern) != "" {
			hint = "j/k scroll · a approve once · s allow similar for all MCP sessions (1h) · d deny · ←/→ choose · Enter submit"
		}
		lines = append(lines, model.approvalChoice.View(), component.WrapContent(component.Muted(hint), width))
	case approvalStageResolving:
		lines = append(lines, component.WrapContent(component.Muted("Resolving request..."), width))
	}
	if len(model.approvals) > 1 {
		lines = append(lines, "", component.WrapContent(component.Muted(fmt.Sprintf("%d more pending request(s)", len(model.approvals)-1)), width))
	}
	return strings.Join(lines, "\n")
}

func (model *Model) syncApprovalViewport(reset bool) {
	if model == nil || !model.approvalActive() {
		return
	}
	width, height := model.layoutSize()
	modalWidth := max(1, min(120, width-4))
	contentWidth := component.ModalContentWidth(modalWidth)
	footer := model.approvalDialogFooter(contentWidth)
	bodyHeight := max(1, height-6)
	viewportHeight := max(1, bodyHeight-lipgloss.Height(footer)-1)
	model.approvalViewport.SetWidth(contentWidth)
	model.approvalViewport.SetHeight(viewportHeight)
	model.approvalViewport.SetContent(model.approvalDialogContent(contentWidth))
	if reset {
		model.approvalViewport.GotoTop()
	}
}

func (model Model) approvalDialogViewport(width, height int) (int, string, int) {
	modalWidth := max(1, min(120, width-4))
	contentWidth := component.ModalContentWidth(modalWidth)
	footer := model.approvalDialogFooter(contentWidth)
	bodyHeight := max(1, height-6)
	viewportHeight := max(1, bodyHeight-lipgloss.Height(footer)-1)
	view := model.approvalViewport
	view.SetWidth(contentWidth)
	view.SetHeight(viewportHeight)
	view.SetContent(model.approvalDialogContent(contentWidth))
	body := view.View()
	if footer != "" {
		body += "\n" + footer
	}
	return modalWidth, body, viewportHeight
}

func approvalArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return component.Muted("None")
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		if data, err := json.MarshalIndent(value, "", "  "); err == nil {
			return string(data)
		}
	}
	return string(raw)
}

func moveApprovalFirst(requests []approval.Request, id string) []approval.Request {
	for index, request := range requests {
		if request.ID != id || index == 0 {
			continue
		}
		active := requests[index]
		copy(requests[1:index+1], requests[:index])
		requests[0] = active
		break
	}
	return requests
}

func removeApprovalRequest(requests []approval.Request, id string) []approval.Request {
	result := requests[:0]
	for _, request := range requests {
		if request.ID != id {
			result = append(result, request)
		}
	}
	return result
}

func (model Model) approvalTime() time.Time {
	if model.approvalNow != nil {
		return model.approvalNow()
	}
	return time.Now()
}

func (model *Model) expireElapsedApprovals(now time.Time) {
	if model == nil || len(model.approvals) == 0 {
		return
	}
	remaining := model.approvals[:0]
	activeID := model.activeApprovalID()
	activeExpired := false
	for _, request := range model.approvals {
		if approvalRequestExpired(request, now) {
			activeExpired = activeExpired || request.ID == activeID
			continue
		}
		remaining = append(remaining, request)
	}
	model.approvals = remaining
	if len(remaining) == 0 {
		model.resetApprovalDialog()
		return
	}
	if activeExpired {
		model.openApprovalChoice()
	}
}

func (model *Model) expireApproval(id string) {
	if model == nil || id == "" {
		return
	}
	wasActive := model.activeApprovalID() == id
	model.approvals = removeApprovalRequest(model.approvals, id)
	if len(model.approvals) == 0 {
		model.resetApprovalDialog()
		return
	}
	if wasActive {
		model.openApprovalChoice()
	}
}

func approvalRequestExpired(request approval.Request, now time.Time) bool {
	return !request.ExpiresAt.IsZero() && !now.Before(request.ExpiresAt)
}

func approvalCountdown(expiresAt, now time.Time) string {
	if expiresAt.IsZero() || !now.Before(expiresAt) {
		return "00:00:00"
	}
	remaining := expiresAt.Sub(now)
	seconds := int64((remaining + time.Second - 1) / time.Second)
	hours := seconds / 3600
	minutes := seconds % 3600 / 60
	seconds %= 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func (model *Model) openCommands() tea.Cmd {
	if model == nil {
		return nil
	}
	context := actionContext(model.router.Current())
	actions, resources, err := model.commandActions(context)
	if err != nil {
		return model.showToast("Commands", err.Error(), component.ToneDanger)
	}
	value := palette.NewWithOptions(actions, context, palette.Options{Title: "Commands", Hint: "Ctrl+K", Placeholder: "Type a command or resource", Recent: model.state.RecentActions})
	model.palette = &value
	model.overlay = overlayCommands
	model.commandResources = resources
	return nil
}

func isCommandsKey(message tea.KeyPressMsg) bool {
	return message.String() == "ctrl+k"
}

func (model Model) commandActions(context action.Context) ([]action.Action, map[string]quickopen.Resource, error) {
	actions := model.actions.Actions(context)
	resources, err := loadQuickOpenResources()
	if err != nil {
		return actions, nil, err
	}
	resourceActions, index := quickopen.Actions(resources)
	return append(actions, resourceActions...), index, nil
}

func (model *Model) closeOverlay() {
	if model == nil {
		return
	}
	model.palette = nil
	model.overlay = overlayNone
	model.commandResources = nil
}

func (model *Model) recordRecent(id string) tea.Cmd {
	if model == nil {
		return nil
	}
	tuistate.RecordRecent(&model.state, id)
	if model.stateRoot != "" {
		if err := tuistate.Save(model.stateRoot, model.state); err != nil {
			return model.showToast("TUI state", err.Error(), component.ToneDanger)
		}
	}
	return nil
}

func (model *Model) navigate(route Route) {
	if model == nil {
		return
	}
	from := model.router.Current()
	model.captureCurrentView()
	model.prepareViewStateForNavigation(from, route)
	model.router.Navigate(route)
	model.loadPage(route)
	if route.Kind == RouteHome || model.currentPage != nil {
		model.rememberStableRoute(route)
	}
}

func (model Model) navigationGuardActive() bool { return model.pendingNavigation != nil }

func (model Model) requestNavigation(intent navigationIntent) (tea.Model, tea.Cmd) {
	if intent.restoreRemembered {
		intent.route = model.resolveRememberedRoute(intent.route)
	}
	if !intent.quit && intent.route == model.router.Current() {
		return model, nil
	}
	if page, ok := model.currentPage.(tuipage.NavigationGuardModel); ok {
		if page.Submitting() {
			return model, nil
		}
		if page.Dirty() {
			model.pendingNavigation = &intent
			model.navigationConfirm = component.NewConfirmButtons("Discard", "Keep editing", false)
			return model, nil
		}
	}
	return model.performNavigation(intent)
}

func (model Model) performNavigation(intent navigationIntent) (tea.Model, tea.Cmd) {
	model.pendingNavigation = nil
	model.navigationConfirm = component.ConfirmButtons{}
	if intent.quit {
		return model, tea.Quit
	}
	if intent.replace {
		model.switchPage(intent.route)
	} else {
		model.navigate(intent.route)
	}
	return model, model.initCurrentPage()
}

func (model Model) resolveNavigationChoice(discard bool) (tea.Model, tea.Cmd) {
	if model.pendingNavigation == nil {
		return model, nil
	}
	if !discard {
		model.pendingNavigation = nil
		model.navigationConfirm = component.ConfirmButtons{}
		return model, nil
	}
	intent := *model.pendingNavigation
	return model.performNavigation(intent)
}

func (model Model) updateNavigationGuardKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc":
		model.pendingNavigation = nil
		model.navigationConfirm = component.ConfirmButtons{}
		return model, nil
	case "enter":
		return model.resolveNavigationChoice(model.navigationConfirm.AffirmativeSelected())
	default:
		return model, model.navigationConfirm.Update(message)
	}
}

func (model Model) backNavigationIntent() (navigationIntent, bool) {
	router := model.router
	if !router.Back() {
		return navigationIntent{}, false
	}
	return navigationIntent{route: router.Current()}, true
}

func (model *Model) switchPage(route Route) {
	if model == nil {
		return
	}
	from := model.router.Current()
	model.captureCurrentView()
	model.prepareViewStateForNavigation(from, route)
	model.router.Switch(route)
	model.loadPage(route)
	if route.Kind == RouteHome || model.currentPage != nil {
		model.rememberStableRoute(route)
	}
}

func (model *Model) prepareViewStateForNavigation(from, to Route) {
	if model == nil || model.pageViewStates == nil || headerOwner(from.Kind) == RouteLogs || headerOwner(to.Kind) != RouteLogs {
		return
	}
	state, ok := model.pageViewStates[RouteLogs].(tuipage.LogsSessionViewState)
	if !ok {
		return
	}
	state.RuntimePaused, state.RuntimeSelectedID = false, ""
	state.ExecutionPaused, state.ExecutionYOffset = false, 0
	state.ToolCallPaused, state.ToolCallYOffset = false, 0
	model.pageViewStates[RouteLogs] = state
}

func (model *Model) captureCurrentView() {
	if model == nil {
		return
	}
	model.rememberStableRoute(model.router.Current())
	model.captureCurrentPageViewState()
}

func (model *Model) rememberStableRoute(route Route) {
	if model == nil || route.Kind == RouteHome || route.Action != "" {
		return
	}
	if guard, ok := model.currentPage.(tuipage.NavigationGuardModel); ok && (guard.Dirty() || guard.Submitting()) {
		return
	}
	if model.lastRoutes == nil {
		model.lastRoutes = map[RouteKind]Route{}
	}
	model.lastRoutes[headerOwner(route.Kind)] = route
}

func (model Model) resolveRememberedRoute(route Route) Route {
	if route.Kind == RouteHome || route.ResourceID != "" || route.Section != "" || route.Action != "" || route.Mode != "" || model.lastRoutes == nil {
		return route
	}
	if remembered, ok := model.lastRoutes[headerOwner(route.Kind)]; ok {
		return remembered
	}
	return route
}

func (model *Model) captureCurrentPageViewState() {
	if model == nil || model.currentPage == nil || model.router.Current().Action != "" {
		return
	}
	page, ok := model.currentPage.(tuipage.SessionViewStateModel)
	if !ok {
		return
	}
	if model.pageViewStates == nil {
		model.pageViewStates = map[RouteKind]any{}
	}
	model.pageViewStates[headerOwner(model.router.Current().Kind)] = page.SessionViewState()
}

func (model *Model) restoreCurrentPageViewState(route Route) {
	if model == nil || model.currentPage == nil || route.Action != "" || model.pageViewStates == nil {
		return
	}
	state, ok := model.pageViewStates[headerOwner(route.Kind)]
	if !ok {
		return
	}
	if logsState, ok := state.(tuipage.LogsSessionViewState); ok {
		logsState.Tab = "runtime"
		switch route.Kind {
		case RouteLogsExec:
			logsState.Tab = "command-execution"
		case RouteLogsTools:
			logsState.Tab = "tool-calls"
		}
		state = logsState
	}
	if page, ok := model.currentPage.(tuipage.SessionViewStateModel); ok {
		page.RestoreSessionViewState(state)
	}
}

func (model *Model) loadPage(route Route) {
	if model == nil {
		return
	}
	if page, ok := model.currentPage.(interface{ Close() }); ok {
		page.Close()
	}
	model.currentPage = nil
	model.homeCommands = nil
	if route.Kind == RouteHome {
		model.resetHomeCommands()
		return
	}
	var value tuipage.Model
	var err error
	switch route.Kind {
	case RouteWorkspaces:
		value, err = tuipage.NewWorkspacesRouteWithContextSessionAction(model.ctx, route.ResourceID, route.Section, route.Action, model.workspaceContextSession(route.ResourceID))
	case RouteContainers:
		value, err = tuipage.NewContainersRouteAction(model.ctx, route.ResourceID, route.Section, route.Action)
	case RouteMCP:
		if route.Action != "" {
			value, err = tuipage.NewMCPRouteAction(model.ctx, route.ResourceID, route.Section, route.Action)
		} else {
			value, err = tuipage.NewMCPRoute(model.ctx, route.ResourceID, route.Section)
		}
	case RouteTunnel:
		value, err = tuipage.NewTunnelDashboardRoute(model.ctx, route.Section, route.Action)
	case RouteTunnels:
		value, err = tuipage.NewManagedTunnelsRouteAction(model.ctx, route.ResourceID, route.Section, route.Action)
	case RouteRequests:
		value, err = tuipage.NewRequestsRouteAction(model.ctx, route.Mode, route.ResourceID, route.Section, route.Action)
	case RouteCompletions:
		value, err = tuipage.NewCompletionsRoute(model.ctx, route.ResourceID)
	case RouteLogs:
		value, err = tuipage.NewLogsRouteAction(model.ctx, route.ResourceID, route.Section, route.Action)
	case RouteLogsExec:
		value, err = tuipage.NewCommandExecutionLogsRoute(model.ctx, route.ResourceID)
	case RouteLogsTools:
		value, err = tuipage.NewToolCallLogsRoute(model.ctx, route.ResourceID)
	case RouteRuntime:
		value, err = tuipage.NewRuntimeRouteAction(model.ctx, route.ResourceID, route.Action)
	case RouteAbout:
		value, err = tuipage.NewAbout(model.ctx)
	case RouteGuide:
		value, err = tuipage.NewGuide(model.ctx, route.ResourceID)
	case RouteConfig:
		value, err = tuipage.NewConfigRouteAction(model.ctx, route.ResourceID, route.Section, route.Action)
	case RouteInstruction:
		value, err = tuipage.NewInstructionRouteAction(model.ctx, route.Section, route.ResourceID, route.Action)
	}
	if err != nil {
		model.notice = err.Error()
		return
	}
	model.currentPage = value
	model.restoreCurrentPageViewState(route)
	if model.currentPage != nil && model.width > 0 && model.height > 0 {
		metrics := model.frameMetrics(model.width, model.height)
		updated, _ := model.currentPage.Update(tea.WindowSizeMsg{Width: metrics.contentWidth, Height: metrics.bodyHeight})
		model.currentPage = updated
	}
}

func (model *Model) workspaceContextSession(workspaceID string) *tuipage.WorkspaceContextSession {
	if model == nil || strings.TrimSpace(workspaceID) == "" {
		return nil
	}
	if model.workspaceContexts == nil {
		model.workspaceContexts = map[string]*tuipage.WorkspaceContextSession{}
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if session := model.workspaceContexts[workspaceID]; session != nil {
		return session
	}
	session := tuipage.NewWorkspaceContextSession()
	model.workspaceContexts[workspaceID] = session
	return session
}

func (model *Model) ensureLogsPage() error {
	if model.router.Current().Kind != RouteLogs {
		model.navigate(Route{Kind: RouteLogs})
	}
	if model.currentPage == nil {
		return fmt.Errorf("logs viewer is unavailable")
	}
	return nil
}

func (model *Model) ensureRuntimePage() error {
	if model.router.Current().Kind != RouteRuntime {
		model.navigate(Route{Kind: RouteRuntime})
	}
	if model.currentPage == nil {
		return fmt.Errorf("runtime/system page is unavailable")
	}
	return nil
}

func (model Model) initCurrentPage() tea.Cmd {
	if model.currentPage == nil {
		return nil
	}
	return model.currentPage.Init()
}

func (model *Model) ensureMCPPage(resourceID string) error {
	if model.router.Current().Kind != RouteMCP || (resourceID != "" && model.router.Current().ResourceID != resourceID) {
		model.navigate(Route{Kind: RouteMCP, ResourceID: resourceID})
	}
	if model.currentPage == nil {
		return fmt.Errorf("MCP page is unavailable")
	}
	return nil
}

func (model *Model) ensureTunnelPage(command tuipage.TunnelCommand, resourceID string) error {
	managed := command == tuipage.TunnelManagedRefresh || command == tuipage.TunnelManagedCreate || command == tuipage.TunnelManagedUpdate || command == tuipage.TunnelManagedConfigure || command == tuipage.TunnelManagedDelete
	kind := RouteTunnel
	if managed {
		kind = RouteTunnels
	}
	if model.router.Current().Kind != kind || (resourceID != "" && model.router.Current().ResourceID != resourceID) {
		model.navigate(Route{Kind: kind, ResourceID: resourceID})
	}
	if model.currentPage == nil {
		return fmt.Errorf("tunnel page is unavailable")
	}
	return nil
}

func (model *Model) ensureRequestPage(resourceID string) error {
	if model.router.Current().Kind != RouteRequests || (resourceID != "" && model.router.Current().ResourceID != resourceID) {
		model.navigate(Route{Kind: RouteRequests, ResourceID: resourceID})
	}
	if model.currentPage == nil {
		return fmt.Errorf("approval inbox is unavailable")
	}
	return nil
}

func (model *Model) ensureConfigPage() error {
	if model.router.Current().Kind != RouteConfig {
		model.navigate(Route{Kind: RouteConfig})
	}
	if model.currentPage == nil {
		return fmt.Errorf("config center is unavailable")
	}
	return nil
}

func (model Model) updatePage(message tea.Msg) (tea.Model, tea.Cmd) {
	if model.currentPage == nil {
		return model, nil
	}
	before := pageNotice(model.currentPage)
	updated, cmd := model.currentPage.Update(message)
	model.currentPage = updated
	after := pageNotice(model.currentPage)
	if after != "" && after != before {
		if page, ok := model.currentPage.(tuipage.ToastNoticeModel); ok && !page.ShouldToastNotice() {
			return model, cmd
		}
		if page, ok := model.currentPage.(tuipage.NoticeModel); ok {
			page.SetNotice("")
		}
		return model, tea.Batch(cmd, model.showToast(model.router.Current().Title(), after, component.ToneNeutral))
	}
	return model, cmd
}

func pageNotice(value tuipage.Model) string {
	if page, ok := value.(tuipage.NoticeModel); ok {
		return strings.TrimSpace(page.Notice())
	}
	return ""
}

func (model *Model) showToast(title, message string, tone component.Tone) tea.Cmd {
	title = strings.TrimSpace(title)
	message = strings.TrimSpace(message)
	if title == "" && message == "" {
		return nil
	}
	model.toastSeq++
	model.toast = toastState{id: model.toastSeq, timer: 1, title: title, message: message, tone: tone}
	return model.toastTimerCmd()
}

func (model *Model) toastTimerCmd() tea.Cmd {
	if model == nil || model.toast.id == 0 || model.toast.hovered {
		return nil
	}
	id, timer := model.toast.id, model.toast.timer
	return tea.Tick(toastDuration, func(time.Time) tea.Msg { return toastDismissMsg{id: id, timer: timer} })
}

func (model *Model) dismissToast() {
	if model != nil {
		model.toast = toastState{}
	}
}

func (model *Model) ensureWorkspacePage(command tuipage.WorkspaceCommand, resourceID string) error {
	container := command == tuipage.WorkspaceContainerCreate || command == tuipage.WorkspaceContainerRename || command == tuipage.WorkspaceContainerDelete || command == tuipage.WorkspaceContainerMembers
	kind := RouteWorkspaces
	if container {
		kind = RouteContainers
	}
	if model.router.Current().Kind != kind || (resourceID != "" && model.router.Current().ResourceID != resourceID) {
		model.navigate(Route{Kind: kind, ResourceID: resourceID})
	}
	if model.currentPage == nil {
		return fmt.Errorf("workspace page is unavailable")
	}
	return nil
}

type mousePage interface {
	MouseTargets(originX, originY, z int) []component.MouseTarget
}

type frameMetrics struct {
	contentWidth   int
	contentX       int
	breadcrumbY    int
	bodyY          int
	bodyHeight     int
	showNavbar     bool
	showBreadcrumb bool
	showFooter     bool
}

func (model Model) render() (string, []component.MouseTarget) {
	width, height := model.layoutSize()
	if width <= 0 || height <= 0 {
		return "", nil
	}
	metrics := model.frameMetrics(width, height)
	targets := []component.MouseTarget{}
	border := lipgloss.NewStyle().Foreground(model.theme.border.GetBorderLeftForeground())
	lines := make([]string, 0, height)
	lines = append(lines, model.topBorder(width, border))
	if metrics.showNavbar {
		header, headerTargets := model.header(metrics.contentWidth, metrics.contentX, 1)
		targets = append(targets, headerTargets...)
		lines = append(lines, frameLine(header, width, border))
		lines = append(lines, frameDivider(width, border))
	}
	if metrics.showBreadcrumb {
		breadcrumb, breadcrumbTargets := model.breadcrumb(metrics.contentWidth, metrics.contentX, metrics.breadcrumbY)
		targets = append(targets, breadcrumbTargets...)
		lines = append(lines, frameLine(breadcrumb, width, border))
		lines = append(lines, frameLine("", width, border))
	}
	body := fitFrameContent(model.page(metrics.contentWidth, metrics.bodyHeight), metrics.contentWidth, metrics.bodyHeight)
	for _, line := range body {
		lines = append(lines, frameLine(line, width, border))
	}
	if metrics.showFooter {
		lines = append(lines, frameDivider(width, border))
		for _, footerLine := range strings.Split(model.shortcutFooterWidth(metrics.contentWidth), "\n") {
			lines = append(lines, frameLine(footerLine, width, border))
		}
	}
	if len(lines) < height {
		lines = append(lines, bottomBorder(width, border))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	if page, ok := model.currentPage.(mousePage); ok {
		targets = append(targets, page.MouseTargets(metrics.contentX, metrics.bodyY, 10)...)
	}
	if model.router.Current().Kind == RouteHome && model.homeCommands != nil {
		panelWidth := homeCommandPanelWidth(metrics.contentWidth)
		panel := model.homeCommands.View(panelWidth)
		x := metrics.contentX + max(0, (metrics.contentWidth-lipgloss.Width(panel))/2)
		y := metrics.bodyY + max(0, (metrics.bodyHeight-lipgloss.Height(panel))/2)
		targets = append(targets, model.homeCommands.MouseTargets(x, y, 10, panelWidth)...)
	}
	for index := range lines {
		lines[index] = fitTerminalLine(lines[index], width)
	}
	return strings.Join(lines, "\n"), targets
}

func (model Model) topBorder(width int, border lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return border.Render("─")
	}
	if width == 2 {
		return border.Render("╭╮")
	}
	label := " " + model.theme.title.Render("CodeMCP") + " "
	used := 2 + lipgloss.Width(label) + 1
	if used > width {
		return border.Render("╭" + strings.Repeat("─", width-2) + "╮")
	}
	return border.Render("╭─") + label + border.Render(strings.Repeat("─", max(0, width-used))+"╮")
}

func bottomBorder(width int, border lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return border.Render("─")
	}
	if width == 2 {
		return border.Render("╰╯")
	}
	return border.Render("╰" + strings.Repeat("─", width-2) + "╯")
}

func (model Model) shortcutFooter() string {
	width, _ := model.layoutSize()
	width, _ = frameContentMetrics(width)
	return model.shortcutFooterWidth(width)
}

func (model Model) shortcutFooterWidth(width int) string {
	if width <= 0 {
		return ""
	}
	bindings := []key.Binding{
		component.Binding([]string{"ctrl+k"}, "ctrl+k", "commands"),
		component.Binding([]string{"alt+left", "alt+right"}, "alt+←/→", "pages"),
	}
	if model.router.Current().Kind == RouteHome {
		bindings = append(bindings, component.Binding([]string{"esc"}, "esc", "quit"))
	} else if len(model.router.stack) > 1 {
		bindings = append(bindings, component.Binding([]string{"esc"}, "esc", "back"))
	} else {
		bindings = append(bindings, component.Binding([]string{"esc"}, "esc", "home"))
	}
	return component.DefaultHelp(width, bindings...)
}

func (model Model) header(width, originX, originY int) (string, []component.MouseTarget) {
	owner := headerOwner(model.router.Current().Kind)
	compact := !headerFullLabelsFit(width)
	parts := make([]string, 0, len(headerPages)*2-1)
	targets := make([]component.MouseTarget, 0, len(headerPages))
	divider := component.Muted("│")
	dividerWidth := lipgloss.Width(divider)
	itemsWidth := max(0, width-dividerWidth*(len(headerPages)-1))
	x := 0
	for index, page := range headerPages {
		if index > 0 {
			parts = append(parts, divider)
			x += dividerWidth
		}
		style := component.NavItemStyle(page.Kind == owner)
		cellWidth := itemsWidth / len(headerPages)
		if index < itemsWidth%len(headerPages) {
			cellWidth++
		}
		label := page.Label
		if compact && page.CompactLabel != "" {
			label = page.CompactLabel
		}
		label = ansi.Truncate(label, max(1, cellWidth), "")
		button := style.Padding(0).Width(cellWidth).Align(lipgloss.Center).Render(label)
		kind := page.Kind
		targets = append(targets, component.MouseTarget{
			ID: "app.header." + string(kind), Rect: component.Rect{X: originX + x, Y: originY, Width: cellWidth, Height: 1}, Z: 1,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return navigateMsg{route: Route{Kind: kind}, sibling: true}
			},
		})
		parts = append(parts, button)
		x += cellWidth
	}
	return fitFrameLine(strings.Join(parts, ""), width), targets
}

func (model Model) breadcrumb(width, originX, originY int) (string, []component.MouseTarget) {
	routes, labels := routeBreadcrumb(model.router.Current())
	if len(routes) < 2 {
		return "", nil
	}
	view, spans := component.BreadcrumbLayout(labels, width)
	targets := make([]component.MouseTarget, 0, len(spans)-1)
	for _, span := range spans {
		if span.Index < 0 || span.Index >= len(routes)-1 {
			continue
		}
		route := routes[span.Index]
		targets = append(targets, component.MouseTarget{
			ID: "app.breadcrumb", Rect: component.Rect{X: originX + span.X, Y: originY, Width: span.Width, Height: 1}, Z: 2,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return navigateMsg{route: route}
			},
		})
	}
	return fitFrameLine(view, width), targets
}

func (model Model) page(width, height int) string {
	if model.currentPage != nil {
		return model.currentPage.View(width, height)
	}
	route := model.router.Current()
	if route.Kind == RouteHome {
		return model.homeView(width, height)
	}
	description := component.WrapContent(model.theme.muted.Render(routeDescription(route)), width)
	notice := ""
	if model.notice != "" {
		notice = "\n\n" + component.WrapContent(model.theme.muted.Render(model.notice), width)
	}
	ready := component.WrapContent(model.theme.subtle.Render("Command Center shell is ready. Domain actions will be added through the shared action registry."), width)
	return component.PageTitle(route.Title(), width) + "\n" + description + notice + "\n\n" + ready
}

func (model Model) homeView(width, height int) string {
	if model.homeCommands == nil {
		return component.CenterLayout(component.Muted("Command panel unavailable"), width, height)
	}
	return component.CenterLayout(model.homeCommands.View(homeCommandPanelWidth(width)), width, height)
}

func homeCommandPanelWidth(width int) int {
	if width <= 0 {
		return 72
	}
	return max(1, min(78, width-4))
}

func (model *Model) resetHomeCommands() {
	if model == nil {
		return
	}
	ctx := actionContext(Route{Kind: RouteHome})
	actions, resources, err := model.commandActions(ctx)
	if err != nil {
		actions = model.actions.Actions(ctx)
		resources = nil
	}
	value := palette.NewWithOptions(actions, ctx, palette.Options{
		Title: "Commands", Hint: "Ctrl+K", Placeholder: "Type a command or resource",
		Footer: "↑/↓ navigate  ·  Enter run  ·  Esc exit  ·  Alt+←/→ pages", Recent: model.state.RecentActions,
	})
	model.homeCommands = &value
	model.commandResources = resources
}

func (model Model) layoutSize() (int, int) {
	width, height := model.width, model.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func frameContentMetrics(width int) (contentWidth, originX int) {
	switch {
	case width >= 4:
		return width - 4, 2
	case width == 3:
		return 1, 1
	default:
		return 0, 0
	}
}

func (model Model) frameMetrics(width, height int) frameMetrics {
	contentWidth, contentX := frameContentMetrics(width)
	showNavbar := model.showNavbar(contentWidth, height)
	fixedHeight := 2
	bodyY := 1
	if showNavbar {
		fixedHeight += 2
		bodyY = 3
	}
	showBreadcrumb := len(routeStack(model.router.Current())) > 1 && contentWidth > 0 && height-fixedHeight > 2
	breadcrumbY := bodyY
	if showBreadcrumb {
		fixedHeight += 2
		bodyY += 2
	}
	showFooter := height >= 6 && contentWidth >= 16
	if showFooter {
		footerHeight := lipgloss.Height(model.shortcutFooterWidth(contentWidth))
		footerCost := 1 + footerHeight
		if footerHeight <= 0 || height-fixedHeight-footerCost < 1 {
			showFooter = false
		} else {
			fixedHeight += footerCost
		}
	}
	return frameMetrics{
		contentWidth:   contentWidth,
		contentX:       contentX,
		breadcrumbY:    breadcrumbY,
		bodyY:          bodyY,
		bodyHeight:     max(0, height-fixedHeight),
		showNavbar:     showNavbar,
		showBreadcrumb: showBreadcrumb,
		showFooter:     showFooter,
	}
}

func (model Model) showNavbar(contentWidth, height int) bool {
	if height < navbarMinHeight || contentWidth <= 0 || len(headerPages) == 0 {
		return false
	}
	minCellWidth := max(0, contentWidth-(len(headerPages)-1)) / len(headerPages)
	maxLabelWidth := 0
	for _, page := range headerPages {
		label := page.CompactLabel
		if label == "" {
			label = page.Label
		}
		maxLabelWidth = max(maxLabelWidth, lipgloss.Width(label))
	}
	return minCellWidth >= maxLabelWidth
}

func headerFullLabelsFit(width int) bool {
	if width <= 0 || len(headerPages) == 0 {
		return false
	}
	minCellWidth := max(0, width-(len(headerPages)-1)) / len(headerPages)
	for _, page := range headerPages {
		if lipgloss.Width(page.Label) > minCellWidth {
			return false
		}
	}
	return true
}

func frameLine(content string, width int, border lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return border.Render("│")
	}
	if width == 2 {
		return border.Render("││")
	}
	if width == 3 {
		return border.Render("│") + fitFrameLine(content, 1) + border.Render("│")
	}
	return border.Render("│") + " " + fitFrameLine(content, width-4) + " " + border.Render("│")
}

func frameDivider(width int, border lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return border.Render("─")
	}
	if width == 2 {
		return border.Render("├┤")
	}
	return border.Render("├" + strings.Repeat("─", width-2) + "┤")
}

func fitFrameContent(content string, width, height int) []string {
	lines := strings.Split(content, "\n")
	result := make([]string, height)
	for index := range result {
		if index < len(lines) {
			result[index] = fitFrameLine(lines[index], width)
		} else {
			result[index] = strings.Repeat(" ", width)
		}
	}
	return result
}

func fitFrameLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	return line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
}

func fitTerminalLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	return line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
}

func routeDescription(route Route) string {
	if route.ResourceID != "" {
		return fmt.Sprintf("Deep-linked resource: %s", route.ResourceID)
	}
	switch route.Kind {
	case RouteHome:
		return "Keyboard-first command center for CodeMCP."
	case RouteWorkspaces:
		return "Browse registered workspaces and workspace containers."
	case RouteContainers:
		return "Browse the Containers tab inside Workspaces."
	case RouteMCP:
		return "Manage configured Upstream servers."
	case RouteTunnel:
		return "Manage runtime and OpenAI Secure MCP Tunnel state."
	case RouteTunnels:
		return "Browse and manage tunnels available through the OpenAI Tunnel Management API."
	case RouteRequests:
		return "Review control approval requests."
	case RouteCompletions:
		return "Inspect durable agent completion history and live accepted completion events."
	case RouteLogs:
		return "Inspect runtime history and live events."
	case RouteLogsExec:
		return "Inspect live command execution output."
	case RouteConfig:
		return "Browse and manage validated runtime configuration."
	case RouteInstruction:
		return "Manage global context, rules, and detected instruction sources."
	case RouteRuntime:
		return "Inspect and control the local managed runtime."
	case RouteAbout:
		return "Build and runtime information."
	case RouteGuide:
		return "Browse embedded TUI documentation by topic."
	default:
		return ""
	}
}
