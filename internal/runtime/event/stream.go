package event

import (
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/sequence"
)

const defaultStreamBuffer = 64

type StreamOverflow = sequence.Overflow
type Subscription = sequence.Subscription[Event]
type Snapshot = sequence.Snapshot[Event]

type Stream struct {
	metadata Metadata
	stream   *sequence.Stream[Event]
}

func NewStream(metadata Metadata) *Stream {
	return &Stream{
		metadata: metadata,
		stream: sequence.New[Event](0, defaultStreamBuffer, func(event *Event, value uint64) {
			event.Sequence = value
		}),
	}
}

func (s *Stream) WriteEvent(event logger.Event) error {
	if s == nil {
		return nil
	}
	s.Publish(fromLoggerEvent(event, s.metadata))
	return nil
}

func (s *Stream) Publish(event Event) Event {
	if s == nil || s.stream == nil {
		return event
	}
	return s.stream.Publish(event)
}

func (s *Stream) Subscribe() chan Event {
	return s.SubscribeDetailed().Events
}

func (s *Stream) SubscribeDetailed() *Subscription {
	sub, _ := s.SubscribeSnapshot(0)
	return sub
}

func (s *Stream) SubscribeSnapshot(recentLimit int) (*Subscription, Snapshot) {
	if s == nil || s.stream == nil {
		var stream *sequence.Stream[Event]
		return stream.Subscribe(nil, recentLimit)
	}
	return s.stream.Subscribe(nil, recentLimit)
}

func (s *Stream) Unsubscribe(ch chan Event) {
	if s == nil || s.stream == nil || ch == nil {
		return
	}
	s.stream.UnsubscribeEvents(ch)
}

func (s *Stream) UnsubscribeDetailed(sub *Subscription) {
	if s != nil && s.stream != nil {
		s.stream.Unsubscribe(sub)
	}
}

func (s *Stream) AcknowledgeOverflow(sub *Subscription) {
	if s != nil && s.stream != nil {
		s.stream.AcknowledgeOverflow(sub)
	}
}

func (s *Stream) LatestSequence() uint64 {
	if s == nil || s.stream == nil {
		return 0
	}
	return s.stream.LatestSequence()
}

type Recorder struct {
	Journal  *Journal
	Stream   *Stream
	metadata Metadata
	mu       sync.Mutex
	sequence uint64
}

func NewRecorder(journal *Journal, metadata Metadata) *Recorder {
	return &Recorder{Journal: journal, Stream: NewStream(metadata), metadata: metadata}
}

func (r *Recorder) WriteEvent(event logger.Event) error {
	if r == nil {
		return nil
	}
	return r.Record(fromLoggerEvent(event, r.metadata))
}

func (r *Recorder) Record(value Event) error {
	if r == nil {
		return nil
	}
	if value.Time.IsZero() {
		value.Time = time.Now().UTC()
	} else {
		value.Time = value.Time.UTC()
	}
	if value.RunID == "" {
		value.RunID = r.metadata.RunID
	}
	if value.PID == 0 {
		value.PID = r.metadata.PID
	}
	if !value.Managed {
		value.Managed = r.metadata.Managed
	}
	if value.ServiceID == "" {
		value.ServiceID = r.metadata.ServiceID
	}
	if value.ServiceScope == "" {
		value.ServiceScope = r.metadata.ServiceScope
	}
	value.Message = sanitizeString(value.Message)
	value.Error = sanitizeString(value.Error)
	value.WorkspaceID = sanitizeString(value.WorkspaceID)
	value.Tool = sanitizeString(value.Tool)
	value.Method = sanitizeString(value.Method)
	value.Source = sanitizeString(value.Source)
	value.Status = sanitizeString(value.Status)
	for index := range value.Fields {
		value.Fields[index].Value = sanitizeValue(value.Fields[index].Key, value.Fields[index].Value)
	}
	r.mu.Lock()
	if latest := r.Stream.LatestSequence(); latest > r.sequence {
		r.sequence = latest
	}
	r.sequence++
	value.Sequence = r.sequence
	err := r.Journal.Append(value)
	if r.Stream != nil && r.Stream.stream != nil {
		r.Stream.stream.EnsureSequence(value.Sequence - 1)
		r.Stream.stream.Publish(value)
	}
	r.mu.Unlock()
	return err
}
