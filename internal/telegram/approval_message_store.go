package telegram

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/configformat"
)

type approvalMessageStore struct {
	mu   sync.RWMutex
	path string
	refs map[string]map[string]int64
}

type approvalMessageStoreFile struct {
	Messages map[string]map[string]int64 `json:"messages"`
}

func newApprovalMessageStore(root string) *approvalMessageStore {
	if strings.TrimSpace(root) == "" {
		root = configformat.RootPath()
	}
	store := &approvalMessageStore{
		path: filepath.Join(root, "telegram-approval-messages.json"),
		refs: map[string]map[string]int64{},
	}
	_ = store.load()
	return store
}

func (s *approvalMessageStore) load() error {
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
	var persisted approvalMessageStoreFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}
	if persisted.Messages == nil {
		persisted.Messages = map[string]map[string]int64{}
	}
	s.mu.Lock()
	s.refs = persisted.Messages
	s.mu.Unlock()
	return nil
}

func (s *approvalMessageStore) get(chatID int64, requestID string) int64 {
	if s == nil || chatID <= 0 || strings.TrimSpace(requestID) == "" {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refs[strconv.FormatInt(chatID, 10)][strings.TrimSpace(requestID)]
}

func (s *approvalMessageStore) put(chatID int64, requestID string, messageID int64) error {
	requestID = strings.TrimSpace(requestID)
	if s == nil || chatID <= 0 || requestID == "" || messageID <= 0 {
		return errors.New("telegram approval message reference is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chatKey := strconv.FormatInt(chatID, 10)
	if s.refs[chatKey] == nil {
		s.refs[chatKey] = map[string]int64{}
	}
	s.refs[chatKey][requestID] = messageID
	return s.saveLocked()
}

func (s *approvalMessageStore) delete(chatID int64, requestID string) error {
	requestID = strings.TrimSpace(requestID)
	if s == nil || chatID <= 0 || requestID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chatKey := strconv.FormatInt(chatID, 10)
	if messages := s.refs[chatKey]; messages != nil {
		delete(messages, requestID)
		if len(messages) == 0 {
			delete(s.refs, chatKey)
		}
	}
	return s.saveLocked()
}

func (s *approvalMessageStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(approvalMessageStoreFile{Messages: s.refs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
