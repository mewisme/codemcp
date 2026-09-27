package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/notification"
)

type TopicRole string

const (
	TopicRequests    TopicRole = "requests"
	TopicCompletions TopicRole = "completions"
	TopicRuntime     TopicRole = "runtime"
	TopicLogs        TopicRole = "logs"
)

var managedTopicRoles = []struct {
	Role TopicRole
	Name string
}{
	{Role: TopicRequests, Name: "Requests"},
	{Role: TopicCompletions, Name: "Completions"},
	{Role: TopicRuntime, Name: "Runtime / System"},
	{Role: TopicLogs, Name: "Logs"},
}

type topicStore struct {
	mu   sync.RWMutex
	path string
	ids  map[string]map[TopicRole]int
}

type topicStoreFile struct {
	Topics map[string]map[TopicRole]int `json:"topics"`
}

func newTopicStore(root string) *topicStore {
	if strings.TrimSpace(root) == "" {
		root = configformat.RootPath()
	}
	store := &topicStore{
		path: filepath.Join(root, "telegram-topics.json"),
		ids:  map[string]map[TopicRole]int{},
	}
	_ = store.load()
	return store
}

func (s *topicStore) load() error {
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
	var persisted topicStoreFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if persisted.Topics == nil {
		persisted.Topics = map[string]map[TopicRole]int{}
	}
	s.ids = persisted.Topics
	return nil
}

func (s *topicStore) get(chatID int64, role TopicRole) int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ids[strconv.FormatInt(chatID, 10)][role]
}

func (s *topicStore) put(chatID int64, role TopicRole, threadID int) error {
	if s == nil || chatID <= 0 || threadID <= 0 {
		return errors.New("telegram topic metadata is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strconv.FormatInt(chatID, 10)
	if s.ids[key] == nil {
		s.ids[key] = map[TopicRole]int{}
	}
	s.ids[key][role] = threadID
	return s.saveLocked()
}

func (s *topicStore) delete(chatID int64, role TopicRole) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strconv.FormatInt(chatID, 10)
	if roles := s.ids[key]; roles != nil {
		delete(roles, role)
		if len(roles) == 0 {
			delete(s.ids, key)
		}
	}
	_ = s.saveLocked()
}

func (s *topicStore) count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, roles := range s.ids {
		total += len(roles)
	}
	return total
}

func (s *topicStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(topicStoreFile{Topics: s.ids}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func topicRoleForNotification(kind notification.Kind) TopicRole {
	value := string(kind)
	switch {
	case strings.HasPrefix(value, "approval."):
		return TopicRequests
	case strings.HasPrefix(value, "completion."):
		return TopicCompletions
	case strings.HasPrefix(value, "log."):
		return TopicLogs
	default:
		return TopicRuntime
	}
}

func (runtime *Runtime) reconcileTopicsBounded(ctx context.Context, api API, cfg config.TelegramConfig, supported bool) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.health.TopicsConfigured = cfg.TopicsEnabled
	runtime.health.TopicsSupported = supported
	runtime.health.TopicsEffective = cfg.TopicsEnabled && supported
	runtime.health.TopicCount = 0
	runtime.health.TopicLastError = ""
	runtime.mu.Unlock()
	if !cfg.TopicsEnabled || !supported {
		return
	}
	topicAPI, ok := api.(TopicAPI)
	if !ok {
		runtime.setTopicError("telegram topic API is unavailable")
		return
	}
	runtime.mu.Lock()
	if runtime.topics == nil {
		runtime.topics = newTopicStore(runtime.root)
	}
	store := runtime.topics
	runtime.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	reconcileCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, chatID := range cfg.AllowedUserIDs {
		for _, managed := range managedTopicRoles {
			if store.get(chatID, managed.Role) > 0 {
				continue
			}
			threadID, err := topicAPI.CreateTopic(reconcileCtx, chatID, managed.Name)
			if err != nil {
				runtime.setTopicError("telegram topic reconciliation failed")
				return
			}
			if err := store.put(chatID, managed.Role, threadID); err != nil {
				runtime.setTopicError("telegram topic metadata persistence failed")
				return
			}
		}
	}
	runtime.mu.Lock()
	runtime.health.TopicCount = store.count()
	runtime.mu.Unlock()
}

func (runtime *Runtime) setTopicError(message string) {
	runtime.mu.Lock()
	runtime.health.TopicLastError = strings.TrimSpace(message)
	runtime.mu.Unlock()
}
