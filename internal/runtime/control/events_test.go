package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventStreamSkipsControlFramesAndDecodesRuntimeEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" || r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatalf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":1}\n\nevent: heartbeat\ndata: {}\n\nid: 2\nevent: runtime\ndata: {\"sequence\":2,\"time\":\"2026-09-06T00:00:00Z\",\"level\":\"info\",\"kind\":\"success\",\"event\":\"server.ready\",\"message\":\"Ready\"}\n\n")
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, state, err := OpenEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if state.PID <= 0 {
		t.Fatalf("state=%#v", state)
	}
	if stream.LatestSequence() != 1 {
		t.Fatalf("ready latest sequence=%d", stream.LatestSequence())
	}
	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 2 || event.Name != "server.ready" || event.Message != "Ready" {
		t.Fatalf("event=%#v", event)
	}
}

func TestEventStreamStopsOnContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	ctx, cancel := context.WithCancel(t.Context())
	stream, _, err := OpenEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	<-started
	done := make(chan error, 1)
	go func() { _, err := stream.Next(); done <- err }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event stream did not stop after cancellation")
	}
}

func TestOpenEventsReturnsLoadedStateWhenStreamEndpointIsUnavailable(t *testing.T) {
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, "http://127.0.0.1:1", "runtime-secret")
	stream, state, err := OpenEvents(t.Context())
	if err == nil || stream != nil {
		t.Fatalf("stream=%v err=%v", stream, err)
	}
	if state.PID <= 0 || state.Token != "runtime-secret" || state.Address == "" {
		t.Fatalf("state lost on stream failure: %#v", state)
	}
}

func TestOpenEventsReturnsLoadedStateOnHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, "temporarily unavailable")
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, state, err := OpenEvents(t.Context())
	if err == nil || stream != nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("stream=%v state=%#v err=%v", stream, state, err)
	}
	if state.PID <= 0 || state.Token != "runtime-secret" {
		t.Fatalf("state lost on HTTP failure: %#v", state)
	}
}

func TestOpenEventsValidatesReadyFrame(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "malformed", body: "event: ready\ndata: {broken}\n\n", want: "decode runtime event stream ready frame"},
		{name: "missing", body: "event: heartbeat\ndata: {}\n\n", want: "EOF"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			root := setupRuntimeControlRoot(t)
			writeRuntimeControlState(t, root, server.URL, "runtime-secret")
			stream, state, err := OpenEvents(t.Context())
			if err == nil || stream != nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("stream=%v state=%#v err=%v", stream, state, err)
			}
			if state.PID <= 0 {
				t.Fatalf("state lost on ready failure: %#v", state)
			}
		})
	}
}

func TestEventStreamReportsMalformedRuntimeFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":0}\n\nevent: runtime\ndata: {broken}\n\n")
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, _, err := OpenEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "decode runtime event") {
		t.Fatalf("err=%v", err)
	}
}

func TestEventStreamReportsGapFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {\"latest_sequence\":1}\n\nevent: gap\ndata: {\"dropped_sequence\":2,\"latest_sequence\":70}\n\n")
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	stream, _, err := OpenEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(); !errors.Is(err, ErrEventStreamGap) {
		t.Fatalf("gap err=%v", err)
	}
}

func TestEventStreamNilSafety(t *testing.T) {
	var stream *EventStream
	if stream.LatestSequence() != 0 {
		t.Fatal("nil stream returned non-zero latest sequence")
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("nil stream next err=%v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("nil stream close err=%v", err)
	}
}
