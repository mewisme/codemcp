package secretstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
)

const servicePrefix = "codemcp"

type Domain string

const (
	DomainTunnel   Domain = "tunnel"
	DomainOAuth    Domain = "oauth"
	DomainUpstream Domain = "upstream"
	DomainCluster  Domain = "cluster"
)

const (
	Marker       = "<secret-file>"
	LegacyMarker = "<os-keyring>"
)

var (
	ErrNotFound    = errors.New("secret not found")
	ErrUnavailable = errors.New("secret store unavailable")
)

type Error struct {
	Operation string
	Account   string
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return "secret store error"
	}
	message := "secret store " + strings.TrimSpace(e.Operation) + " failed"
	if account := strings.TrimSpace(e.Account); account != "" {
		message += " for account " + account
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Backend interface {
	Set(service, account, value string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

type Store struct {
	service string
	backend Backend
	initErr error
}

type Change struct {
	Name  string
	Value string
}

type snapshot struct {
	name   string
	value  string
	exists bool
}

type backendFactory func(root string) Backend

var (
	backendMu             sync.RWMutex
	defaultBackendFactory backendFactory = func(root string) Backend { return newFileBackend(root) }
)

func New(root string) *Store {
	root = strings.TrimSpace(root)
	if root == "" {
		return &Store{initErr: &Error{Operation: "initialize", Err: errors.Join(ErrUnavailable, errors.New("config root is required"))}}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return &Store{initErr: &Error{Operation: "initialize", Err: errors.Join(ErrUnavailable, err)}}
	}
	absolute = filepath.Clean(absolute)
	sum := sha256.Sum256([]byte(absolute))
	backendMu.RLock()
	factory := defaultBackendFactory
	backendMu.RUnlock()
	return &Store{service: servicePrefix + "/" + hex.EncodeToString(sum[:8]), backend: factory(absolute)}
}

func Name(parts ...string) string {
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		encoded = append(encoded, base64.RawURLEncoding.EncodeToString([]byte(part)))
	}
	return strings.Join(encoded, "/")
}

func AccountName(domain Domain, parts ...string) string {
	values := make([]string, 0, len(parts)+1)
	values = append(values, string(domain))
	values = append(values, parts...)
	return Name(values...)
}

func IsMarker(value string) bool {
	value = strings.TrimSpace(value)
	return value == Marker || value == LegacyMarker
}

func (s *Store) Get(name string) (string, error) {
	if err := s.ready("read", name); err != nil {
		return "", err
	}
	value, err := s.backend.Get(s.service, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrNotFound
		}
		return "", &Error{Operation: "read", Account: name, Err: err}
	}
	return value, nil
}

func (s *Store) Set(name, value string) error {
	if err := s.ready("write", name); err != nil {
		return err
	}
	if value == "" {
		err := s.backend.Delete(s.service, name)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return &Error{Operation: "delete", Account: name, Err: err}
		}
		return nil
	}
	if err := s.backend.Set(s.service, name, value); err != nil {
		return &Error{Operation: "write", Account: name, Err: err}
	}
	return nil
}

func (s *Store) MigrateLegacyFiles() (int, error) {
	if err := s.ready("migrate", ""); err != nil {
		return 0, err
	}
	migrator, ok := s.backend.(interface{ MigrateLegacyFiles() (int, error) })
	if !ok {
		return 0, nil
	}
	count, err := migrator.MigrateLegacyFiles()
	if err != nil {
		return count, &Error{Operation: "migrate", Err: err}
	}
	return count, nil
}

func (s *Store) Apply(changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	if err := s.ready("apply", ""); err != nil {
		return err
	}
	latest := map[string]string{}
	order := make([]string, 0, len(changes))
	for _, change := range changes {
		if _, ok := latest[change.Name]; !ok {
			order = append(order, change.Name)
		}
		latest[change.Name] = change.Value
	}
	normalized := make([]Change, 0, len(order))
	for _, name := range order {
		normalized = append(normalized, Change{Name: name, Value: latest[name]})
	}
	if backend, ok := s.backend.(interface {
		Apply(service string, changes []Change) error
	}); ok {
		if err := backend.Apply(s.service, normalized); err != nil {
			return &Error{Operation: "apply", Err: err}
		}
		return nil
	}
	snapshots := make([]snapshot, 0, len(order))
	for _, name := range order {
		value, err := s.Get(name)
		if errors.Is(err, ErrNotFound) {
			snapshots = append(snapshots, snapshot{name: name})
			continue
		}
		if err != nil {
			return err
		}
		snapshots = append(snapshots, snapshot{name: name, value: value, exists: true})
	}
	applied := 0
	for _, item := range snapshots {
		if err := s.Set(item.name, latest[item.name]); err != nil {
			rollbackErr := s.rollback(snapshots[:applied])
			return errors.Join(err, rollbackErr)
		}
		applied++
	}
	return nil
}

func (s *Store) ready(operation, account string) error {
	if s == nil {
		return &Error{Operation: operation, Account: account, Err: ErrUnavailable}
	}
	if s.initErr != nil {
		return &Error{Operation: operation, Account: account, Err: s.initErr}
	}
	if s.backend == nil || strings.TrimSpace(s.service) == "" {
		return &Error{Operation: operation, Account: account, Err: ErrUnavailable}
	}
	return nil
}

func (s *Store) rollback(items []snapshot) error {
	var result error
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		value := ""
		if item.exists {
			value = item.value
		}
		result = errors.Join(result, s.Set(item.name, value))
	}
	return result
}

func UseMemoryForTesting() func() {
	memory := newMemoryBackend()
	backendMu.Lock()
	previous := defaultBackendFactory
	defaultBackendFactory = func(string) Backend { return memory }
	backendMu.Unlock()
	return func() {
		backendMu.Lock()
		defaultBackendFactory = previous
		backendMu.Unlock()
	}
}

type memoryBackend struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{values: map[string]string{}}
}

func (b *memoryBackend) key(service, account string) string { return service + "\x00" + account }

func (b *memoryBackend) Set(service, account, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values[b.key(service, account)] = value
	return nil
}

func (b *memoryBackend) Get(service, account string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	value, ok := b.values[b.key(service, account)]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (b *memoryBackend) Delete(service, account string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := b.key(service, account)
	if _, ok := b.values[key]; !ok {
		return ErrNotFound
	}
	delete(b.values, key)
	return nil
}
