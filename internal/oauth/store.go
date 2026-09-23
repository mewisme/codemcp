package oauth

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	statepkg "go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const storeVersion = 1

type diskStore struct {
	Version     int                   `json:"version"`
	Credentials map[string]Credential `json:"credentials"`
}

func (s *Store) SecretEntries() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readDiskLocked()
	if err != nil {
		return nil, err
	}
	entries := []string{}
	for id, credential := range state.Credentials {
		if secretstore.IsMarker(credential.ClientSecret) {
			entries = append(entries, oauthSecretName(id, "client-secret"))
		}
		if secretstore.IsMarker(credential.AccessToken) {
			entries = append(entries, oauthSecretName(id, "access-token"))
		}
		if secretstore.IsMarker(credential.RefreshToken) {
			entries = append(entries, oauthSecretName(id, "refresh-token"))
		}
	}
	return entries, nil
}

func (s *Store) Get(id string) (Credential, error) {
	span := tracepkg.StartObserver(s.trace, "OAUTH", "oauth.store.get", "Loading OAuth authorization", tracepkg.String("server", id), tracepkg.String("store_path", s.path))
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readLocked()
	if err != nil {
		span.FailMessage("OAuth authorization load failed", errors.New("OAuth store load failed"))
		return Credential{}, err
	}
	value, ok := state.Credentials[id]
	if !ok {
		span.EndMessage("OAuth authorization not configured", tracepkg.Bool("configured", false))
		return Credential{}, ErrCredentialNotFound
	}
	span.EndMessage("OAuth authorization loaded", tracepkg.Bool("configured", true), tracepkg.URL("issuer", value.Issuer), tracepkg.String("registration", value.Registration), tracepkg.Any("scopes", append([]string(nil), value.Scopes...)), tracepkg.Int("scope_count", len(value.Scopes)), tracepkg.Bool("has_refresh", value.RefreshToken != ""), tracepkg.Bool("expires", !value.ExpiresAt.IsZero()))
	return cloneCredential(value), nil
}

func (s *Store) Put(value Credential) error {
	span := tracepkg.StartObserver(s.trace, "OAUTH", "oauth.store.put", "Persisting OAuth authorization", tracepkg.String("server", value.ServerID), tracepkg.String("store_path", s.path), tracepkg.URL("issuer", value.Issuer), tracepkg.String("registration", value.Registration), tracepkg.Any("scopes", append([]string(nil), value.Scopes...)), tracepkg.Bool("has_refresh", value.RefreshToken != ""), tracepkg.Bool("expires", !value.ExpiresAt.IsZero()))
	if value.ServerID == "" {
		err := errors.New("oauth server id is required")
		span.FailMessage("OAuth authorization persistence validation failed", err)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := s.readLocked()
	if err != nil {
		span.FailMessage("OAuth authorization persistence failed", errors.New("OAuth store load failed"))
		return err
	}
	next := cloneDiskStore(previous)
	value.UpdatedAt = time.Now().UTC()
	next.Credentials[value.ServerID] = cloneCredential(value)
	if err := s.writeLocked(previous, next); err != nil {
		span.FailMessage("OAuth authorization persistence failed", errors.New("OAuth store persistence failed"))
		return err
	}
	span.EndMessage("OAuth authorization persisted", tracepkg.Bool("configured", true), tracepkg.Int("entry_count", len(next.Credentials)))
	return nil
}

func (s *Store) Delete(id string) error {
	span := tracepkg.StartObserver(s.trace, "OAUTH", "oauth.store.delete", "Deleting OAuth authorization", tracepkg.String("server", id), tracepkg.String("store_path", s.path))
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := s.readLocked()
	if err != nil {
		span.FailMessage("OAuth authorization deletion failed", errors.New("OAuth store load failed"))
		return err
	}
	if _, ok := previous.Credentials[id]; !ok {
		span.EndMessage("OAuth authorization already absent", tracepkg.Bool("configured", false), tracepkg.Bool("deleted", false), tracepkg.Int("entry_count", len(previous.Credentials)))
		return nil
	}
	next := cloneDiskStore(previous)
	delete(next.Credentials, id)
	if err := s.writeLocked(previous, next); err != nil {
		span.FailMessage("OAuth authorization deletion failed", errors.New("OAuth store persistence failed"))
		return err
	}
	span.EndMessage("OAuth authorization deleted", tracepkg.Bool("configured", false), tracepkg.Bool("deleted", true), tracepkg.Int("entry_count", len(next.Credentials)))
	return nil
}

func (s *Store) Status(id string) (Status, error) {
	span := tracepkg.StartObserver(s.trace, "OAUTH", "oauth.store.status", "Reading OAuth authorization status", tracepkg.String("server", id), tracepkg.String("store_path", s.path))
	credential, err := s.Get(id)
	if errors.Is(err, ErrCredentialNotFound) {
		span.EndMessage("OAuth authorization status loaded", tracepkg.Bool("configured", false))
		return Status{ServerID: id}, nil
	}
	if err != nil {
		span.FailMessage("OAuth authorization status failed", errors.New("OAuth store status failed"))
		return Status{}, err
	}
	status := Status{
		ServerID: id, Configured: true, Issuer: credential.Issuer, Resource: credential.Resource,
		Registration: credential.Registration, ClientID: credential.ClientID, Scopes: append([]string(nil), credential.Scopes...),
		HasRefreshToken: credential.RefreshToken != "",
	}
	if !credential.ExpiresAt.IsZero() {
		expiresAt := credential.ExpiresAt
		status.ExpiresAt = &expiresAt
		status.Expired = !time.Now().Before(expiresAt)
	}
	span.EndMessage("OAuth authorization status loaded", tracepkg.Bool("configured", true), tracepkg.URL("issuer", status.Issuer), tracepkg.URL("resource", status.Resource), tracepkg.String("registration", status.Registration), tracepkg.String("client_id", status.ClientID), tracepkg.Any("scopes", append([]string(nil), status.Scopes...)), tracepkg.Int("scope_count", len(status.Scopes)), tracepkg.Bool("has_refresh", status.HasRefreshToken), tracepkg.Bool("expired", status.Expired))
	return status, nil
}

func (s *Store) readLocked() (diskStore, error) {
	raw, err := s.readDiskLocked()
	if err != nil {
		return diskStore{}, err
	}
	state := cloneDiskStore(raw)
	migratedCount := 0
	for id, credential := range state.Credentials {
		var migrated bool
		credential.ClientSecret, migrated, err = s.loadSecret(id, "client-secret", raw.Credentials[id].ClientSecret)
		if err != nil {
			return diskStore{}, err
		}
		if migrated {
			migratedCount++
		}
		credential.AccessToken, migrated, err = s.loadSecret(id, "access-token", raw.Credentials[id].AccessToken)
		if err != nil {
			return diskStore{}, err
		}
		if migrated {
			migratedCount++
		}
		credential.RefreshToken, migrated, err = s.loadSecret(id, "refresh-token", raw.Credentials[id].RefreshToken)
		if err != nil {
			return diskStore{}, err
		}
		if migrated {
			migratedCount++
		}
		state.Credentials[id] = credential
	}
	if migratedCount > 0 {
		span := tracepkg.StartObserver(s.trace, "OAUTH", "oauth.store.protected-values.migrate", "Migrating OAuth protected values", tracepkg.String("store_path", s.path), tracepkg.Int("protected_value_count", migratedCount))
		if err := s.writeLocked(raw, state); err != nil {
			span.FailMessage("OAuth protected-value migration failed", errors.New("OAuth protected-value migration failed"))
			return diskStore{}, fmt.Errorf("migrate OAuth secrets to secret file store: %w", err)
		}
		span.EndMessage("OAuth protected values migrated", tracepkg.Int("protected_value_count", migratedCount))
	}
	return state, nil
}

func (s *Store) readDiskLocked() (diskStore, error) {
	span := tracepkg.StartObserver(s.trace, "OAUTH", "oauth.store.read", "Reading OAuth store", tracepkg.String("store_path", s.path))
	state := diskStore{Version: storeVersion, Credentials: map[string]Credential{}}
	root, err := s.openRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		span.EndMessage("OAuth store not yet created", tracepkg.Bool("exists", false), tracepkg.Int64("bytes", 0), tracepkg.Int("store_version", storeVersion), tracepkg.Int("entry_count", 0))
		return state, nil
	}
	if err != nil {
		span.FailMessage("OAuth store read failed", errors.New("OAuth store read failed"))
		return diskStore{}, fmt.Errorf("read oauth store: %w", err)
	}
	defer root.Close()
	data, _, err := readOAuthFile(root, s.name)
	if errors.Is(err, os.ErrNotExist) {
		span.EndMessage("OAuth store not yet created", tracepkg.Bool("exists", false), tracepkg.Int64("bytes", 0), tracepkg.Int("store_version", storeVersion), tracepkg.Int("entry_count", 0))
		return state, nil
	}
	if err != nil {
		span.FailMessage("OAuth store read failed", errors.New("OAuth store read failed"))
		return diskStore{}, fmt.Errorf("read oauth store: %w", err)
	}
	if err := configformat.UnmarshalPath(s.path, data, &state); err != nil {
		span.FailMessage("OAuth store decode failed", errors.New("OAuth store decode failed"), tracepkg.Int64("bytes", int64(len(data))))
		return diskStore{}, fmt.Errorf("decode oauth store: %w", err)
	}
	if state.Version != storeVersion {
		err := fmt.Errorf("unsupported oauth store version: %d", state.Version)
		span.FailMessage("OAuth store version unsupported", err, tracepkg.Int("store_version", state.Version), tracepkg.Int("current_version", storeVersion))
		return diskStore{}, err
	}
	if state.Credentials == nil {
		state.Credentials = map[string]Credential{}
	}
	format := ""
	if detected, detectErr := configformat.Detect(s.path); detectErr == nil {
		format = string(detected)
	}
	span.EndMessage("OAuth store read", tracepkg.Bool("exists", true), tracepkg.String("format", format), tracepkg.Int64("bytes", int64(len(data))), tracepkg.Int("store_version", state.Version), tracepkg.Int("entry_count", len(state.Credentials)))
	return state, nil
}

func (s *Store) loadSecret(id, field, stored string) (string, bool, error) {
	if stored == "" {
		return "", false, nil
	}
	if !secretstore.IsMarker(stored) {
		return stored, true, nil
	}
	value, err := s.secrets.Get(oauthSecretName(id, field))
	if errors.Is(err, secretstore.ErrNotFound) {
		return "", false, fmt.Errorf("OAuth %s for %s is configured but missing from secret file store", field, id)
	}
	if err != nil {
		return "", false, err
	}
	return value, false, nil
}

func (s *Store) writeLocked(previous, next diskStore) error {
	span := tracepkg.StartObserver(s.trace, "OAUTH", "oauth.store.write", "Writing OAuth store", tracepkg.String("store_path", s.path), tracepkg.Bool("atomic", true), tracepkg.Int("entry_count", len(next.Credentials)))
	persisted := cloneDiskStore(next)
	for id, credential := range persisted.Credentials {
		if credential.ClientSecret != "" {
			credential.ClientSecret = secretstore.Marker
		}
		if credential.AccessToken != "" {
			credential.AccessToken = secretstore.Marker
		}
		if credential.RefreshToken != "" {
			credential.RefreshToken = secretstore.Marker
		}
		persisted.Credentials[id] = credential
	}
	data, err := configformat.MarshalPath(s.path, persisted)
	if err != nil {
		span.FailMessage("OAuth store encode failed", errors.New("OAuth store encode failed"))
		return fmt.Errorf("encode oauth store: %w", err)
	}
	root, err := s.openRoot(true)
	if err != nil {
		span.FailMessage("OAuth store directory creation failed", errors.New("OAuth store directory creation failed"))
		return fmt.Errorf("create oauth store directory: %w", err)
	}
	defer root.Close()
	snapshot, err := snapshotOAuthFile(root, s.name)
	if err != nil {
		span.FailMessage("OAuth store snapshot failed", errors.New("OAuth store snapshot failed"))
		return err
	}
	if err := statepkg.WriteFileAtomicRoot(root, s.name, data, 0600); err != nil {
		span.FailMessage("OAuth store write failed", errors.New("OAuth store write failed"), tracepkg.Int64("bytes", int64(len(data))))
		return fmt.Errorf("write oauth store: %w", err)
	}
	changes := oauthSecretChanges(previous, next)
	if err := s.secrets.Apply(changes); err != nil {
		restoreErr := restoreOAuthFile(root, s.name, snapshot)
		span.FailMessage("OAuth protected-value persistence failed", errors.New("OAuth protected-value persistence failed"), tracepkg.Int64("bytes", int64(len(data))), tracepkg.Int("protected_value_changes", len(changes)), tracepkg.Bool("store_rollback_succeeded", restoreErr == nil))
		return errors.Join(err, restoreErr)
	}
	format := ""
	if detected, detectErr := configformat.Detect(s.path); detectErr == nil {
		format = string(detected)
	}
	span.EndMessage("OAuth store written", tracepkg.String("format", format), tracepkg.Int64("bytes", int64(len(data))), tracepkg.Int("store_version", storeVersion), tracepkg.Int("entry_count", len(next.Credentials)), tracepkg.Int("protected_value_changes", len(changes)))
	return nil
}

func oauthSecretChanges(previous, next diskStore) []secretstore.Change {
	ids := map[string]bool{}
	for id := range previous.Credentials {
		ids[id] = true
	}
	for id := range next.Credentials {
		ids[id] = true
	}
	changes := []secretstore.Change{}
	for id := range ids {
		old := previous.Credentials[id]
		value, exists := next.Credentials[id]
		for _, field := range []struct{ name, old, next string }{
			{"client-secret", old.ClientSecret, value.ClientSecret}, {"access-token", old.AccessToken, value.AccessToken}, {"refresh-token", old.RefreshToken, value.RefreshToken},
		} {
			if field.next != "" || field.old != "" || !exists {
				changes = append(changes, secretstore.Change{Name: oauthSecretName(id, field.name), Value: field.next})
			}
		}
	}
	return changes
}

func oauthSecretName(id, field string) string { return secretstore.Name("oauth", id, field) }

func cloneDiskStore(value diskStore) diskStore {
	result := diskStore{Version: value.Version, Credentials: make(map[string]Credential, len(value.Credentials))}
	for id, credential := range value.Credentials {
		result.Credentials[id] = cloneCredential(credential)
	}
	return result
}

func cloneCredential(value Credential) Credential {
	value.Scopes = append([]string(nil), value.Scopes...)
	return value
}

type oauthFileSnapshot struct {
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

func snapshotOAuthFile(root *os.Root, path string) (oauthFileSnapshot, error) {
	data, mode, err := readOAuthFile(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return oauthFileSnapshot{}, nil
	}
	if err != nil {
		return oauthFileSnapshot{}, err
	}
	return oauthFileSnapshot{exists: true, data: data, mode: mode}, nil
}

func restoreOAuthFile(root *os.Root, path string, snapshot oauthFileSnapshot) error {
	if !snapshot.exists {
		if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return statepkg.WriteFileAtomicRoot(root, path, snapshot.data, snapshot.mode)
}

func readOAuthFile(root *os.Root, path string) ([]byte, os.FileMode, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("oauth store path is not a regular file: %s", path)
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
		return nil, 0, fmt.Errorf("oauth store path changed while opening: %s", path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	return data, openedInfo.Mode().Perm(), nil
}
