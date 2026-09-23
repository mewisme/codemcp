package instance

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
)

const storeVersion = 1

type Identity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type storeFile struct {
	Version  int      `json:"version"`
	Identity Identity `json:"identity"`
}

type Store struct {
	root     string
	hostname func() (string, error)
	random   io.Reader
	mu       sync.Mutex
}

func DefaultStore() *Store { return NewStore(configformat.RootPath()) }

func NewStore(root string) *Store {
	return &Store{root: filepath.Clean(root), hostname: os.Hostname, random: rand.Reader}
}

func (s *Store) Path() string { return filepath.Join(s.root, "state", "instance.json") }

func (s *Store) LoadOrCreate() (Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity, err := s.loadLocked()
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	id, err := newID(s.random)
	if err != nil {
		return Identity{}, err
	}
	name := "chatgpt-mcp"
	if hostname, hostnameErr := s.hostname(); hostnameErr == nil && strings.TrimSpace(hostname) != "" {
		name = strings.TrimSpace(hostname)
	}
	identity = Identity{ID: id, Name: name, CreatedAt: time.Now().UTC()}
	if err := s.saveLocked(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (s *Store) Load() (Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) SetName(name string) (Identity, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Identity{}, errors.New("instance name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	identity, err := s.loadLocked()
	if errors.Is(err, os.ErrNotExist) {
		id, idErr := newID(s.random)
		if idErr != nil {
			return Identity{}, idErr
		}
		identity = Identity{ID: id, CreatedAt: time.Now().UTC()}
	} else if err != nil {
		return Identity{}, err
	}
	identity.Name = name
	if err := s.saveLocked(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (s *Store) loadLocked() (Identity, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return Identity{}, err
	}
	var stored storeFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return Identity{}, fmt.Errorf("decode instance identity: %w", err)
	}
	if stored.Version != storeVersion {
		return Identity{}, fmt.Errorf("unsupported instance identity version: %d", stored.Version)
	}
	if err := validate(stored.Identity); err != nil {
		return Identity{}, err
	}
	return stored.Identity, nil
}

func (s *Store) saveLocked(identity Identity) error {
	if err := validate(identity); err != nil {
		return err
	}
	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Identity: identity}, "", "  ")
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(s.Path(), append(data, '\n'), 0600)
}

func validate(identity Identity) error {
	if !strings.HasPrefix(identity.ID, "inst_") || len(identity.ID) != len("inst_")+32 {
		return errors.New("invalid instance id")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(identity.ID, "inst_")); err != nil {
		return errors.New("invalid instance id")
	}
	if strings.TrimSpace(identity.Name) == "" {
		return errors.New("instance name is required")
	}
	if identity.CreatedAt.IsZero() {
		return errors.New("instance created_at is required")
	}
	return nil
}

func newID(reader io.Reader) (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return "", fmt.Errorf("generate instance id: %w", err)
	}
	return "inst_" + hex.EncodeToString(raw[:]), nil
}
