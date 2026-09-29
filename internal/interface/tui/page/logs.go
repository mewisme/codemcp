package page

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/logger"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
)

const (
	logsBrowserRecordCap = 1024
	logsTimelineEventCap = 1024
)
const logsDefaultTail = 200
const logsReconnectDelay = 2 * time.Second

type LogsCommand string

const (
	LogsRefresh LogsCommand = "logs.refresh"
	LogsFilter  LogsCommand = "logs.filter"
	LogsToggle  LogsCommand = "logs.toggle"
	LogsInfo    LogsCommand = "logs.info"
	LogsClear   LogsCommand = "logs.clear"
)

type LogsCommandMsg struct{ Command LogsCommand }

type logsOverlay uint8

const (
	logsOverlayNone logsOverlay = iota
	logsOverlayConfirm
	logsOverlayInfo
	logsOverlayOperation
)

type logsBootstrapMsg struct {
	generation uint64
	snapshot   application.LogsSnapshot
	info       application.LogsInfo
	state      runtimecontrol.State
	historyErr error
	infoErr    error
}

type logsStreamOpenMsg struct {
	generation uint64
	stream     *runtimecontrol.EventStream
	state      runtimecontrol.State
	err        error
}

type logsStreamEventMsg struct {
	generation uint64
	event      runtimeevent.Event
	err        error
}

type logsReconnectMsg uint64

type logsClearMsg struct {
	operation uint64
	err       error
}

type LogsPage struct {
	ctx               context.Context
	resourceID        string
	section           string
	action            string
	tab               logsTab
	view              logsDisplayView
	timeline          logsTimelineState
	runtimeClear      map[string]uint64
	executionClear    uint64
	executionClearSet bool
	toolCallClear     uint64
	runtimeScope      logsScopeState
	exec              logsExecutionFeed
	tools             logsToolCallFeed
	detailReady       bool
	modeDialog        *logsModeDialog
	cancel            context.CancelFunc
	browser           component.Browser
	help              component.HelpFooter
	detail            component.DetailPage
	events            []runtimeevent.Event
	options           application.LogsQueryOptions
	query             runtimeevent.Query
	visibility        logger.Visibility
	loaded            bool
	loading           bool
	paused            bool
	connected         bool
	reconnecting      bool
	stream            *runtimecontrol.EventStream
	streamCtx         context.Context
	streamCancel      context.CancelFunc
	streamRunID       string
	streamSeq         uint64
	generation        uint64
	clearSeq          uint64
	overlay           logsOverlay
	editor            *component.Editor
	filterForm        *logsFilterFormData
	confirm           component.ConfirmButtons
	info              application.LogsInfo
	progress          *component.Progress
	notice            string
	toastNotice       bool
	err               error
	width             int
	height            int
	restoreSelectedID string
}

type LogsSessionViewState struct {
	Tab                         string
	View                        string
	RuntimeScope                string
	RuntimeWorkspaceID          string
	RuntimeContainerID          string
	Options                     application.LogsQueryOptions
	Visibility                  logger.Visibility
	RuntimePaused               bool
	RuntimeSelectedID           string
	RuntimeClearSequences       map[string]uint64
	ExecutionScope              string
	ExecutionWorkspaceID        string
	ExecutionContainerID        string
	ExecutionWorkspaceView      string
	ExecutionProcessID          string
	ExecutionProcessExecutionID string
	ExecutionProcessRunning     bool
	ExecutionPaused             bool
	ExecutionYOffset            int
	ExecutionClearSequence      uint64
	ExecutionClearSet           bool
	ToolCallScope               string
	ToolCallWorkspaceID         string
	ToolCallContainerID         string
	ToolCallPaused              bool
	ToolCallYOffset             int
	ToolCallClearSequence       uint64
}

func NewLogs(ctx context.Context) (*LogsPage, error) {
	return NewLogsRouteAction(ctx, "", "", "")
}

func NewLogsRoute(ctx context.Context, resourceID, section string) (*LogsPage, error) {
	return NewLogsRouteAction(ctx, resourceID, section, "")
}

func NewLogsRouteAction(ctx context.Context, resourceID, section, action string) (*LogsPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pageCtx, cancel := context.WithCancel(ctx)
	page := &LogsPage{ctx: pageCtx, cancel: cancel, resourceID: strings.TrimSpace(resourceID), section: strings.TrimSpace(section), action: strings.TrimSpace(action), view: logsViewBrowser, timeline: newLogsTimelineState(), runtimeClear: map[string]uint64{}, runtimeScope: newLogsScopeState(), options: application.LogsQueryOptions{Tail: logsDefaultTail}, visibility: logger.VisibilityVerbose, exec: newLogsExecutionFeed(), tools: newLogsToolCallFeed(), help: component.NewHelpFooter()}
	page.browser = component.NewBrowser(pageCtx, "Logs", nil, nil).WithTitleVisible(false).WithExternalHelp(true)
	page.syncBrowserHelp()
	if page.resourceID != "" {
		page.detail = component.NewDetailPage("Log event · "+page.resourceID, "loading", component.Muted("Loading log event...")).WithTitleVisible(false)
		page.detail.SetBindings(component.DetailPageBinding{Key: "r", Desc: "refresh", Message: LogsCommandMsg{Command: LogsRefresh}})
	}
	if page.action == "filter" {
		page.initFilterEditor()
	}
	return page, nil
}

func NewCommandExecutionLogs(ctx context.Context) (*LogsPage, error) {
	return NewCommandExecutionLogsRoute(ctx, "")
}

func NewCommandExecutionLogsRoute(ctx context.Context, resourceID string) (*LogsPage, error) {
	page, err := NewLogsRoute(ctx, "", "")
	if err != nil {
		return nil, err
	}
	page.tab = logsTabCommandExec
	page.resourceID = strings.TrimSpace(resourceID)
	page.view = logsViewTimeline
	page.syncBrowserHelp()
	if page.resourceID != "" {
		page.detail = component.NewDetailPage("Execution · "+page.resourceID, "loading", component.Muted("Loading execution...")).WithTitleVisible(false)
	}
	return page, nil
}

// NewCommandExecutionLogsRouteAction is kept as a compatibility wrapper for callers
// that have not migrated from the removed settings route.
func NewCommandExecutionLogsRouteAction(ctx context.Context, action string) (*LogsPage, error) {
	if strings.TrimSpace(action) != "" {
		return nil, fmt.Errorf("unsupported command execution action: %s", action)
	}
	return NewCommandExecutionLogsRoute(ctx, "")
}

func (page *LogsPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	switch page.tab {
	case logsTabCommandExec:
		commands := []tea.Cmd{page.startExecutionFeed()}
		if page.resourceID != "" {
			commands = append(commands, page.loadExecutionDetailCmd())
		}
		return tea.Batch(commands...)
	case logsTabToolCalls:
		return page.startToolCallFeed()
	default:
		page.refreshRuntimeScope()
		return page.startBootstrap()
	}
}

func (page *LogsPage) Close() {
	if page == nil {
		return
	}
	// Fence messages already queued by subscriptions and detail requests before
	// cancelling their contexts. A closed page must never accept late results.
	page.generation++
	page.exec.generation++
	page.exec.detailRequest++
	page.tools.generation++
	page.stopStream()
	page.stopExecutionFeed()
	page.stopToolCallFeed()
	page.cleanupSelectedFinishedProcess()
	if page.cancel != nil {
		page.cancel()
	}
}

func (page *LogsPage) OverlayActive() bool {
	return page != nil && (page.overlay != logsOverlayNone || page.modeDialog != nil)
}
func (page *LogsPage) InputActive() bool {
	return page != nil && (page.modeDialog != nil || page.editor != nil || page.view == logsViewBrowser && page.resourceID == "" && page.browser.InputActive())
}
func (page *LogsPage) Dirty() bool {
	return page != nil && page.editor != nil && page.editor.Dirty()
}
func (page *LogsPage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *LogsPage) SessionViewState() any {
	if page == nil {
		return LogsSessionViewState{}
	}
	tab := "runtime"
	switch page.tab {
	case logsTabCommandExec:
		tab = "command-execution"
	case logsTabToolCalls:
		tab = "tool-calls"
	}
	executionYOffset := page.exec.viewport.YOffset()
	if page.exec.restoreYOffsetSet {
		executionYOffset = page.exec.restoreYOffset
	}
	workspaceView := normalizeExecutionWorkspaceView(page.exec.workspaceView)
	processID, processExecutionID, processRunning := page.exec.processID, page.exec.processExecutionID, page.exec.processRunning
	if workspaceView == executionWorkspaceProcess && !processRunning {
		workspaceView, processID, processExecutionID = executionWorkspaceCommands, "", ""
	}
	return LogsSessionViewState{
		Tab: tab, View: string(page.view), Options: page.options, Visibility: page.visibility, RuntimePaused: page.paused, RuntimeSelectedID: page.selectedID(),
		RuntimeScope: string(page.runtimeScope.mode), RuntimeWorkspaceID: page.runtimeScope.workspaceID, RuntimeContainerID: page.runtimeScope.containerID, RuntimeClearSequences: cloneSequenceWatermarks(page.runtimeClear),
		ExecutionScope: string(page.exec.scopeMode), ExecutionWorkspaceID: page.exec.workspaceID, ExecutionContainerID: page.exec.containerID,
		ExecutionWorkspaceView: string(workspaceView), ExecutionProcessID: processID, ExecutionProcessExecutionID: processExecutionID, ExecutionProcessRunning: processRunning,
		ExecutionPaused: page.exec.paused, ExecutionYOffset: executionYOffset, ExecutionClearSequence: page.executionClear, ExecutionClearSet: page.executionClearSet,
		ToolCallScope: string(page.tools.scope.mode), ToolCallWorkspaceID: page.tools.scope.workspaceID, ToolCallContainerID: page.tools.scope.containerID,
		ToolCallPaused: page.tools.paused, ToolCallYOffset: page.tools.viewport.YOffset(), ToolCallClearSequence: page.toolCallClear,
	}
}

func (page *LogsPage) RestoreSessionViewState(value any) {
	if page == nil || page.action != "" {
		return
	}
	state, ok := value.(LogsSessionViewState)
	if !ok {
		return
	}
	switch state.Tab {
	case "command-execution":
		page.tab = logsTabCommandExec
	case "tool-calls":
		page.tab = logsTabToolCalls
	default:
		page.tab = logsTabRuntime
	}
	page.view = normalizeLogsDisplayView(logsDisplayView(state.View))
	page.options, page.visibility, page.paused = state.Options, state.Visibility, state.RuntimePaused
	page.runtimeScope.mode = normalizeExecutionScopeMode(executionScopeMode(state.RuntimeScope))
	page.runtimeScope.workspaceID, page.runtimeScope.containerID = strings.TrimSpace(state.RuntimeWorkspaceID), strings.TrimSpace(state.RuntimeContainerID)
	page.runtimeClear = cloneSequenceWatermarks(state.RuntimeClearSequences)
	if page.runtimeClear == nil {
		page.runtimeClear = map[string]uint64{}
	}
	page.refreshRuntimeScope()
	page.restoreSelectedID = strings.TrimSpace(state.RuntimeSelectedID)
	switch executionScopeMode(state.ExecutionScope) {
	case executionScopeWorkspace, executionScopeContainer:
		page.exec.scopeMode = executionScopeMode(state.ExecutionScope)
	default:
		page.exec.scopeMode = executionScopeCombined
	}
	page.exec.workspaceID = strings.TrimSpace(state.ExecutionWorkspaceID)
	page.exec.containerID = strings.TrimSpace(state.ExecutionContainerID)
	page.exec.workspaceView = normalizeExecutionWorkspaceView(executionWorkspaceView(state.ExecutionWorkspaceView))
	page.exec.processID = strings.TrimSpace(state.ExecutionProcessID)
	page.exec.processExecutionID = strings.TrimSpace(state.ExecutionProcessExecutionID)
	page.exec.processRunning = state.ExecutionProcessRunning
	if page.exec.scopeMode != executionScopeWorkspace || page.exec.workspaceView != executionWorkspaceProcess || page.exec.processID == "" || page.exec.processExecutionID == "" {
		page.exec.workspaceView, page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = executionWorkspaceCommands, "", "", false
	}
	page.exec.paused, page.executionClear = state.ExecutionPaused, state.ExecutionClearSequence
	page.executionClearSet = state.ExecutionClearSet || state.ExecutionClearSequence > 0
	page.exec.restoreYOffset, page.exec.restoreYOffsetSet = max(0, state.ExecutionYOffset), true
	page.tools.scope.mode = normalizeExecutionScopeMode(executionScopeMode(state.ToolCallScope))
	page.tools.scope.workspaceID, page.tools.scope.containerID = strings.TrimSpace(state.ToolCallWorkspaceID), strings.TrimSpace(state.ToolCallContainerID)
	page.refreshToolCallScope()
	page.tools.paused, page.toolCallClear = state.ToolCallPaused, state.ToolCallClearSequence
	page.tools.restoreYOffset = max(0, state.ToolCallYOffset)
	page.syncBrowserHelp()
}

func (page *LogsPage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *LogsPage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
		if page.notice == "" {
			page.toastNotice = false
		}
	}
}

func (page *LogsPage) ShouldToastNotice() bool {
	return page != nil && page.toastNotice
}

func (page *LogsPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case logsExecutionOpenMsg:
		return page, page.finishExecutionFeedOpen(msg)
	case logsExecutionEventMsg:
		return page, page.finishExecutionFeedEvent(msg)
	case logsExecutionReconnectMsg:
		if uint64(msg) != page.exec.generation || page.exec.connected {
			return page, nil
		}
		return page, page.startExecutionFeed()
	case logsExecutionDetailMsg:
		page.finishExecutionDetail(msg)
		return page, nil
	case logsToolCallOpenMsg:
		return page, page.finishToolCallFeedOpen(msg)
	case logsToolCallEventMsg:
		return page, page.finishToolCallEvent(msg)
	case logsToolCallDetailMsg:
		return page, page.finishToolCallDetail(msg)
	case logsToolCallReconnectMsg:
		if uint64(msg) != page.tools.generation || page.tools.connected {
			return page, nil
		}
		return page, page.startToolCallFeed()
	case logsModeProcessesMsg:
		page.finishLogsModeProcesses(msg)
		return page, nil
	case logsExecutionMouseMsg:
		if page.tab == logsTabCommandExec && page.resourceID == "" {
			page.handleExecutionMouse(msg)
		}
		return page, nil
	case logsTimelineMouseMsg:
		if page.resourceID == "" && page.view == logsViewTimeline && page.tab == msg.Tab {
			page.handleTimelineMouse(msg)
		}
		return page, nil
	case logsBootstrapMsg:
		return page, page.finishBootstrap(msg)
	case logsStreamOpenMsg:
		return page, page.finishStreamOpen(msg)
	case logsStreamEventMsg:
		return page, page.finishStreamEvent(msg)
	case logsReconnectMsg:
		if uint64(msg) != page.generation || page.connected || page.overlay == logsOverlayOperation {
			return page, nil
		}
		return page, page.startBootstrap()
	case logsClearMsg:
		if msg.operation != page.clearSeq {
			return page, nil
		}
		page.overlay, page.progress = logsOverlayNone, nil
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.events = nil
		page.notice, page.toastNotice, page.err = "Runtime logs cleared", true, nil
		browserCmd := page.rebuildBrowser("")
		if page.connected {
			return page, browserCmd
		}
		return page, tea.Batch(browserCmd, page.startBootstrap())
	case component.EditorSubmitMsg:
		return page, page.submitFilterEditor()
	case component.EditorCancelMsg:
		return page, page.closeFilterEditor()
	case component.ConfirmChoiceMsg:
		if page.overlay == logsOverlayConfirm {
			page.confirm.Select(msg.Affirmative)
			return page, page.updateClearConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case LogsCommandMsg:
		return page, page.openCommand(msg.Command)
	case component.BrowserOpenMsg:
		if page.resourceID != "" || msg.Row.ID == "" || page.view != logsViewBrowser {
			return page, nil
		}
		path := []string{"logs", msg.Row.ID}
		switch page.tab {
		case logsTabCommandExec:
			path = []string{"logs-exec", msg.Row.ID}
		case logsTabToolCalls:
			path = []string{"logs-tools", msg.Row.ID}
		}
		return page, func() tea.Msg { return NavigateMsg{Path: path} }
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		if page.editor != nil {
			page.resizeFilterEditor()
			return page, nil
		}
		var browserCmd tea.Cmd
		switch {
		case page.resourceID != "":
			page.detail.Resize(msg.Width, msg.Height)
		case page.view == logsViewBrowser:
			browserCmd = page.resizeBrowser()
		case page.tab == logsTabCommandExec:
			tabs := component.PageTabsNotice(logsTabLabels, int(page.tab), page.notice, msg.Width)
			page.resizeExecutionViewport(msg.Width, max(1, msg.Height-lipgloss.Height(tabs)-1))
		case page.tab == logsTabToolCalls:
			tabs := component.PageTabsNotice(logsTabLabels, int(page.tab), page.notice, msg.Width)
			page.resizeToolCallViewport(msg.Width, max(1, msg.Height-lipgloss.Height(tabs)-1))
		default:
			tabs := component.PageTabsNotice(logsTabLabels, int(page.tab), page.notice, msg.Width)
			page.resizeRuntimeTimeline(msg.Width, max(1, msg.Height-lipgloss.Height(tabs)-1))
		}
		return page, browserCmd
	case tea.KeyPressMsg:
		if page.modeDialog != nil {
			return page, page.updateLogsModeDialog(msg)
		}
		if page.overlay == logsOverlayOperation {
			if msg.String() == "esc" {
				page.closeOverlay()
			}
			return page, nil
		}
		if page.editor != nil {
			updated, cmd := page.editor.Update(msg)
			page.editor = &updated
			return page, cmd
		}
		if page.overlay == logsOverlayConfirm {
			return page, page.updateClearConfirm(msg)
		}
		if page.overlay == logsOverlayInfo {
			if msg.String() == "esc" {
				page.closeOverlay()
			}
			return page, nil
		}
		if page.resourceID != "" {
			updated, cmd := page.detail.Update(msg)
			page.detail = updated
			return page, cmd
		}
		if page.view == logsViewBrowser && page.browser.InputActive() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		if page.help.Update(msg) {
			page.browser.SetHelpExpanded(page.help.Expanded())
			return page, nil
		}
		if cmd, handled := page.handleTabKey(msg); handled {
			return page, cmd
		}
		switch page.tab {
		case logsTabCommandExec:
			return page, page.handleExecutionKey(msg)
		case logsTabToolCalls:
			return page, page.handleToolCallKey(msg)
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
	if page.resourceID != "" {
		updated, cmd := page.detail.Update(message)
		page.detail = updated
		return page, cmd
	}
	if page.view == logsViewBrowser {
		switch page.tab {
		case logsTabCommandExec:
			return page, page.updateExecutionBrowser(message)
		case logsTabToolCalls:
			return page, page.updateToolCallBrowser(message)
		}
		before := page.selectedID()
		updated, cmd := page.browser.Update(message)
		page.browser = updated.(component.Browser)
		if !page.paused && before != "" && page.selectedID() != page.tailID() {
			page.paused = true
		}
		return page, cmd
	}
	if page.tab == logsTabCommandExec || page.tab == logsTabToolCalls {
		return page, nil
	}
	if page.view == logsViewTimeline {
		return page, nil
	}
	return page, nil
}

func (page *LogsPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Logs unavailable", "")
	}
	page.width, page.height = width, height
	var content string
	switch {
	case page.editor != nil:
		content = page.filterEditorView(width, height)
	case page.resourceID != "":
		page.detail.SetFeedback(page.notice, page.err)
		page.detail.Resize(width, height)
		content = page.detail.View()
	default:
		tabs := component.PageTabsNotice(logsTabLabels, int(page.tab), page.notice, width)
		bodyHeight := max(1, height-lipgloss.Height(tabs))
		header, help := page.logsChromeView(width)
		var body string
		switch page.tab {
		case logsTabCommandExec:
			layout := component.NewSectionLayout("", "", header, width, bodyHeight, lipgloss.Height(help))
			body = page.executionBodyView(width, layout.BodyHeight)
			content = tabs + "\n" + component.BottomHelp(layout.View(body), help, width, bodyHeight)
		case logsTabToolCalls:
			layout := component.NewSectionLayout("", "", header, width, bodyHeight, lipgloss.Height(help))
			body = page.toolCallBodyView(width, layout.BodyHeight)
			content = tabs + "\n" + component.BottomHelp(layout.View(body), help, width, bodyHeight)
		default:
			layout := component.NewSectionLayout("", "", header, width, bodyHeight, lipgloss.Height(help))
			if page.view == logsViewTimeline {
				body = page.runtimeTimelineBody(width, layout.BodyHeight)
			} else {
				updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: layout.BodyHeight})
				page.browser = updated.(component.Browser)
				body = page.browser.BodyContent()
			}
			content = tabs + "\n" + component.BottomHelp(layout.View(body), help, width, bodyHeight)
		}
	}
	switch page.overlay {
	case logsOverlayConfirm:
		modalWidth := overlayWidth(width, 68)
		body := confirmOverlayBody(page.confirm, "Clear runtime logs?", "Current and rotated runtime logs will be removed. This cannot be undone.", modalWidth)
		content = component.CenterOverlay(content, component.Modal(body, modalWidth), width, height)
	case logsOverlayInfo:
		modalWidth := overlayWidth(width, 78)
		body := component.Title("Logs info") + "\n\n" + detailFields([2]string{"Path", page.info.Path}, [2]string{"Files", fmt.Sprintf("%d", page.info.Files)}, [2]string{"Size", humanBytes(page.info.Bytes)}) + "\n\n" + component.Muted("Esc close")
		content = component.CenterOverlay(content, component.Modal(component.WrapModalBody(body, modalWidth), modalWidth), width, height)
	case logsOverlayOperation:
		body := ""
		if page.progress != nil {
			body = page.progress.View()
		}
		content = component.CenterOverlay(content, component.Modal(body, overlayWidth(width, 62)), width, height)
	}
	if page.modeDialog != nil {
		modalWidth := overlayWidth(width, 72)
		content = component.CenterOverlay(content, component.Modal(page.logsModeDialogView(width), modalWidth), width, height)
	}
	return content
}

func (page *LogsPage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	if page.modeDialog != nil {
		return page.modeDialogMouseTargets(originX, originY, z)
	}
	switch page.overlay {
	case logsOverlayConfirm:
		return confirmOverlayMouseTargets(page.confirm, "Clear runtime logs?", "Current and rotated runtime logs will be removed. This cannot be undone.", overlayWidth(page.width, 68), page.width, page.height, originX, originY, z+20)
	case logsOverlayInfo:
		body := component.Title("Logs info") + "\n\n" + detailFields([2]string{"Path", page.info.Path}, [2]string{"Files", fmt.Sprintf("%d", page.info.Files)}, [2]string{"Size", humanBytes(page.info.Bytes)}) + "\n\n" + component.Muted("Esc close")
		return dismissibleOverlayMouseTargets(body, overlayWidth(page.width, 78), page.width, page.height, originX, originY, z+20)
	case logsOverlayOperation:
		return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
	}
	if page.editor != nil {
		return page.filterEditorMouseTargets(originX, originY, z)
	}
	if page.resourceID != "" {
		return page.detail.MouseTargets(originX, originY, z)
	}
	tabs := component.PageTabsNotice(logsTabLabels, int(page.tab), page.notice, page.width)
	tabTargets := page.logsTabMouseTargets(originX, originY, z+2)
	tabsHeight := lipgloss.Height(tabs)
	bodyHeight := max(1, page.height-tabsHeight)
	header, help := page.logsChromeView(page.width)
	layout := component.NewSectionLayout("", "", header, page.width, bodyHeight, lipgloss.Height(help))
	bodyY := originY + tabsHeight + layout.BodyY
	if page.view == logsViewTimeline {
		if page.tab == logsTabCommandExec {
			return append(tabTargets, page.executionMouseTargets(originX, bodyY, z, page.width, layout.BodyHeight)...)
		}
		return append(tabTargets, timelineMouseTarget(page.tab, originX, bodyY, z, page.width, layout.BodyHeight))
	}
	tabTargets = append(tabTargets, page.browser.MouseTargets(originX, bodyY, z)...)
	helpY := originY + tabsHeight + bodyHeight - lipgloss.Height(help)
	return append(tabTargets, page.browser.HelpMouseTargets(originX, helpY, z+2)...)
}

func (page *LogsPage) logsChromeView(width int) (string, string) {
	switch page.tab {
	case logsTabCommandExec:
		return page.executionHeaderView(width), page.executionHelpView(width)
	case logsTabToolCalls:
		header := page.toolCallStatusView(width)
		if page.tools.err != nil {
			header += "\n" + component.BannerWidth(page.tools.err.Error(), component.ToneDanger, width)
		} else if page.tools.notice != "" {
			header += "\n" + component.WrapContent(component.Muted(page.tools.notice), width)
		}
		return header, page.toolCallHelpView(width)
	default:
		header := page.statusView(width)
		if page.err != nil {
			header += "\n" + component.BannerWidth(page.err.Error(), component.ToneDanger, width)
		}
		return header, page.runtimeHelpView(width)
	}
}

func (page *LogsPage) handleTabKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "1":
		return page.switchLogsTab(logsTabRuntime), true
	case "2":
		return page.switchLogsTab(logsTabCommandExec), true
	case "3":
		return page.switchLogsTab(logsTabToolCalls), true
	}
	delta, ok := component.TabDelta(msg)
	if !ok {
		return nil, false
	}
	return page.moveLogsTab(delta), true
}

func (page *LogsPage) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "v":
		page.toggleLogsView()
		page.syncBrowserHelp()
		return nil, true
	case "m":
		return page.openLogsModeDialog(), true
	case "space":
		return page.togglePause(), true
	case "f":
		return page.openCommand(LogsFilter), true
	case "r":
		return page.openCommand(LogsRefresh), true
	case "i":
		return page.openCommand(LogsInfo), true
	case "c":
		page.clearActiveLogsView()
		return nil, true
	case "d":
		return page.openCommand(LogsClear), true
	}
	if page.view == logsViewTimeline {
		view, cmd := page.timeline.viewport.Update(msg)
		page.timeline.viewport = view
		if !page.timeline.viewport.AtBottom() {
			page.paused = true
		}
		page.maybeExpandRuntimeTimeline()
		return cmd, true
	}
	return nil, false
}

func (page *LogsPage) openCommand(command LogsCommand) tea.Cmd {
	page.err, page.notice, page.toastNotice = nil, "", false
	switch command {
	case LogsRefresh:
		return page.startBootstrap()
	case LogsFilter:
		page.action = "filter"
		page.initFilterEditor()
		return tea.Batch(page.editor.Init(), func() tea.Msg {
			return NavigateMsg{Path: []string{"logs", "filter"}, Replace: true, PreservePage: true}
		})
	case LogsToggle:
		return page.togglePause()
	case LogsInfo:
		info, err := application.LoadLogsInfo()
		if err != nil {
			page.err = err
			return nil
		}
		page.info, page.overlay = info, logsOverlayInfo
		return nil
	case LogsClear:
		page.confirm = component.NewConfirmButtons("Clear", "Cancel", false)
		page.overlay = logsOverlayConfirm
		return nil
	default:
		page.err = fmt.Errorf("unsupported logs action: %s", command)
		return nil
	}
}

func (page *LogsPage) submitFilterEditor() tea.Cmd {
	if page == nil || page.editor == nil || page.filterForm == nil {
		return nil
	}
	options, visibility, err := page.filterForm.Options()
	if err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	page.options, page.visibility = options, visibility
	page.editor, page.filterForm, page.action = nil, nil, ""
	page.err = nil
	page.events = nil
	page.paused = false
	page.timeline.window.invalidate()
	return tea.Batch(page.startBootstrap(), func() tea.Msg { return NavigateMsg{Path: []string{"logs"}, Replace: true, PreservePage: true} })
}

func (page *LogsPage) updateClearConfirm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		page.closeOverlay()
		return nil
	case "enter":
		if !page.confirm.AffirmativeSelected() {
			page.closeOverlay()
			return nil
		}
		page.clearSeq++
		operation := page.clearSeq
		progress := component.NewProgress("Clearing runtime logs")
		page.progress, page.overlay = &progress, logsOverlayOperation
		return func() tea.Msg { return logsClearMsg{operation: operation, err: application.ClearLogs(page.ctx)} }
	default:
		return page.confirm.Update(msg)
	}
}

func (page *LogsPage) togglePause() tea.Cmd {
	page.paused = !page.paused
	page.notice, page.toastNotice = "", false
	if !page.paused {
		if page.view == logsViewTimeline {
			page.timeline.window.invalidate()
			page.refreshRuntimeTimeline()
		} else {
			page.browser.SelectLast()
		}
	}
	page.syncBrowserHelp()
	return nil
}

func (page *LogsPage) clearActiveLogsView() {
	if page == nil {
		return
	}
	switch page.tab {
	case logsTabCommandExec:
		page.executionClear = max(page.executionClear, page.exec.latestSeq)
		for _, event := range page.exec.events {
			page.executionClear = max(page.executionClear, event.Sequence)
		}
		page.executionClearSet = true
		page.exec.window.invalidate()
		page.exec.paused = false
		page.exec.notice = "Command stream view cleared"
	case logsTabToolCalls:
		page.toolCallClear = max(page.toolCallClear, page.tools.latestSeq)
		for _, event := range page.tools.events {
			page.toolCallClear = max(page.toolCallClear, event.Sequence)
		}
		page.tools.window.invalidate()
		page.tools.paused = false
		page.tools.notice = "Tool call stream view cleared"
	default:
		if page.runtimeClear == nil {
			page.runtimeClear = map[string]uint64{}
		}
		for _, event := range page.events {
			if event.Sequence > page.runtimeClear[event.RunID] {
				page.runtimeClear[event.RunID] = event.Sequence
			}
		}
		if page.streamRunID != "" && page.streamSeq > page.runtimeClear[page.streamRunID] {
			page.runtimeClear[page.streamRunID] = page.streamSeq
		}
		page.timeline.window.invalidate()
		page.paused = false
		page.notice, page.toastNotice = "Runtime view cleared", true
	}
	page.refreshActiveLogsView()
	page.syncBrowserHelp()
}

func (page *LogsPage) startBootstrap() tea.Cmd {
	page.stopStream()
	page.refreshRuntimeScope()
	page.timeline.window.invalidate()
	page.generation++
	generation := page.generation
	ctx, cancel := context.WithCancel(page.ctx)
	page.streamCtx, page.streamCancel = ctx, cancel
	page.streamRunID, page.streamSeq = "", 0
	page.loading, page.reconnecting = true, page.loaded
	options, visibility := page.options, page.visibility
	return func() tea.Msg {
		state, _ := runtimecontrol.Load()
		historyOptions := options
		if historyOptions.Session == "" && !historyOptions.All && state.RunID != "" {
			historyOptions.Session = state.RunID
		}
		snapshot, historyErr := application.LoadLogs(historyOptions, visibility, logsTimelineEventCap, time.Now())
		info, infoErr := application.LoadLogsInfo()
		return logsBootstrapMsg{generation: generation, snapshot: snapshot, info: info, state: state, historyErr: historyErr, infoErr: infoErr}
	}
}

func (page *LogsPage) finishBootstrap(msg logsBootstrapMsg) tea.Cmd {
	if msg.generation != page.generation {
		return nil
	}
	page.loading, page.loaded = false, true
	var browserCmd tea.Cmd
	if msg.historyErr != nil {
		page.err = msg.historyErr
	} else {
		page.query = msg.snapshot.Query
		if msg.state.RunID != "" {
			page.streamRunID = msg.state.RunID
			page.streamSeq = msg.snapshot.LatestSequence[msg.state.RunID]
		}
		browserCmd = page.mergeEvents(msg.snapshot.Events)
		if page.view == logsViewTimeline && page.paused {
			page.refreshRuntimeTimeline()
		}
		if page.restoreSelectedID != "" {
			browserCmd = tea.Batch(browserCmd, page.rebuildBrowser(page.restoreSelectedID))
			page.restoreSelectedID = ""
		}
		page.err = nil
	}
	if msg.infoErr == nil {
		page.info = msg.info
	}
	page.reconnecting = !page.connected
	if page.err == nil && page.reconnecting {
		page.notice, page.toastNotice = "Journal loaded; connecting live stream", false
	}
	return tea.Batch(browserCmd, page.openStreamCmd(msg.generation))
}

func (page *LogsPage) openStreamCmd(generation uint64) tea.Cmd {
	ctx := page.streamCtx
	if ctx == nil {
		ctx = page.ctx
	}
	return func() tea.Msg {
		stream, state, err := runtimecontrol.OpenEvents(ctx)
		return logsStreamOpenMsg{generation: generation, stream: stream, state: state, err: err}
	}
}

func (page *LogsPage) finishStreamOpen(msg logsStreamOpenMsg) tea.Cmd {
	if msg.generation != page.generation {
		if msg.stream != nil {
			_ = msg.stream.Close()
		}
		return nil
	}
	if msg.err != nil {
		page.connected, page.reconnecting = false, true
		page.stream = nil
		if page.err == nil {
			page.notice, page.toastNotice = "Runtime offline; showing journal history and retrying live stream", false
		}
		return page.reconnectCmd(msg.generation)
	}
	page.stream, page.connected, page.reconnecting = msg.stream, true, false
	if page.streamRunID != msg.state.RunID {
		page.streamRunID, page.streamSeq = msg.state.RunID, 0
	}
	if msg.stream.LatestSequence() > page.streamSeq {
		page.notice, page.toastNotice = "Live stream advanced during journal load; resyncing journal", false
		return page.startBootstrap()
	}
	if page.options.Session == "" && !page.options.All && msg.state.RunID != "" && page.query.RunID != msg.state.RunID {
		page.notice, page.toastNotice = "Runtime session changed; resyncing journal", false
		return page.startBootstrap()
	}
	page.notice, page.toastNotice = "", false
	return page.nextEventCmd(msg.generation)
}

func (page *LogsPage) nextEventCmd(generation uint64) tea.Cmd {
	stream := page.stream
	if stream == nil {
		return nil
	}
	return func() tea.Msg {
		event, err := stream.Next()
		return logsStreamEventMsg{generation: generation, event: event, err: err}
	}
}

func (page *LogsPage) finishStreamEvent(msg logsStreamEventMsg) tea.Cmd {
	if msg.generation != page.generation {
		return nil
	}
	if msg.err != nil {
		if msg.err == io.EOF || page.ctx.Err() == nil {
			page.connected, page.reconnecting = false, true
			page.stopStreamOnly()
			page.notice, page.toastNotice = "Live stream disconnected; reconnecting", false
			return page.reconnectCmd(msg.generation)
		}
		return nil
	}
	if page.streamGap(msg.event) {
		page.notice, page.toastNotice = "Live stream gap detected; resyncing journal", false
		return page.startBootstrap()
	}
	if page.options.Session == "" && !page.options.All && msg.event.RunID != "" && page.query.RunID != msg.event.RunID {
		page.query.RunID = msg.event.RunID
		page.events = nil
		page.paused = false
		page.timeline.window.invalidate()
	}
	if page.query.Match(msg.event) && msg.event.Visibility <= page.visibility {
		return tea.Batch(page.appendEvent(msg.event), page.nextEventCmd(msg.generation))
	}
	return page.nextEventCmd(msg.generation)
}

func (page *LogsPage) streamGap(event runtimeevent.Event) bool {
	if event.RunID == "" || event.Sequence == 0 {
		return false
	}
	if page.streamRunID != event.RunID {
		page.streamRunID, page.streamSeq = event.RunID, event.Sequence
		return false
	}
	gap := page.streamSeq > 0 && event.Sequence > page.streamSeq+1
	if event.Sequence > page.streamSeq {
		page.streamSeq = event.Sequence
	}
	return gap
}

func (page *LogsPage) reconnectCmd(generation uint64) tea.Cmd {
	return tea.Tick(logsReconnectDelay, func(time.Time) tea.Msg { return logsReconnectMsg(generation) })
}

func (page *LogsPage) mergeEvents(events []runtimeevent.Event) tea.Cmd {
	selected := page.selectedID()
	merged := append(append([]runtimeevent.Event(nil), page.events...), events...)
	seen := map[string]runtimeevent.Event{}
	for _, event := range merged {
		if isToolCallRuntimeEvent(event) {
			continue
		}
		seen[logEventID(event)] = event
	}
	merged = merged[:0]
	for _, event := range seen {
		merged = append(merged, event)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Time.Equal(merged[j].Time) {
			if merged[i].RunID == merged[j].RunID {
				return merged[i].Sequence < merged[j].Sequence
			}
			return merged[i].RunID < merged[j].RunID
		}
		return merged[i].Time.Before(merged[j].Time)
	})
	if len(merged) > logsTimelineEventCap {
		merged = append([]runtimeevent.Event(nil), merged[len(merged)-logsTimelineEventCap:]...)
	}
	page.events = merged
	if !page.paused {
		selected = page.tailID()
	}
	if page.view == logsViewTimeline {
		if !page.paused {
			page.refreshRuntimeTimeline()
		}
		return nil
	}
	return page.rebuildBrowser(selected)
}

func (page *LogsPage) appendEvent(event runtimeevent.Event) tea.Cmd {
	if isToolCallRuntimeEvent(event) {
		return nil
	}
	id := logEventID(event)
	for _, current := range page.events {
		if logEventID(current) == id {
			return nil
		}
	}
	selected, tail := page.selectedID(), page.tailID()
	if !page.paused && selected != "" && tail != "" && selected != tail {
		page.paused = true
	}
	page.events = append(page.events, event)
	if len(page.events) > logsTimelineEventCap {
		page.events = append([]runtimeevent.Event(nil), page.events[len(page.events)-logsTimelineEventCap:]...)
	}
	if !page.paused {
		selected = page.tailID()
	}
	if page.view == logsViewTimeline {
		if !page.paused {
			page.refreshRuntimeTimeline()
		}
		return nil
	}
	return page.rebuildBrowser(selected)
}

func (page *LogsPage) rebuildBrowser(selected string) tea.Cmd {
	if page.resourceID != "" {
		if !page.detailReady {
			page.syncDetail()
		}
		return nil
	}
	visible := page.visibleRuntimeBrowserRecords()
	rows := make([]component.Row, 0, len(visible))
	for _, event := range visible {
		rows = append(rows, page.logRow(event))
	}
	cmd := page.browser.ReplaceRows(rows, selected)
	if !page.paused && selected == "" {
		page.browser.SelectLast()
	}
	if page.width > 0 && page.height > 0 {
		cmd = tea.Batch(cmd, page.resizeBrowser())
	}
	return cmd
}

func (page *LogsPage) resizeBrowser() tea.Cmd {
	tabsHeight := lipgloss.Height(component.PageTabsNotice(logsTabLabels, int(page.tab), page.notice, page.width))
	var header, help string
	switch page.tab {
	case logsTabCommandExec:
		header, help = page.executionHeaderView(page.width), page.executionHelpView(page.width)
	case logsTabToolCalls:
		header, help = page.toolCallStatusView(page.width), page.toolCallHelpView(page.width)
	default:
		header, help = page.statusView(page.width), page.runtimeHelpView(page.width)
	}
	if page.err != nil {
		header += "\n" + component.BannerWidth(page.err.Error(), component.ToneDanger, page.width)
	}
	bodyHeight := max(1, page.height-tabsHeight)
	layout := component.NewSectionLayout("", "", header, page.width, bodyHeight, lipgloss.Height(help))
	updated, cmd := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: layout.BodyHeight})
	page.browser = updated.(component.Browser)
	return cmd
}

func (page *LogsPage) logRow(event runtimeevent.Event) component.Row {
	level := strings.ToUpper(strings.TrimSpace(event.Level))
	componentName := strings.ToUpper(strings.TrimSpace(event.Component))
	titleParts := []string{event.Time.Local().Format("15:04:05")}
	if level != "" {
		titleParts = append(titleParts, level)
	}
	if componentName != "" {
		titleParts = append(titleParts, componentName)
	}
	if event.Name != "" {
		titleParts = append(titleParts, event.Name)
	}
	meta := compactParts(event.WorkspaceID, event.Tool, event.Status)
	fields := application.LogFields(event, page.visibility)
	search := []string{event.RunID, event.Level, event.Kind, event.Name, event.Component, event.Message, event.Error, event.WorkspaceID, event.Tool, event.Method, event.Source, event.Status, event.ServiceID, event.ServiceScope}
	for _, field := range fields {
		value := fmt.Sprint(field.Value)
		search = append(search, field.Key, value)
	}
	return component.Row{ID: logEventID(event), Title: strings.Join(titleParts, "  "), Description: event.Message, Meta: meta, Search: strings.Join(search, " ")}
}

func (page *LogsPage) syncDetail() {
	if page == nil || page.resourceID == "" || page.detailReady {
		return
	}
	var event runtimeevent.Event
	found := false
	for _, current := range page.events {
		if logEventID(current) == page.resourceID && !isToolCallRuntimeEvent(current) {
			event, found = current, true
			break
		}
	}
	if !found {
		page.detail.SetTitle("Log event · " + page.resourceID)
		page.detail.SetMeta("unavailable")
		page.detail.SetContentPreserveScroll(component.Muted("Log event not found in the current journal view."))
		if page.loaded {
			page.err = fmt.Errorf("log event not found: %s", page.resourceID)
		}
		return
	}
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		page.err = fmt.Errorf("encode log event: %w", err)
		return
	}
	page.err = nil
	content := component.RenderCodeBlock(string(data), "json", max(20, page.width))
	meta := compactParts(event.Level, event.Component, event.RunID, fmt.Sprintf("seq %d", event.Sequence))
	page.detail.SetTitle("Log event · " + event.Name)
	page.detail.SetMeta(meta)
	page.detail.SetContentPreserveScroll(content)
	page.detail.SetFeedback("", nil)
	page.detailReady = true
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
}

func (page *LogsPage) statusView(width int) string {
	stream := component.ToneText("● LIVE", component.ToneSuccess)
	if page.loading && !page.loaded {
		stream = component.Muted("↻ LOADING")
	} else if page.reconnecting {
		stream = component.ToneText("↻ RECONNECTING", component.ToneWarning)
	} else if !page.connected {
		stream = component.Muted("○ OFFLINE")
	}
	follow := component.ToneText("● ON", component.ToneSuccess)
	if page.paused {
		follow = component.ToneText("○ PAUSED", component.ToneWarning)
	}
	view := "Browser"
	if page.view == logsViewTimeline {
		view = "Timeline"
	}
	left := component.KeyValue("Stream", stream) + "   "
	if page.view == logsViewTimeline {
		left += component.KeyValue("Follow", follow) + "   "
	}
	left += component.KeyValue("View", view) + "   " + component.KeyValue("Visibility", logsVisibilityValue(page.visibility)) + "   "
	if page.view == logsViewBrowser {
		left += component.KeyValue("Records", fmt.Sprintf("%d / %d", len(page.visibleRuntimeBrowserRecords()), logsBrowserRecordCap))
	} else {
		left += component.KeyValue("Events", fmt.Sprintf("%d / %d", len(page.visibleRuntimeTimelineEvents()), logsTimelineEventCap))
	}
	right := component.KeyValue("Mode", logsScopeLabel(page.runtimeScope))
	if page.query.RunID != "" {
		right += "   " + component.KeyValue("Session", page.query.RunID)
	}
	return component.TwoColumn(left, right, width)
}

func (page *LogsPage) runtimeHelpView(width int) string {
	return page.logsHelpView(width)
}

func (page *LogsPage) syncBrowserHelp() {
	bindings := page.activeHelpBindings()
	page.help.SetBindings(bindings...)
	page.browser.SetHelpBindings(bindings...)
}

func (page *LogsPage) activeHelpBindings() []key.Binding {
	bindings := []key.Binding{component.Binding([]string{"h", "l", "left", "right"}, "←/→", "tabs"), component.Binding([]string{"v"}, "v", "view"), component.Binding([]string{"m"}, "m", "mode")}
	switch page.tab {
	case logsTabCommandExec:
		bindings = append(bindings, component.Binding([]string{"r"}, "r", "reconnect"), component.Binding([]string{"c"}, "c", "clear view"))
	case logsTabToolCalls:
		bindings = append(bindings, component.Binding([]string{"r"}, "r", "reconnect"), component.Binding([]string{"c"}, "c", "clear view"))
	default:
		bindings = append(bindings, component.Binding([]string{"f"}, "f", "filters"), component.Binding([]string{"r"}, "r", "refresh"), component.Binding([]string{"i"}, "i", "info"), component.Binding([]string{"c"}, "c", "clear view"), component.Binding([]string{"d"}, "d", "delete journal"))
	}
	bindings = append(bindings, component.Binding([]string{"space"}, "space", executionFollowLabel(page.activeLogsPaused())))
	return bindings
}

func (page *LogsPage) logsHelpView(width int) string {
	page.help.SetBindings(page.activeHelpBindings()...)
	return page.help.View(width)
}

func (page *LogsPage) activeLogsPaused() bool {
	switch page.tab {
	case logsTabCommandExec:
		return page.exec.paused
	case logsTabToolCalls:
		return page.tools.paused
	default:
		return page.paused
	}
}

func (page *LogsPage) selectedID() string {
	row, ok := page.browser.Selected()
	if !ok {
		return ""
	}
	return row.ID
}
func (page *LogsPage) tailID() string {
	visible := page.visibleRuntimeBrowserRecords()
	if len(visible) == 0 {
		return ""
	}
	return logEventID(visible[len(visible)-1])
}
func (page *LogsPage) stopStream() {
	page.stopStreamOnly()
	if page.streamCancel != nil {
		page.streamCancel()
		page.streamCancel = nil
	}
	page.streamCtx = nil
}
func (page *LogsPage) stopStreamOnly() {
	if page.stream != nil {
		_ = page.stream.Close()
		page.stream = nil
	}
	page.connected = false
}
func (page *LogsPage) closeOverlay() {
	page.overlay = logsOverlayNone
	page.confirm = component.ConfirmButtons{}
	page.progress = nil
}

func logEventID(event runtimeevent.Event) string {
	if event.RunID != "" && event.Sequence > 0 {
		return fmt.Sprintf("%s:%d", event.RunID, event.Sequence)
	}
	return fmt.Sprintf("%d:%s:%s:%s", event.Time.UnixNano(), event.RunID, event.Name, event.Message)
}
func compactParts(values ...string) string {
	result := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return strings.Join(result, " · ")
}
func durationLabel(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("%dms", ms)
}
func shortValue(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= limit {
		return value
	}
	if limit == 1 {
		return ansi.Truncate(value, 1, "")
	}
	return ansi.Truncate(value, limit-1, "") + "…"
}
func humanBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(value)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(value)/(1024*1024))
}
