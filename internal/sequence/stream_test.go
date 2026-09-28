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
	if overflow.DroppedSequence != 2 || overflow.LatestSequence != 2 || !overflow.RequiresResync() {
		t.Fatalf("overflow=%#v", overflow)
	}
	if sub.Dropped() != 2 {
		t.Fatalf("dropped=%d want=2", sub.Dropped())
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

func TestSubscriptionCloseDisposesOnlyThatConsumer(t *testing.T) {
	stream := New[testEvent](2, 1, func(value *testEvent, sequence uint64) { value.Sequence = sequence })
	first, snapshot := stream.Subscribe(nil, 1)
	second, _ := stream.Subscribe(nil, 0)
	if snapshot.Cursor() != 0 || stream.SubscriberCount() != 2 {
		t.Fatalf("snapshot=%#v subscribers=%d", snapshot, stream.SubscriberCount())
	}
	first.Close()
	first.Close()
	if stream.SubscriberCount() != 1 {
		t.Fatalf("subscribers=%d want=1", stream.SubscriberCount())
	}
	if _, ok := <-first.Events; ok {
		t.Fatal("disposed subscription remained open")
	}
	stream.Publish(testEvent{Value: "live"})
	if event := <-second.Events; event.Sequence != 1 || event.Value != "live" {
		t.Fatalf("second subscriber event=%#v", event)
	}
	second.Close()
}

func TestStreamCloseDisposesSubscribersAndRejectsNewLiveAttachment(t *testing.T) {
	stream := New[testEvent](2, 1, func(value *testEvent, sequence uint64) { value.Sequence = sequence })
	stream.Publish(testEvent{Value: "before"})
	sub, _ := stream.Subscribe(nil, 0)
	stream.Close()
	stream.Close()
	if stream.SubscriberCount() != 0 {
		t.Fatalf("subscribers=%d", stream.SubscriberCount())
	}
	if _, ok := <-sub.Events; ok {
		t.Fatal("subscriber remained open after stream close")
	}
	stream.Publish(testEvent{Value: "after"})
	if stream.LatestSequence() != 1 {
		t.Fatalf("closed stream advanced sequence=%d", stream.LatestSequence())
	}
	closed, snapshot := stream.Subscribe(nil, 2)
	if snapshot.LatestSequence != 1 || len(snapshot.Events) != 1 || snapshot.Events[0].Value != "before" {
		t.Fatalf("closed stream snapshot=%#v", snapshot)
	}
	if _, ok := <-closed.Events; ok {
		t.Fatal("post-close subscription remained open")
	}
}

func TestSubscribeSnapshotBarrierHasNoGapOrDuplicateDuringConcurrentPublish(t *testing.T) {
	stream := New[testEvent](256, 256, func(value *testEvent, sequence uint64) { value.Sequence = sequence })
	for index := 0; index < 50; index++ {
		stream.Publish(testEvent{Value: "before"})
	}
	start := make(chan struct{})
	done := make(chan struct{})
	go func() {
		<-start
		for index := 50; index < 200; index++ {
			stream.Publish(testEvent{Value: "concurrent"})
		}
		close(done)
	}()
	close(start)
	sub, snapshot := stream.Subscribe(nil, 256)
	defer sub.Close()
	<-done

	seen := make(map[uint64]int, 200)
	for _, event := range snapshot.Events {
		seen[event.Sequence]++
	}
	for len(sub.Events) > 0 {
		event := <-sub.Events
		seen[event.Sequence]++
	}
	select {
	case overflow := <-sub.Overflow:
		t.Fatalf("barrier attachment overflowed: %#v", overflow)
	default:
	}
	if len(seen) != 200 {
		t.Fatalf("observed %d sequences, want 200; snapshot cursor=%d", len(seen), snapshot.Cursor())
	}
	for sequence := uint64(1); sequence <= 200; sequence++ {
		if seen[sequence] != 1 {
			t.Fatalf("sequence %d observed %d times; snapshot cursor=%d", sequence, seen[sequence], snapshot.Cursor())
		}
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
