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

type Stream struct {
	mu        sync.Mutex
	stream    *sequence.Stream[Event]
	toolCalls *sequence.Stream[Event]
	maxRecent int
}

func NewStream() *Stream {
	return &Stream{
		stream: sequence.New[Event](MaxRecentEvents, defaultSubscriberBuffer, func(event *Event, value uint64) {
			event.Sequence = value
		}),
		toolCalls: sequence.New[Event](MaxRecentToolCalls, defaultSubscriberBuffer, nil),
		maxRecent: defaultRecentLimit,
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
	if s == nil || s.stream == nil {
		var stream *sequence.Stream[Event]
		sub, snapshot := stream.Subscribe(nil, limit)
		return sub, snapshot.Events
	}
	if limit > s.maxRecent {
		limit = s.maxRecent
	}
	sub, snapshot := s.stream.Subscribe(nil, limit)
	return sub, snapshot.Events
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
	if s == nil || s.toolCalls == nil {
		return Event{}, false
	}
	values := s.toolCalls.Recent(MaxRecentToolCalls)
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].CallID == callID {
			return values[index], true
		}
	}
	return Event{}, false
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
	}
	s.mu.Unlock()
}

func Encode(event Event) string {
	data, _ := json.Marshal(event)
	return string(data)
}
