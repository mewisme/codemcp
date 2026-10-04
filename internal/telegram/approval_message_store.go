package telegram

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/configformat"
)

const (
	maxApprovalMessageRefs      = 512
	maxApprovalMessageStoreSize = 1 << 20
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
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxApprovalMessageStoreSize {
		return errors.New("telegram approval message store is invalid or too large")
	}
	data, err := os.ReadFile(s.path)
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
	trimmed := s.trimLocked()
	s.mu.Unlock()
	if trimmed {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.saveLocked()
	}
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
	s.trimLocked()
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

func (s *approvalMessageStore) trimLocked() bool {
	type key struct {
		chat    string
		request string
	}
	keys := make([]key, 0)
	for chat, messages := range s.refs {
		for request := range messages {
			keys = append(keys, key{chat: chat, request: request})
		}
	}
	if len(keys) <= maxApprovalMessageRefs {
		return false
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].chat == keys[j].chat {
			return keys[i].request < keys[j].request
		}
		return keys[i].chat < keys[j].chat
	})
	remove := len(keys) - maxApprovalMessageRefs
	for _, item := range keys[:remove] {
		messages := s.refs[item.chat]
		delete(messages, item.request)
		if len(messages) == 0 {
			delete(s.refs, item.chat)
		}
	}
	return true
}
