package approval

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/sequence"
)

const (
	EventCreated           = "approval.created"
	EventPending           = "approval.pending"
	EventApproved          = "approval.approved"
	EventDenied            = "approval.denied"
	EventExpired           = "approval.expired"
	EventClaimed           = "approval.claimed"
	EventRevoked           = "approval.revoked"
	EventCancelled         = "approval.cancelled"
	EventExplanationReady  = "approval.explanation.ready"
	EventExplanationFailed = "approval.explanation.failed"
)

type EventSubject string

const (
	EventSubjectChallenge EventSubject = "challenge"
	EventSubjectRequest   EventSubject = "request"
	EventSubjectGrant     EventSubject = "grant"
)

type Event struct {
	Sequence           uint64       `json:"sequence,omitempty"`
	Name               string       `json:"name"`
	Subject            EventSubject `json:"subject"`
	ChallengeID        string       `json:"challenge_id,omitempty"`
	RequestID          string       `json:"request_id,omitempty"`
	WorkspaceID        string       `json:"workspace_id"`
	SessionHash        string       `json:"session_hash,omitempty"`
	Source             string       `json:"source,omitempty"`
	TargetTool         string       `json:"target_tool"`
	Status             Status       `json:"status,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	ExpiresAt          time.Time    `json:"expires_at"`
	RetryUntil         time.Time    `json:"retry_until,omitempty"`
	GrantExpiresAt     time.Time    `json:"grant_expires_at,omitempty"`
	ExplanationAttempt uint64       `json:"explanation_attempt,omitempty"`
	Timestamp          time.Time    `json:"timestamp"`
}

type EventOverflow = sequence.Overflow
type EventSubscription = sequence.Subscription[Event]
type EventSnapshot = sequence.Snapshot[Event]

type EventStream struct {
	stream    *sequence.Stream[Event]
	dedupMu   sync.Mutex
	dedupSeen map[string]struct{}
	dedupKeys []string
}

func newEventStream() *EventStream {
	return newEventStreamWithLimits(64, 16)
}

func newEventStreamWithLimits(maxRecent, subscriberSize int) *EventStream {
	return &EventStream{
		stream: sequence.New[Event](maxRecent, subscriberSize, func(event *Event, value uint64) {
			event.Sequence = value
		}),
		dedupSeen: map[string]struct{}{},
	}
}

func (s *EventStream) Subscribe() *EventSubscription {
	sub, _ := s.SubscribeSnapshot(0)
	return sub
}

func (s *EventStream) SubscribeWorkspace(workspaceID string) *EventSubscription {
	sub, _ := s.SubscribeWorkspaceSnapshot(workspaceID, 0)
	return sub
}

func (s *EventStream) SubscribeSnapshot(recentLimit int) (*EventSubscription, EventSnapshot) {
	return s.SubscribeWorkspaceSnapshot("", recentLimit)
}

func (s *EventStream) SubscribeWorkspaceSnapshot(workspaceID string, recentLimit int) (*EventSubscription, EventSnapshot) {
	if s == nil || s.stream == nil {
		var stream *sequence.Stream[Event]
		return stream.Subscribe(nil, recentLimit)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	var filter sequence.Predicate[Event]
	if workspaceID != "" {
		filter = func(event Event) bool { return event.WorkspaceID == workspaceID }
	}
	return s.stream.Subscribe(filter, recentLimit)
}

func (s *EventStream) Unsubscribe(sub *EventSubscription) {
	if s == nil || s.stream == nil {
		return
	}
	s.stream.Unsubscribe(sub)
}

func (s *EventStream) AcknowledgeOverflow(sub *EventSubscription) {
	if s == nil || s.stream == nil {
		return
	}
	s.stream.AcknowledgeOverflow(sub)
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
	event.Name = strings.TrimSpace(event.Name)
	event.ChallengeID = strings.TrimSpace(event.ChallengeID)
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.WorkspaceID = strings.TrimSpace(event.WorkspaceID)
	event.SessionHash = strings.TrimSpace(event.SessionHash)
	event.Source = strings.TrimSpace(event.Source)
	event.TargetTool = strings.TrimSpace(event.TargetTool)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	key := eventLifecycleKey(event)
	s.dedupMu.Lock()
	if _, exists := s.dedupSeen[key]; exists {
		s.dedupMu.Unlock()
		return
	}
	s.dedupSeen[key] = struct{}{}
	s.dedupKeys = append(s.dedupKeys, key)
	const dedupWindow = 256
	if len(s.dedupKeys) > dedupWindow {
		remove := s.dedupKeys[0]
		s.dedupKeys = append([]string(nil), s.dedupKeys[1:]...)
		delete(s.dedupSeen, remove)
	}
	s.stream.Publish(event)
	s.dedupMu.Unlock()
}

func eventLifecycleKey(event Event) string {
	return strings.Join([]string{
		event.Name,
		string(event.Subject),
		event.ChallengeID,
		event.RequestID,
		string(event.Status),
		event.WorkspaceID,
		event.TargetTool,
		event.RetryUntil.UTC().Format(time.RFC3339Nano),
		event.GrantExpiresAt.UTC().Format(time.RFC3339Nano),
		fmt.Sprintf("%d", event.ExplanationAttempt),
	}, "\x00")
}
