package completion

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.mewis.me/codemcp/internal/configformat"
	statepkg "go.mewis.me/codemcp/internal/state"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	workspaceHistoryVersion = 1
	sequenceStoreVersion    = 1
	historyFileName         = "agent-completions.json"
	maxHistoryFileBytes     = 2 << 20
	maxSequenceFileBytes    = 64 << 10
)

type workspaceHistory struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

func workspaceHistoryPath(local workspacestate.Store) (string, error) {
	return local.StatePath(historyFileName)
}

func loadWorkspaceHistory(local workspacestate.Store) (workspaceHistory, bool, error) {
	path, err := workspaceHistoryPath(local)
	if err != nil {
		return workspaceHistory{}, false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceHistory{Version: workspaceHistoryVersion, Records: []Record{}}, false, nil
	}
	if err != nil {
		return workspaceHistory{}, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxHistoryFileBytes {
		return workspaceHistory{}, false, fmt.Errorf("completion history is invalid or exceeds %d bytes", maxHistoryFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return workspaceHistory{}, false, err
	}
	var value workspaceHistory
	if err := json.Unmarshal(data, &value); err != nil {
		return workspaceHistory{}, false, fmt.Errorf("decode completion history: %w", err)
	}
	if value.Version != workspaceHistoryVersion {
		return workspaceHistory{}, false, fmt.Errorf("unsupported completion history version: %d", value.Version)
	}
	if value.Records == nil {
		value.Records = []Record{}
	}
	return value, true, nil
}

func saveWorkspaceHistory(local workspacestate.Store, value workspaceHistory) error {
	path, err := workspaceHistoryPath(local)
	if err != nil {
		return err
	}
	value.Version = workspaceHistoryVersion
	if value.Records == nil {
		value.Records = []Record{}
	}
	return statepkg.WriteJSONAtomic(path, value, 0600)
}

type sequenceStore struct {
	Version        int    `json:"version"`
	LatestSequence uint64 `json:"latest_sequence"`
}

type SequenceAllocator struct {
	mu     sync.Mutex
	path   string
	latest uint64
}

func DefaultSequencePath() string {
	return filepath.Join(configformat.RootPath(), "state", "agent-completion-sequence.json")
}

func NewSequenceAllocator(path string) (*SequenceAllocator, error) {
	if path == "" {
		path = DefaultSequencePath()
	}
	allocator := &SequenceAllocator{path: path}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return allocator, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSequenceFileBytes {
		return nil, fmt.Errorf("completion sequence metadata is invalid or exceeds %d bytes", maxSequenceFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var stored sequenceStore
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode completion sequence metadata: %w", err)
	}
	if stored.Version != sequenceStoreVersion {
		return nil, fmt.Errorf("unsupported completion sequence metadata version: %d", stored.Version)
	}
	allocator.latest = stored.LatestSequence
	return allocator, nil
}

func (a *SequenceAllocator) Next() (uint64, error) {
	if a == nil {
		return 0, errors.New("completion sequence allocator is unavailable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.latest + 1
	if err := statepkg.WriteJSONAtomic(a.path, sequenceStore{Version: sequenceStoreVersion, LatestSequence: next}, 0600); err != nil {
		return 0, err
	}
	a.latest = next
	return next, nil
}

func (a *SequenceAllocator) EnsureAtLeast(sequence uint64) error {
	if a == nil {
		return errors.New("completion sequence allocator is unavailable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if sequence <= a.latest {
		return nil
	}
	if err := statepkg.WriteJSONAtomic(a.path, sequenceStore{Version: sequenceStoreVersion, LatestSequence: sequence}, 0600); err != nil {
		return err
	}
	a.latest = sequence
	return nil
}

func (a *SequenceAllocator) Current() uint64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.latest
}
