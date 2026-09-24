package approval

import (
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/sequence"
)

const (
	EventRequested = "approval.requested"
	EventApproved  = "approval.approved"
	EventDenied    = "approval.denied"
	EventExpired   = "approval.expired"
	EventCancelled = "approval.cancelled"
	EventConsumed  = "approval.consumed"
	EventMismatch  = "approval.mismatch"
)

type Event struct {
	Sequence    uint64    `json:"sequence,omitempty"`
	Name        string    `json:"name"`
	RequestID   string    `json:"request_id"`
	WorkspaceID string    `json:"workspace_id"`
	SessionHash string    `json:"session_hash,omitempty"`
	Source      string    `json:"source,omitempty"`
	TargetTool  string    `json:"target_tool"`
	Title       string    `json:"title"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	RetryUntil  time.Time `json:"retry_until,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

type EventObserver func(Event)

type EventOverflow = sequence.Overflow
type EventSubscription = sequence.Subscription[Event]

type EventStream struct {
	stream *sequence.Stream[Event]
}

func newEventStream() *EventStream {
	return &EventStream{stream: sequence.New[Event](64, 16, func(event *Event, value uint64) {
		event.Sequence = value
	})}
}

func (s *EventStream) Subscribe() *EventSubscription {
	return s.SubscribeWorkspace("")
}

func (s *EventStream) SubscribeWorkspace(workspaceID string) *EventSubscription {
	if s == nil || s.stream == nil {
		var stream *sequence.Stream[Event]
		sub, _ := stream.Subscribe(nil, 0)
		return sub
	}
	workspaceID = strings.TrimSpace(workspaceID)
	var filter sequence.Predicate[Event]
	if workspaceID != "" {
		filter = func(event Event) bool { return event.WorkspaceID == workspaceID }
	}
	sub, _ := s.stream.Subscribe(filter, 0)
	return sub
}

func (s *EventStream) Unsubscribe(sub *EventSubscription) {
	if s == nil || s.stream == nil {
		return
	}
	s.stream.Unsubscribe(sub)
}

func (s *EventStream) Recent(limit int) []Event {
	if s == nil || s.stream == nil {
		return nil
	}
	return s.stream.Recent(limit)
}

func (s *EventStream) LatestSequence() uint64 {
	if s == nil || s.stream == nil {
		return 0
	}
	return s.stream.LatestSequence()
}

func (s *EventStream) Publish(event Event) {
	if s == nil || s.stream == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	s.stream.Publish(event)
}
