package upstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/state"
)

const storeVersion = 1

type Store struct {
	Path    string
	root    string
	name    string
	secrets *secretstore.Store
}

type diskStore struct {
	Version   int      `json:"version"`
	Upstreams []Server `json:"upstreams"`
}

func NewStore(path string) *Store {
	path = filepath.Clean(path)
	root := filepath.Dir(path)
	return &Store{Path: path, root: root, name: filepath.Base(path), secrets: secretstore.New(root)}
}

func (s *Store) SecretEntries() ([]string, error) {
	servers, err := s.readDisk()
	if err != nil {
		return nil, err
	}
	entries := []string{}
	for _, server := range servers {
		for key, value := range server.Headers {
			if SensitiveConfigKey(key) && secretstore.IsMarker(value) {
				entries = append(entries, upstreamSecretName(server.ID, "header", key))
			}
		}
		for key, value := range server.Env {
			if SensitiveConfigKey(key) && secretstore.IsMarker(value) {
				entries = append(entries, upstreamSecretName(server.ID, "env", key))
			}
		}
	}
	return entries, nil
}

func (s *Store) Load() ([]Server, error) {
	raw, err := s.readDisk()
	if err != nil {
		return nil, err
	}
	servers := cloneServers(raw)
	migrate := false
	for index := range servers {
		server := &servers[index]
		for key, stored := range server.Headers {
			if !SensitiveConfigKey(key) || stored == "" {
				continue
			}
			if !secretstore.IsMarker(stored) {
				migrate = true
				continue
			}
			value, err := s.secrets.Get(upstreamSecretName(server.ID, "header", key))
			if errors.Is(err, secretstore.ErrNotFound) {
				return nil, fmt.Errorf("upstream header %s for %s is configured but missing from secret file store", key, server.ID)
			}
			if err != nil {
				return nil, err
			}
			server.Headers[key] = value
		}
		for key, stored := range server.Env {
			if !SensitiveConfigKey(key) || stored == "" {
				continue
			}
			if !secretstore.IsMarker(stored) {
				migrate = true
				continue
			}
			value, err := s.secrets.Get(upstreamSecretName(server.ID, "env", key))
			if errors.Is(err, secretstore.ErrNotFound) {
				return nil, fmt.Errorf("upstream env %s for %s is configured but missing from secret file store", key, server.ID)
			}
			if err != nil {
				return nil, err
			}
			server.Env[key] = value
		}
	}
	if migrate {
		if err := s.saveWithPrevious(raw, servers); err != nil {
			return nil, fmt.Errorf("migrate upstream secrets to secret file store: %w", err)
		}
	}
	return servers, nil
}

func (s *Store) Save(servers []Server) error {
	previous, err := s.readDisk()
	if err != nil {
		return err
	}
	return s.saveWithPrevious(previous, servers)
}

func (s *Store) readDisk() ([]Server, error) {
	root, err := s.openRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return []Server{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, _, err := readStoreFile(root, s.name)
	if errors.Is(err, os.ErrNotExist) {
		return []Server{}, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if _, legacy := envelope["servers"]; legacy {
		return nil, errors.New("legacy upstream store schema is unsupported; migrate upstream.json to upstreams.json before runtime startup")
	}
	var stored diskStore
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}
	if stored.Version != 0 && stored.Version != storeVersion {
		return nil, fmt.Errorf("unsupported upstream store version: %d", stored.Version)
	}
	if stored.Upstreams == nil {
		stored.Upstreams = []Server{}
	}
	return stored.Upstreams, nil
}

func (s *Store) saveWithPrevious(previous, servers []Server) error {
	persisted := cloneServers(servers)
	for index := range persisted {
		for key, value := range persisted[index].Headers {
			if SensitiveConfigKey(key) && value != "" {
				persisted[index].Headers[key] = secretstore.Marker
			}
		}
		for key, value := range persisted[index].Env {
			if SensitiveConfigKey(key) && value != "" {
				persisted[index].Env[key] = secretstore.Marker
			}
		}
	}
	root, err := s.openRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := state.MarshalJSON(diskStore{Version: storeVersion, Upstreams: persisted})
	if err != nil {
		return err
	}
	snapshot, err := snapshotStoreFile(root, s.name)
	if err != nil {
		return err
	}
	if err := state.WriteFileAtomicRoot(root, s.name, data, 0600); err != nil {
		return err
	}
	if err := s.secrets.Apply(upstreamSecretChanges(previous, servers)); err != nil {
		return errors.Join(err, restoreStoreFile(root, s.name, snapshot))
	}
	return nil
}

func upstreamSecretChanges(previous, next []Server) []secretstore.Change {
	old := serversByID(previous)
	current := serversByID(next)
	ids := map[string]bool{}
	for id := range old {
		ids[id] = true
	}
	for id := range current {
		ids[id] = true
	}
	changes := []secretstore.Change{}
	for id := range ids {
		changes = append(changes, mapSecretChanges(id, "header", old[id].Headers, current[id].Headers)...)
		changes = append(changes, mapSecretChanges(id, "env", old[id].Env, current[id].Env)...)
	}
	return changes
}

func mapSecretChanges(id, kind string, previous, next map[string]string) []secretstore.Change {
	keys := map[string]bool{}
	for key := range previous {
		if SensitiveConfigKey(key) {
			keys[key] = true
		}
	}
	for key := range next {
		if SensitiveConfigKey(key) {
			keys[key] = true
		}
	}
	changes := make([]secretstore.Change, 0, len(keys))
	for key := range keys {
		changes = append(changes, secretstore.Change{Name: upstreamSecretName(id, kind, key), Value: next[key]})
	}
	return changes
}

func upstreamSecretName(id, kind, key string) string {
	return secretstore.AccountName(secretstore.DomainUpstream, id, kind, key)
}

func serversByID(values []Server) map[string]Server {
	result := make(map[string]Server, len(values))
	for _, server := range values {
		result[server.ID] = server
	}
	return result
}

func cloneServers(values []Server) []Server {
	result := make([]Server, len(values))
	for index, server := range values {
		server.Args = append([]string(nil), server.Args...)
		server.Tools = append([]string(nil), server.Tools...)
		server.DisabledTools = append([]string(nil), server.DisabledTools...)
		server.Headers = cloneStringMap(server.Headers)
		server.Env = cloneStringMap(server.Env)
		result[index] = server
	}
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

type storeFileSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func (s *Store) openRoot(create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(s.root, 0700); err != nil {
			return nil, err
		}
	}
	return os.OpenRoot(s.root)
}

func snapshotStoreFile(root *os.Root, path string) (storeFileSnapshot, error) {
	data, mode, err := readStoreFile(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return storeFileSnapshot{}, nil
	}
	if err != nil {
		return storeFileSnapshot{}, err
	}
	return storeFileSnapshot{exists: true, data: data, mode: mode}, nil
}
func restoreStoreFile(root *os.Root, path string, snapshot storeFileSnapshot) error {
	if !snapshot.exists {
		if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return state.WriteFileAtomicRoot(root, path, snapshot.data, snapshot.mode)
}

func readStoreFile(root *os.Root, path string) ([]byte, os.FileMode, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("upstream store path is not a regular file: %s", path)
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, 0, fmt.Errorf("upstream store path changed while opening: %s", path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	return data, openedInfo.Mode().Perm(), nil
}
