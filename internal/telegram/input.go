package telegram

import (
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	maxForceReplyPlaceholderRunes = 64
	defaultInputStateTTL          = 5 * time.Minute
)

type PendingInput struct {
	Owner           ViewOwner
	PromptMessageID int64
	Secret          bool
	ExpiresAt       time.Time
}

func InputValue(message Message) (PendingInputValue, error) {
	text := strings.TrimSpace(message.Text)
	if text == "" && message.Document == nil {
		return PendingInputValue{}, errors.New("telegram input reply has no supported value")
	}
	if message.Document != nil {
		if err := ValidateDocument(*message.Document); err != nil {
			return PendingInputValue{}, err
		}
	}
	return PendingInputValue{Text: text, Document: message.Document}, nil
}

type InputStore struct {
	mu      sync.Mutex
	entries map[int64]PendingInput
	ttl     time.Duration
}

func NewInputStore(ttl time.Duration) *InputStore {
	if ttl <= 0 {
		ttl = defaultInputStateTTL
	}
	return &InputStore{entries: map[int64]PendingInput{}, ttl: ttl}
}

func (s *InputStore) Put(owner ViewOwner, promptMessageID int64, secret bool) error {
	if s == nil || owner.ChatID <= 0 || owner.UserID <= 0 || owner.Generation <= 0 || promptMessageID <= 0 {
		return errors.New("telegram input state is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[promptMessageID] = PendingInput{Owner: owner, PromptMessageID: promptMessageID, Secret: secret, ExpiresAt: time.Now().Add(s.ttl)}
	return nil
}

func (s *InputStore) Match(owner ViewOwner, message Message) (PendingInput, bool) {
	if s == nil || message.ReplyToMessage == nil {
		return PendingInput{}, false
	}
	promptID := message.ReplyToMessage.MessageID
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.entries[promptID]
	if !ok {
		return PendingInput{}, false
	}
	if time.Now().After(state.ExpiresAt) {
		delete(s.entries, promptID)
		return PendingInput{}, false
	}
	if state.Owner != owner || message.Chat.ID != owner.ChatID || message.From == nil || message.From.ID != owner.UserID {
		return PendingInput{}, false
	}
	delete(s.entries, promptID)
	return state, true
}

func (s *InputStore) Delete(promptMessageID int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.entries, promptMessageID)
	s.mu.Unlock()
}

func forceReplyPlaceholder(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxForceReplyPlaceholderRunes {
		value = string(runes[:maxForceReplyPlaceholderRunes])
	}
	return value
}
