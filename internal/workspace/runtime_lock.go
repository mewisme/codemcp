package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/oslock"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

var (
	ErrAlreadyActive = errors.New("workspace already active")
	ErrStateLost     = errors.New("workspace state lost or replaced while active")
)

type runtimeState struct {
	active bool
	locks  map[string]*oslock.Lock
	roots  map[string]string
}

type runtimeLockMetadata struct {
	PID         int       `json:"pid"`
	InstanceID  string    `json:"instance_id,omitempty"`
	WorkspaceID string    `json:"workspace_id"`
	StartedAt   time.Time `json:"started_at"`
}

type RuntimeDiagnostics struct {
	Active       bool                         `json:"active"`
	Owned        int                          `json:"owned"`
	RegistryLock string                       `json:"registry_mutation_lock"`
	Workspaces   []WorkspaceRuntimeDiagnostic `json:"workspaces"`
}

type WorkspaceRuntimeDiagnostic struct {
	WorkspaceID string    `json:"workspace_id"`
	Root        string    `json:"root"`
	LockPath    string    `json:"lock_path"`
	Owned       bool      `json:"owned"`
	Locked      bool      `json:"locked"`
	Valid       bool      `json:"valid"`
	PID         int       `json:"pid,omitempty"`
	InstanceID  string    `json:"instance_id,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	Error       string    `json:"error,omitempty"`
}

func newRuntimeState() *runtimeState {
	return &runtimeState{locks: map[string]*oslock.Lock{}, roots: map[string]string{}}
}

// Lock order is runtimeMu -> registry mutation file lock -> Manager.mu -> workspace runtime file lock.
// Operations that cannot change runtime ownership skip runtimeMu entirely.
func (m *Manager) Activate() error {
	if m == nil {
		return errors.New("workspace manager is unavailable")
	}
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	if m.runtime == nil {
		m.runtime = newRuntimeState()
	}
	if m.runtime.active {
		return nil
	}
	if err := m.ensureLoaded(); err != nil {
		return err
	}
	items, err := m.List()
	if err != nil {
		return err
	}
	locks := map[string]*oslock.Lock{}
	roots := map[string]string{}
	for _, item := range items {
		if !item.Available() {
			continue
		}
		if err := validateActiveWorkspaceState(item); err != nil {
			continue
		}
		lock, err := m.acquireRuntimeLock(item)
		if err != nil {
			return errors.Join(err, releaseRuntimeLocks(locks))
		}
		locks[item.ID] = lock
		roots[item.ID] = item.Path
	}
	m.mu.Lock()
	m.runtime.active = true
	m.runtime.locks = locks
	m.runtime.roots = roots
	m.mu.Unlock()
	return nil
}

func (m *Manager) Deactivate() error {
	if m == nil {
		return nil
	}
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	m.mu.Lock()
	if m.runtime == nil || !m.runtime.active {
		m.mu.Unlock()
		return nil
	}
	locks := m.runtime.locks
	m.runtime = newRuntimeState()
	m.mu.Unlock()
	return releaseRuntimeLocks(locks)
}

func (m *Manager) Active() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtime != nil && m.runtime.active
}

func (m *Manager) RuntimeDiagnostics() RuntimeDiagnostics {
	result := RuntimeDiagnostics{}
	if m == nil {
		return result
	}
	result.RegistryLock = m.registryMutationLockPath()
	m.mu.RLock()
	result.Active = m.runtime != nil && m.runtime.active
	items := make([]Workspace, 0, len(m.items))
	owned := map[string]bool{}
	if m.runtime != nil {
		result.Owned = len(m.runtime.locks)
		for id := range m.runtime.locks {
			owned[id] = true
		}
	}
	for _, item := range m.items {
		items = append(items, item)
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for _, item := range items {
		diagnostic := WorkspaceRuntimeDiagnostic{
			WorkspaceID: item.ID,
			Root:        item.Path,
			LockPath:    workspacestate.New(item.Path).RuntimeLockPath(),
			Owned:       owned[item.ID],
		}
		if !item.Available() {
			diagnostic.Error = item.Error
		} else if err := validateActiveWorkspaceState(item); err != nil {
			diagnostic.Error = err.Error()
		} else {
			diagnostic.Valid = true
		}
		if data, err := os.ReadFile(diagnostic.LockPath); err == nil {
			var metadata runtimeLockMetadata
			if json.Unmarshal(data, &metadata) == nil {
				diagnostic.PID = metadata.PID
				diagnostic.InstanceID = metadata.InstanceID
				diagnostic.StartedAt = metadata.StartedAt
			}
		}
		if diagnostic.Owned {
			diagnostic.Locked = true
		} else if _, err := os.Stat(diagnostic.LockPath); err == nil {
			lock, ok, lockErr := oslock.TryAcquireExisting(diagnostic.LockPath, oslock.Exclusive)
			if lockErr != nil {
				diagnostic.Error = lockErr.Error()
			} else if ok {
				_ = lock.Release()
			} else {
				diagnostic.Locked = true
			}
		}
		result.Workspaces = append(result.Workspaces, diagnostic)
	}
	return result
}

func (m *Manager) validateOwnedWorkspaceBeforeMutation(id string) error {
	m.mu.RLock()
	if m.runtime == nil || !m.runtime.active {
		m.mu.RUnlock()
		return nil
	}
	canonical := m.canonicalIDLocked(strings.TrimSpace(id))
	item, ok := m.items[canonical]
	owned := ok && m.runtime.locks[canonical] != nil
	m.mu.RUnlock()
	if !owned {
		return nil
	}
	return validateActiveWorkspaceState(item)
}

func (m *Manager) validateRuntimeOwnership(id string) error {
	m.mu.RLock()
	if m.runtime == nil || !m.runtime.active {
		m.mu.RUnlock()
		return nil
	}
	canonical := m.canonicalIDLocked(strings.TrimSpace(id))
	item, ok := m.items[canonical]
	lock := m.runtime.locks[canonical]
	m.mu.RUnlock()
	if !ok || lock == nil {
		return nil
	}
	if err := validateActiveWorkspaceState(item); err != nil {
		return err
	}
	same, err := lock.SameFile(workspacestate.New(item.Path).RuntimeLockPath())
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrStateLost, item.ID, err)
	}
	if !same {
		return fmt.Errorf("%w: %s: runtime lock replaced", ErrStateLost, item.ID)
	}
	return nil
}

func probeWorkspaceRuntimeLock(item Workspace) error {
	path := workspacestate.New(item.Path).RuntimeLockPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect workspace %s runtime lock: %w", item.ID, err)
	}
	lock, ok, err := oslock.TryAcquireExisting(path, oslock.Exclusive)
	if err != nil {
		return fmt.Errorf("probe workspace %s runtime lock: %w", item.ID, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrAlreadyActive, item.ID)
	}
	return lock.Release()
}

func (m *Manager) acquireRuntimeLock(item Workspace) (*oslock.Lock, error) {
	lock, ok, err := oslock.TryAcquire(workspacestate.New(item.Path).RuntimeLockPath(), oslock.Exclusive)
	if err != nil {
		return nil, fmt.Errorf("lock workspace %s: %w", item.ID, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyActive, item.ID)
	}
	identity, _ := m.Instance()
	metadata, err := json.MarshalIndent(runtimeLockMetadata{
		PID: os.Getpid(), InstanceID: identity.ID, WorkspaceID: item.ID, StartedAt: time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		_ = lock.Release()
		return nil, err
	}
	if err := lock.ReplaceContent(append(metadata, '\n')); err != nil {
		_ = lock.Release()
		return nil, fmt.Errorf("write workspace %s lock metadata: %w", item.ID, err)
	}
	return lock, nil
}

func validateActiveWorkspaceState(item Workspace) error {
	identity, err := workspacestate.New(item.Path).LoadIdentity()
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrStateLost, item.ID, err)
	}
	if identity.ID != item.ID {
		return fmt.Errorf("%w: %s: identity changed to %s", ErrStateLost, item.ID, identity.ID)
	}
	return nil
}

func releaseRuntimeLocks(locks map[string]*oslock.Lock) error {
	var result error
	for _, lock := range locks {
		result = errors.Join(result, lock.Release())
	}
	return result
}
