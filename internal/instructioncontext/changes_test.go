package instructioncontext

import "testing"

func TestChangeStreamSnapshotBoundaryAndOverflow(t *testing.T) {
	stream := newChangeStream(2, 1)
	first := stream.Publish(Change{Kind: "rule", Scope: "workspace", WorkspaceID: "ws_one", Name: "one", Operation: "create"})
	if first.Sequence != 1 {
		t.Fatalf("first change=%#v", first)
	}
	subscription, snapshot := stream.Subscribe(1)
	defer stream.Unsubscribe(subscription)
	if snapshot.LatestSequence != 1 || len(snapshot.Events) != 1 || snapshot.Events[0].Sequence != 1 {
		t.Fatalf("snapshot boundary=%#v", snapshot)
	}

	stream.Publish(Change{Kind: "skill", Scope: "workspace", WorkspaceID: "ws_one", Name: "two", Operation: "create"})
	stream.Publish(Change{Kind: "skill", Scope: "workspace", WorkspaceID: "ws_one", Name: "three", Operation: "update"})
	overflow := <-subscription.Overflow
	if overflow.DroppedSequence != 3 {
		t.Fatalf("overflow=%#v", overflow)
	}
	select {
	case extra := <-subscription.Overflow:
		t.Fatalf("overflow repeated before acknowledgement=%#v", extra)
	default:
	}
	stream.AcknowledgeOverflow(subscription)
	if event := <-subscription.Events; event.Sequence != 2 {
		t.Fatalf("buffered change=%#v", event)
	}
	stream.Publish(Change{Kind: "rule", Scope: "workspace", WorkspaceID: "ws_one", Name: "four", Operation: "update"})
	if event := <-subscription.Events; event.Sequence != 4 {
		t.Fatalf("post-resync change=%#v", event)
	}
}
