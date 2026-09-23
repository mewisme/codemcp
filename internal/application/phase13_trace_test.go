package application

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtimecontrol"
	"go.mewis.me/codemcp/internal/runtimeevent"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type phase13TraceCollector struct {
	mu     sync.Mutex
	events []tracepkg.Event
}

func (c *phase13TraceCollector) Observe(event tracepkg.Event) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

func (c *phase13TraceCollector) Snapshot() []tracepkg.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tracepkg.Event(nil), c.events...)
}

func TestLogsTraceReportsJournalQuerySessionAndTruncation(t *testing.T) {
	root := setupLogsRoot(t)
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index := range 4 {
		if err := journal.Append(runtimeevent.Event{Sequence: uint64(index + 1), Time: now.Add(time.Duration(index) * time.Second), RunID: "run_trace_logs", Level: "info", Component: "SERVER", Name: fmt.Sprintf("event.%d", index), Message: "value"}); err != nil {
			t.Fatal(err)
		}
	}
	collector := &phase13TraceCollector{}
	ctx := tracepkg.WithObserver(t.Context(), collector.Observe)
	snapshot, err := LoadLogsContext(ctx, LogsQueryOptions{Tail: 2}, logger.VisibilityDefault, 0, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 2 || snapshot.Session != "run_trace_logs" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	events := collector.Snapshot()
	if !phase13TraceHasFields(events, "logs.snapshot.load.completed", map[string]any{"events_scanned": 4, "events_matched": 4, "events_returned": 2, "selected_session": "run_trace_logs", "tail_truncated": true, "buffer_truncated": false}) {
		t.Fatalf("missing logs snapshot trace: %#v", events)
	}
	if !phase13TraceHasFields(events, "logs.journal.inspect.completed", map[string]any{"file_count": 1}) {
		t.Fatalf("missing journal info trace: %#v", events)
	}
}

func TestClearLogsTraceReportsLocalFallback(t *testing.T) {
	root := setupLogsRoot(t)
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Time: time.Now().UTC(), RunID: "run_clear_trace", Level: "info", Name: "test", Message: "value"}); err != nil {
		t.Fatal(err)
	}
	collector := &phase13TraceCollector{}
	ctx := tracepkg.WithObserver(t.Context(), collector.Observe)
	if err := ClearLogs(ctx); err != nil {
		t.Fatal(err)
	}
	events := collector.Snapshot()
	if !phase13TraceHasFields(events, "logs.clear.fallback", map[string]any{"reason": "runtime_unavailable", "mode": "local"}) || !phase13TraceHasFields(events, "logs.clear.completed", map[string]any{"mode": "local", "fallback": true}) {
		t.Fatalf("missing local clear fallback trace: %#v", events)
	}
}

func TestApprovalRequestTraceIncludesDomainFactsWithoutReasonLeak(t *testing.T) {
	root := setupLogsRoot(t)
	const secretReason = "private-reason-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/requests/approve" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input["reason"] != secretReason {
			t.Fatalf("reason=%v", input["reason"])
		}
		_ = json.NewEncoder(w).Encode(approval.Request{ID: "req_trace_123", Status: approval.StatusApproved, ResolvedBy: "cli", WorkspaceID: "ws_trace", TargetTool: "run_command"})
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	writeRuntimeState(t, root, runtimecontrol.State{PID: os.Getpid(), Address: parsed.Host, Token: "token", ConfigRoot: root})
	collector := &phase13TraceCollector{}
	ctx := tracepkg.WithObserver(t.Context(), collector.Observe)
	request, err := ResolveApprovalRequest(ctx, "req_trace", true, secretReason)
	if err != nil {
		t.Fatal(err)
	}
	if request.ID != "req_trace_123" || request.Status != approval.StatusApproved {
		t.Fatalf("request=%#v", request)
	}
	events := collector.Snapshot()
	if !phase13TraceHasFields(events, "request.resolve.completed", map[string]any{"request": "req_trace", "request_id": "req_trace_123", "action": "approve", "status": string(approval.StatusApproved)}) {
		t.Fatalf("missing request resolve trace: %#v", events)
	}
	for _, name := range []string{"runtime.control.request.completed", "http.request.completed"} {
		if _, ok := phase13TraceEvent(events, name); !ok {
			t.Fatalf("missing %s: %#v", name, events)
		}
	}
	if strings.Contains(fmt.Sprintf("%#v", events), secretReason) {
		t.Fatalf("approval reason leaked into trace: %#v", events)
	}
}

func phase13TraceEvent(events []tracepkg.Event, name string) (tracepkg.Event, bool) {
	for _, event := range events {
		if event.Name == name {
			return event, true
		}
	}
	return tracepkg.Event{}, false
}

func phase13TraceHasFields(events []tracepkg.Event, name string, expected map[string]any) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		matched := true
		for key, want := range expected {
			got, ok := phase13TraceField(event, key)
			if !ok || fmt.Sprint(got) != fmt.Sprint(want) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func phase13TraceField(event tracepkg.Event, key string) (any, bool) {
	for _, field := range event.Fields {
		if field.Key == key {
			return field.Value, true
		}
	}
	return nil, false
}
