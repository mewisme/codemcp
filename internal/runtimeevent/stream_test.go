package runtimeevent

import (
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/logger"
)

func TestRecorderPersistsAndPublishesSameSequence(t *testing.T) {
	metadata := Metadata{RunID: "run_test", PID: 42}
	journal, err := NewJournal(t.TempDir(), Options{Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder(journal, metadata)
	sub := recorder.Stream.Subscribe()
	defer recorder.Stream.Unsubscribe(sub)
	event := logger.Event{Time: time.Now(), Level: logger.Info, Kind: logger.KindSuccess, Name: "server.ready", Component: "SERVER", Message: "Server ready"}
	if err := recorder.WriteEvent(event); err != nil {
		t.Fatal(err)
	}
	live := <-sub
	if live.Sequence != 1 || live.RunID != "run_test" {
		t.Fatalf("live = %#v", live)
	}
	var persisted Event
	if err := ReadFile(journal.Path(), func(event Event) error { persisted = event; return nil }); err != nil {
		t.Fatal(err)
	}
	if persisted.Sequence != live.Sequence || persisted.Name != live.Name {
		t.Fatalf("persisted=%#v live=%#v", persisted, live)
	}
}

func TestRecorderRecordAddsRuntimeMetadataToSyntheticEvent(t *testing.T) {
	metadata := Metadata{RunID: "run_session", PID: 77, Managed: true, ServiceID: "service_test", ServiceScope: "user"}
	journal, err := NewJournal(t.TempDir(), Options{Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder(journal, metadata)
	if err := recorder.Record(Event{Level: "info", Kind: "action", Name: "runtime.session.started", Component: "SESSION", Message: "Runtime session started"}); err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := ReadFile(journal.Path(), func(event Event) error { got = event; return nil }); err != nil {
		t.Fatal(err)
	}
	if got.Sequence != 1 || got.RunID != metadata.RunID || got.PID != metadata.PID || !got.Managed || got.ServiceID != metadata.ServiceID || got.ServiceScope != metadata.ServiceScope || got.Time.IsZero() {
		t.Fatalf("synthetic event = %#v", got)
	}
}

func TestStreamPublishWriteAndLatestSequence(t *testing.T) {
	stream := NewStream(Metadata{RunID: "run_stream", PID: 99})
	sub := stream.Subscribe()
	defer stream.Unsubscribe(sub)
	first := stream.Publish(Event{RunID: "run_stream", Level: "info", Name: "one", Message: "one"})
	if first.Sequence != 1 || stream.LatestSequence() != 1 {
		t.Fatalf("first=%#v latest=%d", first, stream.LatestSequence())
	}
	if live := <-sub; live.Sequence != 1 || live.Name != "one" {
		t.Fatalf("live=%#v", live)
	}
	if err := stream.WriteEvent(logger.Event{Level: logger.Info, Kind: logger.KindInfo, Name: "two", Message: "two"}); err != nil {
		t.Fatal(err)
	}
	if live := <-sub; live.Sequence != 2 || live.RunID != "run_stream" || live.PID != 99 {
		t.Fatalf("live=%#v", live)
	}
	if stream.LatestSequence() != 2 {
		t.Fatalf("latest=%d", stream.LatestSequence())
	}
}

func TestStreamReportsSubscriberOverflow(t *testing.T) {
	stream := NewStream(Metadata{})
	sub := stream.SubscribeDetailed()
	defer stream.UnsubscribeDetailed(sub)
	for index := 0; index <= defaultStreamBuffer; index++ {
		stream.Publish(Event{Name: "event"})
	}
	select {
	case overflow := <-sub.Overflow:
		if overflow.DroppedSequence != defaultStreamBuffer+1 {
			t.Fatalf("dropped sequence=%d", overflow.DroppedSequence)
		}
	default:
		t.Fatal("subscriber overflow was not reported")
	}
	stream.AcknowledgeOverflow(sub)
	stream.Publish(Event{Name: "event"})
	select {
	case overflow := <-sub.Overflow:
		if overflow.DroppedSequence != defaultStreamBuffer+2 {
			t.Fatalf("second dropped sequence=%d", overflow.DroppedSequence)
		}
	default:
		t.Fatal("subscriber overflow was not re-armed")
	}
}

func TestNilStreamOperationsAreSafe(t *testing.T) {
	var stream *Stream
	if got := stream.Publish(Event{Name: "one"}); got.Name != "one" {
		t.Fatalf("publish=%#v", got)
	}
	if err := stream.WriteEvent(logger.Event{}); err != nil {
		t.Fatal(err)
	}
	if stream.LatestSequence() != 0 {
		t.Fatal("nil stream latest sequence was non-zero")
	}
	sub := stream.Subscribe()
	if _, ok := <-sub; ok {
		t.Fatal("nil stream subscription was not closed")
	}
	stream.Unsubscribe(nil)
}
