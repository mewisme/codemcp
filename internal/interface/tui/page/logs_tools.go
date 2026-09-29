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
	window         logsTimelineWindow
	events         []activity.Event
	records        []activity.ToolCallRecord
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
	details        map[string]activity.ToolCallDetail
	detailErrors   map[string]string
	detailPending  map[string]bool
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

type logsToolCallDetailMsg struct {
	generation uint64
	id         string
	detail     activity.ToolCallDetail
	err        error
}

type logsToolCallReconnectMsg uint64

type toolCallRecord = activity.ToolCallRecord

const toolCallTimelineDetailConcurrency = 6

func newLogsToolCallFeed() logsToolCallFeed {
	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	view.SoftWrap = false
	view.FillHeight = false
	return logsToolCallFeed{
		viewport: view, scope: newLogsScopeState(),
		details: map[string]activity.ToolCallDetail{}, detailErrors: map[string]string{}, detailPending: map[string]bool{},
	}
}

func NewToolCallLogsRoute(ctx context.Context, resourceID string) (*LogsPage, error) {
	page, err := NewLogsRoute(ctx, "", "")
	if err != nil {
		return nil, err
	}
	page.tab = logsTabToolCalls
	page.resourceID = strings.TrimSpace(resourceID)
	page.view = logsViewBrowser
	page.syncBrowserHelp()
	if page.resourceID != "" {
		page.detail = component.NewDetailPage("Tool Call · "+page.resourceID, "loading", component.Muted("Loading tool call...")).WithTitleVisible(false)
	}
	return page, nil
}

func (page *LogsPage) startToolCallFeed() tea.Cmd {
	page.stopToolCallFeed()
	page.refreshToolCallScope()
	page.tools.window.invalidate()
	page.tools.generation++
	page.tools.details = map[string]activity.ToolCallDetail{}
	page.tools.detailErrors = map[string]string{}
	page.tools.detailPending = map[string]bool{}
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
	page.tools.records = normalizeToolCallRecords(snapshot.Records)
	if len(page.tools.records) == 0 && len(page.tools.events) > 0 {
		page.tools.records = aggregateToolCallRecords(page.tools.events)
	}
	page.tools.notice, page.tools.err = "", nil
	page.refreshToolCallView()
	next := page.nextToolCallEventCmd(msg.generation)
	if page.resourceID != "" {
		return tea.Batch(next, page.toolCallDetailCmd(page.resourceID))
	}
	return tea.Batch(next, page.toolCallTimelineHydrateCmd())
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
	page.upsertToolCallRecord(msg.event)
	if msg.event.CallID != "" {
		delete(page.tools.details, msg.event.CallID)
		delete(page.tools.detailErrors, msg.event.CallID)
		delete(page.tools.detailPending, msg.event.CallID)
	}
	page.tools.notice, page.tools.err = "", nil
	if !page.tools.paused {
		page.refreshToolCallView()
	}
	var detail tea.Cmd
	if msg.event.CallID != "" && (page.resourceID == msg.event.CallID || page.view == logsViewTimeline) {
		detail = page.toolCallDetailCmd(msg.event.CallID)
	}
	return tea.Batch(page.nextToolCallEventCmd(msg.generation), detail)
}

func (page *LogsPage) toolCallDetailCmd(id string) tea.Cmd {
	id = strings.TrimSpace(id)
	if page == nil || id == "" {
		return nil
	}
	if page.tools.detailPending[id] {
		return nil
	}
	page.tools.detailPending[id] = true
	generation := page.tools.generation
	ctx := page.ctx
	return func() tea.Msg {
		detail, err := runtimecontrol.GetToolCallDetail(ctx, id)
		return logsToolCallDetailMsg{generation: generation, id: id, detail: detail, err: err}
	}
}

func (page *LogsPage) toolCallTimelineHydrateCmd() tea.Cmd {
	if page == nil || page.resourceID != "" || page.view != logsViewTimeline {
		return nil
	}
	records := page.visibleToolCallTimelineRecords()
	keep := make(map[string]struct{}, len(records))
	for _, record := range records {
		keep[record.CallID] = struct{}{}
	}
	for id := range page.tools.details {
		if _, ok := keep[id]; !ok {
			delete(page.tools.details, id)
			delete(page.tools.detailErrors, id)
		}
	}
	available := toolCallTimelineDetailConcurrency - len(page.tools.detailPending)
	if available <= 0 {
		return nil
	}
	commands := make([]tea.Cmd, 0, available)
	for index := len(records) - 1; index >= 0 && available > 0; index-- {
		record := records[index]
		if detail, ok := page.tools.details[record.CallID]; ok && detail.Sequence >= record.Latest.Sequence {
			continue
		}
		if page.tools.detailPending[record.CallID] {
			continue
		}
		if cmd := page.toolCallDetailCmd(record.CallID); cmd != nil {
			commands = append(commands, cmd)
			available--
		}
	}
	return tea.Batch(commands...)
}

func (page *LogsPage) finishToolCallDetail(msg logsToolCallDetailMsg) tea.Cmd {
	if page == nil || msg.generation != page.tools.generation {
		return nil
	}
	delete(page.tools.detailPending, msg.id)
	if msg.err != nil {
		if _, ok := page.tools.details[msg.id]; !ok {
			page.tools.detailErrors[msg.id] = msg.err.Error()
		}
	} else {
		current, exists := page.tools.details[msg.id]
		if !exists || msg.detail.Sequence >= current.Sequence {
			page.tools.details[msg.id] = msg.detail
			delete(page.tools.detailErrors, msg.id)
		}
	}
	if page.resourceID == msg.id {
		page.syncToolCallDetail()
	} else if page.view == logsViewTimeline && !page.tools.paused {
		page.refreshToolCallView()
	}
	return page.toolCallTimelineHydrateCmd()
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
		return append([]activity.Event(nil), events...)
	}
	return append([]activity.Event(nil), events[len(events)-activity.MaxRecentEvents:]...)
}

func (page *LogsPage) visibleToolCallRecords() []toolCallRecord {
	if page == nil {
		return nil
	}
	if len(page.tools.records) == 0 {
		return aggregateToolCallRecords(page.eligibleToolCallTimelineEvents())
	}
	result := make([]toolCallRecord, 0, len(page.tools.records))
	for _, record := range page.tools.records {
		if record.CallID == "" || record.Latest.Sequence <= page.toolCallClear || !matchScope(page.tools.scope, record.Latest.WorkspaceID) {
			continue
		}
		result = append(result, record)
	}
	if len(result) > activity.MaxRecentToolCalls {
		result = append([]toolCallRecord(nil), result[len(result)-activity.MaxRecentToolCalls:]...)
	}
	return result
}

func (page *LogsPage) visibleToolCallTimelineRecords() []toolCallRecord {
	return aggregateToolCallRecords(page.visibleToolCallTimelineEvents())
}

func (page *LogsPage) visibleToolCallTimelineEvents() []activity.Event {
	events := page.eligibleToolCallTimelineEvents()
	records := aggregateToolCallRecords(events)
	start, end := page.tools.window.rangeFor(len(records), !page.tools.paused)
	if start == 0 && end == len(records) {
		return events
	}
	selected := make(map[string]struct{}, end-start)
	for _, record := range records[start:end] {
		selected[record.CallID] = struct{}{}
	}
	result := make([]activity.Event, 0, len(events))
	for _, event := range events {
		if _, ok := selected[event.CallID]; ok {
			result = append(result, event)
		}
	}
	return result
}

func (page *LogsPage) eligibleToolCallTimelineEvents() []activity.Event {
	if page == nil || len(page.tools.events) == 0 {
		return nil
	}
	visible := make([]activity.Event, 0, len(page.tools.events))
	for _, event := range page.tools.events {
		if event.CallID != "" && event.Sequence > page.toolCallClear && matchScope(page.tools.scope, event.WorkspaceID) {
			visible = append(visible, event)
		}
	}
	if len(visible) > activity.MaxRecentEvents {
		visible = append([]activity.Event(nil), visible[len(visible)-activity.MaxRecentEvents:]...)
	}
	return visible
}

func aggregateToolCallRecords(events []activity.Event) []toolCallRecord {
	byID := map[string]*toolCallRecord{}
	for _, event := range events {
		if event.CallID == "" {
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

func normalizeToolCallRecords(records []activity.ToolCallRecord) []activity.ToolCallRecord {
	if len(records) == 0 {
		return nil
	}
	if len(records) > activity.MaxRecentToolCalls {
		records = records[len(records)-activity.MaxRecentToolCalls:]
	}
	return append([]activity.ToolCallRecord(nil), records...)
}

func (page *LogsPage) upsertToolCallRecord(event activity.Event) {
	if page == nil || event.CallID == "" {
		return
	}
	for index := range page.tools.records {
		if page.tools.records[index].CallID != event.CallID {
			continue
		}
		if event.Sequence >= page.tools.records[index].Latest.Sequence {
			page.tools.records[index].Latest = event
		}
		return
	}
	page.tools.records = append(page.tools.records, activity.ToolCallRecord{CallID: event.CallID, First: event, Latest: event})
	if len(page.tools.records) > activity.MaxRecentToolCalls {
		page.tools.records = append([]activity.ToolCallRecord(nil), page.tools.records[len(page.tools.records)-activity.MaxRecentToolCalls:]...)
	}
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
	page.tools.render = renderToolCallTimelineDetailed(page.visibleToolCallTimelineRecords(), max(1, page.tools.viewport.Width()), page.tools.details, page.tools.detailErrors)
	page.tools.viewport.SetContent(page.tools.render.Content)
	if !page.tools.paused {
		page.tools.viewport.GotoBottom()
	} else {
		page.tools.viewport.SetYOffset(min(offset, max(0, page.tools.viewport.TotalLineCount()-page.tools.viewport.Height())))
	}
}

func (page *LogsPage) maybeExpandToolCallTimeline() {
	if page == nil || !page.tools.paused || page.tools.viewport.YOffset() > logsTimelineNearOldestLines {
		return
	}
	oldLines := page.tools.viewport.TotalLineCount()
	oldOffset := page.tools.viewport.YOffset()
	if !page.tools.window.expand(len(aggregateToolCallRecords(page.eligibleToolCallTimelineEvents()))) {
		return
	}
	page.tools.render = renderToolCallTimelineDetailed(page.visibleToolCallTimelineRecords(), max(1, page.tools.viewport.Width()), page.tools.details, page.tools.detailErrors)
	page.tools.viewport.SetContent(page.tools.render.Content)
	delta := max(0, page.tools.viewport.TotalLineCount()-oldLines)
	page.tools.viewport.SetYOffset(min(oldOffset+delta, max(0, page.tools.viewport.TotalLineCount()-page.tools.viewport.Height())))
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
	if len(page.visibleToolCallTimelineRecords()) == 0 {
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
	return renderToolCallTimelineDetailed(records, width, nil, nil)
}

func renderToolCallTimelineDetailed(records []toolCallRecord, width int, details map[string]activity.ToolCallDetail, detailErrors map[string]string) executionFeedRender {
	type toolCallRenderedBlock struct {
		block  string
		sticky string
	}
	blocks := make([]toolCallRenderedBlock, len(records))
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
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
		content := toolCallTimelineContentDetailed(record, details, detailErrors, max(1, width-4))
		footer := []logFrameField{{Label: "Status", Values: []string{event.Status}}}
		if event.DurationMS > 0 {
			footer = append(footer, logFrameField{Label: "Duration", Values: []string{fmt.Sprintf("%dms", event.DurationMS)}})
		}
		blocks[index] = toolCallRenderedBlock{
			block:  renderLogBlock(top, bottom, "Call", record.CallID, fields, content, footer, width),
			sticky: compactParts("CALL", clock, record.CallID, event.Tool),
		}
	}
	rendered := executionFeedRender{}
	var output strings.Builder
	line := 0
	for index, item := range blocks {
		if index > 0 {
			output.WriteByte('\n')
			line++
		}
		start := line
		bodyStart := start
		for blockLine, value := range strings.Split(strings.TrimSuffix(item.block, "\n"), "\n") {
			if strings.HasPrefix(value, "├") {
				bodyStart = start + blockLine + 1
				break
			}
		}
		line += strings.Count(item.block, "\n")
		output.WriteString(item.block)
		rendered.Segments = append(rendered.Segments, executionRenderedSegment{StartLine: start, BodyStartLine: bodyStart, EndLine: line, StickyLabel: item.sticky})
	}
	rendered.Content = output.String()
	return rendered
}

func toolCallTimelineContentDetailed(record toolCallRecord, details map[string]activity.ToolCallDetail, detailErrors map[string]string, width int) []string {
	if detail, ok := details[record.CallID]; ok {
		content := []string{"REQUEST", component.CodeBlockMarkdown(marshalJSONValue(toolCallRequestArguments(detail.Request)), "json")}
		if detail.Error != nil {
			content = append(content, "ERROR", component.CodeBlockMarkdown(marshalJSONValue(detail.Error), "json"))
		} else if detail.Response != nil {
			content = append(content, "RESPONSE", component.CodeBlockMarkdown(marshalJSONValue(detail.Response), "json"))
		} else {
			content = append(content, "RESPONSE", "waiting...")
		}
		if detail.Diagnostic.Redacted || detail.Diagnostic.Truncated {
			content = append(content, "DIAGNOSTIC", component.CodeBlockMarkdown(marshalJSONValue(detail.Diagnostic), "json"))
		}
		return []string{component.RenderMarkdownCompact(strings.Join(content, "\n\n"), width)}
	}
	if message := strings.TrimSpace(detailErrors[record.CallID]); message != "" {
		return []string{component.RenderMarkdownCompact("DETAIL ERROR\n\n"+component.CodeBlockMarkdown(message, "text"), width)}
	}
	if details != nil {
		return []string{component.Muted("Loading canonical request/response detail...")}
	}
	content := []string{"REQUEST", component.CodeBlockMarkdown(marshalJSONValue(toolCallRequestArguments(record.First.Raw)), "json")}
	latest := record.Latest
	if latest.Phase == "finish" || latest.Status != "running" {
		if latest.Status == "error" || latest.Status == "cancelled" {
			content = append(content, "ERROR", component.CodeBlockMarkdown(marshalJSONValue(map[string]any{"status": latest.Status, "message": latest.Message}), "json"))
		} else {
			response := map[string]any{"status": latest.Status, "duration_ms": latest.DurationMS, "message": latest.Message}
			content = append(content, "RESPONSE", component.CodeBlockMarkdown(marshalJSONValue(response), "json"))
		}
	} else {
		content = append(content, "RESPONSE", "waiting...")
	}
	return []string{component.RenderMarkdownCompact(strings.Join(content, "\n\n"), width)}
}

func toolCallRequestArguments(request any) any {
	root, ok := request.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if arguments, ok := root["arguments"]; ok {
		return arguments
	}
	if params, ok := root["params"].(map[string]any); ok {
		if arguments, ok := params["arguments"]; ok {
			return arguments
		}
	}
	if nested, ok := root["request"].(map[string]any); ok {
		return toolCallRequestArguments(nested)
	}
	return map[string]any{}
}

func marshalJSONValue(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(value))
	}
	return string(data)
}

func (page *LogsPage) syncToolCallDetail() {
	if page == nil || page.resourceID == "" {
		return
	}
	if detail, ok := page.tools.details[page.resourceID]; ok {
		parts := []string{"REQUEST", component.CodeBlockMarkdown(marshalJSONValue(detail.Request), "json")}
		if detail.Error != nil {
			parts = append(parts, "ERROR", component.CodeBlockMarkdown(marshalJSONValue(detail.Error), "json"))
		} else if detail.Response != nil {
			parts = append(parts, "RESPONSE", component.CodeBlockMarkdown(marshalJSONValue(detail.Response), "json"))
		} else {
			parts = append(parts, "RESPONSE", "waiting...")
		}
		if detail.Diagnostic.Redacted || detail.Diagnostic.Truncated {
			parts = append(parts, "DIAGNOSTIC", component.CodeBlockMarkdown(marshalJSONValue(detail.Diagnostic), "json"))
		}
		parts = append(parts, "METADATA", component.CodeBlockMarkdown(marshalJSONValue(map[string]any{
			"call_id": detail.CallID, "tool": detail.Tool, "method": detail.Method, "source": detail.Source,
			"workspace_id": detail.WorkspaceID, "status": detail.Status, "duration_ms": detail.DurationMS,
			"sequence": detail.Sequence, "timestamp": detail.Timestamp,
		}), "json"))
		page.detail.SetTitle("Tool Call · " + page.resourceID)
		page.detail.SetMeta(compactParts(detail.Tool, detail.Status, detail.WorkspaceID))
		page.detail.SetContentPreserveScroll(component.RenderMarkdownCompact(strings.Join(parts, "\n\n"), max(20, page.width)))
		page.detail.SetFeedback("", nil)
		page.detailReady = true
		if page.width > 0 && page.height > 0 {
			page.detail.Resize(page.width, page.height)
		}
		return
	}
	if message := strings.TrimSpace(page.tools.detailErrors[page.resourceID]); message != "" {
		page.detail.SetTitle("Tool Call · " + page.resourceID)
		page.detail.SetMeta("error")
		page.detail.SetContentPreserveScroll(component.RenderMarkdownCompact("ERROR\n\n"+component.CodeBlockMarkdown(message, "text"), max(20, page.width)))
		page.detailReady = true
		return
	}
	page.detail.SetTitle("Tool Call · " + page.resourceID)
	page.detail.SetMeta("loading")
	page.detail.SetContentPreserveScroll(component.Muted("Loading canonical request/response detail..."))
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
	left := component.KeyValue("Stream", stream) + "   " + component.KeyValue("View", map[logsDisplayView]string{logsViewBrowser: "Browser", logsViewTimeline: "Timeline"}[page.view]) + "   "
	if page.view == logsViewBrowser {
		left += component.KeyValue("Calls", fmt.Sprintf("%d / %d", len(page.visibleToolCallRecords()), activity.MaxRecentToolCalls))
	} else {
		left += component.KeyValue("Events", fmt.Sprintf("%d / %d", len(page.visibleToolCallTimelineEvents()), activity.MaxRecentEvents))
	}
	if page.view == logsViewTimeline {
		follow := component.ToneText("● ON", component.ToneSuccess)
		if page.tools.paused {
			follow = component.ToneText("○ PAUSED", component.ToneWarning)
		}
		left = component.KeyValue("Stream", stream) + "   " + component.KeyValue("Follow", follow) + "   " + component.KeyValue("View", "Timeline") + "   " + component.KeyValue("Events", fmt.Sprintf("%d / %d", len(page.visibleToolCallTimelineEvents()), activity.MaxRecentEvents))
	}
	return component.TwoColumn(left, component.KeyValue("Mode", logsScopeLabel(page.tools.scope)), width)
}

func (page *LogsPage) toolCallHelpView(width int) string {
	return page.logsHelpView(width)
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
				page.tools.window.invalidate()
				page.refreshToolCallView()
			}
		}
		page.syncBrowserHelp()
		return nil
	case "r":
		return page.startToolCallFeed()
	case "c":
		page.clearActiveLogsView()
		return nil
	}
	if page.view == logsViewBrowser {
		return page.updateToolCallBrowser(msg)
	}
	view, cmd := page.tools.viewport.Update(msg)
	page.tools.viewport = view
	if !page.tools.viewport.AtBottom() {
		page.tools.paused = true
	}
	page.maybeExpandToolCallTimeline()
	return cmd
}

func (page *LogsPage) updateToolCallBrowser(message tea.Msg) tea.Cmd {
	before := ""
	if row, ok := page.browser.Selected(); ok {
		before = row.ID
	}
	updated, cmd := page.browser.Update(message)
	page.browser = updated.(component.Browser)
	if !page.tools.paused && before != "" {
		if row, ok := page.browser.Selected(); ok && row.ID != before {
			page.tools.paused = true
			page.syncBrowserHelp()
		}
	}
	return cmd
}
