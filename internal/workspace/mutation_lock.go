package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"go.mewis.me/codemcp/internal/oslock"
)

const (
	registryMutationLockName = "workspace-registry.mutation.lock"
	registryMutationTimeout  = 500 * time.Millisecond
	registryMutationPoll     = 10 * time.Millisecond
)

var ErrRegistryBusy = errors.New("workspace registry is busy")

func (m *Manager) beginRegistryMutation() (*oslock.Lock, error) {
	if m == nil {
		return nil, errors.New("workspace manager is unavailable")
	}
	lock, err := acquireRegistryMutationLock(m.registryMutationLockPath(), registryMutationTimeout)
	if err != nil {
		return nil, err
	}
	if err := m.reloadRegistryForMutation(); err != nil {
		_ = lock.Release()
		return nil, err
	}
	return lock, nil
}

func (m *Manager) registryMutationLockPath() string {
	if m == nil {
		return ""
	}
	return filepath.Join(filepath.Dir(m.path), registryMutationLockName)
}

func acquireRegistryMutationLock(path string, timeout time.Duration) (*oslock.Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		lock, ok, err := oslock.TryAcquire(path, oslock.Exclusive)
		if err != nil {
			return nil, fmt.Errorf("lock workspace registry mutation: %w", err)
		}
		if ok {
			return lock, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %s", ErrRegistryBusy, path)
		}
		time.Sleep(registryMutationPoll)
	}
}

func (m *Manager) reloadRegistryForMutation() error {
	m.mu.Lock()
	m.loaded = false
	m.items = map[string]Workspace{}
	m.containers = map[string]WorkspaceContainer{}
	m.aliases = map[string]string{}
	m.mu.Unlock()
	return m.ensureLoadedUnderRegistryLock()
}
