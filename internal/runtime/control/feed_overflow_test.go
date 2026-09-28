package control

import (
	"errors"
	"testing"
)

func TestFeedOverflowErrorPreservesCauseAndResyncCursor(t *testing.T) {
	err := decodeFeedOverflow(ErrExecutionFeedOverflow, `{"dropped_sequence":41,"latest_sequence":57}`)
	if !errors.Is(err, ErrExecutionFeedOverflow) {
		t.Fatalf("overflow cause lost: %v", err)
	}
	overflow, ok := FeedOverflowOf(err)
	if !ok || overflow.DroppedSequence != 41 || overflow.LatestSequence != 57 || !overflow.RequiresResync() {
		t.Fatalf("overflow=%#v ok=%t err=%v", overflow, ok, err)
	}
}

func TestFeedOverflowErrorRejectsMalformedTransportPayload(t *testing.T) {
	err := decodeFeedOverflow(ErrToolCallFeedOverflow, `{"dropped_sequence":`)
	if err == nil || errors.Is(err, ErrToolCallFeedOverflow) {
		t.Fatalf("malformed overflow payload was accepted: %v", err)
	}
	if _, ok := FeedOverflowOf(err); ok {
		t.Fatalf("malformed overflow exposed resync cursor: %v", err)
	}
}
