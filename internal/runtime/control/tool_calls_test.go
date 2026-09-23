package control

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestToolCallFeedReplaysAndContinues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tool-calls/stream" || r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatalf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":4,\"replay_count\":2}\n\nid: 1\nevent: tool_call\ndata: {\"sequence\":1,\"call_id\":\"call_1\",\"kind\":\"tool_call\",\"phase\":\"start\",\"tool\":\"run_command\",\"status\":\"running\",\"timestamp\":\"2026-09-13T00:00:00Z\"}\n\nid: 3\nevent: tool_call\ndata: {\"sequence\":3,\"call_id\":\"call_1\",\"kind\":\"tool_call\",\"phase\":\"finish\",\"tool\":\"run_command\",\"status\":\"ok\",\"timestamp\":\"2026-09-13T00:00:01Z\"}\n\nevent: heartbeat\ndata: {\"latest_sequence\":4}\n\nid: 5\nevent: tool_call\ndata: {\"sequence\":5,\"call_id\":\"call_2\",\"kind\":\"tool_call\",\"phase\":\"start\",\"tool\":\"read_text_file\",\"status\":\"running\",\"timestamp\":\"2026-09-13T00:00:02Z\"}\n\n")
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, state, err := OpenToolCallFeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	snapshot := stream.Snapshot()
	if state.PID <= 0 || snapshot.LatestSequence != 4 || len(snapshot.Events) != 2 || snapshot.Events[0].Phase != "start" || snapshot.Events[1].Phase != "finish" {
		t.Fatalf("state=%#v snapshot=%#v", state, snapshot)
	}
	event, err := stream.Next()
	if err != nil || event.Sequence != 5 || event.CallID != "call_2" || event.Tool != "read_text_file" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

func TestToolCallFeedReportsOverflowAndUnsupported(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":7,\"replay_count\":0}\n\nevent: overflow\ndata: {\"dropped_sequence\":8}\n\n")
		}))
		defer server.Close()
		root := setupRuntimeControlRoot(t)
		writeRuntimeControlState(t, root, server.URL, "runtime-secret")
		stream, _, err := OpenToolCallFeed(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		if _, err := stream.Next(); !errors.Is(err, ErrToolCallFeedOverflow) {
			t.Fatalf("overflow err=%v", err)
		}
	})
	t.Run("unsupported", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		defer server.Close()
		root := setupRuntimeControlRoot(t)
		writeRuntimeControlState(t, root, server.URL, "runtime-secret")
		stream, state, err := OpenToolCallFeed(t.Context())
		if stream != nil || state.PID <= 0 || !errors.Is(err, ErrToolCallFeedUnsupported) {
			t.Fatalf("stream=%v state=%#v err=%v", stream, state, err)
		}
	})
}

func TestExecutionListAndDetailClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatalf("auth=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/executions":
			_, _ = fmt.Fprint(w, `[{"id":"exec_1","workspace_id":"ws_a","tool":"run_command","command":"echo hi","shell":"bash","status":"success"}]`)
		case "/executions/exec_1":
			_, _ = fmt.Fprint(w, `{"execution":{"id":"exec_1","workspace_id":"ws_a","tool":"run_command","command":"echo hi","shell":"bash","status":"success"},"stdout":"hi\n","stderr":""}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	items, err := ListExecutions(t.Context())
	if err != nil || len(items) != 1 || items[0].Shell != "bash" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	snapshot, err := GetExecution(t.Context(), "exec_1")
	if err != nil || snapshot.Execution.ID != "exec_1" || snapshot.Stdout != "hi\n" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}
