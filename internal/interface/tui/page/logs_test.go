package page

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtime/activity"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestLogsPageLoadsHistoryAndShowsOfflineReconnectState(t *testing.T) {
	root := setupLogsPageRoot(t)
	appendLogEvents(t, root, runtimeevent.Event{Sequence: 1, Time: time.Now(), RunID: "run_one", Level: "info", Name: "server.ready", Component: "SERVER", Message: "Ready"})
	page, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	msg := page.Init()()
	updated, connect := page.Update(msg)
	page = updated.(*LogsPage)
	if len(page.events) != 1 || !page.loaded || page.connected || !page.reconnecting || connect == nil {
		t.Fatalf("journal loaded=%t connected=%t reconnect=%t events=%d cmd=%v", page.loaded, page.connected, page.reconnecting, len(page.events), connect)
	}
	if !strings.Contains(page.notice, "Journal loaded") || page.ShouldToastNotice() {
		t.Fatalf("bootstrap notice=%q toast=%t", page.notice, page.ShouldToastNotice())
	}
	updated, reconnect := page.Update(connect())
	page = updated.(*LogsPage)
	if page.connected || !page.reconnecting || reconnect == nil {
		t.Fatalf("offline stream connected=%t reconnect=%t cmd=%v", page.connected, page.reconnecting, reconnect)
	}
	plain := ansi.Strip(page.View(180, 28))
	for _, want := range []string{"Runtime", "Command Execution", "RECONNECTING", "server.ready", "? more"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q: %q", want, plain)
		}
	}
}

func TestLogsPageMergesLiveStreamAfterHistory(t *testing.T) {
	root := setupLogsPageRoot(t)
	base := time.Now().UTC()
	appendLogEvents(t, root, runtimeevent.Event{Sequence: 1, Time: base, RunID: "run_live", Level: "info", Name: "history", Component: "SERVER", Message: "History"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "event: runtime\ndata: {\"sequence\":2,\"time\":%q,\"run_id\":\"run_live\",\"level\":\"warn\",\"event\":\"live\",\"component\":\"TOOL\",\"message\":\"Live\"}\n\n", base.Add(time.Second).Format(time.RFC3339Nano))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run_live")
	page, _ := NewLogs(t.Context())
	defer page.Close()
	updated, connect := page.Update(page.Init()())
	page = updated.(*LogsPage)
	if page.connected || connect == nil || len(page.events) != 1 {
		t.Fatalf("history connected=%t connect=%v events=%d", page.connected, connect, len(page.events))
	}
	updated, next := page.Update(connect())
	page = updated.(*LogsPage)
	if !page.connected || next == nil {
		t.Fatalf("connected=%t next=%v", page.connected, next)
	}
	updated, next = page.Update(next())
	page = updated.(*LogsPage)
	if len(page.events) != 2 || page.events[1].Name != "live" || next == nil {
		t.Fatalf("events=%#v next=%v", page.events, next)
	}
}

func TestLogsPagePauseBuffersWithoutFollowingAndResumeReturnsToTail(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	base := time.Now().UTC()
	page.mergeEvents([]runtimeevent.Event{{Sequence: 1, Time: base, RunID: "run", Level: "info", Name: "one", Message: "one"}, {Sequence: 2, Time: base.Add(time.Second), RunID: "run", Level: "info", Name: "two", Message: "two"}})
	if !page.browser.SelectID("run:1") {
		t.Fatal("could not select first event")
	}
	page.appendEvent(runtimeevent.Event{Sequence: 3, Time: base.Add(2 * time.Second), RunID: "run", Level: "info", Name: "three", Message: "three"})
	if !page.paused || page.selectedID() != "run:1" || len(page.events) != 3 {
		t.Fatalf("paused=%t selected=%q events=%d", page.paused, page.selectedID(), len(page.events))
	}
	page.handleKey(tea.KeyPressMsg{Code: tea.KeySpace})
	if page.paused || page.selectedID() != "run:3" {
		t.Fatalf("resume paused=%t selected=%q", page.paused, page.selectedID())
	}
}

func TestLogsBrowserSpaceResumesExecutionAndToolCallFollow(t *testing.T) {
	execPage, _ := NewCommandExecutionLogs(t.Context())
	defer execPage.Close()
	execPage.view = logsViewBrowser
	execPage.exec.paused = true
	execPage.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, ExecutionID: "exec_1", Type: shellruntime.ExecutionEventStarted, Execution: &shellruntime.ExecutionInfo{ID: "exec_1", Tool: "run_command"}},
		{Sequence: 2, ExecutionID: "exec_2", Type: shellruntime.ExecutionEventStarted, Execution: &shellruntime.ExecutionInfo{ID: "exec_2", Tool: "run_command"}},
	}
	execPage.rebuildExecutionBrowser()
	if !execPage.browser.SelectID("exec_1") {
		t.Fatal("could not select first execution")
	}
	execPage.handleExecutionKey(tea.KeyPressMsg{Code: tea.KeySpace})
	row, ok := execPage.browser.Selected()
	if !ok || execPage.exec.paused || row.ID != "exec_2" {
		t.Fatalf("execution resume paused=%t selected=%#v", execPage.exec.paused, row)
	}

	toolPage, _ := NewToolCallLogsRoute(t.Context(), "")
	defer toolPage.Close()
	toolPage.view = logsViewBrowser
	toolPage.tools.paused = true
	toolPage.tools.events = []activity.Event{
		{Sequence: 1, CallID: "call_1", Tool: "read_file", Status: "ok"},
		{Sequence: 2, CallID: "call_2", Tool: "read_file", Status: "ok"},
	}
	toolPage.rebuildToolCallBrowser()
	if !toolPage.browser.SelectID("call_1") {
		t.Fatal("could not select first tool call")
	}
	toolPage.handleToolCallKey(tea.KeyPressMsg{Code: tea.KeySpace})
	row, ok = toolPage.browser.Selected()
	if !ok || toolPage.tools.paused || row.ID != "call_2" {
		t.Fatalf("tool call resume paused=%t selected=%#v", toolPage.tools.paused, row)
	}
}

func TestLogsEventChildDetailStaysPinnedWhileLiveEventsAppend(t *testing.T) {
	page, _ := NewLogsRoute(t.Context(), "run:2", "")
	defer page.Close()
	base := time.Now().UTC()
	page.mergeEvents([]runtimeevent.Event{
		{Sequence: 1, Time: base, RunID: "run", Level: "info", Name: "one", Message: "one"},
		{Sequence: 2, Time: base.Add(time.Second), RunID: "run", Level: "info", Name: "two", Message: "two"},
	})
	if page.OverlayActive() || page.paused {
		t.Fatalf("overlay=%t paused=%t", page.OverlayActive(), page.paused)
	}
	page.appendEvent(runtimeevent.Event{Sequence: 3, Time: base.Add(2 * time.Second), RunID: "run", Level: "info", Name: "three", Message: "three"})
	if page.paused {
		t.Fatalf("detail child unexpectedly paused follow state")
	}
	detail := ansi.Strip(page.View(100, 26))
	if !strings.Contains(detail, `"event"`) || !strings.Contains(detail, `"two"`) || strings.Contains(detail, `"three"`) || strings.Contains(detail, "Log event · two") {
		t.Fatalf("detail jumped after live append: %q", detail)
	}
}

func TestLogsPageBufferIsBounded(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	base := time.Now().UTC()
	events := make([]runtimeevent.Event, logsTimelineEventCap+500)
	for index := range events {
		events[index] = runtimeevent.Event{Sequence: uint64(index + 1), Time: base.Add(time.Duration(index) * time.Millisecond), RunID: "run", Level: "info", Name: "event", Message: "value"}
	}
	page.mergeEvents(events)
	if len(page.events) != logsTimelineEventCap || page.events[0].Sequence != 501 || page.events[len(page.events)-1].Sequence != uint64(logsTimelineEventCap+500) {
		t.Fatalf("bounded events=%d first=%d last=%d", len(page.events), page.events[0].Sequence, page.events[len(page.events)-1].Sequence)
	}
}

func TestLogsBrowserAndTimelineRetentionSemanticsAreIndependent(t *testing.T) {
	page, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	base := time.Now().UTC()

	runtimeEvents := make([]runtimeevent.Event, logsTimelineEventCap)
	for index := range runtimeEvents {
		runtimeEvents[index] = runtimeevent.Event{Sequence: uint64(index + 1), Time: base.Add(time.Duration(index) * time.Millisecond), RunID: "run_retention", Level: "info", Name: "runtime.event", Message: fmt.Sprintf("runtime %d", index)}
	}
	page.events = runtimeEvents
	if got := len(page.visibleRuntimeBrowserRecords()); got != logsBrowserRecordCap {
		t.Fatalf("runtime Browser records=%d want=%d", got, logsBrowserRecordCap)
	}
	if got := len(page.eligibleRuntimeTimelineEvents()); got != logsTimelineEventCap {
		t.Fatalf("runtime eligible Timeline events=%d want=%d", got, logsTimelineEventCap)
	}
	if got := len(page.visibleRuntimeTimelineEvents()); got != logsTimelinePageSize {
		t.Fatalf("runtime initial Timeline events=%d want=%d", got, logsTimelinePageSize)
	}
	page.view = logsViewBrowser
	if status := ansi.Strip(page.statusView(160)); !strings.Contains(status, "Records") || strings.Contains(status, "Events") {
		t.Fatalf("runtime Browser status=%q", status)
	}
	page.view = logsViewTimeline
	if status := ansi.Strip(page.statusView(160)); !strings.Contains(status, "Events") || strings.Contains(status, "Records") {
		t.Fatalf("runtime Timeline status=%q", status)
	}

	page.tab, page.view = logsTabCommandExec, logsViewBrowser
	page.exec.scopeMode, page.exec.workspaceID = executionScopeWorkspace, "ws_selected"
	page.exec.executions = make([]shellruntime.ExecutionInfo, 0, shellruntime.MaxRecentExecutions+300)
	for index := 0; index < shellruntime.MaxRecentExecutions+300; index++ {
		workspaceID := "ws_other"
		if index >= 100 {
			workspaceID = "ws_selected"
		}
		page.exec.executions = append(page.exec.executions, shellruntime.ExecutionInfo{ID: fmt.Sprintf("exec_%04d", index), WorkspaceID: workspaceID, Tool: "run_command", Command: "echo", Status: shellruntime.ExecutionStatusSuccess})
	}
	page.exec.events = make([]shellruntime.ExecutionFeedEvent, shellruntime.MaxExecutionFeedEvents+300)
	for index := range page.exec.events {
		page.exec.events[index] = shellruntime.ExecutionFeedEvent{Sequence: uint64(index + 1), ExecutionID: fmt.Sprintf("raw_%04d", index), WorkspaceID: "ws_selected", Type: shellruntime.ExecutionEventOutput, Data: "x"}
	}
	page.exec.events = trimExecutionFeed(page.exec.events)
	executions := page.visibleExecutions()
	if len(executions) != shellruntime.MaxRecentExecutions || executions[0].ID != "exec_0300" || executions[len(executions)-1].ID != fmt.Sprintf("exec_%04d", shellruntime.MaxRecentExecutions+299) {
		t.Fatalf("filtered logical executions len=%d first=%q last=%q", len(executions), executions[0].ID, executions[len(executions)-1].ID)
	}
	if len(page.visibleExecutionEvents()) != shellruntime.MaxExecutionFeedEvents {
		t.Fatalf("execution Timeline raw events=%d want=%d", len(page.visibleExecutionEvents()), shellruntime.MaxExecutionFeedEvents)
	}
	if status := ansi.Strip(page.executionStatusView(180)); !strings.Contains(status, "Executions") || strings.Contains(status, "Events") {
		t.Fatalf("execution Browser status=%q", status)
	}
	page.view = logsViewTimeline
	if status := ansi.Strip(page.executionStatusView(180)); !strings.Contains(status, "Events") || strings.Contains(status, "Executions") {
		t.Fatalf("execution Timeline status=%q", status)
	}

	page.tab, page.view = logsTabToolCalls, logsViewBrowser
	page.tools.scope.mode, page.tools.scope.workspaceID = executionScopeWorkspace, "ws_selected"
	page.tools.records = make([]activity.ToolCallRecord, 0, activity.MaxRecentToolCalls+300)
	for index := 0; index < activity.MaxRecentToolCalls+300; index++ {
		workspaceID := "ws_other"
		if index >= 100 {
			workspaceID = "ws_selected"
		}
		event := activity.Event{Sequence: uint64(index + 1), CallID: fmt.Sprintf("call_%04d", index), Kind: string(activity.EventToolCall), Phase: "finish", Tool: "read_file", WorkspaceID: workspaceID, Status: "ok", Timestamp: base.Add(time.Duration(index) * time.Millisecond)}
		page.tools.records = append(page.tools.records, activity.ToolCallRecord{CallID: event.CallID, First: event, Latest: event})
	}
	page.tools.events = make([]activity.Event, activity.MaxRecentEvents+300)
	for index := range page.tools.events {
		page.tools.events[index] = activity.Event{Sequence: uint64(index + 1), CallID: fmt.Sprintf("raw_call_%04d", index), Kind: string(activity.EventToolCall), Phase: "progress", Tool: "read_file", WorkspaceID: "ws_selected", Status: "running", Timestamp: base.Add(time.Duration(index) * time.Millisecond)}
	}
	page.tools.events = trimToolCallEvents(page.tools.events)
	calls := page.visibleToolCallRecords()
	if len(calls) != activity.MaxRecentToolCalls || calls[0].CallID != "call_0300" || calls[len(calls)-1].CallID != fmt.Sprintf("call_%04d", activity.MaxRecentToolCalls+299) {
		t.Fatalf("filtered logical calls len=%d first=%q last=%q", len(calls), calls[0].CallID, calls[len(calls)-1].CallID)
	}
	if len(page.eligibleToolCallTimelineEvents()) != activity.MaxRecentEvents {
		t.Fatalf("tool eligible Timeline raw events=%d want=%d", len(page.eligibleToolCallTimelineEvents()), activity.MaxRecentEvents)
	}
	if len(page.visibleToolCallTimelineEvents()) != logsTimelinePageSize {
		t.Fatalf("tool initial Timeline raw events=%d want=%d", len(page.visibleToolCallTimelineEvents()), logsTimelinePageSize)
	}
	if status := ansi.Strip(page.toolCallStatusView(180)); !strings.Contains(status, "Calls") || strings.Contains(status, "Events") {
		t.Fatalf("tool Browser status=%q", status)
	}
	page.view = logsViewTimeline
	if status := ansi.Strip(page.toolCallStatusView(180)); !strings.Contains(status, "Events") || strings.Contains(status, "Calls") {
		t.Fatalf("tool Timeline status=%q", status)
	}
}

func TestLogsTimelineWindowsStartNewestExpandAndKeepLogicalRecordsWhole(t *testing.T) {
	page, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	base := time.Now().UTC()

	page.view = logsViewTimeline
	page.events = make([]runtimeevent.Event, 100)
	for index := range page.events {
		workspaceID := "ws_other"
		if index >= 50 {
			workspaceID = "ws_selected"
		}
		page.events[index] = runtimeevent.Event{Sequence: uint64(index + 1), Time: base.Add(time.Duration(index) * time.Millisecond), RunID: "run_window", WorkspaceID: workspaceID, Level: "info", Name: "runtime.event", Message: fmt.Sprintf("event %d", index)}
	}
	page.timeline.viewport.SetWidth(80)
	page.timeline.viewport.SetHeight(8)
	page.refreshRuntimeTimeline()
	runtimeWindow := page.visibleRuntimeTimelineEvents()
	if len(runtimeWindow) != logsTimelinePageSize || runtimeWindow[0].Sequence != 61 || runtimeWindow[len(runtimeWindow)-1].Sequence != 100 {
		t.Fatalf("runtime initial window len=%d range=%d..%d", len(runtimeWindow), runtimeWindow[0].Sequence, runtimeWindow[len(runtimeWindow)-1].Sequence)
	}
	if len(page.visibleRuntimeBrowserRecords()) != 100 {
		t.Fatalf("runtime Browser was windowed: %d", len(page.visibleRuntimeBrowserRecords()))
	}
	page.paused = true
	page.timeline.viewport.SetYOffset(0)
	oldLines := page.timeline.viewport.TotalLineCount()
	page.handleTimelineMouse(logsTimelineMouseMsg{Tab: logsTabRuntime, Wheel: -1})
	runtimeWindow = page.visibleRuntimeTimelineEvents()
	delta := page.timeline.viewport.TotalLineCount() - oldLines
	if len(runtimeWindow) != 80 || runtimeWindow[0].Sequence != 21 || delta <= 0 || page.timeline.viewport.YOffset() != delta {
		t.Fatalf("runtime expanded len=%d first=%d offset=%d delta=%d", len(runtimeWindow), runtimeWindow[0].Sequence, page.timeline.viewport.YOffset(), delta)
	}
	for index := 1; index < len(runtimeWindow); index++ {
		if runtimeWindow[index-1].Sequence >= runtimeWindow[index].Sequence {
			t.Fatalf("runtime chronology broke at %d: %d >= %d", index, runtimeWindow[index-1].Sequence, runtimeWindow[index].Sequence)
		}
	}
	page.togglePause()
	runtimeWindow = page.visibleRuntimeTimelineEvents()
	if page.paused || len(runtimeWindow) != logsTimelinePageSize || runtimeWindow[0].Sequence != 61 || !page.timeline.viewport.AtBottom() {
		t.Fatalf("runtime follow reset paused=%t len=%d first=%d bottom=%t", page.paused, len(runtimeWindow), runtimeWindow[0].Sequence, page.timeline.viewport.AtBottom())
	}
	page.paused = true
	page.timeline.viewport.SetYOffset(2)
	pausedOffset := page.timeline.viewport.YOffset()
	_ = page.appendEvent(runtimeevent.Event{Sequence: 101, Time: base.Add(101 * time.Millisecond), RunID: "run_window", WorkspaceID: "ws_selected", Level: "info", Name: "runtime.event", Message: "live"})
	runtimeWindow = page.visibleRuntimeTimelineEvents()
	if page.timeline.viewport.YOffset() != pausedOffset || runtimeWindow[len(runtimeWindow)-1].Sequence != 100 {
		t.Fatalf("paused runtime moved offset=%d want=%d last=%d", page.timeline.viewport.YOffset(), pausedOffset, runtimeWindow[len(runtimeWindow)-1].Sequence)
	}
	page.togglePause()
	runtimeWindow = page.visibleRuntimeTimelineEvents()
	if runtimeWindow[len(runtimeWindow)-1].Sequence != 101 {
		t.Fatalf("runtime resume did not reach newest event: %d", runtimeWindow[len(runtimeWindow)-1].Sequence)
	}

	page.runtimeScope.mode, page.runtimeScope.workspaceID = executionScopeWorkspace, "ws_selected"
	page.timeline.window.invalidate()
	filteredRuntime := page.visibleRuntimeTimelineEvents()
	if len(page.visibleRuntimeBrowserRecords()) != 51 || len(filteredRuntime) != logsTimelinePageSize {
		t.Fatalf("runtime scope before window browser=%d timeline=%d", len(page.visibleRuntimeBrowserRecords()), len(filteredRuntime))
	}
	for _, event := range filteredRuntime {
		if event.WorkspaceID != "ws_selected" {
			t.Fatalf("runtime window contains out-of-scope event: %#v", event)
		}
	}

	page.tab, page.view = logsTabCommandExec, logsViewTimeline
	page.exec = newLogsExecutionFeed()
	sequence := uint64(0)
	for index := 0; index < 100; index++ {
		id := fmt.Sprintf("exec_%03d", index)
		info := shellruntime.ExecutionInfo{ID: id, WorkspaceID: "ws_exec", Tool: "run_command", Command: fmt.Sprintf("echo %d", index), Status: shellruntime.ExecutionStatusSuccess}
		page.exec.executions = append(page.exec.executions, info)
		sequence++
		page.exec.events = append(page.exec.events, shellruntime.ExecutionFeedEvent{Sequence: sequence, ExecutionID: id, WorkspaceID: "ws_exec", Type: shellruntime.ExecutionEventStarted, Execution: &info})
		sequence++
		page.exec.events = append(page.exec.events, shellruntime.ExecutionFeedEvent{Sequence: sequence, ExecutionID: id, WorkspaceID: "ws_exec", Type: shellruntime.ExecutionEventOutput, Stream: "stdout", Data: "output\n"})
		sequence++
		page.exec.events = append(page.exec.events, shellruntime.ExecutionFeedEvent{Sequence: sequence, ExecutionID: id, WorkspaceID: "ws_exec", Type: shellruntime.ExecutionEventCompleted, Execution: &info, Status: shellruntime.ExecutionStatusSuccess})
	}
	page.exec.viewport.SetWidth(80)
	page.exec.viewport.SetHeight(8)
	page.refreshExecutionViewport()
	executionWindow := page.visibleExecutionTimelineEvents()
	ids := executionTimelineRecordIDs(executionWindow)
	if len(ids) != logsTimelinePageSize || ids[0] != "exec_060" || ids[len(ids)-1] != "exec_099" || len(executionWindow) != logsTimelinePageSize*3 {
		t.Fatalf("execution window ids=%d first=%q last=%q events=%d", len(ids), ids[0], ids[len(ids)-1], len(executionWindow))
	}
	counts := map[string]int{}
	for _, event := range executionWindow {
		counts[event.ExecutionID]++
	}
	for _, id := range ids {
		if counts[id] != 3 {
			t.Fatalf("execution %s split across window: %d events", id, counts[id])
		}
	}
	if len(page.visibleExecutions()) != 100 {
		t.Fatalf("execution Browser was windowed: %d", len(page.visibleExecutions()))
	}
	page.exec.paused = true
	page.exec.viewport.SetYOffset(0)
	oldLines = page.exec.viewport.TotalLineCount()
	page.handleExecutionMouse(logsExecutionMouseMsg{Wheel: -1})
	ids = executionTimelineRecordIDs(page.visibleExecutionTimelineEvents())
	delta = page.exec.viewport.TotalLineCount() - oldLines
	if len(ids) != 80 || ids[0] != "exec_020" || delta <= 0 || page.exec.viewport.YOffset() != delta {
		t.Fatalf("execution expanded ids=%d first=%q offset=%d delta=%d", len(ids), ids[0], page.exec.viewport.YOffset(), delta)
	}

	page.tab, page.view = logsTabToolCalls, logsViewTimeline
	page.tools = newLogsToolCallFeed()
	sequence = 0
	for index := 0; index < 100; index++ {
		callID := fmt.Sprintf("call_%03d", index)
		sequence++
		start := activity.Event{Sequence: sequence, CallID: callID, Kind: string(activity.EventToolCall), Phase: "start", Tool: "read_file", WorkspaceID: "ws_tools", Status: "running", Timestamp: base.Add(time.Duration(sequence) * time.Millisecond)}
		sequence++
		progress := activity.Event{Sequence: sequence, CallID: callID, Kind: string(activity.EventToolCall), Phase: "progress", Tool: "read_file", WorkspaceID: "ws_tools", Status: "running", Timestamp: base.Add(time.Duration(sequence) * time.Millisecond)}
		sequence++
		finish := activity.Event{Sequence: sequence, CallID: callID, Kind: string(activity.EventToolCall), Phase: "finish", Tool: "read_file", WorkspaceID: "ws_tools", Status: "ok", Timestamp: base.Add(time.Duration(sequence) * time.Millisecond)}
		page.tools.events = append(page.tools.events, start, progress, finish)
		page.tools.records = append(page.tools.records, activity.ToolCallRecord{CallID: callID, First: start, Latest: finish})
	}
	page.tools.viewport.SetWidth(80)
	page.tools.viewport.SetHeight(8)
	page.refreshToolCallView()
	toolWindow := page.visibleToolCallTimelineRecords()
	if len(toolWindow) != logsTimelinePageSize || toolWindow[0].CallID != "call_060" || toolWindow[len(toolWindow)-1].CallID != "call_099" || len(page.visibleToolCallTimelineEvents()) != logsTimelinePageSize*3 {
		t.Fatalf("tool window calls=%d first=%q last=%q events=%d", len(toolWindow), toolWindow[0].CallID, toolWindow[len(toolWindow)-1].CallID, len(page.visibleToolCallTimelineEvents()))
	}
	toolCounts := map[string]int{}
	for _, event := range page.visibleToolCallTimelineEvents() {
		toolCounts[event.CallID]++
	}
	for _, record := range toolWindow {
		if toolCounts[record.CallID] != 3 {
			t.Fatalf("tool call %s split across window: %d events", record.CallID, toolCounts[record.CallID])
		}
	}
	if len(page.visibleToolCallRecords()) != 100 {
		t.Fatalf("tool Browser was windowed: %d", len(page.visibleToolCallRecords()))
	}
	page.tools.paused = true
	page.tools.viewport.SetYOffset(0)
	oldLines = page.tools.viewport.TotalLineCount()
	page.handleTimelineMouse(logsTimelineMouseMsg{Tab: logsTabToolCalls, Wheel: -1})
	toolWindow = page.visibleToolCallTimelineRecords()
	delta = page.tools.viewport.TotalLineCount() - oldLines
	if len(toolWindow) != 80 || toolWindow[0].CallID != "call_020" || delta <= 0 || page.tools.viewport.YOffset() != delta {
		t.Fatalf("tool expanded calls=%d first=%q offset=%d delta=%d", len(toolWindow), toolWindow[0].CallID, page.tools.viewport.YOffset(), delta)
	}
}

func TestLogsTimelineKeyboardNearOldestExpandsWindow(t *testing.T) {
	page, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.view = logsViewTimeline
	base := time.Now().UTC()
	page.events = make([]runtimeevent.Event, 100)
	for index := range page.events {
		page.events[index] = runtimeevent.Event{Sequence: uint64(index + 1), Time: base.Add(time.Duration(index) * time.Millisecond), RunID: "run_keyboard", Level: "info", Name: "runtime.event", Message: fmt.Sprintf("event %d", index)}
	}
	page.timeline.viewport.SetWidth(80)
	page.timeline.viewport.SetHeight(8)
	page.refreshRuntimeTimeline()
	page.paused = true
	page.timeline.viewport.SetYOffset(1)
	if got := len(page.visibleRuntimeTimelineEvents()); got != logsTimelinePageSize {
		t.Fatalf("initial keyboard window=%d", got)
	}
	if _, handled := page.handleKey(tea.KeyPressMsg{Code: tea.KeyUp}); !handled {
		t.Fatal("runtime timeline keyboard scroll was not handled")
	}
	if got := len(page.visibleRuntimeTimelineEvents()); got != logsTimelinePageSize*2 {
		t.Fatalf("keyboard expansion window=%d want=%d", got, logsTimelinePageSize*2)
	}
}

func TestPausedExecutionAndToolTimelinesIgnoreLiveViewportMovement(t *testing.T) {
	page, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()

	page.tab, page.view = logsTabCommandExec, logsViewTimeline
	page.exec = newLogsExecutionFeed()
	for index := 0; index < logsTimelinePageSize; index++ {
		id := fmt.Sprintf("exec_%03d", index)
		info := shellruntime.ExecutionInfo{ID: id, Tool: "run_command", Status: shellruntime.ExecutionStatusRunning}
		page.exec.events = append(page.exec.events, shellruntime.ExecutionFeedEvent{Sequence: uint64(index + 1), ExecutionID: id, Type: shellruntime.ExecutionEventStarted, Execution: &info})
		page.exec.executions = append(page.exec.executions, info)
	}
	page.exec.latestSeq = logsTimelinePageSize
	page.exec.viewport.SetWidth(80)
	page.exec.viewport.SetHeight(8)
	page.refreshExecutionViewport()
	page.exec.paused = true
	page.exec.viewport.SetYOffset(2)
	execOffset := page.exec.viewport.YOffset()
	execWindow := executionTimelineRecordIDs(page.visibleExecutionTimelineEvents())
	newExec := shellruntime.ExecutionInfo{ID: "exec_live", Tool: "run_command", Status: shellruntime.ExecutionStatusRunning}
	page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: page.exec.generation, event: shellruntime.ExecutionFeedEvent{Sequence: logsTimelinePageSize + 1, ExecutionID: newExec.ID, Type: shellruntime.ExecutionEventStarted, Execution: &newExec}})
	if page.exec.viewport.YOffset() != execOffset || !reflect.DeepEqual(executionTimelineRecordIDs(page.visibleExecutionTimelineEvents()), execWindow) {
		t.Fatalf("paused execution viewport moved offset=%d want=%d before=%#v after=%#v", page.exec.viewport.YOffset(), execOffset, execWindow, executionTimelineRecordIDs(page.visibleExecutionTimelineEvents()))
	}

	page.tab, page.view = logsTabToolCalls, logsViewTimeline
	page.tools = newLogsToolCallFeed()
	base := time.Now().UTC()
	for index := 0; index < logsTimelinePageSize; index++ {
		callID := fmt.Sprintf("call_%03d", index)
		event := activity.Event{Sequence: uint64(index + 1), CallID: callID, Kind: string(activity.EventToolCall), Phase: "start", Tool: "read_file", Status: "running", Timestamp: base.Add(time.Duration(index) * time.Millisecond)}
		page.tools.events = append(page.tools.events, event)
		page.tools.records = append(page.tools.records, activity.ToolCallRecord{CallID: callID, First: event, Latest: event})
	}
	page.tools.latestSeq = logsTimelinePageSize
	page.tools.viewport.SetWidth(80)
	page.tools.viewport.SetHeight(8)
	page.refreshToolCallView()
	page.tools.paused = true
	page.tools.viewport.SetYOffset(2)
	toolOffset := page.tools.viewport.YOffset()
	toolWindow := page.visibleToolCallTimelineRecords()
	beforeCalls := make([]string, 0, len(toolWindow))
	for _, record := range toolWindow {
		beforeCalls = append(beforeCalls, record.CallID)
	}
	newTool := activity.Event{Sequence: logsTimelinePageSize + 1, CallID: "call_live", Kind: string(activity.EventToolCall), Phase: "start", Tool: "read_file", Status: "running", Timestamp: base.Add(time.Second)}
	page.finishToolCallEvent(logsToolCallEventMsg{generation: page.tools.generation, event: newTool})
	afterRecords := page.visibleToolCallTimelineRecords()
	afterCalls := make([]string, 0, len(afterRecords))
	for _, record := range afterRecords {
		afterCalls = append(afterCalls, record.CallID)
	}
	if page.tools.viewport.YOffset() != toolOffset || !reflect.DeepEqual(afterCalls, beforeCalls) {
		t.Fatalf("paused tool viewport moved offset=%d want=%d before=%#v after=%#v", page.tools.viewport.YOffset(), toolOffset, beforeCalls, afterCalls)
	}
}

func TestLogsClearViewUsesSessionWatermarksWithoutDestroyingRetainedHistory(t *testing.T) {
	page, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	base := time.Now().UTC()
	runtimeOld := []runtimeevent.Event{
		{Sequence: 1, RunID: "run_clear", Time: base, Level: "info", Name: "one"},
		{Sequence: 2, RunID: "run_clear", Time: base.Add(time.Millisecond), Level: "info", Name: "two"},
	}
	page.events = append([]runtimeevent.Event(nil), runtimeOld...)
	page.streamRunID, page.streamSeq = "run_clear", 2
	page.tab = logsTabRuntime
	if _, handled := page.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"}); !handled {
		t.Fatal("runtime clear-view key was not handled")
	}
	if len(page.events) != 2 || len(page.visibleRuntimeEvents()) != 0 || page.runtimeClear["run_clear"] != 2 || page.OverlayActive() {
		t.Fatalf("runtime clear retained=%d visible=%d watermark=%d overlay=%t", len(page.events), len(page.visibleRuntimeEvents()), page.runtimeClear["run_clear"], page.OverlayActive())
	}

	page.tab = logsTabCommandExec
	execInfo := shellruntime.ExecutionInfo{ID: "exec_old", WorkspaceID: "ws", Tool: "run_command", Status: shellruntime.ExecutionStatusSuccess}
	page.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, ExecutionID: execInfo.ID, WorkspaceID: "ws", Type: shellruntime.ExecutionEventStarted, Execution: &execInfo},
		{Sequence: 2, ExecutionID: execInfo.ID, WorkspaceID: "ws", Type: shellruntime.ExecutionEventCompleted, Execution: &execInfo, Status: shellruntime.ExecutionStatusSuccess},
	}
	page.exec.executions = []shellruntime.ExecutionInfo{execInfo}
	page.exec.latestSeq = 2
	page.handleExecutionKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if len(page.exec.events) != 2 || len(page.visibleExecutionEvents()) != 0 || len(page.visibleExecutions()) != 0 || page.executionClear != 2 {
		t.Fatalf("execution clear retained=%d visible_events=%d visible_execs=%d watermark=%d", len(page.exec.events), len(page.visibleExecutionEvents()), len(page.visibleExecutions()), page.executionClear)
	}

	page.tab = logsTabToolCalls
	toolStart := activity.Event{Sequence: 1, CallID: "call_old", Kind: string(activity.EventToolCall), Phase: "start", Tool: "read_file", Status: "running", Timestamp: base}
	toolFinish := activity.Event{Sequence: 2, CallID: "call_old", Kind: string(activity.EventToolCall), Phase: "finish", Tool: "read_file", Status: "ok", Timestamp: base.Add(time.Millisecond)}
	page.tools.events = []activity.Event{toolStart, toolFinish}
	page.tools.records = []activity.ToolCallRecord{{CallID: "call_old", First: toolStart, Latest: toolFinish}}
	page.tools.latestSeq = 2
	page.handleToolCallKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if len(page.tools.events) != 2 || len(page.eligibleToolCallTimelineEvents()) != 0 || len(page.visibleToolCallRecords()) != 0 || page.toolCallClear != 2 {
		t.Fatalf("tool clear retained=%d visible_events=%d visible_calls=%d watermark=%d", len(page.tools.events), len(page.eligibleToolCallTimelineEvents()), len(page.visibleToolCallRecords()), page.toolCallClear)
	}

	state := page.SessionViewState().(LogsSessionViewState)
	if state.RuntimeClearSequences["run_clear"] != 2 || state.ExecutionClearSequence != 2 || state.ToolCallClearSequence != 2 {
		t.Fatalf("clear watermarks not captured: %#v", state)
	}
	restored, _ := NewLogs(t.Context())
	defer restored.Close()
	restored.RestoreSessionViewState(state)
	restored.events = append([]runtimeevent.Event(nil), runtimeOld...)
	restored.exec.events = append([]shellruntime.ExecutionFeedEvent(nil), page.exec.events...)
	restored.exec.executions = append([]shellruntime.ExecutionInfo(nil), page.exec.executions...)
	restored.tools.events = append([]activity.Event(nil), page.tools.events...)
	restored.tools.records = append([]activity.ToolCallRecord(nil), page.tools.records...)
	if len(restored.visibleRuntimeEvents()) != 0 || len(restored.visibleExecutionEvents()) != 0 || len(restored.visibleExecutions()) != 0 || len(restored.eligibleToolCallTimelineEvents()) != 0 || len(restored.visibleToolCallRecords()) != 0 {
		t.Fatal("replayed retained history resurrected after session clear")
	}
	restored.events = append(restored.events, runtimeevent.Event{Sequence: 3, RunID: "run_clear", Time: base.Add(2 * time.Millisecond), Level: "info", Name: "three"})
	newExec := shellruntime.ExecutionInfo{ID: "exec_new", WorkspaceID: "ws", Tool: "run_command", Status: shellruntime.ExecutionStatusRunning}
	restored.exec.events = append(restored.exec.events, shellruntime.ExecutionFeedEvent{Sequence: 3, ExecutionID: newExec.ID, WorkspaceID: "ws", Type: shellruntime.ExecutionEventStarted, Execution: &newExec})
	restored.exec.executions = append(restored.exec.executions, newExec)
	newTool := activity.Event{Sequence: 3, CallID: "call_new", Kind: string(activity.EventToolCall), Phase: "start", Tool: "read_file", Status: "running", Timestamp: base.Add(2 * time.Millisecond)}
	restored.tools.events = append(restored.tools.events, newTool)
	restored.tools.records = append(restored.tools.records, activity.ToolCallRecord{CallID: newTool.CallID, First: newTool, Latest: newTool})
	if len(restored.visibleRuntimeEvents()) != 1 || len(restored.visibleExecutionEvents()) != 1 || len(restored.visibleExecutions()) != 1 || len(restored.eligibleToolCallTimelineEvents()) != 1 || len(restored.visibleToolCallRecords()) != 1 {
		t.Fatalf("post-clear live records missing runtime=%d exec_events=%d execs=%d tool_events=%d calls=%d", len(restored.visibleRuntimeEvents()), len(restored.visibleExecutionEvents()), len(restored.visibleExecutions()), len(restored.eligibleToolCallTimelineEvents()), len(restored.visibleToolCallRecords()))
	}

	fresh, _ := NewLogs(t.Context())
	defer fresh.Close()
	fresh.events = append([]runtimeevent.Event(nil), runtimeOld...)
	fresh.exec.events = append([]shellruntime.ExecutionFeedEvent(nil), page.exec.events...)
	fresh.exec.executions = append([]shellruntime.ExecutionInfo(nil), page.exec.executions...)
	fresh.tools.events = append([]activity.Event(nil), page.tools.events...)
	fresh.tools.records = append([]activity.ToolCallRecord(nil), page.tools.records...)
	if len(fresh.visibleRuntimeEvents()) != 2 || len(fresh.visibleExecutionEvents()) != 2 || len(fresh.visibleExecutions()) != 1 || len(fresh.eligibleToolCallTimelineEvents()) != 2 || len(fresh.visibleToolCallRecords()) != 1 {
		t.Fatal("fresh TUI process inherited session clear watermarks")
	}
}

func TestLogsPageMouseActionsUseKeyboardMessages(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.width, page.height = 180, 28
	updated, _ := page.browser.Update(tea.KeyPressMsg{Code: '?'})
	page.browser = updated.(component.Browser)
	_ = page.View(page.width, page.height)
	targets := page.MouseTargets(0, 0, 1)
	want := map[string]bool{"v": false, "m": false, "f": false, "r": false, "i": false, "c": false, "d": false}
	for _, target := range targets {
		if target.ID != "browser.help" {
			continue
		}
		message, ok := target.Handle(component.MouseEvent{Button: tea.MouseLeft}).(tea.KeyPressMsg)
		if ok {
			if _, exists := want[message.String()]; exists {
				want[message.String()] = true
			}
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("logs mouse action %q not found", key)
		}
	}
}

func TestLogsBrowserMouseRoutesThroughExecutionAndToolCallPages(t *testing.T) {
	t.Run("execution", func(t *testing.T) {
		page, err := NewCommandExecutionLogs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		page.view = logsViewBrowser
		page.exec.events = []shellruntime.ExecutionFeedEvent{
			{Sequence: 1, ExecutionID: "exec_1", Type: shellruntime.ExecutionEventStarted, Execution: &shellruntime.ExecutionInfo{ID: "exec_1", Tool: "run_command", Command: "echo one"}},
			{Sequence: 2, ExecutionID: "exec_2", Type: shellruntime.ExecutionEventStarted, Execution: &shellruntime.ExecutionInfo{ID: "exec_2", Tool: "run_command", Command: "echo two"}},
		}
		page.rebuildExecutionBrowser()
		_ = page.View(100, 30)
		rows := browserRowTargets(page.MouseTargets(0, 0, 10))
		if len(rows) != 2 {
			t.Fatalf("execution row targets=%d", len(rows))
		}
		page = dispatchLogsMouse(t, page, rows[0], tea.MouseLeft)
		selected, ok := page.browser.Selected()
		if !ok || selected.ID != "exec_1" || !page.exec.paused {
			t.Fatalf("execution first click selected=%#v paused=%t", selected, page.exec.paused)
		}
		page.exec.paused = false
		page = dispatchLogsMouse(t, page, rows[0], tea.MouseWheelDown)
		selected, ok = page.browser.Selected()
		if !ok || selected.ID != "exec_2" || !page.exec.paused {
			t.Fatalf("execution wheel selected=%#v paused=%t", selected, page.exec.paused)
		}
		page = dispatchLogsMouse(t, page, rows[0], tea.MouseLeft)
		_, navigation := dispatchLogsMouseOpen(t, page, rows[0])
		if strings.Join(navigation.Path, "/") != "logs-exec/exec_1" {
			t.Fatalf("execution mouse open=%#v", navigation)
		}
	})

	t.Run("tool calls", func(t *testing.T) {
		page, err := NewToolCallLogsRoute(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		page.view = logsViewBrowser
		page.tools.events = []activity.Event{
			{Sequence: 1, CallID: "call_1", Tool: "read_file", Status: "ok", Timestamp: time.Now()},
			{Sequence: 2, CallID: "call_2", Tool: "read_file", Status: "ok", Timestamp: time.Now().Add(time.Millisecond)},
		}
		page.rebuildToolCallBrowser()
		_ = page.View(100, 30)
		rows := browserRowTargets(page.MouseTargets(0, 0, 10))
		if len(rows) != 2 {
			t.Fatalf("tool row targets=%d", len(rows))
		}
		page = dispatchLogsMouse(t, page, rows[0], tea.MouseLeft)
		selected, ok := page.browser.Selected()
		if !ok || selected.ID != "call_1" || !page.tools.paused {
			t.Fatalf("tool first click selected=%#v paused=%t", selected, page.tools.paused)
		}
		page.tools.paused = false
		page = dispatchLogsMouse(t, page, rows[0], tea.MouseWheelDown)
		selected, ok = page.browser.Selected()
		if !ok || selected.ID != "call_2" || !page.tools.paused {
			t.Fatalf("tool wheel selected=%#v paused=%t", selected, page.tools.paused)
		}
		page = dispatchLogsMouse(t, page, rows[0], tea.MouseLeft)
		_, navigation := dispatchLogsMouseOpen(t, page, rows[0])
		if strings.Join(navigation.Path, "/") != "logs-tools/call_1" {
			t.Fatalf("tool mouse open=%#v", navigation)
		}
	})
}

func TestLogsDetailMouseWheelRoutesThroughPageUpdate(t *testing.T) {
	for _, test := range []struct {
		name string
		new  func() (*LogsPage, error)
	}{
		{name: "runtime", new: func() (*LogsPage, error) { return NewLogsRoute(t.Context(), "runtime_detail", "") }},
		{name: "execution", new: func() (*LogsPage, error) { return NewCommandExecutionLogsRoute(t.Context(), "exec_detail") }},
		{name: "tool call", new: func() (*LogsPage, error) { return NewToolCallLogsRoute(t.Context(), "call_detail") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := test.new()
			if err != nil {
				t.Fatal(err)
			}
			defer page.Close()
			page.detail = component.NewDetailPage("Detail", "mouse", strings.Repeat("line\n", 120)).WithTitleVisible(false)
			_ = page.View(60, 10)
			target := mouseTargetByID(t, page.MouseTargets(0, 0, 10), "detail.scroll")
			cmd := component.DispatchMouse([]component.MouseTarget{target}, tea.MouseClickMsg(tea.Mouse{X: target.Rect.X, Y: target.Rect.Y, Button: tea.MouseWheelDown}))
			if cmd == nil {
				t.Fatal("detail wheel produced no command")
			}
			updated, _ := page.Update(cmd())
			page = updated.(*LogsPage)
			if page.detail.YOffset() == 0 {
				t.Fatal("detail wheel did not scroll viewport through LogsPage.Update")
			}
		})
	}
}

func TestRuntimeDetailStaysPinnedAcrossUnrelatedLiveReplayAndClear(t *testing.T) {
	base := time.Now().UTC()
	target := runtimeevent.Event{Sequence: 1, RunID: "run_pin", Time: base, Level: "warn", Component: "TARGET_COMPONENT", Name: "target.event", Message: strings.Repeat("target detail payload ", 120)}
	page, err := NewLogsRoute(t.Context(), logEventID(target), "")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.width, page.height = 52, 10
	page.events = []runtimeevent.Event{target}
	page.loaded = true
	page.query.RunID = target.RunID
	page.syncDetail()
	if !page.detailReady {
		t.Fatal("runtime detail did not become ready")
	}
	offset := scrollLogsDetail(t, page)

	page.generation = 4
	page.streamRunID, page.streamSeq = target.RunID, target.Sequence
	unrelated := runtimeevent.Event{Sequence: 2, RunID: target.RunID, Time: base.Add(time.Second), Level: "info", Component: "OTHER_COMPONENT", Name: "other.event", Message: "unrelated live event"}
	page.finishStreamEvent(logsStreamEventMsg{generation: 4, event: unrelated})
	if got := page.detail.YOffset(); got != offset {
		t.Fatalf("unrelated runtime event moved detail offset=%d want=%d", got, offset)
	}
	plain := ansi.Strip(page.detail.View())
	if !strings.Contains(plain, "TARGET_COMPONENT") || strings.Contains(plain, "OTHER_COMPONENT") {
		t.Fatalf("runtime detail changed resource after unrelated live event: %q", plain)
	}

	page.runtimeClear[target.RunID] = unrelated.Sequence
	page.mergeEvents([]runtimeevent.Event{target, unrelated, {Sequence: 3, RunID: target.RunID, Time: base.Add(2 * time.Second), Level: "error", Component: "REPLAY_COMPONENT", Name: "replay.event"}})
	if got := page.detail.YOffset(); got != offset {
		t.Fatalf("runtime replay/clear moved detail offset=%d want=%d", got, offset)
	}
	plain = ansi.Strip(page.detail.View())
	if !strings.Contains(plain, "TARGET_COMPONENT") || strings.Contains(plain, "REPLAY_COMPONENT") {
		t.Fatalf("runtime replay replaced pinned detail: %q", plain)
	}
}

func TestToolCallDetailRefreshesSameCallInPlaceOnly(t *testing.T) {
	page, err := NewToolCallLogsRoute(t.Context(), "call_target")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.width, page.height = 52, 10
	page.tools.generation = 7
	start := activity.Event{Sequence: 1, CallID: "call_target", Kind: string(activity.EventToolCall), Phase: "start", Tool: "read_file", WorkspaceID: "ws_target", Status: "running", Raw: map[string]any{"arguments": strings.Repeat("target argument ", 120)}, Timestamp: time.Now().UTC()}
	page.tools.events = []activity.Event{start}
	page.tools.records = []activity.ToolCallRecord{{CallID: start.CallID, First: start, Latest: start}}
	page.tools.latestSeq = start.Sequence
	page.syncToolCallDetail()
	if !page.detailReady {
		t.Fatal("tool call detail did not become ready")
	}
	offset := scrollLogsDetail(t, page)

	unrelated := activity.Event{Sequence: 2, CallID: "call_other", Kind: string(activity.EventToolCall), Phase: "finish", Tool: "write_file", WorkspaceID: "ws_other", Status: "failed", Timestamp: start.Timestamp.Add(time.Second)}
	page.finishToolCallEvent(logsToolCallEventMsg{generation: 7, event: unrelated})
	if got := page.detail.YOffset(); got != offset {
		t.Fatalf("unrelated tool call moved detail offset=%d want=%d", got, offset)
	}
	plain := ansi.Strip(page.detail.View())
	if !strings.Contains(plain, "read_file") || strings.Contains(plain, "write_file") {
		t.Fatalf("unrelated tool call replaced detail: %q", plain)
	}

	finish := activity.Event{Sequence: 3, CallID: start.CallID, Kind: string(activity.EventToolCall), Phase: "finish", Tool: start.Tool, WorkspaceID: start.WorkspaceID, Status: "success", Raw: map[string]any{"result": strings.Repeat("updated result ", 120)}, Timestamp: start.Timestamp.Add(2 * time.Second)}
	page.finishToolCallEvent(logsToolCallEventMsg{generation: 7, event: finish})
	if got := page.detail.YOffset(); got != offset {
		t.Fatalf("same-call refresh moved detail offset=%d want=%d", got, offset)
	}
	plain = ansi.Strip(page.detail.View())
	if !strings.Contains(plain, "success") {
		t.Fatalf("same-call finish did not refresh detail: %q", plain)
	}

	page.toolCallClear = finish.Sequence
	page.tools.records = nil
	page.tools.events = nil
	page.syncToolCallDetail()
	if got := page.detail.YOffset(); got != offset {
		t.Fatalf("tool replay/clear moved pinned detail offset=%d want=%d", got, offset)
	}
	if plain = ansi.Strip(page.detail.View()); !strings.Contains(plain, "success") || strings.Contains(plain, "unavailable") {
		t.Fatalf("tool replay/clear replaced pinned detail: %q", plain)
	}
}

func TestToolCallReconnectSnapshotUpdatesPinnedDetailInPlace(t *testing.T) {
	root := setupLogsPageRoot(t)
	base := time.Now().UTC()
	start := activity.Event{Sequence: 1, CallID: "call_reconnect", Kind: string(activity.EventToolCall), Phase: "start", Tool: "read_file", WorkspaceID: "ws_reconnect", Status: "running", Raw: map[string]any{"arguments": strings.Repeat("before reconnect ", 120)}, Timestamp: base}
	finish := activity.Event{Sequence: 4, CallID: start.CallID, Kind: string(activity.EventToolCall), Phase: "finish", Tool: start.Tool, WorkspaceID: start.WorkspaceID, Status: "success", Raw: map[string]any{"result": strings.Repeat("after reconnect ", 120)}, Timestamp: base.Add(time.Second)}
	ready, _ := json.Marshal(map[string]any{"latest_sequence": finish.Sequence, "replay_count": 0, "records": []activity.ToolCallRecord{{CallID: start.CallID, First: start, Latest: finish}}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tool-calls/stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: ready\ndata: %s\n\n", ready)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run_tool_reconnect")

	page, err := NewToolCallLogsRoute(t.Context(), start.CallID)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.width, page.height = 52, 10
	page.tools.events = []activity.Event{start}
	page.tools.records = []activity.ToolCallRecord{{CallID: start.CallID, First: start, Latest: start}}
	page.tools.latestSeq = start.Sequence
	page.syncToolCallDetail()
	offset := scrollLogsDetail(t, page)

	open := page.startToolCallFeed()
	if open == nil {
		t.Fatal("tool reconnect command missing")
	}
	msg, ok := open().(logsToolCallOpenMsg)
	if !ok || msg.err != nil || msg.stream == nil {
		t.Fatalf("tool reconnect open=%T err=%v stream=%v", msg, msg.err, msg.stream != nil)
	}
	page.finishToolCallFeedOpen(msg)
	if got := page.detail.YOffset(); got != offset {
		t.Fatalf("tool reconnect snapshot moved detail offset=%d want=%d", got, offset)
	}
	plain := ansi.Strip(page.detail.View())
	if !strings.Contains(plain, "success") || page.resourceID != start.CallID {
		t.Fatalf("tool reconnect snapshot lost pinned resource=%q view=%q", page.resourceID, plain)
	}
}

func TestExecutionReconnectSnapshotSchedulesCanonicalPinnedRefresh(t *testing.T) {
	root := setupLogsPageRoot(t)
	ready, _ := json.Marshal(shellruntime.ExecutionFeedSnapshot{Executions: []shellruntime.ExecutionInfo{{ID: "exec_reconnect", Tool: "run_command", WorkspaceID: "ws_reconnect", Status: shellruntime.ExecutionStatusSuccess}}, LatestSequence: 4})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/executions/stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: ready\ndata: %s\n\n", ready)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run_exec_reconnect")

	page, err := NewCommandExecutionLogsRoute(t.Context(), "exec_reconnect")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.width, page.height = 52, 10
	page.exec.detailRequest = 1
	page.finishExecutionDetail(logsExecutionDetailMsg{id: "exec_reconnect", request: 1, snapshot: shellruntime.ExecutionSnapshot{Execution: shellruntime.ExecutionInfo{ID: "exec_reconnect", Tool: "run_command", WorkspaceID: "ws_reconnect", Status: shellruntime.ExecutionStatusRunning}, Stdout: strings.Repeat("before reconnect\n", 120)}})
	offset := scrollLogsDetail(t, page)
	request := page.exec.detailRequest

	open := page.startExecutionFeed()
	if open == nil {
		t.Fatal("execution reconnect command missing")
	}
	msg, ok := open().(logsExecutionOpenMsg)
	if !ok || msg.err != nil || msg.stream == nil {
		t.Fatalf("execution reconnect open=%T err=%v stream=%v", msg, msg.err, msg.stream != nil)
	}
	cmd := page.finishExecutionFeedOpen(msg)
	if cmd == nil || page.exec.detailRequest != request+1 {
		t.Fatalf("execution reconnect did not schedule canonical detail refresh request=%d want=%d cmd=%v", page.exec.detailRequest, request+1, cmd)
	}
	if got := page.detail.YOffset(); got != offset || page.resourceID != "exec_reconnect" {
		t.Fatalf("execution reconnect reset pinned detail offset=%d want=%d resource=%q", got, offset, page.resourceID)
	}
}

func TestExecutionDetailFencesStaleRequestsAndPreservesScroll(t *testing.T) {
	page, err := NewCommandExecutionLogsRoute(t.Context(), "exec_target")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.width, page.height = 52, 10
	page.exec.detailRequest = 1
	initial := shellruntime.ExecutionSnapshot{Execution: shellruntime.ExecutionInfo{ID: "exec_target", Tool: "run_command", WorkspaceID: "ws_exec", Status: shellruntime.ExecutionStatusRunning}, Stdout: strings.Repeat("initial output line\n", 120), LatestSequence: 1}
	page.finishExecutionDetail(logsExecutionDetailMsg{id: "exec_target", request: 1, snapshot: initial})
	if !page.detailReady {
		t.Fatal("execution detail did not become ready")
	}
	offset := scrollLogsDetail(t, page)

	page.exec.detailRequest = 2
	newer := shellruntime.ExecutionSnapshot{Execution: shellruntime.ExecutionInfo{ID: "exec_target", Tool: "run_command", WorkspaceID: "ws_exec", Status: shellruntime.ExecutionStatusSuccess}, Stdout: strings.Repeat("newer output line\n", 120), LatestSequence: 2}
	page.finishExecutionDetail(logsExecutionDetailMsg{id: "exec_target", request: 2, snapshot: newer})
	if got := page.detail.YOffset(); got != offset {
		t.Fatalf("new execution detail moved offset=%d want=%d", got, offset)
	}
	plain := ansi.Strip(page.detail.View())
	if !strings.Contains(plain, shellruntime.ExecutionStatusSuccess) {
		t.Fatalf("new execution detail was not applied: %q", plain)
	}

	stale := shellruntime.ExecutionSnapshot{Execution: shellruntime.ExecutionInfo{ID: "exec_target", Tool: "run_command", WorkspaceID: "ws_exec", Status: shellruntime.ExecutionStatusFailed}, Stderr: "STALE", LatestSequence: 1}
	page.finishExecutionDetail(logsExecutionDetailMsg{id: "exec_target", request: 1, snapshot: stale})
	page.finishExecutionDetail(logsExecutionDetailMsg{id: "exec_other", request: 2, snapshot: stale})
	plain = ansi.Strip(page.detail.View())
	if got := page.detail.YOffset(); got != offset || !strings.Contains(plain, shellruntime.ExecutionStatusSuccess) || strings.Contains(plain, shellruntime.ExecutionStatusFailed) {
		t.Fatalf("stale execution detail overwrote current view offset=%d want=%d view=%q", got, offset, plain)
	}

	page.exec.generation = 9
	page.exec.latestSeq = 2
	before := page.exec.detailRequest
	page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: 9, event: shellruntime.ExecutionFeedEvent{Sequence: 3, ExecutionID: "exec_other", Type: shellruntime.ExecutionEventOutput, Data: "other"}})
	if page.exec.detailRequest != before {
		t.Fatalf("unrelated execution scheduled detail refresh request=%d want=%d", page.exec.detailRequest, before)
	}
	cmd := page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: 9, event: shellruntime.ExecutionFeedEvent{Sequence: 4, ExecutionID: "exec_target", Type: shellruntime.ExecutionEventOutput, Data: "target"}})
	if cmd == nil || page.exec.detailRequest != before+1 {
		t.Fatalf("same execution did not schedule canonical detail refresh request=%d want=%d cmd=%v", page.exec.detailRequest, before+1, cmd)
	}
}

func TestLogsBrowserMouseTargetAndSelectionSurviveLiveRebuild(t *testing.T) {
	page, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	base := time.Now().UTC()
	first := runtimeevent.Event{Sequence: 1, RunID: "run_browser", Time: base, Level: "info", Name: "first", Message: "first"}
	second := runtimeevent.Event{Sequence: 2, RunID: "run_browser", Time: base.Add(time.Second), Level: "info", Name: "second", Message: "second"}
	page.events = []runtimeevent.Event{first, second}
	page.query.RunID = first.RunID
	page.rebuildBrowser("")
	page.width, page.height = 100, 30
	_ = page.View(page.width, page.height)
	rows := browserRowTargets(page.MouseTargets(0, 0, 10))
	if len(rows) != 2 {
		t.Fatalf("runtime browser row targets=%d", len(rows))
	}
	firstTarget := rows[0]
	page = dispatchLogsMouse(t, page, firstTarget, tea.MouseLeft)
	selected, ok := page.browser.Selected()
	if !ok || selected.ID != logEventID(first) || !page.paused {
		t.Fatalf("selected=%#v paused=%t", selected, page.paused)
	}
	page.browser.SetHelpExpanded(true)
	third := runtimeevent.Event{Sequence: 3, RunID: first.RunID, Time: base.Add(2 * time.Second), Level: "warn", Name: "third", Message: "third"}
	page.appendEvent(third)
	selected, ok = page.browser.Selected()
	if !ok || selected.ID != logEventID(first) || !page.browser.HelpExpanded() {
		t.Fatalf("live rebuild lost browser state selected=%#v help=%t", selected, page.browser.HelpExpanded())
	}
	_, navigation := dispatchLogsMouseOpen(t, page, firstTarget)
	if strings.Join(navigation.Path, "/") != "logs/"+logEventID(first) {
		t.Fatalf("stale mouse target opened wrong resource: %#v", navigation)
	}
}

func TestExecutionAndToolBrowsersPreserveRetainedSelectionOnReorder(t *testing.T) {
	t.Run("execution", func(t *testing.T) {
		page, err := NewCommandExecutionLogs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		page.view, page.exec.paused = logsViewBrowser, true
		page.exec.executions = []shellruntime.ExecutionInfo{
			{ID: "exec_a", Tool: "run_command", Status: shellruntime.ExecutionStatusRunning},
			{ID: "exec_b", Tool: "run_command", Status: shellruntime.ExecutionStatusSuccess},
		}
		page.rebuildExecutionBrowser()
		page.browser.SelectLast()
		page.browser.SetHelpExpanded(true)
		page.exec.executions = []shellruntime.ExecutionInfo{
			{ID: "exec_new", Tool: "run_command", Status: shellruntime.ExecutionStatusRunning},
			{ID: "exec_b", Tool: "run_command", Status: shellruntime.ExecutionStatusSuccess},
			{ID: "exec_a", Tool: "run_command", Status: shellruntime.ExecutionStatusRunning},
		}
		page.rebuildExecutionBrowser()
		selected, ok := page.browser.Selected()
		if !ok || selected.ID != "exec_b" || !page.browser.HelpExpanded() {
			t.Fatalf("execution rebuild state selected=%#v help=%t", selected, page.browser.HelpExpanded())
		}
	})

	t.Run("tool call", func(t *testing.T) {
		page, err := NewToolCallLogsRoute(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		page.view, page.tools.paused = logsViewBrowser, true
		base := time.Now().UTC()
		a := activity.Event{Sequence: 1, CallID: "call_a", Kind: string(activity.EventToolCall), Tool: "read_file", Status: "running", Timestamp: base}
		b := activity.Event{Sequence: 2, CallID: "call_b", Kind: string(activity.EventToolCall), Tool: "write_file", Status: "success", Timestamp: base.Add(time.Second)}
		page.tools.records = []activity.ToolCallRecord{{CallID: a.CallID, First: a, Latest: a}, {CallID: b.CallID, First: b, Latest: b}}
		page.rebuildToolCallBrowser()
		page.browser.SelectLast()
		page.browser.SetHelpExpanded(true)
		newEvent := activity.Event{Sequence: 3, CallID: "call_new", Kind: string(activity.EventToolCall), Tool: "run_command", Status: "running", Timestamp: base.Add(2 * time.Second)}
		page.tools.records = []activity.ToolCallRecord{{CallID: newEvent.CallID, First: newEvent, Latest: newEvent}, {CallID: b.CallID, First: b, Latest: b}, {CallID: a.CallID, First: a, Latest: a}}
		page.rebuildToolCallBrowser()
		selected, ok := page.browser.Selected()
		if !ok || selected.ID != "call_b" || !page.browser.HelpExpanded() {
			t.Fatalf("tool rebuild state selected=%#v help=%t", selected, page.browser.HelpExpanded())
		}
	})
}

func TestLogsCloseFencesQueuedFeedAndDetailMessages(t *testing.T) {
	page, err := NewCommandExecutionLogsRoute(t.Context(), "exec_target")
	if err != nil {
		t.Fatal(err)
	}
	page.generation = 3
	page.exec.generation = 4
	page.exec.detailRequest = 5
	page.tools.generation = 6
	page.Close()

	page.finishStreamEvent(logsStreamEventMsg{generation: 3, event: runtimeevent.Event{Sequence: 1, RunID: "run_late", Time: time.Now().UTC(), Level: "info", Name: "late.runtime"}})
	page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: 4, event: shellruntime.ExecutionFeedEvent{Sequence: 1, ExecutionID: "exec_target", Type: shellruntime.ExecutionEventOutput, Data: "late exec"}})
	page.finishToolCallEvent(logsToolCallEventMsg{generation: 6, event: activity.Event{Sequence: 1, CallID: "call_late", Kind: string(activity.EventToolCall), Timestamp: time.Now().UTC()}})
	page.finishExecutionDetail(logsExecutionDetailMsg{id: "exec_target", request: 5, snapshot: shellruntime.ExecutionSnapshot{Execution: shellruntime.ExecutionInfo{ID: "exec_target", Status: shellruntime.ExecutionStatusSuccess}}})
	if len(page.events) != 0 || len(page.exec.events) != 0 || len(page.tools.events) != 0 || page.detailReady {
		t.Fatalf("closed page accepted queued messages runtime=%d exec=%d tools=%d detail_ready=%t", len(page.events), len(page.exec.events), len(page.tools.events), page.detailReady)
	}
}

func scrollLogsDetail(t *testing.T, page *LogsPage) int {
	t.Helper()
	_ = page.View(page.width, page.height)
	for range 5 {
		target := mouseTargetByID(t, page.MouseTargets(0, 0, 10), "detail.scroll")
		cmd := component.DispatchMouse([]component.MouseTarget{target}, tea.MouseClickMsg(tea.Mouse{X: target.Rect.X, Y: target.Rect.Y, Button: tea.MouseWheelDown}))
		if cmd == nil {
			t.Fatal("detail wheel produced no command")
		}
		updated, _ := page.Update(cmd())
		page = updated.(*LogsPage)
	}
	if page.detail.YOffset() == 0 {
		t.Fatal("detail content was not scrollable")
	}
	return page.detail.YOffset()
}

func browserRowTargets(targets []component.MouseTarget) []component.MouseTarget {
	rows := make([]component.MouseTarget, 0)
	for _, target := range targets {
		if target.ID == "browser.row" {
			rows = append(rows, target)
		}
	}
	return rows
}

func mouseTargetByID(t *testing.T, targets []component.MouseTarget, id string) component.MouseTarget {
	t.Helper()
	for _, target := range targets {
		if target.ID == id {
			return target
		}
	}
	t.Fatalf("mouse target %q not found", id)
	return component.MouseTarget{}
}

func dispatchLogsMouse(t *testing.T, page *LogsPage, target component.MouseTarget, button tea.MouseButton) *LogsPage {
	t.Helper()
	cmd := component.DispatchMouse([]component.MouseTarget{target}, tea.MouseClickMsg(tea.Mouse{X: target.Rect.X, Y: target.Rect.Y, Button: button}))
	if cmd == nil {
		t.Fatalf("mouse %v produced no command for %s", button, target.ID)
	}
	updated, _ := page.Update(cmd())
	return updated.(*LogsPage)
}

func dispatchLogsMouseOpen(t *testing.T, page *LogsPage, target component.MouseTarget) (*LogsPage, NavigateMsg) {
	t.Helper()
	cmd := component.DispatchMouse([]component.MouseTarget{target}, tea.MouseClickMsg(tea.Mouse{X: target.Rect.X, Y: target.Rect.Y, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("mouse open produced no browser command")
	}
	updated, open := page.Update(cmd())
	page = updated.(*LogsPage)
	if open == nil {
		t.Fatal("selected row second click produced no open command")
	}
	updated, navigate := page.Update(open())
	page = updated.(*LogsPage)
	if navigate == nil {
		t.Fatal("browser open produced no navigation command")
	}
	message, ok := navigate().(NavigateMsg)
	if !ok {
		t.Fatalf("browser open message=%T", navigate())
	}
	return page, message
}

func TestShortValuePreservesUTF8AndDisplayWidth(t *testing.T) {
	for _, tc := range []struct {
		value string
		limit int
	}{
		{value: "workspace-你好-very-long", limit: 12},
		{value: "café-déjà-vu", limit: 8},
		{value: "🙂🙂🙂", limit: 3},
	} {
		got := shortValue(tc.value, tc.limit)
		if !utf8.ValidString(got) {
			t.Fatalf("shortValue(%q, %d) returned invalid UTF-8: %q", tc.value, tc.limit, got)
		}
		if lipgloss.Width(got) > tc.limit {
			t.Fatalf("shortValue(%q, %d) width=%d value=%q", tc.value, tc.limit, lipgloss.Width(got), got)
		}
	}
}

func TestLogsPageDefaultsToVerboseWithoutExposingDebugFields(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	if page.visibility != logger.VisibilityVerbose {
		t.Fatalf("default visibility=%d", page.visibility)
	}
	event := runtimeevent.Event{Sequence: 1, Time: time.Now(), RunID: "run", Level: "info", Name: "safe", Message: "message", Fields: []runtimeevent.Field{{Key: "visible", Value: "ok"}, {Key: "verbose", Value: "useful", Visibility: logger.VisibilityVerbose}, {Key: "secret-debug", Value: "never-show", Visibility: logger.VisibilityDebug}}}
	row := page.logRow(event)
	joined := row.Search
	page.resourceID, page.section, page.events = "run:1", "fields", []runtimeevent.Event{event}
	page.syncDetail()
	joined += "\n" + ansi.Strip(page.detail.View())
	if !strings.Contains(joined, "visible") || !strings.Contains(joined, "verbose") || !strings.Contains(joined, "useful") || strings.Contains(joined, "secret-debug") || strings.Contains(joined, "never-show") {
		t.Fatalf("row leaked hidden field: %q", joined)
	}
}

func TestLogsPageDefaultVerboseShowsRuntimeEventsButHidesDebugNoise(t *testing.T) {
	root := setupLogsPageRoot(t)
	base := time.Now().UTC()
	appendLogEvents(t, root,
		runtimeevent.Event{Sequence: 1, Time: base, RunID: "run_visibility", Level: "info", Kind: "info", Name: "approval.pending", Component: "APPROVAL", Message: "Control approval requested", Visibility: logger.VisibilityDefault},
		runtimeevent.Event{Sequence: 2, Time: base.Add(time.Millisecond), RunID: "run_visibility", Level: "info", Kind: "success", Name: "tunnel.connected", Component: "TUNNEL", Message: "Tunnel connected", Visibility: logger.VisibilityDefault},
		runtimeevent.Event{Sequence: 3, Time: base.Add(2 * time.Millisecond), RunID: "run_visibility", Level: "info", Kind: "success", Name: "tool.call.completed", Component: "TOOL", Message: "Tool call completed", Tool: "run_command", Status: "ok", Visibility: logger.VisibilityVerbose},
		runtimeevent.Event{Sequence: 4, Time: base.Add(3 * time.Millisecond), RunID: "run_visibility", Level: "info", Kind: "info", Name: "tool.call.started", Component: "TOOL", Message: "Tool call started", Tool: "run_command", Status: "running", Visibility: logger.VisibilityVerbose},
	)
	writeLogsRuntimeState(t, root, "http://127.0.0.1:1", "run_visibility")
	page, _ := NewLogs(t.Context())
	defer page.Close()
	updated, _ := page.Update(page.Init()())
	page = updated.(*LogsPage)
	got := make([]string, 0, len(page.events))
	for _, event := range page.events {
		got = append(got, event.Name)
	}
	want := []string{"approval.pending", "tunnel.connected"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default verbose events=%#v want %#v", got, want)
	}
	status := ansi.Strip(page.statusView(180))
	if !strings.Contains(status, "View") || !strings.Contains(status, "verbose") {
		t.Fatalf("status missing visibility: %q", status)
	}
}

func TestLogsPageLiveVisibilityNormalVerboseAndDebug(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.generation = 1
	page.query.RunID = "run_live_visibility"
	page.streamRunID = "run_live_visibility"
	base := time.Now().UTC()
	feed := func(sequence uint64, name string, visibility logger.Visibility) {
		page.finishStreamEvent(logsStreamEventMsg{generation: 1, event: runtimeevent.Event{Sequence: sequence, Time: base.Add(time.Duration(sequence) * time.Millisecond), RunID: "run_live_visibility", Level: "info", Name: name, Message: name, Visibility: visibility}})
	}

	page.visibility = logger.VisibilityDefault
	feed(1, "normal", logger.VisibilityDefault)
	feed(2, "verbose-hidden", logger.VisibilityVerbose)
	if len(page.events) != 1 || page.events[0].Name != "normal" || page.streamSeq != 2 {
		t.Fatalf("normal view events=%#v seq=%d", page.events, page.streamSeq)
	}

	page.visibility = logger.VisibilityVerbose
	feed(3, "verbose", logger.VisibilityVerbose)
	feed(4, "debug-hidden", logger.VisibilityDebug)
	if len(page.events) != 2 || page.events[1].Name != "verbose" || page.streamSeq != 4 {
		t.Fatalf("verbose view events=%#v seq=%d", page.events, page.streamSeq)
	}

	page.visibility = logger.VisibilityDebug
	feed(5, "debug", logger.VisibilityDebug)
	if len(page.events) != 3 || page.events[2].Name != "debug" || page.streamSeq != 5 {
		t.Fatalf("debug view events=%#v seq=%d", page.events, page.streamSeq)
	}
}

func TestLogsFilterFormUsesSharedQueryValidation(t *testing.T) {
	editor, data := newLogsFilterEditor(application.LogsQueryOptions{Tail: 100}, logger.VisibilityVerbose)
	if editor.ActiveSectionID() != "range" {
		t.Fatalf("active section=%q", editor.ActiveSectionID())
	}
	if data.Visibility != "verbose" {
		t.Fatalf("form visibility=%q", data.Visibility)
	}
	data.Tail, data.Level, data.Event = "25", "warn", "tool.*"
	options, visibility, err := data.Options()
	if err != nil || options.Tail != 25 || options.Level != "warn" || options.Event != "tool.*" || visibility != logger.VisibilityVerbose {
		t.Fatalf("options=%#v visibility=%d err=%v", options, visibility, err)
	}
	data.All, data.Session = true, "run_one"
	if _, _, err := data.Options(); err == nil {
		t.Fatal("all + session was accepted")
	}
	data.All, data.Session, data.Visibility = false, "", "invalid"
	if _, _, err := data.Options(); err == nil {
		t.Fatal("invalid visibility was accepted")
	}
}

func TestLogsVisibilityValuesRoundTrip(t *testing.T) {
	for _, test := range []struct {
		visibility logger.Visibility
		value      string
	}{
		{visibility: logger.VisibilityDefault, value: "normal"},
		{visibility: logger.VisibilityVerbose, value: "verbose"},
		{visibility: logger.VisibilityDebug, value: "debug"},
	} {
		if got := logsVisibilityValue(test.visibility); got != test.value {
			t.Fatalf("logsVisibilityValue(%d)=%q want %q", test.visibility, got, test.value)
		}
		got, err := parseLogsVisibility(test.value)
		if err != nil || got != test.visibility {
			t.Fatalf("parseLogsVisibility(%q)=%d,%v want %d", test.value, got, err, test.visibility)
		}
	}
}

func TestLogsClearRequiresExplicitConfirmationAndInfoShowsJournal(t *testing.T) {
	root := setupLogsPageRoot(t)
	appendLogEvents(t, root, runtimeevent.Event{Sequence: 1, Time: time.Now(), RunID: "run", Level: "info", Name: "one", Message: "one"})
	page, _ := NewLogs(t.Context())
	defer page.Close()
	if cmd := page.openCommand(LogsClear); cmd != nil || page.overlay != logsOverlayConfirm || page.confirm.AffirmativeSelected() {
		t.Fatalf("clear overlay=%d affirmative=%t cmd=%v", page.overlay, page.confirm.AffirmativeSelected(), cmd)
	}
	if cmd := page.updateClearConfirm(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || page.overlay != logsOverlayNone {
		t.Fatalf("default clear executed: overlay=%d cmd=%v", page.overlay, cmd)
	}
	page.openCommand(LogsClear)
	page.confirm.Select(true)
	if cmd := page.updateClearConfirm(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil || page.overlay != logsOverlayOperation {
		t.Fatalf("confirmed clear cmd=%v overlay=%d", cmd, page.overlay)
	}
	page.closeOverlay()
	page.openCommand(LogsInfo)
	if page.overlay != logsOverlayInfo || page.info.Path != runtimeevent.Path(root) || page.info.Files == 0 {
		t.Fatalf("info=%#v overlay=%d", page.info, page.overlay)
	}
}

func TestLogsPageCloseCancelsLiveStream(t *testing.T) {
	root := setupLogsPageRoot(t)
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		closed <- struct{}{}
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run")
	page, _ := NewLogs(t.Context())
	updated, connect := page.Update(page.Init()())
	page = updated.(*LogsPage)
	if connect == nil || page.connected {
		t.Fatalf("connect=%v connected=%t", connect, page.connected)
	}
	updated, next := page.Update(connect())
	page = updated.(*LogsPage)
	if next == nil || !page.connected {
		t.Fatalf("next=%v connected=%t", next, page.connected)
	}
	page.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("stream context not cancelled on page close")
	}
}

func TestLogsPageLoadsCurrentRuntimeSessionBeforeOpeningStream(t *testing.T) {
	root := setupLogsPageRoot(t)
	base := time.Now().UTC()
	appendLogEvents(t, root,
		runtimeevent.Event{Sequence: 1, Time: base, RunID: "run_current", Level: "info", Name: "current", Message: "Current runtime"},
		runtimeevent.Event{Sequence: 1, Time: base.Add(time.Second), RunID: "run_old", Level: "info", Name: "old", Message: "Latest journal entry but old session"},
	)
	writeLogsRuntimeState(t, root, "http://127.0.0.1:1", "run_current")
	page, _ := NewLogs(t.Context())
	defer page.Close()
	updated, connect := page.Update(page.Init()())
	page = updated.(*LogsPage)
	if connect == nil || page.query.RunID != "run_current" || len(page.events) != 1 || page.events[0].Name != "current" {
		t.Fatalf("session=%q events=%#v connect=%v", page.query.RunID, page.events, connect)
	}
}

func TestLogsStatusShowsFullSessionID(t *testing.T) {
	page, _ := NewLogs(t.Context())
	session := "run_0123456789abcdef0123456789abcdef0123456789abcdef"
	page.query.RunID = session
	plain := ansi.Strip(page.statusView(160))
	if !strings.Contains(plain, session) || strings.Contains(plain, "run_0123456789abcdef0123…") {
		t.Fatalf("session was truncated: %q", plain)
	}
}

func TestLogsStatusPlacesSessionBesideFullStreamRow(t *testing.T) {
	page, _ := NewLogs(t.Context())
	page.connected = true
	page.query.RunID = "run_0123456789abcdef"
	lines := strings.Split(ansi.Strip(page.statusView(120)), "\n")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "Session  run_0123456789abcdef") {
		t.Fatalf("status row = %#v", lines)
	}
	for _, want := range []string{"Stream", "View", "Records", "Mode"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("status row missing %q: %#v", want, lines)
		}
	}
	if strings.Contains(lines[0], "Follow") {
		t.Fatalf("browser status unexpectedly includes follow: %#v", lines)
	}
}

func TestLogsClearKeepsLiveStreamAndStableNotice(t *testing.T) {
	page, _ := NewLogs(t.Context())
	page.connected = true
	page.loaded = true
	page.generation = 7
	page.clearSeq = 3
	page.streamRunID = "run_live"
	page.streamSeq = 41
	page.events = []runtimeevent.Event{{RunID: "run_live", Sequence: 41, Message: "before clear"}}
	updated, _ := page.Update(logsClearMsg{operation: 3})
	page = updated.(*LogsPage)
	if !page.connected || page.generation != 7 || len(page.events) != 0 || page.notice != "Runtime logs cleared" || !page.ShouldToastNotice() {
		t.Fatalf("clear state connected=%t generation=%d events=%d notice=%q toast=%t", page.connected, page.generation, len(page.events), page.notice, page.ShouldToastNotice())
	}
	page.finishStreamEvent(logsStreamEventMsg{generation: 7, event: runtimeevent.Event{RunID: "run_live", Sequence: 42, Message: "after clear"}})
	if page.notice != "Runtime logs cleared" || page.generation != 7 || page.streamSeq != 42 {
		t.Fatalf("live stream changed clear feedback: notice=%q generation=%d sequence=%d", page.notice, page.generation, page.streamSeq)
	}
}

func TestLogsPageResyncsJournalWhenLiveSequenceHasGap(t *testing.T) {
	root := setupLogsPageRoot(t)
	appendLogEvents(t, root,
		runtimeevent.Event{Sequence: 1, Time: time.Now().UTC(), RunID: "run_gap", Level: "info", Name: "one", Message: "one"},
		runtimeevent.Event{Sequence: 2, Time: time.Now().UTC(), RunID: "run_gap", Level: "info", Name: "two", Message: "two"},
		runtimeevent.Event{Sequence: 3, Time: time.Now().UTC(), RunID: "run_gap", Level: "info", Name: "three", Message: "three"},
	)
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.generation = 4
	page.connected = true
	page.query.RunID = "run_gap"
	page.streamRunID, page.streamSeq = "run_gap", 1
	cmd := page.finishStreamEvent(logsStreamEventMsg{generation: 4, event: runtimeevent.Event{Sequence: 3, Time: time.Now().UTC(), RunID: "run_gap", Level: "info", Name: "three", Message: "three"}})
	if cmd == nil || page.generation != 5 || len(page.events) != 0 || !strings.Contains(page.notice, "gap") {
		t.Fatalf("gap resync generation=%d events=%d notice=%q cmd=%v", page.generation, len(page.events), page.notice, cmd)
	}
}

func TestLogsPageResyncsWhenStreamAdvancedDuringJournalLoad(t *testing.T) {
	root := setupLogsPageRoot(t)
	base := time.Now().UTC()
	appendLogEvents(t, root, runtimeevent.Event{Sequence: 1, Time: base, RunID: "run_race", Level: "info", Name: "one", Message: "one"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":2}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run_race")
	page, _ := NewLogs(t.Context())
	defer page.Close()
	updated, connect := page.Update(page.Init()())
	page = updated.(*LogsPage)
	if page.streamSeq != 1 || connect == nil {
		t.Fatalf("journal watermark=%d connect=%v", page.streamSeq, connect)
	}
	generation := page.generation
	updated, resync := page.Update(connect())
	page = updated.(*LogsPage)
	if resync == nil || page.generation != generation+1 || page.connected || !strings.Contains(page.notice, "advanced") {
		t.Fatalf("resync generation=%d connected=%t notice=%q cmd=%v", page.generation, page.connected, page.notice, resync)
	}
}

func TestLogsPageExplicitSessionAndAllOverrideRuntimeSessionSelection(t *testing.T) {
	root := setupLogsPageRoot(t)
	base := time.Now().UTC()
	appendLogEvents(t, root,
		runtimeevent.Event{Sequence: 1, Time: base, RunID: "run_current", Level: "info", Name: "current", Message: "current"},
		runtimeevent.Event{Sequence: 1, Time: base.Add(time.Second), RunID: "run_old", Level: "info", Name: "old", Message: "old"},
	)
	writeLogsRuntimeState(t, root, "http://127.0.0.1:1", "run_current")
	for _, test := range []struct {
		name      string
		configure func(*LogsPage)
		wantRun   string
		wantCount int
	}{
		{name: "explicit-session", configure: func(page *LogsPage) { page.options.Session = "run_old" }, wantRun: "run_old", wantCount: 1},
		{name: "all", configure: func(page *LogsPage) { page.options.All = true }, wantRun: "", wantCount: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, _ := NewLogs(t.Context())
			defer page.Close()
			test.configure(page)
			updated, connect := page.Update(page.Init()())
			page = updated.(*LogsPage)
			if connect == nil || page.query.RunID != test.wantRun || len(page.events) != test.wantCount {
				t.Fatalf("run=%q events=%d connect=%v", page.query.RunID, len(page.events), connect)
			}
		})
	}
}

func TestLogsPageStreamSequenceTracksFilteredEventsWithoutFalseGap(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.generation = 7
	page.query = runtimeevent.Query{RunID: "run_filter", MinLevel: "error"}
	page.streamRunID = "run_filter"
	if cmd := page.finishStreamEvent(logsStreamEventMsg{generation: 7, event: runtimeevent.Event{Sequence: 1, Time: time.Now().UTC(), RunID: "run_filter", Level: "info", Name: "hidden", Message: "hidden"}}); cmd != nil {
		t.Fatalf("filtered event unexpectedly scheduled command: %v", cmd)
	}
	if page.streamSeq != 1 || len(page.events) != 0 || page.generation != 7 {
		t.Fatalf("filtered sequence=%d events=%d generation=%d", page.streamSeq, len(page.events), page.generation)
	}
	page.finishStreamEvent(logsStreamEventMsg{generation: 7, event: runtimeevent.Event{Sequence: 2, Time: time.Now().UTC(), RunID: "run_filter", Level: "error", Name: "visible", Message: "visible"}})
	if page.streamSeq != 2 || len(page.events) != 1 || page.events[0].Name != "visible" || page.generation != 7 {
		t.Fatalf("visible sequence=%d events=%#v generation=%d", page.streamSeq, page.events, page.generation)
	}
}

func TestLogsPageUpdateCoversBrowserActionsAndOverlays(t *testing.T) {
	root := setupLogsPageRoot(t)
	appendLogEvents(t, root, runtimeevent.Event{Sequence: 1, Time: time.Now().UTC(), RunID: "run_ui", Level: "info", Name: "one", Message: "one"})
	page, _ := NewLogs(t.Context())
	defer page.Close()
	if page.OverlayActive() || page.InputActive() {
		t.Fatal("new logs page unexpectedly active")
	}
	updated, _ := page.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	page = updated.(*LogsPage)
	if page.width != 120 || page.height != 30 {
		t.Fatalf("size=%dx%d", page.width, page.height)
	}

	updated, filterCmd := page.Update(LogsCommandMsg{Command: LogsFilter})
	page = updated.(*LogsPage)
	if filterCmd == nil || page.editor == nil || page.OverlayActive() || !page.InputActive() {
		t.Fatalf("filter editor=%v active=%t input=%t", page.editor != nil, page.OverlayActive(), page.InputActive())
	}
	updated, cancelCmd := page.Update(component.EditorCancelMsg{})
	page = updated.(*LogsPage)
	if cancelCmd == nil || page.editor != nil || page.OverlayActive() || page.InputActive() {
		t.Fatalf("cancel editor=%v active=%t input=%t", page.editor != nil, page.OverlayActive(), page.InputActive())
	}

	updated, _ = page.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	page = updated.(*LogsPage)
	if page.overlay != logsOverlayInfo || !page.OverlayActive() {
		t.Fatalf("info overlay=%d", page.overlay)
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	page = updated.(*LogsPage)
	if page.overlay != logsOverlayNone {
		t.Fatalf("info close overlay=%d", page.overlay)
	}

	updated, _ = page.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	page = updated.(*LogsPage)
	if page.overlay != logsOverlayConfirm || page.confirm.AffirmativeSelected() {
		t.Fatalf("clear overlay=%d affirmative=%t", page.overlay, page.confirm.AffirmativeSelected())
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	page = updated.(*LogsPage)
	if page.overlay != logsOverlayNone {
		t.Fatalf("clear cancel overlay=%d", page.overlay)
	}

	updated, _ = page.Update(LogsCommandMsg{Command: LogsToggle})
	page = updated.(*LogsPage)
	if !page.paused {
		t.Fatal("toggle command did not pause")
	}
	page.view = logsViewTimeline
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	page = updated.(*LogsPage)
	if page.paused {
		t.Fatal("space did not resume timeline follow")
	}
}

func TestLogsPageUpdateSubmitsAndRejectsFilters(t *testing.T) {
	setupLogsPageRoot(t)
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.initFilterEditor()
	page.filterForm.Tail = "25"
	page.filterForm.Visibility = "debug"
	page.filterForm.Level = "warn"
	page.filterForm.Event = "tool.*"
	updated, bootstrap := page.Update(component.EditorSubmitMsg{})
	page = updated.(*LogsPage)
	if bootstrap == nil || page.editor != nil || page.options.Tail != 25 || page.options.Level != "warn" || page.options.Event != "tool.*" || page.visibility != logger.VisibilityDebug {
		t.Fatalf("options=%#v visibility=%d editor=%v cmd=%v", page.options, page.visibility, page.editor != nil, bootstrap)
	}

	page.initFilterEditor()
	page.filterForm.All, page.filterForm.Session = true, "run_stale"
	updated, bootstrap = page.Update(component.EditorSubmitMsg{})
	page = updated.(*LogsPage)
	plain := ansi.Strip(page.View(44, 18))
	if bootstrap != nil || page.editor == nil || page.filterForm.Session != "run_stale" || page.err != nil || !strings.Contains(plain, "all sessions and session filter") {
		t.Fatalf("invalid filter editor=%v pageErr=%v cmd=%v view=%q", page.editor != nil, page.err, bootstrap, plain)
	}
}

func TestLogsFilterDeepLinkUsesNativeWrappedEditor(t *testing.T) {
	page, err := NewLogsRouteAction(t.Context(), "", "", "filter")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	if page.editor == nil || page.OverlayActive() || !page.InputActive() {
		t.Fatalf("editor=%v overlay=%t input=%t", page.editor != nil, page.OverlayActive(), page.InputActive())
	}
	view := page.View(28, 18)
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 28 {
			t.Fatalf("line width=%d want <=28: %q", got, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(view)
	for _, want := range []string{"Range", "Filters"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("filter editor missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Log Filters") {
		t.Fatalf("filter editor retained redundant page title: %q", plain)
	}
	if !strings.Contains(plain, "enter next") {
		t.Fatalf("filter editor does not advertise Enter navigation: %q", plain)
	}
}

func TestLogsPageStreamOpenAndReconnectBranches(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.generation = 10
	page.loaded = true
	page.query.RunID = "run_one"
	page.streamRunID, page.streamSeq = "run_one", 3

	staleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":3}\n\n")
	}))
	defer staleServer.Close()
	root := setupLogsPageRoot(t)
	writeLogsRuntimeState(t, root, staleServer.URL, "run_one")
	stream, state, err := runtimecontrol.OpenEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cmd := page.finishStreamOpen(logsStreamOpenMsg{generation: 9, stream: stream, state: state}); cmd != nil {
		t.Fatalf("stale stream scheduled cmd=%v", cmd)
	}

	page.generation = 10
	page.err = nil
	cmd := page.finishStreamOpen(logsStreamOpenMsg{generation: 10, state: state, err: fmt.Errorf("offline")})
	if cmd == nil || page.connected || !page.reconnecting || !strings.Contains(page.notice, "offline") {
		t.Fatalf("offline connected=%t reconnect=%t notice=%q cmd=%v", page.connected, page.reconnecting, page.notice, cmd)
	}

	page.generation = 10
	page.connected = false
	page.reconnecting = true
	updated, connect := page.Update(logsReconnectMsg(9))
	page = updated.(*LogsPage)
	if connect != nil || page.generation != 10 {
		t.Fatalf("stale reconnect generation=%d cmd=%v", page.generation, connect)
	}
}

func TestLogsPageStreamEventDisconnectAndGapHelpers(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.generation = 2
	page.connected = true
	page.streamRunID, page.streamSeq = "run", 4
	if page.streamGap(runtimeevent.Event{}) {
		t.Fatal("empty event reported gap")
	}
	if page.streamGap(runtimeevent.Event{RunID: "other", Sequence: 7}) || page.streamRunID != "other" || page.streamSeq != 7 {
		t.Fatalf("new run tracking=%q/%d", page.streamRunID, page.streamSeq)
	}
	if page.streamGap(runtimeevent.Event{RunID: "other", Sequence: 8}) {
		t.Fatal("sequential event reported gap")
	}
	if !page.streamGap(runtimeevent.Event{RunID: "other", Sequence: 10}) {
		t.Fatal("sequence gap not detected")
	}

	page.connected = true
	cmd := page.finishStreamEvent(logsStreamEventMsg{generation: 2, err: io.EOF})
	if cmd == nil || page.connected || !page.reconnecting || !strings.Contains(page.notice, "disconnected") {
		t.Fatalf("disconnect connected=%t reconnect=%t notice=%q cmd=%v", page.connected, page.reconnecting, page.notice, cmd)
	}
	if page.ShouldToastNotice() {
		t.Fatal("stream disconnect status unexpectedly marked as toast")
	}
	if cmd := page.finishStreamEvent(logsStreamEventMsg{generation: 1, event: runtimeevent.Event{RunID: "run", Sequence: 1}}); cmd != nil {
		t.Fatalf("stale event scheduled cmd=%v", cmd)
	}
}

func TestLogsCommandExecutionRouteStreamsCombinedOutputInEventOrder(t *testing.T) {
	root := setupLogsPageRoot(t)
	code := 0
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_test", WorkspaceID: "ws_a", Tool: "run_command", Command: "printf demo", CWD: "/tmp", Source: "mcp", CallID: "call_test", SessionHash: "session-test", ReceivedByInstanceID: "instance-a", ExecutedByInstanceID: "instance-b", StartedAt: started.Format(time.RFC3339Nano), Status: shellruntime.ExecutionStatusRunning}
	snapshot := shellruntime.ExecutionFeedSnapshot{Events: []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Status: shellruntime.ExecutionStatusRunning, Timestamp: started.Format(time.RFC3339Nano)},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Stream: "stdout", Data: "out\n", Timestamp: started.Add(500 * time.Millisecond).Format(time.RFC3339Nano)},
	}, LatestSequence: 2}
	ready, _ := json.Marshal(snapshot)
	live := shellruntime.ExecutionFeedEvent{Sequence: 3, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Stream: "stderr", Data: "err\n", Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)}
	liveData, _ := json.Marshal(live)
	finished := info
	finished.FinishedAt, finished.Status, finished.ExitCode = started.Add(2*time.Second).Format(time.RFC3339Nano), shellruntime.ExecutionStatusSuccess, &code
	completed := shellruntime.ExecutionFeedEvent{Sequence: 4, Type: shellruntime.ExecutionEventCompleted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &finished, Status: shellruntime.ExecutionStatusSuccess, ExitCode: &code, Timestamp: finished.FinishedAt}
	completedData, _ := json.Marshal(completed)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/executions/stream" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: ready\ndata: %s\n\nid: 3\nevent: output\ndata: %s\n\nid: 4\nevent: completed\ndata: %s\n\n", ready, liveData, completedData)
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run_exec")
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	open := page.Init()
	if page.tab != logsTabCommandExec || open == nil {
		t.Fatalf("tab=%d open=%v", page.tab, open)
	}
	updated, next := page.Update(open())
	page = updated.(*LogsPage)
	if !page.exec.connected || len(page.exec.events) != 2 || next == nil {
		t.Fatalf("connected=%t events=%#v next=%v", page.exec.connected, page.exec.events, next)
	}
	updated, next = page.Update(next())
	page = updated.(*LogsPage)
	if len(page.exec.events) != 3 || next == nil {
		t.Fatalf("live events=%#v next=%v", page.exec.events, next)
	}
	updated, next = page.Update(next())
	page = updated.(*LogsPage)
	if len(page.exec.events) != 4 || next == nil {
		t.Fatalf("completed events=%#v next=%v", page.exec.events, next)
	}
	plain := ansi.Strip(page.View(120, 32))
	for _, want := range []string{"Runtime", "Command Execution", "Mode  combined", "START", "exec_test", "printf demo", "Workspace  ws_a", "Source  mcp", "Session  session-test", "Call  call_test", "Route", "• received: instance-a", "• executed: instance-b", "out", "err", "END", "Status  success", "Exit  0", "Duration  2s", "←/→ tabs"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("command exec view missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "stdout:") || strings.Contains(plain, "stderr:") {
		t.Fatalf("combined view split streams: %q", plain)
	}
}

func TestLogsCommandExecutionUnsupportedRuntimeStopsReconnectLoop(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.exec.generation = 7
	page.exec.loading = true
	cmd := page.finishExecutionFeedOpen(logsExecutionOpenMsg{generation: 7, err: runtimecontrol.ErrExecutionFeedUnsupported})
	if cmd != nil || page.exec.connected || page.exec.reconnecting || !page.exec.unsupported || !page.exec.loaded {
		t.Fatalf("cmd=%v connected=%t reconnecting=%t unsupported=%t loaded=%t", cmd, page.exec.connected, page.exec.reconnecting, page.exec.unsupported, page.exec.loaded)
	}
	view := ansi.Strip(page.executionStatusView(100))
	if !strings.Contains(view, "RESTART REQUIRED") || !strings.Contains(page.exec.notice, "Restart the running server") {
		t.Fatalf("status=%q notice=%q", view, page.exec.notice)
	}
}

func TestLogsCommandExecutionReconnectPreservesOpenError(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.exec.generation = 9
	page.exec.loading = true
	want := errors.New("execution stream failed")
	cmd := page.finishExecutionFeedOpen(logsExecutionOpenMsg{generation: 9, err: want})
	if cmd == nil || !page.exec.reconnecting || !errors.Is(page.exec.err, want) {
		t.Fatalf("cmd=%v reconnecting=%t err=%v", cmd, page.exec.reconnecting, page.exec.err)
	}
	if !strings.Contains(ansi.Strip(page.executionHeaderView(100)), want.Error()) {
		t.Fatalf("header=%q", ansi.Strip(page.executionHeaderView(100)))
	}
}

func TestLogsCommandExecutionEmptyViewPinsHelpToBottom(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.connected, page.exec.loaded = true, true
	plain := ansi.Strip(page.View(120, 28))
	lines := strings.Split(plain, "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 0 || !strings.Contains(lines[last], "reconnect") {
		t.Fatalf("bottom help not pinned: last=%d line=%q view=%q", last, lines[last], plain)
	}
	status := -1
	waiting := -1
	for index, line := range lines {
		if status < 0 && strings.Contains(line, "Stream") && strings.Contains(line, "Follow") && strings.Contains(line, "Events") && strings.Contains(line, "1024") && strings.Contains(line, "Mode") {
			status = index
		}
		if strings.Contains(line, "Waiting for command output") {
			waiting = index
			break
		}
	}
	if status < 0 || status+1 >= len(lines) || !strings.Contains(lines[status+1], "──") {
		t.Fatalf("command execution status is not above divider: status=%d view=%q", status, plain)
	}
	if strings.Contains(plain, "live output") {
		t.Fatalf("command execution retained redundant live output label: %q", plain)
	}
	if waiting < 0 || last-waiting < 10 {
		t.Fatalf("empty body did not reserve vertical space: waiting=%d help=%d", waiting, last)
	}
}

func TestCommandExecutionStickyHeaderAppearsAfterSegmentHeaderScrollsAway(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_sticky", WorkspaceID: "ws_sticky", Command: "go test ./...", StartedAt: started.Format(time.RFC3339Nano)}
	page.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Data: strings.Repeat("line\n", 30), Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)},
	}
	page.exec.paused = true
	page.exec.viewport.SetWidth(80)
	page.exec.viewport.SetHeight(8)
	page.refreshExecutionViewport()
	if len(page.exec.render.Segments) != 1 {
		t.Fatalf("segments=%#v", page.exec.render.Segments)
	}
	segment := page.exec.render.Segments[0]
	page.exec.viewport.SetYOffset(max(0, segment.BodyStartLine-1))
	if sticky := page.executionStickyHeader(80); sticky != "" {
		t.Fatalf("sticky appeared before header fully scrolled away: %q", ansi.Strip(sticky))
	}
	page.exec.viewport.SetYOffset(segment.BodyStartLine)
	sticky := ansi.Strip(page.executionStickyHeader(80))
	for _, want := range []string{"START", "exec_sticky", "ws_sticky", "$ go test ./..."} {
		if !strings.Contains(sticky, want) {
			t.Fatalf("sticky header missing %q: %q", want, sticky)
		}
	}
	if got := lipgloss.Width(sticky); got != 80 {
		t.Fatalf("sticky width=%d want 80: %q", got, sticky)
	}
}

func TestCommandExecutionStickyHeaderTracksTopSegment(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	started := time.Now().UTC()
	first := shellruntime.ExecutionInfo{ID: "exec_first", WorkspaceID: "ws_first", Command: "first", StartedAt: started.Format(time.RFC3339Nano)}
	second := shellruntime.ExecutionInfo{ID: "exec_second", WorkspaceID: "ws_second", Command: "second", StartedAt: started.Add(time.Second).Format(time.RFC3339Nano)}
	page.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: first.ID, WorkspaceID: first.WorkspaceID, Execution: &first, Timestamp: first.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: first.ID, WorkspaceID: first.WorkspaceID, Execution: &first, Data: strings.Repeat("first-line\n", 20), Timestamp: started.Add(500 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 3, Type: shellruntime.ExecutionEventStarted, ExecutionID: second.ID, WorkspaceID: second.WorkspaceID, Execution: &second, Timestamp: second.StartedAt},
		{Sequence: 4, Type: shellruntime.ExecutionEventOutput, ExecutionID: second.ID, WorkspaceID: second.WorkspaceID, Execution: &second, Data: strings.Repeat("second-line\n", 20), Timestamp: started.Add(1500 * time.Millisecond).Format(time.RFC3339Nano)},
	}
	page.exec.paused = true
	page.exec.viewport.SetWidth(80)
	page.exec.viewport.SetHeight(6)
	page.refreshExecutionViewport()
	if len(page.exec.render.Segments) != 2 {
		t.Fatalf("segments=%#v", page.exec.render.Segments)
	}
	page.exec.viewport.SetYOffset(page.exec.render.Segments[0].BodyStartLine)
	if sticky := ansi.Strip(page.executionStickyHeader(80)); !strings.Contains(sticky, "exec_first") || strings.Contains(sticky, "exec_second") {
		t.Fatalf("first sticky=%q", sticky)
	}
	page.exec.viewport.SetYOffset(page.exec.render.Segments[1].BodyStartLine)
	if sticky := ansi.Strip(page.executionStickyHeader(80)); !strings.Contains(sticky, "exec_second") || strings.Contains(sticky, "exec_first") {
		t.Fatalf("second sticky=%q", sticky)
	}
}

func TestTrimExecutionFeedRetainsLatestEvents(t *testing.T) {
	events := make([]shellruntime.ExecutionFeedEvent, shellruntime.MaxExecutionFeedEvents+3)
	for index := range events {
		events[index].Sequence = uint64(index + 1)
	}
	kept := trimExecutionFeed(events)
	if len(kept) != shellruntime.MaxExecutionFeedEvents || kept[0].Sequence != 4 || kept[len(kept)-1].Sequence != uint64(shellruntime.MaxExecutionFeedEvents+3) {
		t.Fatalf("kept=%d range=%d..%d", len(kept), kept[0].Sequence, kept[len(kept)-1].Sequence)
	}
}

func TestFormatExecutionFeedCombinesStdoutAndStderrWithoutStreamSections(t *testing.T) {
	code := 7
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_order", WorkspaceID: "ws_a", Command: "demo", CWD: "/work", StartedAt: started.Format(time.RFC3339Nano)}
	finished := info
	finished.FinishedAt = started.Add(2 * time.Second).Format(time.RFC3339Nano)
	events := []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, Execution: &info, Timestamp: started.Format(time.RFC3339Nano)},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, Execution: &info, Stream: "stdout", Data: "A", Timestamp: started.Add(250 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 3, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, Execution: &info, Stream: "stderr", Data: "B", Timestamp: started.Add(500 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 4, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, Execution: &info, Stream: "stdout", Data: "C\n", Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)},
		{Sequence: 5, Type: shellruntime.ExecutionEventCompleted, ExecutionID: info.ID, Execution: &finished, Status: shellruntime.ExecutionStatusFailed, ExitCode: &code, Timestamp: finished.FinishedAt},
	}
	view := formatExecutionFeed(events)
	if !strings.Contains(view, "│ ABC") || !strings.Contains(view, "╰─ END ") || !strings.Contains(view, "Status  failed") || !strings.Contains(view, "Exit  7") || !strings.Contains(view, "Duration  2s") || strings.Count(view, "╭") != 1 || strings.Count(view, "╯") != 1 || strings.Count(view, "├") != 2 || strings.Contains(view, "stdout") || strings.Contains(view, "stderr") {
		t.Fatalf("combined feed=%q", view)
	}
}

func TestFormatExecutionFeedMarksInterleavedContinuations(t *testing.T) {
	code := 0
	started := time.Now().UTC()
	infoA := shellruntime.ExecutionInfo{ID: "exec_a", WorkspaceID: "ws_a", Command: "first", StartedAt: started.Format(time.RFC3339Nano)}
	infoB := shellruntime.ExecutionInfo{ID: "exec_b", WorkspaceID: "ws_b", Command: "second", StartedAt: started.Add(100 * time.Millisecond).Format(time.RFC3339Nano)}
	finishedA, finishedB := infoA, infoB
	finishedA.FinishedAt = started.Add(900 * time.Millisecond).Format(time.RFC3339Nano)
	finishedB.FinishedAt = started.Add(700 * time.Millisecond).Format(time.RFC3339Nano)
	events := []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: infoA.ID, WorkspaceID: infoA.WorkspaceID, Execution: &infoA, Timestamp: infoA.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: infoA.ID, WorkspaceID: infoA.WorkspaceID, Execution: &infoA, Data: "A1\n", Timestamp: started.Add(200 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 3, Type: shellruntime.ExecutionEventStarted, ExecutionID: infoB.ID, WorkspaceID: infoB.WorkspaceID, Execution: &infoB, Timestamp: infoB.StartedAt},
		{Sequence: 4, Type: shellruntime.ExecutionEventOutput, ExecutionID: infoB.ID, WorkspaceID: infoB.WorkspaceID, Execution: &infoB, Data: "B1\n", Timestamp: started.Add(300 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 5, Type: shellruntime.ExecutionEventOutput, ExecutionID: infoA.ID, WorkspaceID: infoA.WorkspaceID, Execution: &infoA, Data: "A2\n", Timestamp: started.Add(500 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 6, Type: shellruntime.ExecutionEventOutput, ExecutionID: infoB.ID, WorkspaceID: infoB.WorkspaceID, Execution: &infoB, Data: "B2\n", Timestamp: started.Add(600 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 7, Type: shellruntime.ExecutionEventCompleted, ExecutionID: infoB.ID, WorkspaceID: infoB.WorkspaceID, Execution: &finishedB, Status: shellruntime.ExecutionStatusSuccess, ExitCode: &code, Timestamp: finishedB.FinishedAt},
		{Sequence: 8, Type: shellruntime.ExecutionEventCompleted, ExecutionID: infoA.ID, WorkspaceID: infoA.WorkspaceID, Execution: &finishedA, Status: shellruntime.ExecutionStatusSuccess, ExitCode: &code, Timestamp: finishedA.FinishedAt},
	}
	view := formatExecutionFeed(events)
	if strings.Count(view, "╭─ START ") != 2 || strings.Count(view, "╭─ CONTINUE ") != 3 || strings.Count(view, "╰─ PAUSE ") != 3 || strings.Count(view, "╰─ END ") != 2 || strings.Count(view, "├") != 10 {
		t.Fatalf("interleaved markers=%q", view)
	}
	for _, want := range []string{"CONTINUE", "PAUSE", "Execution  exec_a", "Execution  exec_b", "+500ms", "│ A1", "│ B1", "│ A2", "│ B2", "No output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("interleaved feed missing %q: %q", want, view)
		}
	}
	if !(strings.Index(view, "A1") < strings.Index(view, "B1") && strings.Index(view, "B1") < strings.Index(view, "A2") && strings.Index(view, "A2") < strings.Index(view, "B2")) {
		t.Fatalf("interleaved output order changed: %q", view)
	}
}

func TestFormatExecutionFeedSeparatesConcurrentExecutionsInSameWorkspace(t *testing.T) {
	started := time.Now().UTC()
	infoA := shellruntime.ExecutionInfo{ID: "exec_a", WorkspaceID: "ws_same", StartedAt: started.Format(time.RFC3339Nano)}
	infoB := shellruntime.ExecutionInfo{ID: "exec_b", WorkspaceID: "ws_same", StartedAt: started.Add(time.Millisecond).Format(time.RFC3339Nano)}
	view := formatExecutionFeed([]shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: infoA.ID, WorkspaceID: infoA.WorkspaceID, Execution: &infoA, Timestamp: infoA.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: infoA.ID, WorkspaceID: infoA.WorkspaceID, Execution: &infoA, Data: "A\n", Timestamp: started.Add(2 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 3, Type: shellruntime.ExecutionEventStarted, ExecutionID: infoB.ID, WorkspaceID: infoB.WorkspaceID, Execution: &infoB, Timestamp: infoB.StartedAt},
		{Sequence: 4, Type: shellruntime.ExecutionEventOutput, ExecutionID: infoB.ID, WorkspaceID: infoB.WorkspaceID, Execution: &infoB, Data: "B\n", Timestamp: started.Add(3 * time.Millisecond).Format(time.RFC3339Nano)},
		{Sequence: 5, Type: shellruntime.ExecutionEventOutput, ExecutionID: infoA.ID, WorkspaceID: infoA.WorkspaceID, Execution: &infoA, Data: "A2\n", Timestamp: started.Add(4 * time.Millisecond).Format(time.RFC3339Nano)},
	})
	if strings.Count(view, "╭─ START ") != 2 || strings.Count(view, "╭─ CONTINUE ") != 1 || strings.Count(view, "╰─ PAUSE ") != 2 || strings.Count(view, "╰─ RUNNING ") != 1 || !strings.Contains(view, "CONTINUE") || !strings.Contains(view, "Execution  exec_a") || strings.Contains(view, "exec_id=") {
		t.Fatalf("same-workspace interleave=%q", view)
	}
}

func TestFormatExecutionFeedKeepsLiveTailRunningUntilInterrupted(t *testing.T) {
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_live", WorkspaceID: "ws_live", StartedAt: started.Format(time.RFC3339Nano)}
	view := formatExecutionFeed([]shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Data: "still running\n", Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)},
	})
	if !strings.Contains(view, "╰─ RUNNING ") || strings.Contains(view, "╰─ PAUSE ") || strings.Contains(view, "╰─ END ") {
		t.Fatalf("live tail marker=%q", view)
	}
}

func TestExecutionFrameFitsRenderWidthAndKeepsCommandInside(t *testing.T) {
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_resize", WorkspaceID: "ws_resize", Command: strings.Repeat("command-token-", 12), CWD: strings.Repeat("nested/", 16), StartedAt: started.Format(time.RFC3339Nano), ReceivedByInstanceID: "receive-a", ExecutedByInstanceID: "execute-b"}
	event := shellruntime.ExecutionFeedEvent{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt}
	for _, width := range []int{120, 80, 24, 12} {
		view := formatExecutionFeed([]shellruntime.ExecutionFeedEvent{event}, width)
		for _, line := range strings.Split(view, "\n") {
			if line == "" {
				continue
			}
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width=%d line=%d: %q", width, got, line)
			}
		}
		lines := strings.Split(view, "\n")
		if len(lines) == 0 || lipgloss.Width(lines[0]) != width || strings.Contains(lines[0], "exec_resize") || strings.Contains(lines[0], "exec_id=") || !strings.Contains(lines[0], "START") {
			t.Fatalf("width=%d top border=%q", width, lines[0])
		}
		flat := strings.Join(strings.Fields(strings.NewReplacer("│", "", "╭", "", "╮", "", "╰", "", "╯", "", "─", "", "•", "").Replace(ansi.Strip(view))), "")
		if !strings.Contains(flat, "exec_resize") {
			t.Fatalf("width=%d execution id not rendered inside frame: %q", width, view)
		}
		if !strings.Contains(view, "Route") || !strings.Contains(flat, "received:receive-a") || !strings.Contains(flat, "executed:execute-b") || !strings.Contains(flat, "command-token") {
			t.Fatalf("width=%d frame content=%q", width, view)
		}
		separator := strings.Index(ansi.Strip(view), "├")
		command := strings.Index(ansi.Strip(view), "comm")
		close := strings.Index(ansi.Strip(view), "╰")
		if separator < 0 || command <= separator || close <= command {
			t.Fatalf("width=%d command is not inside stream body: %q", width, view)
		}
	}
}

func TestFormatExecutionFeedShowsNoOutputForEmptySegment(t *testing.T) {
	started := time.Now().UTC()
	code := 0
	info := shellruntime.ExecutionInfo{ID: "exec_empty", WorkspaceID: "ws_empty", StartedAt: started.Format(time.RFC3339Nano)}
	finished := info
	finished.FinishedAt = started.Add(time.Second).Format(time.RFC3339Nano)
	view := formatExecutionFeed([]shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventCompleted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &finished, Status: shellruntime.ExecutionStatusSuccess, ExitCode: &code, Timestamp: finished.FinishedAt},
	})
	if !strings.Contains(view, "│ No output") || !strings.Contains(view, "╰─ END ") || strings.Count(view, "├") != 2 {
		t.Fatalf("empty segment=%q", view)
	}
}

func TestExecutionFrameTabsDoNotBreakRightBorder(t *testing.T) {
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_tabs", WorkspaceID: "ws_tabs", Command: "go test ./...", StartedAt: started.Format(time.RFC3339Nano)}
	view := formatExecutionFeed([]shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Data: "ok\tgo.mewis.me/codemcp/internal/interface/tui\t1.263s\n?\tgo.mewis.me/codemcp/internal/interface/tui/testutil\t[no test files]\n", Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)},
	}, 80)
	for _, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipgloss.Width(line); got != 80 {
			t.Fatalf("frame line width=%d want 80: %q", got, line)
		}
		if strings.Contains(line, "\t") {
			t.Fatalf("raw tab remained in frame: %q", line)
		}
	}
}

func TestExecutionFrameWindowsNewlinesDoNotBreakRightBorder(t *testing.T) {
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_crlf", WorkspaceID: "ws_crlf", Command: "node -e test", StartedAt: started.Format(time.RFC3339Nano)}
	view := formatExecutionFeed([]shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Data: "node:internal/modules/cjs/loader:1520\r\n    at defaultResolveImplForCJSLoading (node:internal/modules/cjs/loader:1095:10)\r\nnext\rprogress\r\n", Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)},
	}, 84)
	if strings.Contains(view, "\r") {
		t.Fatalf("raw carriage return remained in frame: %q", view)
	}
	for _, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipgloss.Width(line); got != 84 {
			t.Fatalf("frame line width=%d want 84: %q", got, line)
		}
	}
	for _, want := range []string{"node:internal/modules/cjs/loader:1520", "10)", "next", "progress"} {
		if !strings.Contains(view, want) {
			t.Fatalf("frame missing %q: %q", want, view)
		}
	}
}

func TestExecutionFrameUnsafeTerminalControlsDoNotBreakRightBorder(t *testing.T) {
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_controls", WorkspaceID: "ws_controls", Command: "demo\b\x00\rcommand", CWD: "C:\\work\x00\vdir", StartedAt: started.Format(time.RFC3339Nano)}
	view := formatExecutionFeed([]shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Data: "alpha\r\nbeta\rprogress\b!\x00nul\vvertical\fform\x1b[31mred\x1b[0m\n", Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)},
	}, 84)
	plain := ansi.Strip(view)
	for _, control := range []string{"\r", "\b", "\x00", "\v", "\f", "\x1b"} {
		if strings.Contains(plain, control) {
			t.Fatalf("unsafe control %q remained in frame: %q", control, plain)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if got := lipgloss.Width(line); got != 84 {
			t.Fatalf("frame line width=%d want 84: %q", got, line)
		}
	}
	for _, want := range []string{"alpha", "beta", "progress!", "nulverticalformred", "demo command", "C:\\workdir"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("frame missing sanitized content %q: %q", want, view)
		}
	}
}

func TestPausedExecutionFeedDefersViewportRefreshUntilResume(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	info := shellruntime.ExecutionInfo{ID: "exec_pause", WorkspaceID: "ws_a", Tool: "run_command", Command: "demo"}
	page.exec.events = []shellruntime.ExecutionFeedEvent{{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info}}
	page.exec.latestSeq, page.exec.generation = 1, 7
	page.refreshExecutionViewport()
	before := page.exec.viewport.GetContent()
	page.exec.paused = true
	page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: 7, event: shellruntime.ExecutionFeedEvent{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Data: "new output\n"}})
	if got := page.exec.viewport.GetContent(); got != before {
		t.Fatal("paused feed rebuilt viewport content")
	}
	page.handleExecutionKey(tea.KeyPressMsg{Code: tea.KeySpace})
	if got := page.exec.viewport.GetContent(); !strings.Contains(got, "new output") || page.exec.paused {
		t.Fatalf("resume did not refresh buffered output: paused=%t content=%q", page.exec.paused, got)
	}
}

func TestExecutionScopeFiltersCombinedWorkspaceAndContainer(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, ExecutionID: "exec_a", WorkspaceID: "ws_a", Type: shellruntime.ExecutionEventOutput, Data: "A\n"},
		{Sequence: 2, ExecutionID: "exec_b", WorkspaceID: "ws_b", Type: shellruntime.ExecutionEventOutput, Data: "B\n"},
		{Sequence: 3, ExecutionID: "exec_c", WorkspaceID: "ws_c", Type: shellruntime.ExecutionEventOutput, Data: "C\n"},
	}
	if got := len(page.visibleExecutionEvents()); got != 3 {
		t.Fatalf("combined events=%d", got)
	}
	page.exec.scopeMode, page.exec.workspaceID = executionScopeWorkspace, "ws_b"
	if visible := page.visibleExecutionEvents(); len(visible) != 1 || visible[0].WorkspaceID != "ws_b" {
		t.Fatalf("workspace events=%#v", visible)
	}
	page.exec.scopeMode, page.exec.containerMembers = executionScopeContainer, map[string]struct{}{"ws_a": {}, "ws_c": {}}
	if visible := page.visibleExecutionEvents(); len(visible) != 2 || visible[0].WorkspaceID != "ws_a" || visible[1].WorkspaceID != "ws_c" {
		t.Fatalf("container events=%#v", visible)
	}
}

func TestExecutionScopeSeparatesCommandsFromSelectedProcess(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	command := shellruntime.ExecutionInfo{ID: "exec_command", WorkspaceID: "ws_a", Tool: "run_command"}
	process := shellruntime.ExecutionInfo{ID: "exec_process", WorkspaceID: "ws_a", Tool: "start_process"}
	otherProcess := shellruntime.ExecutionInfo{ID: "exec_other", WorkspaceID: "ws_a", Tool: "start_process"}
	page.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, ExecutionID: command.ID, WorkspaceID: "ws_a", Type: shellruntime.ExecutionEventOutput, Execution: &command, Data: "command\n"},
		{Sequence: 2, ExecutionID: process.ID, WorkspaceID: "ws_a", Type: shellruntime.ExecutionEventOutput, Execution: &process, Data: "process\n"},
		{Sequence: 3, ExecutionID: otherProcess.ID, WorkspaceID: "ws_a", Type: shellruntime.ExecutionEventOutput, Execution: &otherProcess, Data: "other\n"},
	}
	if visible := page.visibleExecutionEvents(); len(visible) != 1 || visible[0].ExecutionID != command.ID {
		t.Fatalf("combined command view=%#v", visible)
	}
	page.exec.scopeMode, page.exec.workspaceID, page.exec.workspaceView = executionScopeWorkspace, "ws_a", executionWorkspaceCommands
	if visible := page.visibleExecutionEvents(); len(visible) != 1 || visible[0].ExecutionID != command.ID {
		t.Fatalf("workspace command view=%#v", visible)
	}
	page.exec.workspaceView, page.exec.processID, page.exec.processExecutionID = executionWorkspaceProcess, "proc_a", process.ID
	if visible := page.visibleExecutionEvents(); len(visible) != 1 || visible[0].ExecutionID != process.ID {
		t.Fatalf("process view=%#v", visible)
	}
}

func TestSelectedProcessUsesExistingExecutionRenderer(t *testing.T) {
	started := time.Now().UTC()
	code := 0
	info := shellruntime.ExecutionInfo{ID: "exec_process", WorkspaceID: "ws_a", Tool: "start_process", Command: "serve", CWD: "/work", StartedAt: started.Format(time.RFC3339Nano), Status: shellruntime.ExecutionStatusRunning}
	finished := info
	finished.FinishedAt, finished.Status, finished.ExitCode = started.Add(time.Second).Format(time.RFC3339Nano), shellruntime.ExecutionStatusSuccess, &code
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.scopeMode, page.exec.workspaceID, page.exec.workspaceView = executionScopeWorkspace, "ws_a", executionWorkspaceProcess
	page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = "proc_a", info.ID, true
	page.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info, Data: "ready\n", Timestamp: started.Add(500 * time.Millisecond).Format(time.RFC3339Nano)},
	}
	view := formatExecutionFeed(page.visibleExecutionEvents(), 80)
	if !strings.Contains(view, "START") || !strings.Contains(ansi.Strip(view), "serve") || !strings.Contains(view, "ready") || !strings.Contains(view, "RUNNING") {
		t.Fatalf("running process view=%q", view)
	}
	page.exec.events = append(page.exec.events, shellruntime.ExecutionFeedEvent{Sequence: 3, Type: shellruntime.ExecutionEventCompleted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &finished, Status: shellruntime.ExecutionStatusSuccess, ExitCode: &code, Timestamp: finished.FinishedAt})
	view = formatExecutionFeed(page.visibleExecutionEvents(), 80)
	if !strings.Contains(view, "END") || !strings.Contains(view, "Status  success") || !strings.Contains(view, "Exit  0") {
		t.Fatalf("completed process view=%q", view)
	}
}

func TestFinishedProcessCleanupOnlyRunsAfterDetach(t *testing.T) {
	oldDelete := deleteFinishedProcess
	defer func() { deleteFinishedProcess = oldDelete }()
	called := make(chan string, 1)
	deleteFinishedProcess = func(_ context.Context, workspaceID, processID string) error {
		called <- workspaceID + "/" + processID
		return nil
	}
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.scopeMode, page.exec.workspaceID, page.exec.workspaceView = executionScopeWorkspace, "ws_a", executionWorkspaceProcess
	page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = "proc_a", "exec_a", false
	cmd := page.switchLogsTab(logsTabRuntime)
	if cmd == nil {
		t.Fatal("finished process detach returned no cleanup command")
	}
	message := cmd()
	if batch, ok := message.(tea.BatchMsg); ok {
		for _, item := range batch {
			if item != nil {
				_ = item()
			}
		}
	}
	select {
	case got := <-called:
		if got != "ws_a/proc_a" {
			t.Fatalf("cleanup target=%q", got)
		}
	default:
		t.Fatal("finished process was not cleaned up")
	}
	if page.exec.workspaceView != executionWorkspaceCommands || page.exec.processID != "" {
		t.Fatalf("detached process state=%#v", page.exec)
	}
}

func TestRunningProcessDetachNeverCleansOrStopsProcess(t *testing.T) {
	oldDelete := deleteFinishedProcess
	defer func() { deleteFinishedProcess = oldDelete }()
	called := false
	deleteFinishedProcess = func(context.Context, string, string) error { called = true; return nil }
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.scopeMode, page.exec.workspaceID, page.exec.workspaceView = executionScopeWorkspace, "ws_a", executionWorkspaceProcess
	page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = "proc_a", "exec_a", true
	if cmd := page.detachSelectedProcessCmd(); cmd != nil {
		_ = cmd()
	}
	if called {
		t.Fatal("running process detach attempted cleanup")
	}
}

func TestLogsSessionStateKeepsRunningProcessButDropsFinishedSelection(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.scopeMode, page.exec.workspaceID, page.exec.workspaceView = executionScopeWorkspace, "ws_a", executionWorkspaceProcess
	page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = "proc_a", "exec_a", true
	running := page.SessionViewState().(LogsSessionViewState)
	if running.ExecutionWorkspaceView != string(executionWorkspaceProcess) || running.ExecutionProcessID != "proc_a" || running.ExecutionProcessExecutionID != "exec_a" || !running.ExecutionProcessRunning {
		t.Fatalf("running state=%#v", running)
	}
	page.exec.processRunning = false
	finished := page.SessionViewState().(LogsSessionViewState)
	if finished.ExecutionWorkspaceView != string(executionWorkspaceCommands) || finished.ExecutionProcessID != "" || finished.ExecutionProcessExecutionID != "" {
		t.Fatalf("finished state=%#v", finished)
	}
}

func TestLogsCloseCleansFinishedSelectedProcess(t *testing.T) {
	oldDelete := deleteFinishedProcess
	defer func() { deleteFinishedProcess = oldDelete }()
	called := ""
	deleteFinishedProcess = func(_ context.Context, workspaceID, processID string) error {
		called = workspaceID + "/" + processID
		return nil
	}
	page, _ := NewCommandExecutionLogs(t.Context())
	page.exec.scopeMode, page.exec.workspaceID, page.exec.workspaceView = executionScopeWorkspace, "ws_a", executionWorkspaceProcess
	page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = "proc_a", "exec_a", false
	page.Close()
	if called != "ws_a/proc_a" {
		t.Fatalf("close cleanup=%q", called)
	}
}

func TestLogsSessionStateRestoresRunningProcessSelection(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.RestoreSessionViewState(LogsSessionViewState{
		Tab: "command-execution", ExecutionScope: string(executionScopeWorkspace), ExecutionWorkspaceID: "ws_a",
		ExecutionWorkspaceView: string(executionWorkspaceProcess), ExecutionProcessID: "proc_a", ExecutionProcessExecutionID: "exec_a", ExecutionProcessRunning: true,
	})
	if page.exec.scopeMode != executionScopeWorkspace || page.exec.workspaceView != executionWorkspaceProcess || page.exec.processID != "proc_a" || page.exec.processExecutionID != "exec_a" || !page.exec.processRunning {
		t.Fatalf("restored process state=%#v", page.exec)
	}
}

func TestLogsModeDialogAppliesContainerWithoutReconnectingExecutionFeed(t *testing.T) {
	setupLogsPageRoot(t)
	manager := workspace.NewManager(workspace.DefaultStorePath())
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspacesToContainer(container.ID, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.generation = 9
	page.openLogsModeDialog()
	if page.modeDialog == nil || len(page.modeDialog.options) != 4 {
		t.Fatalf("mode dialog=%#v", page.modeDialog)
	}
	page.modeDialog.index = 2
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if page.modeDialog == nil || page.modeDialog.stage != logsModeStageContainer || len(page.modeDialog.options) != 1 {
		t.Fatalf("container dialog=%#v", page.modeDialog)
	}
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if page.modeDialog != nil || page.exec.scopeMode != executionScopeContainer || page.exec.containerID != container.ID || page.exec.containerName != "project" || len(page.exec.containerMembers) != 2 || page.exec.generation != 9 {
		t.Fatalf("scope applied=%#v generation=%d", page.exec, page.exec.generation)
	}
}

func TestLogsModeDialogEnterAppliesWorkspaceImmediately(t *testing.T) {
	setupLogsPageRoot(t)
	manager := workspace.NewManager(workspace.DefaultStorePath())
	workspaceItem, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.openLogsModeDialog()
	page.modeDialog.index = 1
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if page.modeDialog == nil || page.modeDialog.stage != logsModeStageWorkspace {
		t.Fatalf("workspace dialog=%#v", page.modeDialog)
	}
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if page.modeDialog != nil || page.exec.scopeMode != executionScopeWorkspace || page.exec.workspaceID != workspaceItem.ID {
		t.Fatalf("scope mode=%s workspace=%s", page.exec.scopeMode, page.exec.workspaceID)
	}
}

func TestLogsModeDialogProcessOnlyExistsForCommandExecution(t *testing.T) {
	exec, _ := NewCommandExecutionLogs(t.Context())
	defer exec.Close()
	exec.openLogsModeDialog()
	if len(exec.modeDialog.options) != 4 || exec.modeDialog.options[3].value != "process" {
		t.Fatalf("execution mode options=%#v", exec.modeDialog.options)
	}
	tools, _ := NewToolCallLogsRoute(t.Context(), "")
	defer tools.Close()
	tools.openLogsModeDialog()
	if len(tools.modeDialog.options) != 3 {
		t.Fatalf("tool mode options=%#v", tools.modeDialog.options)
	}
	for _, option := range tools.modeDialog.options {
		if option.value == "process" {
			t.Fatalf("tool calls exposed process option: %#v", tools.modeDialog.options)
		}
	}
}

func TestLogsViewShortcutDoesNotReconnectExecutionFeed(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.generation = 11
	if page.view != logsViewTimeline {
		t.Fatalf("default view=%s", page.view)
	}
	page.handleExecutionKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if page.view != logsViewBrowser || page.exec.generation != 11 {
		t.Fatalf("view=%s generation=%d", page.view, page.exec.generation)
	}
	page.handleExecutionKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if page.view != logsViewTimeline || page.exec.generation != 11 {
		t.Fatalf("view=%s generation=%d", page.view, page.exec.generation)
	}
}

func TestRuntimeViewShortcutAndModeDoNotReconnectStream(t *testing.T) {
	setupLogsPageRoot(t)
	manager := workspace.NewManager(workspace.DefaultStorePath())
	workspaceItem, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.generation = 7
	page.handleKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if page.view != logsViewTimeline || page.generation != 7 {
		t.Fatalf("runtime view=%s generation=%d", page.view, page.generation)
	}
	page.openLogsModeDialog()
	if len(page.modeDialog.options) != 3 {
		t.Fatalf("runtime mode options=%#v", page.modeDialog.options)
	}
	page.modeDialog.index = 1
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if page.modeDialog != nil || page.runtimeScope.mode != executionScopeWorkspace || page.runtimeScope.workspaceID != workspaceItem.ID || page.generation != 7 {
		t.Fatalf("runtime scope=%#v generation=%d", page.runtimeScope, page.generation)
	}
}

func TestRuntimeTimelineFiltersToolCallsAndRendersVisibleFields(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.visibility = logger.VisibilityVerbose
	page.events = []runtimeevent.Event{
		{Sequence: 1, Time: time.Now(), Level: "info", Name: "server.ready", Component: "SERVER", Message: "Ready", WorkspaceID: "ws_a", Fields: []runtimeevent.Field{{Key: "visible", Value: "ok"}, {Key: "debug", Value: "hidden", Visibility: logger.VisibilityDebug}}},
		{Sequence: 2, Time: time.Now(), Level: "info", Name: "tool.call.finish", Component: "TOOLS", Message: "duplicate", WorkspaceID: "ws_a"},
	}
	visible := page.visibleRuntimeEvents()
	if len(visible) != 1 || visible[0].Name != "server.ready" {
		t.Fatalf("visible runtime events=%#v", visible)
	}
	plain := ansi.Strip(renderRuntimeTimeline(visible, 100, page.visibility).Content)
	if !strings.Contains(plain, "visible") || !strings.Contains(plain, "ok") || strings.Contains(plain, "hidden") || strings.Contains(plain, "tool.call.finish") {
		t.Fatalf("runtime timeline=%q", plain)
	}
}

func TestToolCallsMergeLifecycleAndRenderFullRequestResponse(t *testing.T) {
	page, _ := NewToolCallLogsRoute(t.Context(), "")
	defer page.Close()
	page.tools.events = []activity.Event{
		{Sequence: 1, Timestamp: time.Now(), Kind: string(activity.EventToolCall), Phase: "start", CallID: "call_1", Tool: "run_command", WorkspaceID: "ws_a", Status: "running", Raw: map[string]any{"arguments": map[string]any{"command": "go test ./..."}}},
		{Sequence: 2, Timestamp: time.Now(), Kind: string(activity.EventToolCall), Phase: "finish", CallID: "call_1", Tool: "run_command", WorkspaceID: "ws_a", Status: "ok", DurationMS: 12, Raw: map[string]any{"arguments": map[string]any{"command": "go test ./..."}, "result": map[string]any{"exit_code": 0, "stdout": "ok"}}},
	}
	records := page.visibleToolCallRecords()
	if len(records) != 1 || records[0].First.Phase != "start" || records[0].Latest.Phase != "finish" {
		t.Fatalf("tool records=%#v", records)
	}
	plain := ansi.Strip(renderToolCallTimeline(records, 100).Content)
	for _, want := range []string{"REQUEST", "RESPONSE", "go test ./...", "exit_code", "stdout", "12ms"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("tool timeline missing %q: %q", want, plain)
		}
	}
	page.resourceID = "call_1"
	page.width, page.height = 100, 30
	page.syncToolCallDetail()
	detail := ansi.Strip(page.detail.View())
	for _, want := range []string{"call_1", "arguments", "result", "go test ./...", "exit_code"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("tool detail missing %q: %q", want, detail)
		}
	}
}

func TestToolCallTimelineAlignsBodyWithLabels(t *testing.T) {
	record := toolCallRecord{
		CallID: "call_1",
		First:  activity.Event{Sequence: 1, Timestamp: time.Now(), Kind: string(activity.EventToolCall), Phase: "start", CallID: "call_1", Tool: "read_file", Status: "running", Raw: map[string]any{"arguments": map[string]any{"head": 80, "path": "I:/Project/workspaces/hiresense/.env"}}},
		Latest: activity.Event{Sequence: 2, Timestamp: time.Now(), Kind: string(activity.EventToolCall), Phase: "finish", CallID: "call_1", Tool: "read_file", Status: "error", Raw: map[string]any{"error": "head: must be an integer"}},
	}
	plain := ansi.Strip(renderToolCallTimeline([]toolCallRecord{record}, 100).Content)
	labelColumn, bodyColumn := -1, -1
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "REQUEST") {
			labelColumn = strings.Index(line, "REQUEST")
		}
		if strings.Contains(line, "{") {
			bodyColumn = strings.Index(line, "{")
		}
	}
	if labelColumn < 0 || bodyColumn < 0 || labelColumn != bodyColumn {
		t.Fatalf("tool timeline columns label=%d body=%d: %q", labelColumn, bodyColumn, plain)
	}
}

func TestToolCallViewShortcutAndModeDoNotReconnectFeed(t *testing.T) {
	setupLogsPageRoot(t)
	manager := workspace.NewManager(workspace.DefaultStorePath())
	workspaceItem, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := NewToolCallLogsRoute(t.Context(), "")
	defer page.Close()
	page.tools.generation = 13
	page.handleToolCallKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if page.view != logsViewTimeline || page.tools.generation != 13 {
		t.Fatalf("tool view=%s generation=%d", page.view, page.tools.generation)
	}
	page.openLogsModeDialog()
	page.modeDialog.index = 1
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	page.updateLogsModeDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if page.modeDialog != nil || page.tools.scope.mode != executionScopeWorkspace || page.tools.scope.workspaceID != workspaceItem.ID || page.tools.generation != 13 {
		t.Fatalf("tool scope=%#v generation=%d", page.tools.scope, page.tools.generation)
	}
}

func TestCommandExecutionSettingsRouteIsRemoved(t *testing.T) {
	if _, err := NewCommandExecutionLogsRouteAction(t.Context(), "settings"); err == nil {
		t.Fatal("legacy command execution settings route unexpectedly accepted")
	}
}

func TestCommandExecutionUsesFullBodyHeight(t *testing.T) {
	page, err := NewCommandExecutionLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.width, page.height = 100, 30
	help := page.executionHelpView(page.width)
	tabs := component.PageTabsNotice(logsTabLabels, int(page.tab), page.notice, page.width)
	bodyHeight := page.height - lipgloss.Height(tabs)
	layout := component.NewSectionLayout("", "", page.executionHeaderView(page.width), page.width, bodyHeight, lipgloss.Height(help))
	page.executionBodyView(page.width, layout.BodyHeight)
	want := layout.BodyHeight
	if got := page.exec.viewport.Height(); got != want {
		t.Fatalf("execution viewport height=%d want=%d", got, want)
	}
}

func TestRuntimeLogsStatusRendersAboveDividerWithoutLiveJournalLabel(t *testing.T) {
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.connected, page.loaded = true, true
	plain := ansi.Strip(page.View(120, 28))
	lines := strings.Split(plain, "\n")
	status := -1
	for index, line := range lines {
		if strings.Contains(line, "Stream") && strings.Contains(line, "View") && strings.Contains(line, "Records") && strings.Contains(line, "Mode") {
			status = index
			break
		}
	}
	if status < 0 || status+1 >= len(lines) || !strings.Contains(lines[status+1], "──") {
		t.Fatalf("runtime log status is not above divider: status=%d view=%q", status, plain)
	}
	if strings.Contains(plain, "live journal") {
		t.Fatalf("runtime logs retained redundant live journal label: %q", plain)
	}
}

func TestExecutionScopeRefreshTracksMembershipAndStaleContainer(t *testing.T) {
	setupLogsPageRoot(t)
	manager := workspace.NewManager(workspace.DefaultStorePath())
	first, _ := manager.Register(t.TempDir())
	second, _ := manager.Register(t.TempDir())
	container, _ := manager.CreateContainer("project")
	_, _ = manager.AddWorkspaceToContainer(container.ID, first.ID)
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.scopeMode, page.exec.containerID = executionScopeContainer, container.ID
	page.refreshExecutionScope()
	if len(page.exec.containerMembers) != 1 {
		t.Fatalf("initial members=%#v", page.exec.containerMembers)
	}
	writer := workspace.NewManager(workspace.DefaultStorePath())
	if _, err := writer.AddWorkspaceToContainer(container.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	page.refreshExecutionScope()
	if len(page.exec.containerMembers) != 2 {
		t.Fatalf("refreshed members=%#v", page.exec.containerMembers)
	}
	if err := writer.DeleteContainer(container.ID); err != nil {
		t.Fatal(err)
	}
	page.refreshExecutionScope()
	if !page.exec.scopeStale || page.exec.scopeNotice == "" || len(page.visibleExecutionEvents()) != 0 {
		t.Fatalf("stale scope stale=%t notice=%q visible=%#v", page.exec.scopeStale, page.exec.scopeNotice, page.visibleExecutionEvents())
	}
}

func TestExecutionScopeFilteringDoesNotCreateSequenceGaps(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.generation = 4
	page.exec.scopeMode, page.exec.workspaceID = executionScopeWorkspace, "ws_selected"
	page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: 4, event: shellruntime.ExecutionFeedEvent{Sequence: 1, ExecutionID: "visible", WorkspaceID: "ws_selected", Type: shellruntime.ExecutionEventStarted}})
	page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: 4, event: shellruntime.ExecutionFeedEvent{Sequence: 2, ExecutionID: "hidden", WorkspaceID: "ws_other", Type: shellruntime.ExecutionEventOutput, Data: "hidden\n"}})
	page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: 4, event: shellruntime.ExecutionFeedEvent{Sequence: 3, ExecutionID: "visible", WorkspaceID: "ws_selected", Type: shellruntime.ExecutionEventOutput, Data: "visible\n"}})
	visible := page.visibleExecutionEvents()
	if page.exec.latestSeq != 3 || len(page.exec.events) != 3 || len(visible) != 2 || page.exec.generation != 4 || strings.Contains(page.exec.notice, "gap") || strings.Contains(formatExecutionFeed(visible), "[CONTINUE]") {
		t.Fatalf("filtered sequence=%d raw=%d visible=%d generation=%d notice=%q", page.exec.latestSeq, len(page.exec.events), len(page.visibleExecutionEvents()), page.exec.generation, page.exec.notice)
	}
	page.exec.scopeMode, page.exec.containerMembers = executionScopeContainer, map[string]struct{}{"ws_selected": {}}
	visible = page.visibleExecutionEvents()
	if len(visible) != 2 || strings.Contains(formatExecutionFeed(visible), "[CONTINUE]") {
		t.Fatalf("container-filtered events=%#v view=%q", visible, formatExecutionFeed(visible))
	}
}

func TestExecutionScopeSurvivesSnapshotReplayAndOverflow(t *testing.T) {
	root := setupLogsPageRoot(t)
	manager := workspace.NewManager(workspace.DefaultStorePath())
	selected, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := shellruntime.ExecutionFeedSnapshot{Events: []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, ExecutionID: "selected", WorkspaceID: selected.ID, Type: shellruntime.ExecutionEventOutput, Data: "selected\n"},
		{Sequence: 2, ExecutionID: "other", WorkspaceID: other.ID, Type: shellruntime.ExecutionEventOutput, Data: "other\n"},
	}, LatestSequence: 2}
	ready, _ := json.Marshal(snapshot)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: ready\ndata: %s\n\n", ready)
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run_scope")
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.exec.scopeMode, page.exec.workspaceID = executionScopeWorkspace, selected.ID
	open := page.Init()
	updated, next := page.Update(open())
	page = updated.(*LogsPage)
	if !page.exec.connected || page.exec.scopeMode != executionScopeWorkspace || page.exec.workspaceID != selected.ID || page.exec.latestSeq != 2 || len(page.exec.events) != 2 || len(page.visibleExecutionEvents()) != 1 || next == nil {
		t.Fatalf("snapshot scope=%s workspace=%s seq=%d raw=%d visible=%d connected=%t", page.exec.scopeMode, page.exec.workspaceID, page.exec.latestSeq, len(page.exec.events), len(page.visibleExecutionEvents()), page.exec.connected)
	}
	generation := page.exec.generation
	reconnect := page.finishExecutionFeedEvent(logsExecutionEventMsg{generation: generation, err: runtimecontrol.ErrExecutionFeedOverflow})
	if reconnect == nil || page.exec.scopeMode != executionScopeWorkspace || page.exec.workspaceID != selected.ID || !page.exec.reconnecting {
		t.Fatalf("overflow reset scope: mode=%s workspace=%s reconnect=%t cmd=%v", page.exec.scopeMode, page.exec.workspaceID, page.exec.reconnecting, reconnect)
	}
}

func TestLogsSessionViewStateRestoresStablePreferencesOnly(t *testing.T) {
	page, err := NewCommandExecutionLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page.options = application.LogsQueryOptions{Tail: 55, All: true, Level: "warn", Workspace: "ws_runtime"}
	page.visibility = logger.VisibilityDebug
	page.paused = true
	page.exec.scopeMode, page.exec.workspaceID, page.exec.paused = executionScopeWorkspace, "ws_exec", true
	page.exec.events = []shellruntime.ExecutionFeedEvent{{Sequence: 1, ExecutionID: "exec_state", WorkspaceID: "ws_exec", Type: shellruntime.ExecutionEventOutput, Data: strings.Repeat("line\n", 40)}}
	page.resizeExecutionViewport(60, 8)
	page.exec.viewport.SetYOffset(7)
	page.runtimeClear = map[string]uint64{"run_state": 7}
	page.executionClear = 8
	page.toolCallClear = 9
	page.exec.stream = &runtimecontrol.ExecutionFeedStream{}

	state, ok := page.SessionViewState().(LogsSessionViewState)
	if !ok {
		t.Fatalf("state type=%T", page.SessionViewState())
	}
	fresh, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	fresh.RestoreSessionViewState(state)
	restored := fresh.SessionViewState().(LogsSessionViewState)
	if restored.Tab != "command-execution" || restored.Options != state.Options || restored.Visibility != logger.VisibilityDebug || !restored.RuntimePaused || restored.ExecutionScope != string(executionScopeWorkspace) || restored.ExecutionWorkspaceID != "ws_exec" || !restored.ExecutionPaused || restored.RuntimeClearSequences["run_state"] != 7 || restored.ExecutionClearSequence != 8 || restored.ToolCallClearSequence != 9 {
		t.Fatalf("restored state=%#v want=%#v", restored, state)
	}
	state.RuntimeClearSequences["run_state"] = 99
	if fresh.runtimeClear["run_state"] != 7 {
		t.Fatalf("runtime clear watermarks were not deep-cloned: %#v", fresh.runtimeClear)
	}
	if fresh.exec.stream != nil || len(fresh.exec.events) != 0 || fresh.loaded || fresh.exec.loaded {
		t.Fatalf("transient state restored: stream=%v events=%d runtime_loaded=%t exec_loaded=%t", fresh.exec.stream != nil, len(fresh.exec.events), fresh.loaded, fresh.exec.loaded)
	}
	if !fresh.exec.restoreYOffsetSet || fresh.exec.restoreYOffset != 7 {
		t.Fatalf("deferred offset set=%t offset=%d", fresh.exec.restoreYOffsetSet, fresh.exec.restoreYOffset)
	}
}

func TestLogsSessionViewStateRestoresAndClampsExecutionOffset(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.RestoreSessionViewState(LogsSessionViewState{Tab: "command-execution", ExecutionScope: string(executionScopeCombined), ExecutionPaused: true, ExecutionYOffset: 999})
	page.exec.events = []shellruntime.ExecutionFeedEvent{{Sequence: 1, ExecutionID: "exec_offset", Type: shellruntime.ExecutionEventOutput, Data: strings.Repeat("line\n", 30)}}
	page.exec.viewport.SetWidth(40)
	page.exec.viewport.SetHeight(6)
	page.refreshExecutionViewport()
	page.restoreExecutionViewportOffset()
	want := max(0, page.exec.viewport.TotalLineCount()-page.exec.viewport.Height())
	if page.exec.viewport.YOffset() != want || page.exec.restoreYOffsetSet {
		t.Fatalf("offset=%d want=%d pending=%t", page.exec.viewport.YOffset(), want, page.exec.restoreYOffsetSet)
	}
}

func TestLogsModeDialogWrapsAtNarrowWidths(t *testing.T) {
	page, _ := NewCommandExecutionLogs(t.Context())
	defer page.Close()
	page.openLogsModeDialog()
	for _, width := range []int{120, 80, 24} {
		view := component.Modal(page.logsModeDialogView(width), overlayWidth(width, 72))
		for _, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width=%d line=%d: %q", width, got, ansi.Strip(line))
			}
		}
	}
}

func TestLogsRuntimeAndCommandExecutionRemainTabbedParentViews(t *testing.T) {
	setupLogsPageRoot(t)
	page, _ := NewLogs(t.Context())
	defer page.Close()
	page.width, page.height = 100, 24
	if view := ansi.Strip(page.View(page.width, page.height)); !strings.Contains(view, "Runtime") || !strings.Contains(view, "Command Execution") || !strings.Contains(view, "Tool Calls") {
		t.Fatalf("runtime logs tab view=%q", view)
	}
	updated, cmd := page.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	page = updated.(*LogsPage)
	if page.tab != logsTabCommandExec || cmd == nil {
		t.Fatalf("execution tab=%d cmd=%v", page.tab, cmd)
	}
	if view := ansi.Strip(page.View(page.width, page.height)); !strings.Contains(view, "Runtime") || !strings.Contains(view, "Command Execution") || !strings.Contains(view, "Tool Calls") || !strings.Contains(view, "Waiting for command executions") {
		t.Fatalf("execution tab view=%q", view)
	}
	foundTabTarget := false
	for _, target := range page.MouseTargets(0, 0, 1) {
		if target.ID == "logs.tab" {
			foundTabTarget = true
			break
		}
	}
	if !foundTabTarget {
		t.Fatal("logs tab mouse target missing")
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	page = updated.(*LogsPage)
	if page.tab != logsTabRuntime {
		t.Fatalf("left did not return runtime tab: %d", page.tab)
	}
}

func TestLogsFormattingHelpersCoverBoundaries(t *testing.T) {
	if logEventID(runtimeevent.Event{Time: time.Unix(1, 2), Name: "event", Message: "message"}) == "" {
		t.Fatal("fallback event id empty")
	}
	if durationLabel(0) != "" || durationLabel(12) != "12ms" {
		t.Fatalf("duration labels=%q/%q", durationLabel(0), durationLabel(12))
	}
	if shortValue("abc", 0) != "" || shortValue("abc", 3) != "abc" || lipgloss.Width(shortValue("abcdef", 1)) > 1 {
		t.Fatalf("short values=%q/%q/%q", shortValue("abc", 0), shortValue("abc", 3), shortValue("abcdef", 1))
	}
	for value, want := range map[int64]string{10: "10 B", 2048: "2.0 KiB", 2 * 1024 * 1024: "2.0 MiB"} {
		if got := humanBytes(value); got != want {
			t.Fatalf("humanBytes(%d)=%q want %q", value, got, want)
		}
	}
}

func TestExecutionCommandPromptIsFlushAndOutputKeepsAlignment(t *testing.T) {
	started := time.Now().UTC()
	info := shellruntime.ExecutionInfo{ID: "exec_prompt", WorkspaceID: "ws_a", Command: "printf demo", Shell: "bash", StartedAt: started.Format(time.RFC3339Nano)}
	view := ansi.Strip(formatExecutionFeed([]shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, Execution: &info, Timestamp: info.StartedAt},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, Execution: &info, Data: "out\n", Timestamp: started.Add(time.Second).Format(time.RFC3339Nano)},
	}, 48))
	commandLine, outputLine := "", ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "$ printf demo") {
			commandLine = line
		}
		if strings.Contains(line, "out") {
			outputLine = line
		}
	}
	if !strings.HasPrefix(commandLine, "│ $ printf demo") {
		t.Fatalf("command indentation=%q", commandLine)
	}
	if !strings.HasPrefix(outputLine, "│ out") {
		t.Fatalf("output alignment=%q", outputLine)
	}
	if !strings.Contains(view, "Shell  bash") {
		t.Fatalf("shell metadata missing: %q", view)
	}
}

func setupLogsPageRoot(t *testing.T) string {
	t.Helper()
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func appendLogEvents(t *testing.T, root string, events ...runtimeevent.Event) {
	t.Helper()
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := journal.Append(event); err != nil {
			t.Fatal(err)
		}
	}
}

func writeLogsRuntimeState(t *testing.T, root, rawURL, runID string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	state := runtimecontrol.State{PID: os.Getpid(), Address: parsed.Host, Token: "token", RunID: runID, ConfigRoot: root}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runtimecontrol.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLogsPageBootstrapLoadsJournalBeforeOpeningLiveHTTP(t *testing.T) {
	root := setupLogsPageRoot(t)
	appendLogEvents(t, root, runtimeevent.Event{Sequence: 1, Time: time.Now().UTC(), RunID: "run_fast", Level: "info", Name: "journal.ready", Message: "Journal ready"})
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	writeLogsRuntimeState(t, root, server.URL, "run_fast")
	page, _ := NewLogs(t.Context())
	defer page.Close()
	bootstrap := page.Init()
	if bootstrap == nil {
		t.Fatal("bootstrap command missing")
	}
	msg := bootstrap()
	select {
	case <-requests:
		t.Fatal("bootstrap contacted live HTTP before journal load completed")
	default:
	}
	updated, connect := page.Update(msg)
	page = updated.(*LogsPage)
	if connect == nil || len(page.events) != 1 || page.events[0].Name != "journal.ready" || !page.loaded {
		t.Fatalf("journal bootstrap loaded=%t events=%#v connect=%v", page.loaded, page.events, connect)
	}
}

func TestCommandExecutionViewportReflowsLongReadableContent(t *testing.T) {
	page, err := NewCommandExecutionLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	commandToken, outputToken := strings.Repeat("c", 64), strings.Repeat("o", 80)
	info := shellruntime.ExecutionInfo{ID: "exec_long", WorkspaceID: "ws_long", Tool: "run_command", Command: "printf " + commandToken, CWD: "/very/long/workspace/" + commandToken}
	page.exec.events = []shellruntime.ExecutionFeedEvent{
		{Sequence: 1, Type: shellruntime.ExecutionEventStarted, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Execution: &info},
		{Sequence: 2, Type: shellruntime.ExecutionEventOutput, ExecutionID: info.ID, WorkspaceID: info.WorkspaceID, Stream: "stdout", Data: outputToken},
	}
	page.exec.paused = true
	for _, width := range []int{24, 11} {
		page.resizeExecutionViewport(width, 8)
		content := page.exec.viewport.GetContent()
		for _, line := range strings.Split(content, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width=%d line=%d: %q", width, got, ansi.Strip(line))
			}
		}
		flat := strings.NewReplacer("\n", "", " ", "", "│", "", "├", "", "┤", "", "╭", "", "╮", "", "╰", "", "╯", "", "─", "").Replace(ansi.Strip(content))
		if !strings.Contains(flat, commandToken) || !strings.Contains(flat, outputToken) {
			t.Fatalf("width=%d content was truncated: %q", width, flat)
		}
	}
}

func TestConfirmOverlayBodyWrapsLongDescription(t *testing.T) {
	confirm := component.NewConfirmButtons("Continue", "Cancel", true)
	description := "Remove " + strings.Repeat("nested/", 12) + "workspace"
	width := 44
	body := confirmOverlayBody(confirm, "Confirm operation", description, width)
	for _, line := range strings.Split(body, "\n") {
		if got := lipgloss.Width(line); got > component.ModalContentWidth(width) {
			t.Fatalf("confirm body line width=%d want <=%d: %q", got, component.ModalContentWidth(width), ansi.Strip(line))
		}
	}
	flat := strings.ReplaceAll(strings.ReplaceAll(ansi.Strip(body), "\n", ""), " ", "")
	if !strings.Contains(flat, strings.ReplaceAll(description, " ", "")) {
		t.Fatalf("confirm description changed: %q", ansi.Strip(body))
	}
}
