package telegram

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
)

const durableOperationTTL = 10 * time.Minute

type durableOperationMessage struct {
	ChatID        int64     `json:"chat_id"`
	MessageID     int64     `json:"message_id"`
	Operation     string    `json:"operation"`
	PreviousRunID string    `json:"previous_run_id,omitempty"`
	ServiceID     string    `json:"service_id,omitempty"`
	ServiceScope  string    `json:"service_scope,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type operationMessageStore struct {
	mu      sync.RWMutex
	path    string
	records map[string]durableOperationMessage
}

type operationMessageStoreFile struct {
	Records map[string]durableOperationMessage `json:"records"`
}

func newOperationMessageStore(root string) *operationMessageStore {
	if strings.TrimSpace(root) == "" {
		root = configformat.RootPath()
	}
	store := &operationMessageStore{
		path:    filepath.Join(root, "telegram-operation-messages.json"),
		records: map[string]durableOperationMessage{},
	}
	_ = store.load()
	return store
}

func durableOperationKey(chatID, messageID int64) string {
	return strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(messageID, 10)
}

func (s *operationMessageStore) load() error {
	if s == nil {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var persisted operationMessageStoreFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}
	if persisted.Records == nil {
		persisted.Records = map[string]durableOperationMessage{}
	}
	s.mu.Lock()
	s.records = persisted.Records
	s.mu.Unlock()
	return nil
}

func (s *operationMessageStore) put(record durableOperationMessage) error {
	if s == nil || record.ChatID <= 0 || record.MessageID <= 0 || strings.TrimSpace(record.Operation) == "" || record.CreatedAt.IsZero() {
		return errors.New("telegram durable operation message reference is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[durableOperationKey(record.ChatID, record.MessageID)] = record
	return s.saveLocked()
}

func (s *operationMessageStore) delete(chatID, messageID int64) error {
	if s == nil || chatID <= 0 || messageID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, durableOperationKey(chatID, messageID))
	return s.saveLocked()
}

func (s *operationMessageStore) list() []durableOperationMessage {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]durableOperationMessage, 0, len(s.records))
	for _, record := range s.records {
		result = append(result, record)
	}
	return result
}

func (s *operationMessageStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(operationMessageStoreFile{Records: s.records}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
