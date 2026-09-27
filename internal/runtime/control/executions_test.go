package control

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestExecutionFeedStreamReplaysCombinedEventsAndContinues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/executions/stream" || r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatalf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"events\":[{\"sequence\":1,\"type\":\"started\",\"execution_id\":\"exec_1\",\"workspace_id\":\"ws_a\",\"execution\":{\"id\":\"exec_1\",\"workspace_id\":\"ws_a\",\"tool\":\"run_command\",\"command\":\"printf test\",\"cwd\":\"/tmp\",\"started_at\":\"2026-09-06T00:00:00Z\",\"status\":\"running\"},\"timestamp\":\"2026-09-06T00:00:00Z\"}],\"executions\":[{\"id\":\"exec_history\",\"workspace_id\":\"ws_a\",\"tool\":\"run_command\",\"command\":\"echo history\",\"status\":\"success\"}],\"latest_sequence\":1}\n\nevent: heartbeat\ndata: {\"latest_sequence\":1}\n\nid: 2\nevent: output\ndata: {\"sequence\":2,\"type\":\"output\",\"execution_id\":\"exec_1\",\"workspace_id\":\"ws_a\",\"stream\":\"stderr\",\"data\":\"err\\n\",\"timestamp\":\"2026-09-06T00:00:01Z\"}\n\n")
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, state, err := OpenExecutionFeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	snapshot := stream.Snapshot()
	if state.PID <= 0 || snapshot.LatestSequence != 1 || len(snapshot.Events) != 1 || snapshot.Events[0].ExecutionID != "exec_1" || len(snapshot.Executions) != 1 || snapshot.Executions[0].ID != "exec_history" {
		t.Fatalf("state=%#v snapshot=%#v", state, snapshot)
	}
	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 2 || event.Type != shellruntime.ExecutionEventOutput || event.Stream != "stderr" || event.Data != "err\n" {
		t.Fatalf("event=%#v", event)
	}
}

func TestExecutionFeedStreamReplaysFramedEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":1,\"replay_count\":1}\n\nid: 1\nevent: started\ndata: {\"sequence\":1,\"type\":\"started\",\"execution_id\":\"exec_replay\"}\n\nid: 2\nevent: output\ndata: {\"sequence\":2,\"type\":\"output\",\"execution_id\":\"exec_replay\",\"data\":\"live\"}\n\n")
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, _, err := OpenExecutionFeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	snapshot := stream.Snapshot()
	if snapshot.LatestSequence != 1 || len(snapshot.Events) != 1 || snapshot.Events[0].ExecutionID != "exec_replay" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	event, err := stream.Next()
	if err != nil || event.Sequence != 2 || event.Data != "live" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

func TestExecutionFeedStreamAcceptsLargeReplayFrame(t *testing.T) {
	data := strings.Repeat("x", 5*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: ready\ndata: {\"latest_sequence\":1,\"replay_count\":1}\n\nid: 1\nevent: output\ndata: {\"sequence\":1,\"type\":\"output\",\"execution_id\":\"exec_large\",\"data\":%q}\n\n", data)
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, _, err := OpenExecutionFeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	snapshot := stream.Snapshot()
	if len(snapshot.Events) != 1 || snapshot.Events[0].Data != data {
		t.Fatalf("large snapshot events=%d data_bytes=%d", len(snapshot.Events), len(snapshot.Events[0].Data))
	}
}

func TestExecutionFeedStreamReportsUnsupportedRunningServer(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, state, err := OpenExecutionFeed(t.Context())
	if stream != nil || state.PID <= 0 || !errors.Is(err, ErrExecutionFeedUnsupported) {
		t.Fatalf("stream=%v state=%#v err=%v", stream, state, err)
	}
}

func TestExecutionFeedStreamReportsOverflowAndPreservesStateOnFailure(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: ready\ndata: {\"events\":[],\"latest_sequence\":4}\n\nevent: overflow\ndata: {\"dropped_sequence\":5}\n\n")
		}))
		defer server.Close()
		root := setupRuntimeControlRoot(t)
		writeRuntimeControlState(t, root, server.URL, "runtime-secret")
		stream, _, err := OpenExecutionFeed(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		if _, err := stream.Next(); !errors.Is(err, ErrExecutionFeedOverflow) {
			t.Fatalf("overflow err=%v", err)
		}
	})
	t.Run("http", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "offline")
		}))
		defer server.Close()
		root := setupRuntimeControlRoot(t)
		writeRuntimeControlState(t, root, server.URL, "runtime-secret")
		stream, state, err := OpenExecutionFeed(t.Context())
		if err == nil || stream != nil || state.PID <= 0 || !strings.Contains(err.Error(), "HTTP 503") {
			t.Fatalf("stream=%v state=%#v err=%v", stream, state, err)
		}
	})
}
