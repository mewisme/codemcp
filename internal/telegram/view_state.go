package telegram

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const (
	defaultViewStateTTL = 10 * time.Minute
	defaultViewStateMax = 512
)

var (
	ErrViewStateStale   = errors.New("telegram view state is stale")
	ErrViewStateForeign = errors.New("telegram view state belongs to another session")
)

type ViewOwner struct {
	ChatID     int64
	UserID     int64
	Generation uint64
}

type viewStateEntry struct {
	owner     ViewOwner
	value     any
	expiresAt time.Time
}

type ViewStateStore struct {
	mu          sync.Mutex
	entries     map[string]viewStateEntry
	order       []string
	ttl         time.Duration
	max         int
	perOwnerMax int
	now         func() time.Time
}

func NewViewStateStore(ttl time.Duration, maxEntries int) *ViewStateStore {
	if ttl <= 0 {
		ttl = defaultViewStateTTL
	}
	if maxEntries <= 0 {
		maxEntries = defaultViewStateMax
	}
	perOwner := min(maxEntries, 64)
	return &ViewStateStore{entries: map[string]viewStateEntry{}, ttl: ttl, max: maxEntries, perOwnerMax: perOwner, now: time.Now}
}

func (s *ViewStateStore) Put(owner ViewOwner, value any) (string, error) {
	if s == nil || owner.ChatID <= 0 || owner.UserID <= 0 || owner.Generation == 0 {
		return "", errors.New("invalid telegram view owner")
	}
	token, err := randomViewToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	for s.ownerCountLocked(owner) >= s.perOwnerMax {
		if !s.evictOldestOwnerLocked(owner) {
			break
		}
	}
	for len(s.entries) >= s.max && len(s.order) > 0 {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
	s.entries[token] = viewStateEntry{owner: owner, value: value, expiresAt: s.now().Add(s.ttl)}
	s.order = append(s.order, token)
	return token, nil
}

func (s *ViewStateStore) Get(token string, owner ViewOwner) (any, error) {
	if s == nil {
		return nil, ErrViewStateStale
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	entry, ok := s.entries[token]
	if !ok {
		return nil, ErrViewStateStale
	}
	if entry.owner != owner {
		return nil, ErrViewStateForeign
	}
	return entry.value, nil
}

func (s *ViewStateStore) Delete(token string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}

func (s *ViewStateStore) ownerCountLocked(owner ViewOwner) int {
	count := 0
	for _, entry := range s.entries {
		if entry.owner == owner {
			count++
		}
	}
	return count
}

func (s *ViewStateStore) evictOldestOwnerLocked(owner ViewOwner) bool {
	for index, token := range s.order {
		entry, ok := s.entries[token]
		if ok && entry.owner == owner {
			delete(s.entries, token)
			s.order = append(s.order[:index], s.order[index+1:]...)
			return true
		}
	}
	return false
}

func (s *ViewStateStore) pruneLocked() {
	now := s.now()
	kept := s.order[:0]
	for _, token := range s.order {
		entry, ok := s.entries[token]
		if !ok {
			continue
		}
		if !entry.expiresAt.After(now) {
			delete(s.entries, token)
			continue
		}
		kept = append(kept, token)
	}
	s.order = kept
}

func randomViewToken() (string, error) {
	data := make([]byte, 9)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
