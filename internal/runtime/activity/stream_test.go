package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFindCallAndCallHandler(t *testing.T) {
	stream := NewStream()
	callID := "019a1111-2222-7333-8444-555555555555"
	stream.Publish(Event{CallID: callID, Kind: string(EventToolCall), Phase: "start", Tool: "run_command", Status: "running", Raw: map[string]any{"arguments": map[string]any{"command": "echo ok", "token": "secret-value"}}})
	stream.Publish(Event{CallID: callID, Kind: string(EventToolCall), Phase: "finish", Tool: "run_command", Status: "ok", Raw: map[string]any{"result": map[string]any{"stdout": "ok", "exit_code": 0}}})
	event, ok := stream.FindCall(callID)
	if !ok || event.Tool != "run_command" {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
	if event.Raw != nil {
		t.Fatalf("summary event retained raw payload: %#v", event.Raw)
	}
	detail, ok := stream.FindCallDetail(callID)
	if !ok || detail.Request == nil || detail.Response == nil || !detail.Diagnostic.Redacted {
		t.Fatalf("detail=%#v ok=%v", detail, ok)
	}
	recorder := httptest.NewRecorder()
	CallHandler(stream).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/activity/"+callID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var decoded ToolCallDetail
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CallID != event.CallID || decoded.Tool != event.Tool || decoded.Request == nil || decoded.Response == nil {
		t.Fatalf("decoded=%#v", decoded)
	}
	encoded := recorder.Body.String()
	if strings.Contains(encoded, "secret-value") || strings.Contains(encoded, `"raw"`) || !strings.Contains(encoded, "redacted") {
		t.Fatalf("unsafe or incomplete detail=%s", encoded)
	}
	recorder = httptest.NewRecorder()
	CallHandler(stream).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/activity/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d", recorder.Code)
	}
}

func TestToolCallDiagnosticIsBoundedAndEvictedWithLogicalHistory(t *testing.T) {
	stream := NewStream()
	stream.Publish(Event{
		CallID: "call_bounded", Kind: string(EventToolCall), Phase: "start", Tool: "read_text_file", Status: "running",
		Raw: map[string]any{"arguments": map[string]any{"path": "/tmp/value", "authorization": "Bearer private", "command": "deploy --token TOP-SECRET", "query": strings.Repeat("x", DiagnosticMaxStringBytes+512)}},
	})
	detail, ok := stream.FindCallDetail("call_bounded")
	if !ok || !detail.Diagnostic.Redacted || !detail.Diagnostic.Truncated {
		t.Fatalf("bounded detail=%#v ok=%t", detail, ok)
	}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Bearer private") || strings.Contains(string(data), "TOP-SECRET") || len(data) > DiagnosticMaxTotalBytes+8192 {
		t.Fatalf("detail bounds/redaction failed bytes=%d data=%s", len(data), data)
	}

	for index := 0; index < MaxRecentToolCalls; index++ {
		callID := fmt.Sprintf("call_fill_%04d", index)
		stream.Publish(Event{CallID: callID, Kind: string(EventToolCall), Phase: "finish", Tool: "noop", Status: "ok"})
	}
	if _, ok := stream.FindCallDetail("call_bounded"); ok {
		t.Fatal("evicted logical call retained diagnostic detail")
	}
}

func TestLogicalToolCallHistoryIsIndependentFromRawLifecycleRetention(t *testing.T) {
	stream := NewStream()
	base := time.Now().UTC()
	for index := 0; index < MaxRecentToolCalls; index++ {
		callID := fmt.Sprintf("call_%04d", index)
		for phaseIndex, phase := range []string{"start", "progress", "finish"} {
			status := "running"
			if phase == "finish" {
				status = "ok"
			}
			stream.Publish(Event{
				CallID: callID, Kind: string(EventToolCall), Phase: phase, Tool: "read_file", WorkspaceID: "ws_retention",
				Status: status, Timestamp: base.Add(time.Duration(index*3+phaseIndex) * time.Millisecond),
			})
		}
	}

	records := stream.RecentToolCalls(MaxRecentToolCalls)
	if len(records) != MaxRecentToolCalls {
		t.Fatalf("logical tool calls=%d want=%d", len(records), MaxRecentToolCalls)
	}
	if records[0].CallID != "call_0000" || records[len(records)-1].CallID != fmt.Sprintf("call_%04d", MaxRecentToolCalls-1) {
		t.Fatalf("logical tool call bounds first=%q last=%q", records[0].CallID, records[len(records)-1].CallID)
	}
	if records[0].First.Phase != "start" || records[0].Latest.Phase != "finish" {
		t.Fatalf("logical lifecycle first=%q latest=%q", records[0].First.Phase, records[0].Latest.Phase)
	}
	if latest, ok := stream.FindCall("call_0000"); !ok || latest.Phase != "finish" {
		t.Fatalf("oldest logical call lookup=%#v ok=%t", latest, ok)
	}

	sub, raw, _ := stream.SubscribeToolCallsSnapshot(MaxRecentEvents)
	defer stream.UnsubscribeDetailed(sub)
	if len(raw) != MaxRecentEvents {
		t.Fatalf("raw tool call lifecycle events=%d want=%d", len(raw), MaxRecentEvents)
	}
	for _, event := range raw {
		if event.CallID == "call_0000" {
			t.Fatal("oldest logical call unexpectedly survived raw lifecycle feed; test no longer proves independent retention")
		}
	}
	stream.Publish(Event{CallID: "call_extra", Kind: string(EventToolCall), Phase: "start", Tool: "read_file", Status: "running", Timestamp: base.Add(4 * time.Second)})
	stream.Publish(Event{CallID: "call_extra", Kind: string(EventToolCall), Phase: "finish", Tool: "read_file", Status: "ok", Timestamp: base.Add(4*time.Second + time.Millisecond)})
	if _, ok := stream.FindCall("call_0000"); ok {
		t.Fatal("oldest logical call was not evicted after capacity+1")
	}
	if latest, ok := stream.FindCall("call_0001"); !ok || latest.Phase != "finish" {
		t.Fatalf("next stable logical call was evicted too early: %#v ok=%t", latest, ok)
	}
}

func TestStreamRecentIsBoundedAndOrdered(t *testing.T) {
	stream := NewStream()
	stream.maxRecent = 3
	for _, message := range []string{"a", "b", "c", "d"} {
		stream.Publish(Event{Kind: string(EventSystem), Message: message})
	}
	recent := stream.Recent(10)
	if len(recent) != 3 || recent[0].Message != "b" || recent[2].Message != "d" {
		t.Fatalf("recent = %#v", recent)
	}
	if recent[0].Sequence != 2 || recent[1].Sequence != 3 || recent[2].Sequence != 4 || stream.LatestSequence() != 4 {
		t.Fatalf("sequences = %#v latest=%d", recent, stream.LatestSequence())
	}
	for _, event := range recent {
		if event.Timestamp.IsZero() || event.Timestamp.Location() != time.UTC {
			t.Fatalf("timestamp not normalized: %#v", event.Timestamp)
		}
	}
}

func TestSlowSubscriberReceivesOverflowSignal(t *testing.T) {
	stream := NewStream()
	sub, _ := stream.SubscribeDetailed(0)
	defer stream.UnsubscribeDetailed(sub)
	for index := 0; index <= defaultSubscriberBuffer; index++ {
		stream.Publish(Event{Kind: string(EventSystem), Message: "event"})
	}
	select {
	case overflow := <-sub.Overflow:
		if overflow.DroppedSequence != defaultSubscriberBuffer+1 {
			t.Fatalf("dropped sequence = %d", overflow.DroppedSequence)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for overflow signal")
	}
	before := len(sub.Events)
	stream.Publish(Event{Kind: string(EventSystem), Message: "ignored after overflow"})
	if len(sub.Events) != before {
		t.Fatalf("overflowed subscriber kept receiving events: before=%d after=%d", before, len(sub.Events))
	}
}

func TestSSEEmitsReadyHeartbeatAndEventIDs(t *testing.T) {
	stream := NewStream()
	stream.Publish(Event{Kind: string(EventSystem), Message: "before"})
	ctx, cancel := context.WithCancel(context.Background())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/?history=10", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		handlerWithHeartbeat(stream, 5*time.Millisecond).ServeHTTP(recorder, request)
		close(done)
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop after cancellation")
	}
	body := recorder.Body.String()
	for _, expected := range []string{"id: 1\n", "event: activity\n", "event: ready\n", "event: heartbeat\n", `"latest_sequence":1`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("SSE body missing %q: %q", expected, body)
		}
	}
}

func TestSubscribeWithRecentDoesNotReplayFutureEvent(t *testing.T) {
	stream := NewStream()
	stream.Publish(Event{Message: "before"})
	ch, recent := stream.SubscribeWithRecent(10)
	defer stream.Unsubscribe(ch)
	if len(recent) != 1 || recent[0].Message != "before" {
		t.Fatalf("recent = %#v", recent)
	}
	stream.Publish(Event{Message: "after"})
	select {
	case event := <-ch:
		if event.Message != "after" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestHistoryLimit(t *testing.T) {
	for raw, want := range map[string]int{"": 100, "0": 0, "10": 10, "999": 999, "bad": 100, "-1": 100} {
		request := httptest.NewRequest("GET", "/?history="+raw, nil)
		if got := historyLimit(request); got != want {
			t.Fatalf("history=%q: got %d want %d", raw, got, want)
		}
	}
}

func TestToolCallHistoryIsBoundedIndependentlyFromGeneralActivity(t *testing.T) {
	stream := NewStream()
	for index := 0; index < MaxRecentToolCalls+17; index++ {
		stream.Publish(Event{Kind: string(EventSystem), Message: "system"})
		stream.Publish(Event{CallID: fmt.Sprintf("call_%04d", index), Kind: string(EventToolCall), Tool: "run_command"})
	}
	for range MaxRecentEvents * 2 {
		stream.Publish(Event{Kind: string(EventSystem), Message: "system burst"})
	}
	sub, recent := stream.SubscribeToolCallsDetailed(MaxRecentToolCalls)
	defer stream.UnsubscribeDetailed(sub)
	if len(recent) != MaxRecentToolCalls {
		t.Fatalf("tool history len=%d want %d", len(recent), MaxRecentToolCalls)
	}
	if recent[0].CallID != "call_0017" || recent[len(recent)-1].CallID != fmt.Sprintf("call_%04d", MaxRecentToolCalls+16) {
		t.Fatalf("tool history bounds=%s..%s", recent[0].CallID, recent[len(recent)-1].CallID)
	}
	if len(stream.Recent(MaxRecentEvents)) != MaxRecentEvents {
		t.Fatalf("general history len=%d want %d", len(stream.Recent(MaxRecentEvents)), MaxRecentEvents)
	}
}

func TestToolCallSubscriberIgnoresUnrelatedActivity(t *testing.T) {
	stream := NewStream()
	sub, _ := stream.SubscribeToolCallsDetailed(0)
	defer stream.UnsubscribeDetailed(sub)
	for range defaultSubscriberBuffer + 5 {
		stream.Publish(Event{Kind: string(EventSystem), Message: "system"})
	}
	select {
	case event := <-sub.Events:
		t.Fatalf("tool subscriber received unrelated event: %#v", event)
	default:
	}
	select {
	case overflow := <-sub.Overflow:
		t.Fatalf("tool subscriber overflowed from unrelated activity: %#v", overflow)
	default:
	}
	stream.Publish(Event{CallID: "call_1", Kind: string(EventToolCall), Tool: "run_command"})
	select {
	case event := <-sub.Events:
		if event.CallID != "call_1" || event.Tool != "run_command" {
			t.Fatalf("tool event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool call event")
	}
}

func TestToolCallSnapshotWatermarkPrecedesBufferedLiveEvents(t *testing.T) {
	stream := NewStream()
	stream.Publish(Event{Kind: string(EventSystem), Message: "system"})
	stream.Publish(Event{CallID: "call_history", Kind: string(EventToolCall), Tool: "run_command"})
	sub, recent, latestSequence := stream.SubscribeToolCallsSnapshot(MaxRecentToolCalls)
	defer stream.UnsubscribeDetailed(sub)
	if latestSequence != 2 || len(recent) != 1 || recent[0].Sequence != 2 {
		t.Fatalf("snapshot latest=%d recent=%#v", latestSequence, recent)
	}
	stream.Publish(Event{CallID: "call_live", Kind: string(EventToolCall), Tool: "run_command"})
	select {
	case event := <-sub.Events:
		if event.Sequence != 3 || event.Sequence <= latestSequence || event.CallID != "call_live" {
			t.Fatalf("live event=%#v snapshot latest=%d", event, latestSequence)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for buffered live tool call")
	}
}

func TestActivitySnapshotBarrierPrecedesBufferedLiveEvent(t *testing.T) {
	stream := NewStream()
	stream.Publish(Event{Kind: string(EventBackground), Message: "before"})
	sub, snapshot := stream.SubscribeSnapshot(10)
	defer stream.UnsubscribeDetailed(sub)
	if snapshot.LatestSequence != 1 || len(snapshot.Events) != 1 || snapshot.Events[0].Sequence != 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	stream.Publish(Event{Kind: string(EventBackground), Message: "after"})
	select {
	case event := <-sub.Events:
		if event.Sequence != 2 || event.Sequence <= snapshot.LatestSequence || event.Message != "after" {
			t.Fatalf("live event=%#v barrier=%d", event, snapshot.LatestSequence)
		}
	case <-time.After(time.Second):
		t.Fatal("activity event after snapshot barrier was missed")
	}
}
