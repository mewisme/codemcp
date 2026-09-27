package activity

import (
	"encoding/json"
	"sync"

	"go.mewis.me/codemcp/internal/sequence"
)

const (
	MaxRecentEvents         = 1024
	MaxRecentToolCalls      = 1024
	defaultRecentLimit      = MaxRecentEvents
	defaultSubscriberBuffer = 32
)

type Overflow = sequence.Overflow
type Subscription = sequence.Subscription[Event]
type Snapshot = sequence.Snapshot[Event]

type ToolCallRecord struct {
	CallID string `json:"call_id"`
	First  Event  `json:"first"`
	Latest Event  `json:"latest"`
}

type Stream struct {
	mu              sync.Mutex
	stream          *sequence.Stream[Event]
	toolCalls       *sequence.Stream[Event]
	toolCallRecords map[string]ToolCallRecord
	toolCallOrder   []string
	maxRecent       int
}

func NewStream() *Stream {
	return &Stream{
		stream: sequence.New[Event](MaxRecentEvents, defaultSubscriberBuffer, func(event *Event, value uint64) {
			event.Sequence = value
		}),
		toolCalls:       sequence.New[Event](MaxRecentEvents, defaultSubscriberBuffer, nil),
		toolCallRecords: map[string]ToolCallRecord{},
		maxRecent:       defaultRecentLimit,
	}
}

func (s *Stream) Subscribe() chan Event {
	ch, _ := s.SubscribeWithRecent(0)
	return ch
}

func (s *Stream) SubscribeWithRecent(limit int) (chan Event, []Event) {
	sub, recent := s.SubscribeDetailed(limit)
	return sub.Events, recent
}

func (s *Stream) SubscribeDetailed(limit int) (*Subscription, []Event) {
	sub, snapshot := s.SubscribeSnapshot(limit)
	return sub, snapshot.Events
}

func (s *Stream) SubscribeSnapshot(limit int) (*Subscription, Snapshot) {
	if s == nil || s.stream == nil {
		var stream *sequence.Stream[Event]
		return stream.Subscribe(nil, limit)
	}
	if limit > s.maxRecent {
		limit = s.maxRecent
	}
	return s.stream.Subscribe(nil, limit)
}

func (s *Stream) SubscribeToolCallsDetailed(limit int) (*Subscription, []Event) {
	sub, recent, _ := s.SubscribeToolCallsSnapshot(limit)
	return sub, recent
}

func (s *Stream) SubscribeToolCallsSnapshot(limit int) (*Subscription, []Event, uint64) {
	if s == nil || s.toolCalls == nil {
		var stream *sequence.Stream[Event]
		sub, snapshot := stream.Subscribe(nil, limit)
		return sub, snapshot.Events, 0
	}
	s.mu.Lock()
	sub, snapshot := s.toolCalls.Subscribe(nil, limit)
	latestSequence := s.stream.LatestSequence()
	s.mu.Unlock()
	return sub, snapshot.Events, latestSequence
}

func (s *Stream) Unsubscribe(ch chan Event) {
	if s == nil {
		return
	}
	if s.stream != nil {
		s.stream.UnsubscribeEvents(ch)
	}
	if s.toolCalls != nil {
		s.toolCalls.UnsubscribeEvents(ch)
	}
}

func (s *Stream) UnsubscribeDetailed(sub *Subscription) {
	if s == nil || sub == nil {
		return
	}
	if s.stream != nil {
		s.stream.Unsubscribe(sub)
	}
	if s.toolCalls != nil {
		s.toolCalls.Unsubscribe(sub)
	}
}

func (s *Stream) FindCall(callID string) (Event, bool) {
	if s == nil {
		return Event{}, false
	}
	s.mu.Lock()
	record, ok := s.toolCallRecords[callID]
	s.mu.Unlock()
	if !ok {
		return Event{}, false
	}
	return record.Latest, true
}

func (s *Stream) RecentToolCalls(limit int) []ToolCallRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > MaxRecentToolCalls {
		limit = MaxRecentToolCalls
	}
	start := max(0, len(s.toolCallOrder)-limit)
	result := make([]ToolCallRecord, 0, len(s.toolCallOrder)-start)
	for _, callID := range s.toolCallOrder[start:] {
		if record, ok := s.toolCallRecords[callID]; ok {
			result = append(result, record)
		}
	}
	return result
}

func (s *Stream) Recent(limit int) []Event {
	if s == nil || s.stream == nil {
		return nil
	}
	if limit > s.maxRecent {
		limit = s.maxRecent
	}
	return s.stream.Recent(limit)
}

func (s *Stream) LatestSequence() uint64 {
	if s == nil || s.stream == nil {
		return 0
	}
	return s.stream.LatestSequence()
}

func (s *Stream) Publish(event Event) {
	if s == nil || s.stream == nil {
		return
	}
	event = normalizeEvent(event)
	s.mu.Lock()
	event = s.stream.Publish(event)
	if event.Kind == string(EventToolCall) && s.toolCalls != nil {
		s.toolCalls.Publish(event)
		s.updateToolCallRecordLocked(event)
	}
	s.mu.Unlock()
}

func (s *Stream) updateToolCallRecordLocked(event Event) {
	if s == nil || event.CallID == "" {
		return
	}
	record, ok := s.toolCallRecords[event.CallID]
	if !ok {
		s.toolCallRecords[event.CallID] = ToolCallRecord{CallID: event.CallID, First: event, Latest: event}
		s.toolCallOrder = append(s.toolCallOrder, event.CallID)
	} else if event.Sequence >= record.Latest.Sequence {
		record.Latest = event
		s.toolCallRecords[event.CallID] = record
	}
	if len(s.toolCallOrder) <= MaxRecentToolCalls {
		return
	}
	remove := len(s.toolCallOrder) - MaxRecentToolCalls
	for _, callID := range s.toolCallOrder[:remove] {
		delete(s.toolCallRecords, callID)
	}
	s.toolCallOrder = append([]string(nil), s.toolCallOrder[remove:]...)
}

func Encode(event Event) string {
	data, _ := json.Marshal(event)
	return string(data)
}
