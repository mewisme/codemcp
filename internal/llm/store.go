package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/oslock"
	"go.mewis.me/codemcp/internal/state"
)

const (
	StoreVersion      = 1
	StoreRelativePath = "llm/providers.json"
	maxStoreBytes     = 4 << 20
	storeReadAttempts = 3
)

const storeLockRelativePath = ".llm-providers.lock"

var errStoreChanged = errors.New("llm provider store changed while opening")

type Store struct {
	root string
}

type diskStore struct {
	Version        int        `json:"version"`
	ActiveProvider ProviderID `json:"active_provider"`
	Providers      []Provider `json:"providers"`
}

func NewStore(root string) *Store {
	return &Store{root: strings.TrimSpace(root)}
}

func (s *Store) Path() string {
	if s == nil || s.root == "" {
		return ""
	}
	return filepath.Join(s.root, filepath.FromSlash(StoreRelativePath))
}

func (s *Store) Load() (Catalog, error) {
	if err := s.validateRoot(); err != nil {
		return Catalog{}, err
	}
	for attempt := 0; attempt < storeReadAttempts; attempt++ {
		value, err := s.loadOnce()
		if !errors.Is(err, errStoreChanged) {
			return value, err
		}
	}
	return Catalog{}, errStoreChanged
}

func (s *Store) Save(value Catalog) error {
	if err := s.validateRoot(); err != nil {
		return err
	}
	normalized, err := NormalizeCatalog(cloneCatalog(value))
	if err != nil {
		return err
	}
	lock, err := s.acquireMutationLock()
	if err != nil {
		return err
	}
	defer lock.Release()
	return s.writeLocked(normalized)
}

// Update serializes read-modify-write mutations across Store instances and
// processes so callers do not lose unrelated provider changes.
func (s *Store) Update(mutate func(Catalog) (Catalog, error)) (Catalog, error) {
	if err := s.validateRoot(); err != nil {
		return Catalog{}, err
	}
	if mutate == nil {
		return Catalog{}, errors.New("llm provider store update requires mutation function")
	}
	lock, err := s.acquireMutationLock()
	if err != nil {
		return Catalog{}, err
	}
	defer lock.Release()

	current, err := s.loadLocked()
	if err != nil {
		return Catalog{}, err
	}
	next, err := mutate(cloneCatalog(current))
	if err != nil {
		return Catalog{}, err
	}
	next, err = NormalizeCatalog(next)
	if err != nil {
		return Catalog{}, err
	}
	if err := s.writeLocked(next); err != nil {
		return Catalog{}, err
	}
	return cloneCatalog(next), nil
}

// NormalizePortableStoreJSON validates the provider-store schema, rejects
// secret-bearing or unknown fields, reconciles missing core providers in
// memory, and returns canonical portable JSON without mutating the source.
func NormalizePortableStoreJSON(data []byte) ([]byte, error) {
	stored, err := decodeDiskStore(data)
	if err != nil {
		return nil, err
	}
	value, err := reconcileDiskStore(stored)
	if err != nil {
		return nil, err
	}
	return encodeDiskStore(value)
}

func (s *Store) loadOnce() (Catalog, error) {
	root, err := os.OpenRoot(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultCatalog(), nil
	}
	if err != nil {
		return Catalog{}, err
	}
	defer root.Close()
	return loadFromRoot(root)
}

func (s *Store) loadLocked() (Catalog, error) {
	root, err := os.OpenRoot(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultCatalog(), nil
	}
	if err != nil {
		return Catalog{}, err
	}
	defer root.Close()
	return loadFromRoot(root)
}

func loadFromRoot(root *os.Root) (Catalog, error) {
	data, err := readStoreFile(root, filepath.FromSlash(StoreRelativePath))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultCatalog(), nil
	}
	if err != nil {
		return Catalog{}, err
	}
	stored, err := decodeDiskStore(data)
	if err != nil {
		return Catalog{}, err
	}
	return reconcileDiskStore(stored)
}

func (s *Store) writeLocked(value Catalog) error {
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := encodeDiskStore(value)
	if err != nil {
		return err
	}
	return state.WriteFileAtomicRoot(root, filepath.FromSlash(StoreRelativePath), data, 0600)
}

func (s *Store) acquireMutationLock() (*oslock.Lock, error) {
	lock, err := oslock.Acquire(filepath.Join(s.root, storeLockRelativePath), oslock.Exclusive)
	if err != nil {
		return nil, fmt.Errorf("lock llm provider store: %w", err)
	}
	return lock, nil
}

func (s *Store) validateRoot() error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return errors.New("llm provider store root is required")
	}
	return nil
}

func decodeDiskStore(data []byte) (diskStore, error) {
	if len(data) == 0 {
		return diskStore{}, errors.New("llm provider store is empty")
	}
	if len(data) > maxStoreBytes {
		return diskStore{}, errors.New("llm provider store exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored diskStore
	if err := decoder.Decode(&stored); err != nil {
		return diskStore{}, fmt.Errorf("decode llm provider store: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return diskStore{}, errors.New("decode llm provider store: trailing JSON value")
		}
		return diskStore{}, fmt.Errorf("decode llm provider store trailing data: %w", err)
	}
	if stored.Version != StoreVersion {
		return diskStore{}, fmt.Errorf("unsupported llm provider store version: %d", stored.Version)
	}
	return stored, nil
}

func encodeDiskStore(value Catalog) ([]byte, error) {
	normalized, err := NormalizeCatalog(cloneCatalog(value))
	if err != nil {
		return nil, err
	}
	return state.MarshalJSON(diskStore{
		Version:        StoreVersion,
		ActiveProvider: normalized.ActiveProvider,
		Providers:      normalized.Providers,
	})
}

func reconcileDiskStore(stored diskStore) (Catalog, error) {
	value := Catalog{
		ActiveProvider: stored.ActiveProvider,
		Providers:      append([]Provider(nil), stored.Providers...),
	}
	if value.ActiveProvider == "" {
		value.ActiveProvider = OpenRouterID
	}
	seen := make(map[ProviderID]struct{}, len(value.Providers))
	for _, provider := range value.Providers {
		id, err := NormalizeProviderID(string(provider.ID))
		if err != nil {
			return Catalog{}, err
		}
		if _, exists := seen[id]; exists {
			return Catalog{}, NewError(ErrorDuplicateID, "providers", fmt.Sprintf("duplicate provider id %q", id))
		}
		seen[id] = struct{}{}
	}
	if _, exists := seen[OpenRouterID]; !exists {
		value.Providers = append(value.Providers, DefaultOpenRouter())
	}
	if _, exists := seen[OllamaID]; !exists {
		value.Providers = append(value.Providers, DefaultOllama())
	}
	return NormalizeCatalog(value)
}

func readStoreFile(root *os.Root, path string) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("llm provider store path is not a regular file: %s", path)
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, errStoreChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStoreBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxStoreBytes {
		return nil, errors.New("llm provider store exceeds size limit")
	}
	return data, nil
}

func cloneCatalog(value Catalog) Catalog {
	value.Providers = append([]Provider(nil), value.Providers...)
	return value
}
