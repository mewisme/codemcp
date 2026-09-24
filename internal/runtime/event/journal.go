package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/state"
)

const (
	DefaultMaxBytes int64 = 10 << 20
	DefaultMaxFiles       = 5
	MaxEventBytes         = state.DefaultMaxJSONLLineBytes
)

type Options struct {
	MaxBytes int64
	MaxFiles int
	Metadata Metadata
}

type Journal struct {
	path     string
	maxBytes int64
	maxFiles int
	metadata Metadata
	mu       sync.Mutex
}

func NewJournal(root string, options Options) (*Journal, error) {
	if root == "" {
		return nil, errors.New("runtime event journal root is required")
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	if options.MaxFiles <= 0 {
		options.MaxFiles = DefaultMaxFiles
	}
	logRoot := filepath.Join(root, "logs")
	if err := os.MkdirAll(logRoot, 0700); err != nil {
		return nil, err
	}
	return &Journal{path: filepath.Join(logRoot, "runtime.jsonl"), maxBytes: options.MaxBytes, maxFiles: options.MaxFiles, metadata: options.Metadata}, nil
}

func (j *Journal) Path() string {
	if j == nil {
		return ""
	}
	return j.path
}

func (j *Journal) WriteEvent(event logger.Event) error {
	if j == nil {
		return nil
	}
	return j.Append(fromLoggerEvent(event, j.metadata))
}

func (j *Journal) Append(event Event) error {
	if j == nil {
		return nil
	}
	if event.Version == 0 {
		event.Version = Version
	}
	if event.Version != Version {
		return fmt.Errorf("unsupported runtime event version: %d", event.Version)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	incoming := int64(len(data) + 1)
	if incoming > MaxEventBytes {
		return fmt.Errorf("runtime event exceeds %d byte limit", MaxEventBytes)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.rotateIfNeeded(incoming); err != nil {
		return err
	}
	return state.AppendJSONL(j.path, event, 0600, MaxEventBytes)
}

func (j *Journal) Clear() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	var clearErr error
	for _, path := range append(j.FilesOldestFirst(), j.path) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			clearErr = errors.Join(clearErr, err)
		}
	}
	return clearErr
}

func (j *Journal) rotateIfNeeded(incoming int64) error {
	info, err := os.Stat(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size()+incoming <= j.maxBytes {
		return nil
	}
	if j.maxFiles <= 1 {
		return os.Remove(j.path)
	}
	_ = os.Remove(j.rotatedPath(j.maxFiles - 1))
	for index := j.maxFiles - 2; index >= 1; index-- {
		from := j.rotatedPath(index)
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, j.rotatedPath(index+1)); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(j.path, j.rotatedPath(1))
}

func (j *Journal) rotatedPath(index int) string { return fmt.Sprintf("%s.%d", j.path, index) }

func (j *Journal) FilesOldestFirst() []string {
	if j == nil {
		return nil
	}
	files := make([]string, 0, j.maxFiles)
	for index := j.maxFiles - 1; index >= 1; index-- {
		path := j.rotatedPath(index)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	if _, err := os.Stat(j.path); err == nil {
		files = append(files, j.path)
	}
	return files
}

func ReadFile(path string, fn func(Event) error) error {
	return state.ReadJSONL(path, MaxEventBytes, func(line []byte) error {
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		if event.Version == 0 {
			event.Version = Version
		}
		if event.Version != Version {
			return fmt.Errorf("unsupported runtime event version: %d", event.Version)
		}
		if fn != nil {
			return fn(event)
		}
		return nil
	})
}
