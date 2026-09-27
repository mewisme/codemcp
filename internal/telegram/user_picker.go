package telegram

import (
	"errors"
	"sync"
	"time"
)

type PendingUserSelection struct {
	Owner      ViewOwner
	RequestID  int
	CurrentRaw string
	ExpiresAt  time.Time
}

type UserSelectionStore struct {
	mu      sync.Mutex
	entries map[int]PendingUserSelection
	ttl     time.Duration
}

func NewUserSelectionStore(ttl time.Duration) *UserSelectionStore {
	if ttl <= 0 {
		ttl = defaultInputStateTTL
	}
	return &UserSelectionStore{entries: map[int]PendingUserSelection{}, ttl: ttl}
}

func (s *UserSelectionStore) Put(owner ViewOwner, requestID int, currentRaw string) error {
	if s == nil || owner.ChatID <= 0 || owner.UserID <= 0 || owner.Generation <= 0 || requestID <= 0 {
		return errors.New("telegram user-picker state is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[requestID] = PendingUserSelection{Owner: owner, RequestID: requestID, CurrentRaw: currentRaw, ExpiresAt: time.Now().Add(s.ttl)}
	return nil
}

func (s *UserSelectionStore) Match(owner ViewOwner, requestID int) (PendingUserSelection, bool) {
	if s == nil || requestID <= 0 {
		return PendingUserSelection{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.entries[requestID]
	if !ok {
		return PendingUserSelection{}, false
	}
	if time.Now().After(state.ExpiresAt) {
		delete(s.entries, requestID)
		return PendingUserSelection{}, false
	}
	if state.Owner != owner {
		return PendingUserSelection{}, false
	}
	delete(s.entries, requestID)
	return state, true
}

func (s *UserSelectionStore) Delete(requestID int) {
	if s == nil || requestID <= 0 {
		return
	}
	s.mu.Lock()
	delete(s.entries, requestID)
	s.mu.Unlock()
}
