package completion

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/configformat"
	statepkg "go.mewis.me/codemcp/internal/state"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	workspaceHistoryVersion = 1
	sequenceStoreVersion    = 1
	historyFileName         = "agent-completions.json"
	archiveFileName         = "agent-completions.archive.jsonl"
	maxHistoryFileBytes     = 2 << 20
	maxSequenceFileBytes    = 64 << 10
	maxArchiveRecordBytes   = 16 << 10
)

type workspaceHistory struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

func workspaceHistoryPath(local workspacestate.Store) (string, error) {
	return local.StatePath(historyFileName)
}

func workspaceArchivePath(local workspacestate.Store) (string, error) {
	return local.StatePath(archiveFileName)
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

func loadWorkspaceRecords(local workspacestate.Store) ([]Record, bool, error) {
	history, hotExists, err := loadWorkspaceHistory(local)
	if err != nil {
		return nil, false, err
	}
	archive, err := loadWorkspaceArchive(local)
	if err != nil {
		return nil, false, err
	}
	return mergeCompletionRecords(archive, history.Records), hotExists || len(archive) > 0, nil
}

func loadWorkspaceArchive(local workspacestate.Store) ([]Record, error) {
	records, _, err := scanWorkspaceArchive(local)
	return records, err
}

func scanWorkspaceArchive(local workspacestate.Store) ([]Record, bool, error) {
	path, err := workspaceArchivePath(local)
	if err != nil {
		return nil, false, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []Record{}, false, nil
	} else if err != nil {
		return nil, false, err
	}
	records := []Record{}
	tailIssue, err := statepkg.ReadJSONLRecoverTail(path, maxArchiveRecordBytes, func(line []byte) error {
		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("decode completion archive: %w", err)
		}
		if strings.TrimSpace(record.ID) == "" || record.Sequence == 0 || strings.TrimSpace(record.WorkspaceID) == "" {
			return errors.New("completion archive contains invalid record")
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return records, tailIssue, nil
}

func appendWorkspaceArchive(local workspacestate.Store, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	path, err := workspaceArchivePath(local)
	if err != nil {
		return err
	}
	_, tailIssue, err := scanWorkspaceArchive(local)
	if err != nil {
		return err
	}
	repaired, err := statepkg.RepairJSONLTail(path)
	if err != nil {
		return fmt.Errorf("repair completion archive tail: %w", err)
	}
	if tailIssue && !repaired {
		if err := statepkg.DropJSONLTailRecord(path); err != nil {
			return fmt.Errorf("drop corrupt completion archive tail: %w", err)
		}
	}
	for _, record := range records {
		if err := statepkg.AppendJSONL(path, record, 0600, maxArchiveRecordBytes); err != nil {
			return fmt.Errorf("append completion archive: %w", err)
		}
	}
	return nil
}

func archiveTailIssue(local workspacestate.Store) (bool, error) {
	_, tailIssue, err := scanWorkspaceArchive(local)
	return tailIssue, err
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
