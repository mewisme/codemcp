package sequence

import "testing"

type testEvent struct {
	Sequence uint64
	Kind     string
	Value    string
}

func TestStreamSnapshotFilteringAndMonotonicSequence(t *testing.T) {
	stream := New[testEvent](3, 2, func(value *testEvent, sequence uint64) { value.Sequence = sequence })
	stream.Publish(testEvent{Kind: "a", Value: "one"})
	stream.Publish(testEvent{Kind: "b", Value: "two"})
	stream.Publish(testEvent{Kind: "a", Value: "three"})
	stream.Publish(testEvent{Kind: "a", Value: "four"})

	sub, snapshot := stream.Subscribe(func(value testEvent) bool { return value.Kind == "a" }, 2)
	defer stream.Unsubscribe(sub)
	if snapshot.LatestSequence != 4 || len(snapshot.Events) != 2 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Events[0].Value != "three" || snapshot.Events[0].Sequence != 3 || snapshot.Events[1].Value != "four" || snapshot.Events[1].Sequence != 4 {
		t.Fatalf("filtered snapshot=%#v", snapshot.Events)
	}
	published := stream.Publish(testEvent{Kind: "a", Value: "five"})
	if published.Sequence != 5 || stream.LatestSequence() != 5 {
		t.Fatalf("published=%#v latest=%d", published, stream.LatestSequence())
	}
	if event := <-sub.Events; event.Sequence != 5 || event.Value != "five" {
		t.Fatalf("subscriber event=%#v", event)
	}
}

func TestStreamBoundsRecentAndSignalsOverflowOnce(t *testing.T) {
	stream := New[testEvent](2, 1, func(value *testEvent, sequence uint64) { value.Sequence = sequence })
	sub, _ := stream.Subscribe(nil, 0)
	defer stream.Unsubscribe(sub)

	stream.Publish(testEvent{Value: "one"})
	stream.Publish(testEvent{Value: "two"})
	stream.Publish(testEvent{Value: "three"})

	overflow := <-sub.Overflow
	if overflow.DroppedSequence != 2 {
		t.Fatalf("overflow=%#v", overflow)
	}
	select {
	case extra := <-sub.Overflow:
		t.Fatalf("unexpected repeated overflow=%#v", extra)
	default:
	}
	recent := stream.Recent(10)
	if len(recent) != 2 || recent[0].Sequence != 2 || recent[1].Sequence != 3 {
		t.Fatalf("recent=%#v", recent)
	}

	stream.AcknowledgeOverflow(sub)
	<-sub.Events
	stream.Publish(testEvent{Value: "four"})
	if event := <-sub.Events; event.Sequence != 4 {
		t.Fatalf("event=%#v", event)
	}
}

func TestNilStreamSubscriptionIsClosed(t *testing.T) {
	var stream *Stream[testEvent]
	sub, snapshot := stream.Subscribe(nil, 10)
	if snapshot.LatestSequence != 0 || len(snapshot.Events) != 0 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if _, ok := <-sub.Events; ok {
		t.Fatal("nil stream events channel is open")
	}
	if _, ok := <-sub.Overflow; ok {
		t.Fatal("nil stream overflow channel is open")
	}
}

func TestStreamEnsureSequenceSeedsDurableContinuation(t *testing.T) {
	stream := New[testEvent](2, 1, func(value *testEvent, sequence uint64) { value.Sequence = sequence })
	stream.EnsureSequence(41)
	published := stream.Publish(testEvent{Value: "next"})
	if published.Sequence != 42 || stream.LatestSequence() != 42 {
		t.Fatalf("published=%#v latest=%d", published, stream.LatestSequence())
	}
	stream.EnsureSequence(7)
	if stream.LatestSequence() != 42 {
		t.Fatalf("sequence regressed to %d", stream.LatestSequence())
	}
}
