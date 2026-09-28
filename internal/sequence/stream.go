package sequence

import (
	"sync"
	"sync/atomic"
)

type Overflow struct {
	DroppedSequence uint64 `json:"dropped_sequence"`
	LatestSequence  uint64 `json:"latest_sequence"`
}

type Snapshot[T any] struct {
	Events         []T
	LatestSequence uint64
}

func (s Snapshot[T]) Cursor() uint64 { return s.LatestSequence }

func (o Overflow) RequiresResync() bool { return o.DroppedSequence > 0 }

type Predicate[T any] func(T) bool

type Subscription[T any] struct {
	Events   chan T
	Overflow chan Overflow

	filter      Predicate[T]
	overflow    bool
	closed      bool
	dispose     func()
	disposeOnce sync.Once
	dropped     atomic.Uint64
}

func (s *Subscription[T]) Close() {
	if s == nil {
		return
	}
	s.disposeOnce.Do(func() {
		if s.dispose != nil {
			s.dispose()
		}
	})
}

func (s *Subscription[T]) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

type Stream[T any] struct {
	mu             sync.RWMutex
	subs           map[chan T]*Subscription[T]
	recent         []T
	maxRecent      int
	subscriberSize int
	nextSequence   uint64
	assignSequence func(*T, uint64)
	closed         bool
	dropped        atomic.Uint64
}

func New[T any](maxRecent, subscriberSize int, assignSequence func(*T, uint64)) *Stream[T] {
	if maxRecent < 0 {
		maxRecent = 0
	}
	if subscriberSize < 1 {
		subscriberSize = 1
	}
	return &Stream[T]{
		subs:           map[chan T]*Subscription[T]{},
		maxRecent:      maxRecent,
		subscriberSize: subscriberSize,
		assignSequence: assignSequence,
	}
}

func (s *Stream[T]) Publish(value T) T {
	if s == nil {
		return value
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return value
	}
	s.nextSequence++
	if s.assignSequence != nil {
		s.assignSequence(&value, s.nextSequence)
	}
	if s.maxRecent > 0 {
		s.recent = append(s.recent, value)
		if overflow := len(s.recent) - s.maxRecent; overflow > 0 {
			s.recent = append([]T(nil), s.recent[overflow:]...)
		}
	}
	for _, sub := range s.subs {
		if sub.closed || (sub.filter != nil && !sub.filter(value)) {
			continue
		}
		if sub.overflow {
			sub.dropped.Add(1)
			s.dropped.Add(1)
			continue
		}
		select {
		case sub.Events <- value:
		default:
			sub.overflow = true
			sub.dropped.Add(1)
			s.dropped.Add(1)
			sub.Overflow <- Overflow{DroppedSequence: s.nextSequence, LatestSequence: s.nextSequence}
		}
	}
	s.mu.Unlock()
	return value
}

func (s *Stream[T]) Subscribe(filter Predicate[T], recentLimit int) (*Subscription[T], Snapshot[T]) {
	if s == nil {
		sub := &Subscription[T]{Events: make(chan T), Overflow: make(chan Overflow)}
		close(sub.Events)
		close(sub.Overflow)
		return sub, Snapshot[T]{}
	}
	sub := &Subscription[T]{
		Events:   make(chan T, s.subscriberSize),
		Overflow: make(chan Overflow, 1),
		filter:   filter,
	}
	s.mu.Lock()
	snapshot := Snapshot[T]{
		Events:         recentFiltered(s.recent, filter, recentLimit),
		LatestSequence: s.nextSequence,
	}
	if s.closed {
		close(sub.Events)
		close(sub.Overflow)
		sub.closed = true
		s.mu.Unlock()
		return sub, snapshot
	}
	sub.dispose = func() { s.Unsubscribe(sub) }
	s.subs[sub.Events] = sub
	s.mu.Unlock()
	return sub, snapshot
}

func (s *Stream[T]) Unsubscribe(sub *Subscription[T]) {
	if s == nil || sub == nil {
		return
	}
	s.mu.Lock()
	if current := s.subs[sub.Events]; current != nil && current == sub && !current.closed {
		delete(s.subs, sub.Events)
		close(current.Events)
		close(current.Overflow)
		current.closed = true
	}
	s.mu.Unlock()
}

func (s *Stream[T]) UnsubscribeEvents(events chan T) {
	if s == nil || events == nil {
		return
	}
	s.mu.Lock()
	if sub := s.subs[events]; sub != nil && !sub.closed {
		delete(s.subs, events)
		close(sub.Events)
		close(sub.Overflow)
		sub.closed = true
	}
	s.mu.Unlock()
}

func (s *Stream[T]) AcknowledgeOverflow(sub *Subscription[T]) {
	if s == nil || sub == nil {
		return
	}
	s.mu.Lock()
	if current := s.subs[sub.Events]; current != nil && current == sub && !current.closed {
		current.overflow = false
	}
	s.mu.Unlock()
}

func (s *Stream[T]) SubscriberCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subs)
}

func (s *Stream[T]) OverflowedCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, sub := range s.subs {
		if sub.overflow {
			count++
		}
	}
	return count
}

func (s *Stream[T]) DroppedCount() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

func (s *Stream[T]) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for events, sub := range s.subs {
		delete(s.subs, events)
		if !sub.closed {
			close(sub.Events)
			close(sub.Overflow)
			sub.closed = true
		}
	}
	s.mu.Unlock()
}

func (s *Stream[T]) Recent(limit int) []T {
	if s == nil || limit <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return recentFiltered(s.recent, nil, limit)
}

func (s *Stream[T]) RecentFiltered(filter Predicate[T], limit int) []T {
	if s == nil || limit <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return recentFiltered(s.recent, filter, limit)
}

func (s *Stream[T]) LatestSequence() uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextSequence
}

func (s *Stream[T]) EnsureSequence(sequence uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if sequence > s.nextSequence {
		s.nextSequence = sequence
	}
	s.mu.Unlock()
}

func recentFiltered[T any](values []T, filter Predicate[T], limit int) []T {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	filtered := make([]T, 0, len(values))
	for _, value := range values {
		if filter == nil || filter(value) {
			filtered = append(filtered, value)
		}
	}
	if limit > len(filtered) {
		limit = len(filtered)
	}
	result := make([]T, limit)
	copy(result, filtered[len(filtered)-limit:])
	return result
}
