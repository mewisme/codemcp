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
	events := make([]runtimeevent.Event, logsBufferCap+500)
	for index := range events {
		events[index] = runtimeevent.Event{Sequence: uint64(index + 1), Time: base.Add(time.Duration(index) * time.Millisecond), RunID: "run", Level: "info", Name: "event", Message: "value"}
	}
	page.mergeEvents(events)
	if len(page.events) != logsBufferCap || page.events[0].Sequence != 501 || page.events[len(page.events)-1].Sequence != uint64(logsBufferCap+500) {
		t.Fatalf("bounded events=%d first=%d last=%d", len(page.events), page.events[0].Sequence, page.events[len(page.events)-1].Sequence)
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
	want := map[string]bool{"v": false, "m": false, "f": false, "r": false, "i": false, "d": false}
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
	for _, want := range []string{"Stream", "View", "Events", "Mode"} {
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
		if strings.Contains(line, "Stream") && strings.Contains(line, "View") && strings.Contains(line, "Events") && strings.Contains(line, "Mode") {
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
	if restored.Tab != "command-execution" || restored.Options != state.Options || restored.Visibility != logger.VisibilityDebug || !restored.RuntimePaused || restored.ExecutionScope != string(executionScopeWorkspace) || restored.ExecutionWorkspaceID != "ws_exec" || !restored.ExecutionPaused {
		t.Fatalf("restored state=%#v want=%#v", restored, state)
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
