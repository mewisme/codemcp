package config

import (
	"errors"
	"sync"
)

var ErrRuntimeStoreUnavailable = errors.New("runtime config store unavailable")

type RuntimeStore struct {
	mu    sync.RWMutex
	value Config
}

func NewRuntimeStore(value Config) *RuntimeStore {
	return &RuntimeStore{value: cloneConfig(value)}
}

func (s *RuntimeStore) Snapshot() Config {
	if s == nil {
		return Config{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.value)
}

func (s *RuntimeStore) Update(update func(Config) (Config, error)) (Config, error) {
	if s == nil {
		return Config{}, ErrRuntimeStoreUnavailable
	}
	if update == nil {
		return Config{}, errors.New("runtime config update is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := cloneConfig(s.value)
	next, err := update(current)
	if err != nil {
		return current, err
	}
	s.value = cloneConfig(next)
	return cloneConfig(s.value), nil
}

func cloneConfig(value Config) Config {
	if value.HTTP.Exposure.Interfaces != nil {
		value.HTTP.Exposure.Interfaces = append([]string{}, value.HTTP.Exposure.Interfaces...)
	}
	if value.Permissions.AllowDirs != nil {
		value.Permissions.AllowDirs = append([]string{}, value.Permissions.AllowDirs...)
	}
	if value.Shell.Path != nil {
		value.Shell.Path = append([]string{}, value.Shell.Path...)
	}
	return value
}
