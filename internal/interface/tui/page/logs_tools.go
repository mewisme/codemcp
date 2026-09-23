package page

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/runtime/activity"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

type logsToolCallFeed struct {
	viewport       viewport.Model
	render         executionFeedRender
	events         []activity.Event
	scope          logsScopeState
	stream         *runtimecontrol.ToolCallFeedStream
	streamCancel   context.CancelFunc
	generation     uint64
	latestSeq      uint64
	loaded         bool
	loading        bool
	connected      bool
	reconnecting   bool
	unsupported    bool
	paused         bool
	notice         string
	err            error
	restoreYOffset int
}

type logsToolCallOpenMsg struct {
	generation uint64
	stream     *runtimecontrol.ToolCallFeedStream
	err        error
}

type logsToolCallEventMsg struct {
	generation uint64
	event      activity.Event
	err        error
}

type logsToolCallReconnectMsg uint64

type toolCallRecord struct {
	CallID string
	First  activity.Event
	Latest activity.Event
}

func newLogsToolCallFeed() logsToolCallFeed {
	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	view.SoftWrap = false
	view.FillHeight = false
	return logsToolCallFeed{viewport: view, scope: newLogsScopeState()}
}

func NewToolCallLogsRoute(ctx context.Context, resourceID string) (*LogsPage, error) {
	page, err := NewLogsRoute(ctx, "", "")
	if err != nil {
		return nil, err
	}
	page.tab = logsTabToolCalls
	page.resourceID = strings.TrimSpace(resourceID)
	page.view = logsViewBrowser
	if page.resourceID != "" {
		page.detail = component.NewDetailPage("Tool Call · "+page.resourceID, "loading", component.Muted("Loading tool call...")).WithTitleVisible(false)
	}
	return page, nil
}

func (page *LogsPage) startToolCallFeed() tea.Cmd {
	page.stopToolCallFeed()
	page.refreshToolCallScope()
	page.tools.generation++
	generation := page.tools.generation
	ctx, cancel := context.WithCancel(page.ctx)
	page.tools.streamCancel = cancel
	page.tools.loading, page.tools.reconnecting = true, page.tools.loaded
	page.tools.unsupported, page.tools.err = false, nil
	return func() tea.Msg {
		stream, _, err := runtimecontrol.OpenToolCallFeed(ctx)
		return logsToolCallOpenMsg{generation: generation, stream: stream, err: err}
	}
}

func (page *LogsPage) finishToolCallFeedOpen(msg logsToolCallOpenMsg) tea.Cmd {
	if msg.generation != page.tools.generation {
		if msg.stream != nil {
			_ = msg.stream.Close()
		}
		return nil
	}
	page.tools.loading = false
	if msg.err != nil {
		page.tools.loaded = true
		page.tools.err = msg.err
		if errors.Is(msg.err, runtimecontrol.ErrToolCallFeedUnsupported) {
			page.tools.connected, page.tools.reconnecting, page.tools.unsupported = false, false, true
			page.tools.notice = "Restart the running server to enable tool call streaming"
			return nil
		}
		page.tools.connected, page.tools.reconnecting = false, true
		page.tools.notice = "Runtime offline; reconnecting tool call stream"
		return page.toolCallReconnectCmd(msg.generation)
	}
	page.tools.stream, page.tools.connected, page.tools.reconnecting, page.tools.loaded = msg.stream, true, false, true
	snapshot := msg.stream.Snapshot()
	page.tools.latestSeq = snapshot.LatestSequence
	page.tools.events = trimToolCallEvents(snapshot.Events)
	page.tools.notice, page.tools.err = "", nil
	page.refreshToolCallView()
	if page.resourceID != "" {
		page.syncToolCallDetail()
	}
	return page.nextToolCallEventCmd(msg.generation)
}

func (page *LogsPage) nextToolCallEventCmd(generation uint64) tea.Cmd {
	stream := page.tools.stream
	if stream == nil {
		return nil
	}
	return func() tea.Msg {
		event, err := stream.Next()
		return logsToolCallEventMsg{generation: generation, event: event, err: err}
	}
}

func (page *LogsPage) finishToolCallEvent(msg logsToolCallEventMsg) tea.Cmd {
	if msg.generation != page.tools.generation {
		return nil
	}
	if msg.err != nil {
		if !errors.Is(msg.err, runtimecontrol.ErrToolCallFeedOverflow) && msg.err != io.EOF && page.ctx.Err() != nil {
			return nil
		}
		page.tools.connected, page.tools.reconnecting = false, true
		page.tools.notice = "Tool call stream disconnected; replaying bounded history"
		page.stopToolCallFeedOnly()
		return page.toolCallReconnectCmd(msg.generation)
	}
	if msg.event.Sequence == 0 || msg.event.Sequence <= page.tools.latestSeq {
		return page.nextToolCallEventCmd(msg.generation)
	}
	page.tools.latestSeq = msg.event.Sequence
	page.tools.events = trimToolCallEvents(append(page.tools.events, msg.event))
	page.tools.notice, page.tools.err = "", nil
	if !page.tools.paused {
		page.refreshToolCallView()
	}
	if page.resourceID != "" && msg.event.CallID == page.resourceID {
		page.syncToolCallDetail()
	}
	return page.nextToolCallEventCmd(msg.generation)
}

func (page *LogsPage) toolCallReconnectCmd(generation uint64) tea.Cmd {
	return tea.Tick(logsReconnectDelay, func(time.Time) tea.Msg { return logsToolCallReconnectMsg(generation) })
}

func (page *LogsPage) stopToolCallFeed() {
	page.stopToolCallFeedOnly()
	if page.tools.streamCancel != nil {
		page.tools.streamCancel()
		page.tools.streamCancel = nil
	}
}

func (page *LogsPage) stopToolCallFeedOnly() {
	if page.tools.stream != nil {
		_ = page.tools.stream.Close()
		page.tools.stream = nil
	}
	page.tools.connected = false
}

func trimToolCallEvents(events []activity.Event) []activity.Event {
	if len(events) <= activity.MaxRecentEvents {
		return events
	}
	return append([]activity.Event(nil), events[len(events)-activity.MaxRecentEvents:]...)
}

func (page *LogsPage) visibleToolCallRecords() []toolCallRecord {
	byID := map[string]*toolCallRecord{}
	for _, event := range page.tools.events {
		if event.CallID == "" || !matchScope(page.tools.scope, event.WorkspaceID) {
			continue
		}
		record := byID[event.CallID]
		if record == nil {
			record = &toolCallRecord{CallID: event.CallID, First: event, Latest: event}
			byID[event.CallID] = record
		} else if event.Sequence >= record.Latest.Sequence {
			record.Latest = event
		}
	}
	result := make([]toolCallRecord, 0, len(byID))
	for _, record := range byID {
		result = append(result, *record)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].First.Sequence < result[j].First.Sequence })
	return result
}

func (page *LogsPage) rebuildToolCallBrowser() {
	rows := make([]component.Row, 0)
	for _, record := range page.visibleToolCallRecords() {
		event := record.Latest
		clock := event.Timestamp.Local().Format("15:04:05")
		rows = append(rows, component.Row{ID: record.CallID, Title: compactParts(clock, event.Tool, event.Status), Description: record.CallID, Meta: event.WorkspaceID, Search: compactParts(record.CallID, event.Tool, event.Status, event.WorkspaceID, event.Source)})
	}
	selected := ""
	if row, ok := page.browser.Selected(); ok {
		selected = row.ID
	}
	if !page.tools.paused {
		selected = ""
	}
	_ = page.browser.ReplaceRows(rows, selected)
	if !page.tools.paused {
		page.browser.SelectLast()
	}
}

func (page *LogsPage) refreshToolCallView() {
	if page == nil || page.tab != logsTabToolCalls || page.resourceID != "" {
		return
	}
	if page.view == logsViewBrowser {
		page.rebuildToolCallBrowser()
		return
	}
	offset := page.tools.viewport.YOffset()
	page.tools.render = renderToolCallTimeline(page.visibleToolCallRecords(), max(1, page.tools.viewport.Width()))
	page.tools.viewport.SetContent(page.tools.render.Content)
	if !page.tools.paused {
		page.tools.viewport.GotoBottom()
	} else {
		page.tools.viewport.SetYOffset(min(offset, max(0, page.tools.viewport.TotalLineCount()-page.tools.viewport.Height())))
	}
}

func (page *LogsPage) resizeToolCallViewport(width, height int) {
	width, height = max(1, width), max(1, height)
	if page.tools.viewport.Width() != width {
		page.tools.viewport.SetWidth(width)
		page.refreshToolCallView()
	}
	page.tools.viewport.SetHeight(height)
	if !page.tools.paused {
		page.tools.viewport.GotoBottom()
	}
}

func (page *LogsPage) toolCallBodyView(width, height int) string {
	if page.view == logsViewBrowser {
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: height})
		page.browser = updated.(component.Browser)
		return page.browser.BodyContent()
	}
	page.resizeToolCallViewport(width, height)
	sticky := stickyLogHeader(page.tools.render, page.tools.viewport.YOffset(), width)
	if sticky != "" {
		page.resizeToolCallViewport(width, max(1, height-lipgloss.Height(sticky)))
		sticky = stickyLogHeader(page.tools.render, page.tools.viewport.YOffset(), width)
	}
	body := page.tools.viewport.View()
	if len(page.visibleToolCallRecords()) == 0 {
		empty := page.tools.viewport
		empty.SetContent(component.Muted("Waiting for tool calls"))
		body = empty.View()
	}
	if sticky != "" {
		return sticky + "\n" + body
	}
	return body
}

func renderToolCallTimeline(records []toolCallRecord, width int) executionFeedRender {
	rendered := executionFeedRender{}
	var output strings.Builder
	line := 0
	for index, record := range records {
		if index > 0 {
			output.WriteByte('\n')
			line++
		}
		event := record.Latest
		clock := record.First.Timestamp.Local().Format("15:04:05.000")
		top := "CALL " + clock
		bottom := strings.ToUpper(strings.TrimSpace(event.Status))
		if bottom == "" {
			bottom = "RUNNING"
		}
		fields := []logFrameField{{Label: "Tool", Values: []string{event.Tool}}}
		if event.WorkspaceID != "" {
			fields = append(fields, logFrameField{Label: "Workspace", Values: []string{event.WorkspaceID}})
		}
		if event.Source != "" {
			fields = append(fields, logFrameField{Label: "Source", Values: []string{event.Source}})
		}
		content := toolCallTimelineContent(record, max(1, width-4))
		footer := []logFrameField{{Label: "Status", Values: []string{event.Status}}}
		if event.DurationMS > 0 {
			footer = append(footer, logFrameField{Label: "Duration", Values: []string{fmt.Sprintf("%dms", event.DurationMS)}})
		}
		block := renderLogBlock(top, bottom, "Call", record.CallID, fields, content, footer, width)
		start := line
		bodyStart := start
		for blockLine, value := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
			if strings.HasPrefix(value, "├") {
				bodyStart = start + blockLine + 1
				break
			}
		}
		line += strings.Count(block, "\n")
		output.WriteString(block)
		sticky := compactParts("CALL", clock, record.CallID, event.Tool)
		rendered.Segments = append(rendered.Segments, executionRenderedSegment{StartLine: start, BodyStartLine: bodyStart, EndLine: line, StickyLabel: sticky})
	}
	rendered.Content = output.String()
	return rendered
}

func toolCallTimelineContent(record toolCallRecord, width int) []string {
	request := any(nil)
	if record.First.Raw != nil {
		request = record.First.Raw["arguments"]
		if request == nil {
			request = record.First.Raw["params"]
		}
	}
	content := []string{"REQUEST", component.CodeBlockMarkdown(marshalJSONValue(request), "json")}
	latest := record.Latest
	if latest.Phase == "finish" || latest.Status != "running" {
		if latest.Raw != nil && latest.Raw["error"] != nil {
			content = append(content, "ERROR", component.CodeBlockMarkdown(marshalJSONValue(latest.Raw["error"]), "json"))
		} else {
			var response any
			if latest.Raw != nil {
				response = latest.Raw["result"]
			}
			content = append(content, "RESPONSE", component.CodeBlockMarkdown(marshalJSONValue(response), "json"))
		}
	} else {
		content = append(content, "RESPONSE", "waiting...")
	}
	return []string{component.RenderMarkdownCompact(strings.Join(content, "\n\n"), width)}
}

func marshalJSONValue(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(value))
	}
	return string(data)
}

func (page *LogsPage) syncToolCallDetail() {
	for _, record := range page.visibleToolCallRecords() {
		if record.CallID != page.resourceID {
			continue
		}
		data, _ := json.MarshalIndent(record.Latest, "", "  ")
		content := component.RenderCodeBlock(string(data), "json", max(20, page.width))
		meta := compactParts(record.Latest.Tool, record.Latest.Status, record.Latest.WorkspaceID)
		page.detail = component.NewDetailPage("Tool Call · "+record.CallID, meta, content).WithTitleVisible(false)
		if page.width > 0 && page.height > 0 {
			page.detail.Resize(page.width, page.height)
		}
		return
	}
	page.detail = component.NewDetailPage("Tool Call · "+page.resourceID, "unavailable", component.Muted("Tool call not found in retained history.")).WithTitleVisible(false)
}

func (page *LogsPage) toolCallStatusView(width int) string {
	stream := component.ToneText("● LIVE", component.ToneSuccess)
	if page.tools.loading && !page.tools.loaded {
		stream = component.Muted("↻ LOADING")
	} else if page.tools.unsupported {
		stream = component.ToneText("○ RESTART REQUIRED", component.ToneWarning)
	} else if page.tools.reconnecting {
		stream = component.ToneText("↻ RECONNECTING", component.ToneWarning)
	} else if !page.tools.connected {
		stream = component.Muted("○ OFFLINE")
	}
	left := component.KeyValue("Stream", stream) + "   " + component.KeyValue("View", map[logsDisplayView]string{logsViewBrowser: "Browser", logsViewTimeline: "Timeline"}[page.view]) + "   " + component.KeyValue("Events", fmt.Sprintf("%d / %d", len(page.tools.events), activity.MaxRecentEvents))
	if page.view == logsViewTimeline {
		follow := component.ToneText("● ON", component.ToneSuccess)
		if page.tools.paused {
			follow = component.ToneText("○ PAUSED", component.ToneWarning)
		}
		left = component.KeyValue("Stream", stream) + "   " + component.KeyValue("Follow", follow) + "   " + component.KeyValue("View", "Timeline") + "   " + component.KeyValue("Events", fmt.Sprintf("%d / %d", len(page.tools.events), activity.MaxRecentEvents))
	}
	return component.TwoColumn(left, component.KeyValue("Mode", logsScopeLabel(page.tools.scope)), width)
}

func (page *LogsPage) toolCallHelpView(width int) string {
	bindings := []key.Binding{component.Binding([]string{"h", "l", "left", "right"}, "←/→", "tabs"), component.Binding([]string{"v"}, "v", "view"), component.Binding([]string{"m"}, "m", "mode"), component.Binding([]string{"r"}, "r", "reconnect")}
	bindings = append(bindings, component.Binding([]string{"space"}, "space", executionFollowLabel(page.tools.paused)))
	return component.NewHelpFooter(bindings...).View(width)
}

func (page *LogsPage) handleToolCallKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "v":
		page.toggleLogsView()
		page.syncBrowserHelp()
		return nil
	case "m":
		return page.openLogsModeDialog()
	case "space":
		page.tools.paused = !page.tools.paused
		if !page.tools.paused {
			if page.view == logsViewBrowser {
				page.browser.SelectLast()
			} else {
				page.refreshToolCallView()
			}
		}
		page.syncBrowserHelp()
		return nil
	case "r":
		return page.startToolCallFeed()
	}
	if page.view == logsViewBrowser {
		before := ""
		if row, ok := page.browser.Selected(); ok {
			before = row.ID
		}
		updated, cmd := page.browser.Update(msg)
		page.browser = updated.(component.Browser)
		if !page.tools.paused && before != "" {
			if row, ok := page.browser.Selected(); ok && row.ID != before {
				page.tools.paused = true
				page.syncBrowserHelp()
			}
		}
		return cmd
	}
	view, cmd := page.tools.viewport.Update(msg)
	page.tools.viewport = view
	if !page.tools.viewport.AtBottom() {
		page.tools.paused = true
	}
	return cmd
}
