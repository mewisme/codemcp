package page

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/runtimecontrol"
	shellruntime "go.mewis.me/codemcp/internal/shell"
	"go.mewis.me/codemcp/internal/tui/component"
)

type logsTab int

const (
	logsTabRuntime logsTab = iota
	logsTabCommandExec
	logsTabToolCalls
)

var logsTabLabels = []string{"Runtime", "Command Execution", "Tool Calls"}

type logsExecutionFeed struct {
	viewport           viewport.Model
	render             executionFeedRender
	events             []shellruntime.ExecutionFeedEvent
	scopeMode          executionScopeMode
	workspaceID        string
	workspaceView      executionWorkspaceView
	processID          string
	processExecutionID string
	processRunning     bool
	containerID        string
	containerName      string
	containerMembers   map[string]struct{}
	scopeStale         bool
	scopeNotice        string
	stream             *runtimecontrol.ExecutionFeedStream
	streamCtx          context.Context
	streamCancel       context.CancelFunc
	generation         uint64
	latestSeq          uint64
	loaded             bool
	loading            bool
	connected          bool
	reconnecting       bool
	unsupported        bool
	paused             bool
	notice             string
	err                error
	restoreYOffset     int
	restoreYOffsetSet  bool
}

type executionFeedRender struct {
	Content  string
	Segments []executionRenderedSegment
}

type executionRenderedSegment struct {
	StartLine     int
	BodyStartLine int
	EndLine       int
	StickyLabel   string
}

type logsExecutionOpenMsg struct {
	generation uint64
	stream     *runtimecontrol.ExecutionFeedStream
	err        error
}

type logsExecutionEventMsg struct {
	generation uint64
	event      shellruntime.ExecutionFeedEvent
	err        error
}

type logsExecutionReconnectMsg uint64

type logsExecutionMouseMsg struct{ Wheel int }

func newLogsExecutionFeed() logsExecutionFeed {
	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	view.SoftWrap = false
	view.FillHeight = false
	return logsExecutionFeed{viewport: view, scopeMode: executionScopeCombined, workspaceView: executionWorkspaceCommands, containerMembers: map[string]struct{}{}}
}

func (page *LogsPage) switchLogsTab(tab logsTab) tea.Cmd {
	if tab > logsTabToolCalls || page.resourceID != "" || tab == page.tab {
		return nil
	}
	cleanup := tea.Cmd(nil)
	if page.tab == logsTabCommandExec && tab != logsTabCommandExec {
		cleanup = page.detachSelectedProcessCmd()
	}
	page.tab = tab
	path := []string{"logs"}
	switch tab {
	case logsTabCommandExec:
		path = []string{"logs-exec"}
	case logsTabToolCalls:
		path = []string{"logs-tools"}
	}
	navigate := func() tea.Msg { return NavigateMsg{Path: path, Replace: true} }
	return tea.Batch(cleanup, navigate)
}

func (page *LogsPage) moveLogsTab(delta int) tea.Cmd {
	next := component.MoveTab(int(page.tab), len(logsTabLabels), delta)
	return page.switchLogsTab(logsTab(next))
}

func (page *LogsPage) startExecutionFeed() tea.Cmd {
	page.stopExecutionFeed()
	page.refreshExecutionScope()
	page.exec.generation++
	generation := page.exec.generation
	ctx, cancel := context.WithCancel(page.ctx)
	page.exec.streamCtx, page.exec.streamCancel = ctx, cancel
	page.exec.loading, page.exec.reconnecting = true, page.exec.loaded
	page.exec.unsupported = false
	page.exec.err = nil
	return func() tea.Msg {
		stream, _, err := runtimecontrol.OpenExecutionFeed(ctx)
		return logsExecutionOpenMsg{generation: generation, stream: stream, err: err}
	}
}

func (page *LogsPage) finishExecutionFeedOpen(msg logsExecutionOpenMsg) tea.Cmd {
	if msg.generation != page.exec.generation {
		if msg.stream != nil {
			_ = msg.stream.Close()
		}
		return nil
	}
	page.exec.loading = false
	if msg.err != nil {
		page.exec.stream = nil
		page.exec.loaded = true
		page.exec.err = nil
		if errors.Is(msg.err, runtimecontrol.ErrExecutionFeedUnsupported) {
			page.exec.connected, page.exec.reconnecting, page.exec.unsupported = false, false, true
			page.exec.notice = "Restart the running server to enable command execution streaming"
			return nil
		}
		page.exec.err = msg.err
		page.exec.connected, page.exec.reconnecting, page.exec.unsupported = false, true, false
		page.exec.notice = "Runtime offline; reconnecting command execution stream"
		return page.executionReconnectCmd(msg.generation)
	}
	page.exec.stream, page.exec.connected, page.exec.reconnecting, page.exec.loaded, page.exec.unsupported = msg.stream, true, false, true, false
	snapshot := msg.stream.Snapshot()
	page.exec.latestSeq = snapshot.LatestSequence
	page.exec.events = trimExecutionFeed(snapshot.Events)
	page.syncSelectedProcessRunningFromEvents()
	page.refreshExecutionScope()
	page.exec.notice, page.exec.err = "", nil
	if page.view == logsViewBrowser {
		page.rebuildExecutionBrowser()
	} else {
		page.refreshExecutionViewport()
		page.restoreExecutionViewportOffset()
	}
	return page.nextExecutionEventCmd(msg.generation)
}

func (page *LogsPage) syncSelectedProcessRunningFromEvents() {
	if page == nil || page.exec.processExecutionID == "" {
		return
	}
	for index := len(page.exec.events) - 1; index >= 0; index-- {
		event := page.exec.events[index]
		if event.ExecutionID != page.exec.processExecutionID {
			continue
		}
		page.exec.processRunning = event.Type != shellruntime.ExecutionEventCompleted
		return
	}
}

func (page *LogsPage) nextExecutionEventCmd(generation uint64) tea.Cmd {
	stream := page.exec.stream
	if stream == nil {
		return nil
	}
	return func() tea.Msg {
		event, err := stream.Next()
		return logsExecutionEventMsg{generation: generation, event: event, err: err}
	}
}

func (page *LogsPage) finishExecutionFeedEvent(msg logsExecutionEventMsg) tea.Cmd {
	if msg.generation != page.exec.generation {
		return nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, runtimecontrol.ErrExecutionFeedOverflow) {
			page.exec.notice = "Command stream overflowed; replaying bounded history"
		} else if msg.err == io.EOF || page.ctx.Err() == nil {
			page.exec.notice = "Command stream disconnected; reconnecting"
		} else {
			return nil
		}
		page.exec.connected, page.exec.reconnecting = false, true
		page.stopExecutionFeedOnly()
		return page.executionReconnectCmd(msg.generation)
	}
	if msg.event.Sequence == 0 || msg.event.Sequence <= page.exec.latestSeq {
		return page.nextExecutionEventCmd(msg.generation)
	}
	if page.exec.latestSeq > 0 && msg.event.Sequence > page.exec.latestSeq+1 {
		page.exec.notice = "Command stream gap detected; replaying bounded history"
		return page.startExecutionFeed()
	}
	page.exec.latestSeq = msg.event.Sequence
	page.appendExecutionFeedEvent(msg.event)
	if page.exec.processExecutionID != "" && msg.event.ExecutionID == page.exec.processExecutionID && msg.event.Type == shellruntime.ExecutionEventCompleted {
		page.exec.processRunning = false
	}
	page.exec.notice, page.exec.err = "", nil
	if !page.exec.paused {
		if page.view == logsViewBrowser {
			page.rebuildExecutionBrowser()
		} else {
			page.refreshExecutionViewport()
		}
	}
	return page.nextExecutionEventCmd(msg.generation)
}

func (page *LogsPage) executionReconnectCmd(generation uint64) tea.Cmd {
	return tea.Tick(logsReconnectDelay, func(time.Time) tea.Msg { return logsExecutionReconnectMsg(generation) })
}

func (page *LogsPage) handleExecutionKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "v":
		page.toggleLogsView()
		page.syncBrowserHelp()
		return nil
	case "m":
		return page.openLogsModeDialog()
	case "space":
		page.exec.paused = !page.exec.paused
		if !page.exec.paused {
			if page.view == logsViewBrowser {
				page.browser.SelectLast()
			} else {
				page.refreshExecutionViewport()
			}
		}
		page.syncBrowserHelp()
		return nil
	case "r":
		return page.startExecutionFeed()
	case "c":
		page.exec.events = nil
		page.exec.notice = "Command stream view cleared"
		page.refreshActiveLogsView()
		return nil
	}
	if page.view == logsViewBrowser {
		before := ""
		if row, ok := page.browser.Selected(); ok {
			before = row.ID
		}
		updated, cmd := page.browser.Update(msg)
		page.browser = updated.(component.Browser)
		if !page.exec.paused && before != "" {
			if row, ok := page.browser.Selected(); ok && row.ID != before {
				page.exec.paused = true
				page.syncBrowserHelp()
			}
		}
		return cmd
	}
	view, cmd := page.exec.viewport.Update(msg)
	page.exec.viewport = view
	if !page.exec.viewport.AtBottom() {
		page.exec.paused = true
	}
	return cmd
}

func (page *LogsPage) resizeExecutionViewport(width, height int) {
	width, height = max(1, width), max(1, height)
	offset := page.exec.viewport.YOffset()
	if page.exec.viewport.Width() != width {
		page.exec.viewport.SetWidth(width)
		page.exec.render = renderExecutionFeed(page.visibleExecutionEvents(), width)
		page.exec.viewport.SetContent(page.exec.render.Content)
	}
	page.exec.viewport.SetHeight(height)
	if !page.exec.paused {
		page.exec.viewport.GotoBottom()
		return
	}
	maxOffset := max(0, page.exec.viewport.TotalLineCount()-page.exec.viewport.Height())
	page.exec.viewport.SetYOffset(min(offset, maxOffset))
}

func (page *LogsPage) refreshExecutionViewport() {
	offset := page.exec.viewport.YOffset()
	page.exec.render = renderExecutionFeed(page.visibleExecutionEvents(), max(1, page.exec.viewport.Width()))
	page.exec.viewport.SetContent(page.exec.render.Content)
	if !page.exec.paused {
		page.exec.viewport.GotoBottom()
		return
	}
	maxOffset := max(0, page.exec.viewport.TotalLineCount()-page.exec.viewport.Height())
	page.exec.viewport.SetYOffset(min(offset, maxOffset))
}

func (page *LogsPage) restoreExecutionViewportOffset() {
	if page == nil || !page.exec.restoreYOffsetSet {
		return
	}
	page.exec.restoreYOffsetSet = false
	if !page.exec.paused {
		page.exec.viewport.GotoBottom()
		return
	}
	maxOffset := max(0, page.exec.viewport.TotalLineCount()-page.exec.viewport.Height())
	page.exec.viewport.SetYOffset(min(page.exec.restoreYOffset, maxOffset))
}

func (page *LogsPage) executionStatusView(width int) string {
	stream := component.ToneText("● LIVE", component.ToneSuccess)
	if page.exec.loading && !page.exec.loaded {
		stream = component.Muted("↻ LOADING")
	} else if page.exec.unsupported {
		stream = component.ToneText("○ RESTART REQUIRED", component.ToneWarning)
	} else if page.exec.reconnecting {
		stream = component.ToneText("↻ RECONNECTING", component.ToneWarning)
	} else if !page.exec.connected {
		stream = component.Muted("○ OFFLINE")
	}
	follow := component.ToneText("● ON", component.ToneSuccess)
	if page.exec.paused {
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
	left += component.KeyValue("View", view) + "   " + component.KeyValue("Events", fmt.Sprintf("%d / %d", len(page.visibleExecutionEvents()), shellruntime.MaxExecutionFeedEvents))
	return component.TwoColumn(left, component.KeyValue("Mode", page.executionScopeLabel()), width)
}

func (page *LogsPage) executionHeaderView(width int) string {
	parts := []string{page.executionStatusView(width)}
	if page.exec.err != nil {
		parts = append(parts, component.BannerWidth(page.exec.err.Error(), component.ToneDanger, width))
	} else if page.exec.notice != "" {
		parts = append(parts, component.WrapContent(component.Muted(page.exec.notice), width))
	} else if page.exec.scopeNotice != "" {
		parts = append(parts, component.WrapContent(component.Muted(page.exec.scopeNotice), width))
	}
	return strings.Join(parts, "\n")
}

func (page *LogsPage) executionHelpView(width int) string {
	bindings := []key.Binding{
		component.Binding([]string{"h", "l", "left", "right"}, "←/→", "tabs"), component.Binding([]string{"v"}, "v", "view"), component.Binding([]string{"m"}, "m", "mode"),
		component.Binding([]string{"r"}, "r", "reconnect"), component.Binding([]string{"c"}, "c", "clear view"),
	}
	bindings = append(bindings, component.Binding([]string{"space"}, "space", executionFollowLabel(page.exec.paused)))
	return component.NewHelpFooter(bindings...).View(width)
}

func (page *LogsPage) executionBodyView(width, height int) string {
	bodyHeight := max(1, height)
	if page.view == logsViewBrowser {
		return page.executionBrowserBody(width, bodyHeight)
	}
	page.resizeExecutionViewport(width, bodyHeight)
	sticky := page.executionStickyHeader(width)
	if sticky != "" {
		page.resizeExecutionViewport(width, max(1, bodyHeight-lipgloss.Height(sticky)))
		sticky = page.executionStickyHeader(width)
	}
	body := page.exec.viewport.View()
	if len(page.visibleExecutionEvents()) == 0 {
		empty := page.exec.viewport
		empty.SetContent(component.Muted("Waiting for command output"))
		body = empty.View()
	}
	content := ""
	if sticky != "" {
		content = sticky + "\n"
	}
	content += body
	return content
}

func (page *LogsPage) executionStickyHeader(width int) string {
	if page == nil || len(page.exec.render.Segments) == 0 {
		return ""
	}
	top := page.exec.viewport.YOffset()
	for _, segment := range page.exec.render.Segments {
		if top < segment.BodyStartLine || top >= segment.EndLine {
			continue
		}
		return executionStickyFrame(segment.StickyLabel, width)
	}
	return ""
}

func executionStickyFrame(label string, width int) string {
	if width <= 0 || strings.TrimSpace(label) == "" {
		return ""
	}
	if width < 4 {
		return ansi.Truncate(label, width, "")
	}
	label = ansi.Truncate(label, max(0, width-6), "…")
	used := 4 + lipgloss.Width(label)
	return "╭─ " + label + " " + strings.Repeat("─", max(0, width-used-1)) + "╮"
}

func (page *LogsPage) logsTabMouseTargets(originX, originY, z int) []component.MouseTarget {
	_, spans := component.PageTabsLayout(logsTabLabels, int(page.tab), page.notice, page.width)
	targets := make([]component.MouseTarget, 0, len(spans))
	for _, span := range spans {
		tab := logsTab(span.Index)
		targets = append(targets, component.MouseTarget{
			ID: "logs.tab", Rect: component.Rect{X: originX + span.X, Y: originY, Width: span.Width, Height: 1}, Z: z,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				switch tab {
				case logsTabCommandExec:
					return tea.KeyPressMsg{Code: '2'}
				case logsTabToolCalls:
					return tea.KeyPressMsg{Code: '3'}
				default:
					return tea.KeyPressMsg{Code: '1'}
				}
			},
		})
	}
	return targets
}

func (page *LogsPage) executionMouseTargets(originX, originY, z, width, height int) []component.MouseTarget {
	return []component.MouseTarget{{
		ID: "logs.exec.scroll", Rect: component.Rect{X: originX, Y: originY, Width: width, Height: height}, Z: z,
		Handle: func(event component.MouseEvent) tea.Msg {
			switch event.Button {
			case tea.MouseWheelUp:
				return logsExecutionMouseMsg{Wheel: -1}
			case tea.MouseWheelDown:
				return logsExecutionMouseMsg{Wheel: 1}
			default:
				return nil
			}
		},
	}}
}

func (page *LogsPage) handleExecutionMouse(msg logsExecutionMouseMsg) {
	if msg.Wheel < 0 {
		page.exec.viewport.ScrollUp(3)
	} else if msg.Wheel > 0 {
		page.exec.viewport.ScrollDown(3)
	}
	page.exec.paused = !page.exec.viewport.AtBottom()
}

func (page *LogsPage) stopExecutionFeed() {
	page.stopExecutionFeedOnly()
	if page.exec.streamCancel != nil {
		page.exec.streamCancel()
		page.exec.streamCancel = nil
	}
	page.exec.streamCtx = nil
}

func (page *LogsPage) stopExecutionFeedOnly() {
	if page.exec.stream != nil {
		_ = page.exec.stream.Close()
		page.exec.stream = nil
	}
	page.exec.connected = false
}

func (page *LogsPage) appendExecutionFeedEvent(event shellruntime.ExecutionFeedEvent) {
	if page == nil {
		return
	}
	page.exec.events = append(page.exec.events, event)
	page.exec.events = trimExecutionFeed(page.exec.events)
}

func trimExecutionFeed(events []shellruntime.ExecutionFeedEvent) []shellruntime.ExecutionFeedEvent {
	if len(events) <= shellruntime.MaxExecutionFeedEvents {
		return events
	}
	return append([]shellruntime.ExecutionFeedEvent(nil), events[len(events)-shellruntime.MaxExecutionFeedEvents:]...)
}

func formatExecutionFeed(events []shellruntime.ExecutionFeedEvent, widths ...int) string {
	return renderExecutionFeed(events, widths...).Content
}

func renderExecutionFeed(events []shellruntime.ExecutionFeedEvent, widths ...int) executionFeedRender {
	width := 80
	if len(widths) > 0 && widths[0] > 0 {
		width = widths[0]
	}
	type executionSegment struct {
		start       shellruntime.ExecutionFeedEvent
		end         shellruntime.ExecutionFeedEvent
		last        shellruntime.ExecutionFeedEvent
		body        strings.Builder
		first       bool
		final       bool
		interrupted bool
	}
	seen := map[string]bool{}
	segments := []executionSegment{}
	var current *executionSegment
	closeCurrent := func(end shellruntime.ExecutionFeedEvent, final, interrupted bool) {
		if current == nil {
			return
		}
		current.end, current.final, current.interrupted = end, final, interrupted
		segments = append(segments, *current)
		current = nil
	}
	openSegment := func(event shellruntime.ExecutionFeedEvent) {
		current = &executionSegment{start: event, last: event, first: !seen[event.ExecutionID] || event.Type == shellruntime.ExecutionEventStarted}
		seen[event.ExecutionID] = true
	}
	for _, event := range events {
		if current == nil || event.ExecutionID != current.start.ExecutionID {
			if current != nil {
				closeCurrent(current.last, false, true)
			}
			openSegment(event)
		}
		switch event.Type {
		case shellruntime.ExecutionEventStarted:
			current.start = event
		case shellruntime.ExecutionEventOutput:
			current.body.WriteString(event.Data)
		case shellruntime.ExecutionEventCompleted:
			current.last = event
			closeCurrent(event, true, false)
			continue
		}
		current.last = event
	}
	if current != nil {
		closeCurrent(current.last, false, false)
	}
	var output strings.Builder
	rendered := executionFeedRender{}
	stickyLabels := map[string]string{}
	line := 0
	for index, segment := range segments {
		if index > 0 {
			output.WriteString("\n")
			line++
		}
		block := formatExecutionSegment(segment.start, segment.end, segment.body.String(), segment.first, segment.final, segment.interrupted, width)
		startLine := line
		bodyStartLine := startLine
		for blockLine, value := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
			if strings.HasPrefix(value, "├") {
				bodyStartLine = startLine + blockLine + 1
				break
			}
		}
		blockLines := strings.Count(block, "\n")
		if blockLines == 0 && block != "" {
			blockLines = 1
		}
		line += blockLines
		output.WriteString(block)
		stickyLabel := executionStickyLabel(segment.start)
		if previous := stickyLabels[segment.start.ExecutionID]; previous != "" && !segment.first {
			stickyLabel = previous
		} else if segment.start.ExecutionID != "" {
			stickyLabels[segment.start.ExecutionID] = stickyLabel
		}
		rendered.Segments = append(rendered.Segments, executionRenderedSegment{StartLine: startLine, BodyStartLine: bodyStartLine, EndLine: line, StickyLabel: stickyLabel})
	}
	rendered.Content = output.String()
	return rendered
}

func executionStickyLabel(event shellruntime.ExecutionFeedEvent) string {
	parts := []string{"START"}
	if started := executionStartClock(event); started != "" {
		parts = append(parts, started)
	}
	if event.ExecutionID != "" {
		parts = append(parts, event.ExecutionID)
	}
	workspaceID := event.WorkspaceID
	if event.Execution != nil && event.Execution.WorkspaceID != "" {
		workspaceID = event.Execution.WorkspaceID
	}
	if workspaceID != "" {
		parts = append(parts, workspaceID)
	}
	if event.Execution != nil && strings.TrimSpace(event.Execution.Command) != "" {
		parts = append(parts, "$ "+sanitizeExecutionInline(event.Execution.Command))
	}
	return strings.Join(parts, " · ")
}

func executionStartClock(event shellruntime.ExecutionFeedEvent) string {
	if event.Execution != nil && event.Execution.StartedAt != "" {
		if started, err := time.Parse(time.RFC3339Nano, event.Execution.StartedAt); err == nil {
			return started.Local().Format("15:04:05.000")
		}
	}
	if value := executionEventTime(event); !value.IsZero() {
		return value.Local().Format("15:04:05.000")
	}
	return ""
}

func formatExecutionSegment(start, end shellruntime.ExecutionFeedEvent, body string, first, final, interrupted bool, width int) string {
	headerKind := "CONTINUE"
	if first {
		headerKind = "START"
	}
	headerFields := executionHeaderFields(start, first)
	content := []string{}
	if first && start.Execution != nil && strings.TrimSpace(start.Execution.Command) != "" {
		language := strings.TrimSpace(start.Execution.Shell)
		if language == "" {
			language = "shell"
		}
		content = append(content, component.RenderCodeBlock(sanitizeExecutionInline(start.Execution.Command), language, max(1, width-4)))
	}
	if clean := strings.TrimSuffix(sanitizeExecutionOutput(body), "\n"); clean != "" {
		content = append(content, strings.Split(clean, "\n")...)
	}
	if len(content) == 0 {
		content = append(content, "No output")
	}
	footerKind := "RUNNING"
	footerFields := []executionFrameField{}
	if final {
		footerKind = "END"
		footerFields = executionEndFields(end)
	} else if interrupted {
		footerKind = "PAUSE"
	}
	return executionSegmentFrame(headerKind, footerKind, start, end, headerFields, content, footerFields, width)
}

func sanitizeExecutionOutput(value string) string {
	value = ansi.Strip(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

func sanitizeExecutionInline(value string) string {
	return strings.Join(strings.Fields(sanitizeExecutionOutput(value)), " ")
}

func executionHeaderFields(event shellruntime.ExecutionFeedEvent, first bool) []executionFrameField {
	if !first {
		workspaceID := event.WorkspaceID
		if event.Execution != nil && event.Execution.WorkspaceID != "" {
			workspaceID = event.Execution.WorkspaceID
		}
		if workspaceID == "" {
			return nil
		}
		return []executionFrameField{{Label: "Workspace", Values: []string{workspaceID}}}
	}
	fields := []executionFrameField{}
	info := event.Execution
	if info == nil {
		if event.WorkspaceID != "" {
			fields = append(fields, executionFrameField{Label: "Workspace", Values: []string{event.WorkspaceID}})
		}
		return fields
	}
	if info.WorkspaceID != "" {
		fields = append(fields, executionFrameField{Label: "Workspace", Values: []string{info.WorkspaceID}})
	}
	if info.Source != "" {
		fields = append(fields, executionFrameField{Label: "Source", Values: []string{info.Source}})
	}
	if info.SessionHash != "" {
		fields = append(fields, executionFrameField{Label: "Session", Values: []string{info.SessionHash}})
	}
	if info.CallID != "" {
		fields = append(fields, executionFrameField{Label: "Call", Values: []string{info.CallID}})
	}
	route := []string{}
	if info.ReceivedByInstanceID != "" {
		route = append(route, "received: "+info.ReceivedByInstanceID)
	}
	if info.ExecutedByInstanceID != "" {
		route = append(route, "executed: "+info.ExecutedByInstanceID)
	}
	if len(route) > 0 {
		fields = append(fields, executionFrameField{Label: "Route", Values: route})
	}
	if info.CWD != "" {
		fields = append(fields, executionFrameField{Label: "CWD", Values: []string{info.CWD}})
	}
	if info.Shell != "" {
		fields = append(fields, executionFrameField{Label: "Shell", Values: []string{info.Shell}})
	}
	return fields
}

func executionEndFields(event shellruntime.ExecutionFeedEvent) []executionFrameField {
	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "completed"
	}
	fields := []executionFrameField{{Label: "Status", Values: []string{status}}}
	if event.ExitCode != nil {
		fields = append(fields, executionFrameField{Label: "Exit", Values: []string{fmt.Sprintf("%d", *event.ExitCode)}})
	}
	if duration := executionEventDuration(event); duration != "" {
		fields = append(fields, executionFrameField{Label: "Duration", Values: []string{duration}})
	}
	return fields
}

func executionSegmentFrame(headerKind, footerKind string, start, end shellruntime.ExecutionFeedEvent, headerFields []logFrameField, content []string, footerFields []logFrameField, width int) string {
	headerLabel := strings.TrimSpace(headerKind + " " + executionEventClock(start))
	if headerKind == "CONTINUE" {
		if elapsed := executionEventElapsed(start); elapsed != "" {
			headerLabel += " +" + elapsed
		}
	}
	footerLabel := strings.TrimSpace(footerKind + " " + executionEventClock(end))
	return renderLogBlock(headerLabel, footerLabel, "Execution", start.ExecutionID, headerFields, content, footerFields, width)
}

type executionFrameField = logFrameField

func executionEventClock(event shellruntime.ExecutionFeedEvent) string {
	value := executionEventTime(event)
	if value.IsZero() {
		return "--:--:--.---"
	}
	return value.Local().Format("15:04:05.000")
}

func executionEventElapsed(event shellruntime.ExecutionFeedEvent) string {
	if event.Execution == nil {
		return ""
	}
	started, err := time.Parse(time.RFC3339Nano, event.Execution.StartedAt)
	if err != nil {
		return ""
	}
	current := executionEventTime(event)
	if current.IsZero() || current.Before(started) {
		return ""
	}
	return current.Sub(started).Round(time.Millisecond).String()
}

func executionEventDuration(event shellruntime.ExecutionFeedEvent) string {
	if event.Execution == nil {
		return ""
	}
	started, err := time.Parse(time.RFC3339Nano, event.Execution.StartedAt)
	if err != nil {
		return ""
	}
	finished := executionEventTime(event)
	if event.Execution.FinishedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, event.Execution.FinishedAt); err == nil {
			finished = parsed
		}
	}
	if finished.IsZero() || finished.Before(started) {
		return ""
	}
	return finished.Sub(started).Round(time.Millisecond).String()
}

func executionEventTime(event shellruntime.ExecutionFeedEvent) time.Time {
	if event.Timestamp != "" {
		if value, err := time.Parse(time.RFC3339Nano, event.Timestamp); err == nil {
			return value
		}
	}
	if event.Execution == nil {
		return time.Time{}
	}
	value := event.Execution.StartedAt
	if event.Type == shellruntime.ExecutionEventCompleted && event.Execution.FinishedAt != "" {
		value = event.Execution.FinishedAt
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func executionFollowLabel(paused bool) string {
	if paused {
		return "resume"
	}
	return "pause"
}
