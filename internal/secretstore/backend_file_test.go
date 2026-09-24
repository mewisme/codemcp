package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestFileBackendRoundTripAndPersistence(t *testing.T) {
	root := t.TempDir()
	name := Name("tunnel", "runtime-key")
	first := New(root)
	if err := first.Set(name, "secret-value"); err != nil {
		t.Fatal(err)
	}
	second := New(root)
	value, err := second.Get(name)
	if err != nil || value != "secret-value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	backend, ok := second.backend.(*fileBackend)
	if !ok {
		t.Fatalf("backend=%T", second.backend)
	}
	path, err := backend.path(second.service, name)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(root, "state", "secrets") {
		t.Fatalf("secret path=%q", path)
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("secret extension=%q want .json", filepath.Ext(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("secret envelope is not JSON: %q", raw)
	}
	envelope, err := decodeSecretEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Version != secretEnvelopeVersion || envelope.Algorithm != secretAlgorithm || envelope.KeyID != keyIDFromRelative(path) {
		t.Fatalf("secret envelope=%#v", envelope)
	}
	if strings.Contains(string(raw), "secret-value") {
		t.Fatalf("plaintext leaked into encrypted secret file: %q", raw)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("secret mode=%#o want 0600", info.Mode().Perm())
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if dirInfo.Mode().Perm() != 0700 {
			t.Fatalf("secret directory mode=%#o want 0700", dirInfo.Mode().Perm())
		}
		keyInfo, err := os.Stat(filepath.Join(root, "state", "secrets", masterKeyName))
		if err != nil {
			t.Fatal(err)
		}
		if keyInfo.Mode().Perm() != 0600 {
			t.Fatalf("master key mode=%#o want 0600", keyInfo.Mode().Perm())
		}
	}
	if err := second.Set(name, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestFileBackendRequiresExplicitLegacyMigration(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	backend := store.backend.(*fileBackend)
	name := Name("tunnel", "admin-key")
	currentPath, err := backend.path(store.service, name)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := strings.TrimSuffix(currentPath, ".json") + ".secret"
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy-plaintext"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime read legacy secret err=%v", err)
	}
	migrated, err := store.MigrateLegacyFiles()
	if err != nil || migrated != 1 {
		t.Fatalf("migrated=%d err=%v", migrated, err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy secret still exists: %v", err)
	}
	raw, err := os.ReadFile(currentPath)
	if err != nil || !json.Valid(raw) || strings.Contains(string(raw), "legacy-plaintext") {
		t.Fatalf("migrated envelope=%q err=%v", raw, err)
	}
	again, err := New(root).Get(name)
	if err != nil || again != "legacy-plaintext" {
		t.Fatalf("reloaded value=%q err=%v", again, err)
	}
}

func TestMigrateLegacyEncryptedSecretToJSONEnvelope(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	backend := store.backend.(*fileBackend)
	name := Name("oauth", "legacy-access-token")
	currentPath, err := backend.path(store.service, name)
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSuffix(currentPath, ".json") + ".secret"
	if err := os.MkdirAll(backend.root, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := legacyEncryptedTestBlob(t, backend, []byte("migrate-me"))
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := store.MigrateLegacyFiles()
	if err != nil || migrated != 1 {
		t.Fatalf("migrated=%d err=%v", migrated, err)
	}
	raw, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || strings.Contains(string(raw), "migrate-me") || strings.Contains(string(raw), legacyEncryptedPrefix) {
		t.Fatalf("migration did not produce JSON envelope: %q", raw)
	}
	value, err := store.Get(name)
	if err != nil || value != "migrate-me" {
		t.Fatalf("migrated value=%q err=%v", value, err)
	}
	second, err := store.MigrateLegacyFiles()
	if err != nil || second != 0 {
		t.Fatalf("second migrate=%d err=%v", second, err)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	backend := newFileBackend(t.TempDir()).(*fileBackend)
	const keyID = "abc123"
	sealed, err := backend.seal([]byte("round-trip-secret"), keyID)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(sealed) || strings.Contains(string(sealed), "round-trip-secret") {
		t.Fatalf("sealed envelope=%q", sealed)
	}
	opened, err := backend.open(sealed, keyID)
	if err != nil || string(opened) != "round-trip-secret" {
		t.Fatalf("opened=%q err=%v", opened, err)
	}
	if _, err := backend.open(sealed, "different"); err == nil {
		t.Fatal("secret envelope opened under the wrong key id")
	}
}

func TestSecretMutationRollbackRestoresAppliedFiles(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "state", "secrets"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	relative := filepath.Join("state", "secrets", "first.json")
	if err := root.WriteFile(relative, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	err = applySecretMutations(root, []secretMutation{
		{relative: relative, data: []byte("new"), previous: []byte("old"), existed: true},
		{relative: filepath.Join("..", "escape.json"), data: []byte("fail")},
	})
	if err == nil {
		t.Fatal("expected transactional apply failure")
	}
	data, err := root.ReadFile(relative)
	if err != nil || string(data) != "old" {
		t.Fatalf("rollback data=%q err=%v", data, err)
	}
}

func TestFileBackendApplyPersistsOnlyEncryptedJSONEnvelopes(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	values := []Change{
		{Name: Name("oauth", "client-secret"), Value: "client-secret-plaintext"},
		{Name: Name("upstream", "authorization"), Value: "bearer-plaintext"},
	}
	if err := store.Apply(values); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "state", "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	jsonFiles := 0
	for _, entry := range entries {
		if entry.Name() == masterKeyName {
			continue
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			t.Fatalf("unexpected current secret-store entry: %s", entry.Name())
		}
		raw, err := os.ReadFile(filepath.Join(root, "state", "secrets", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(raw) {
			t.Fatalf("secret file is not JSON: %s", entry.Name())
		}
		for _, value := range values {
			if strings.Contains(string(raw), value.Value) {
				t.Fatalf("plaintext %q leaked into %s", value.Value, entry.Name())
			}
		}
		jsonFiles++
	}
	if jsonFiles != len(values) {
		t.Fatalf("JSON secret files=%d want=%d", jsonFiles, len(values))
	}
	for _, value := range values {
		got, err := store.Get(value.Name)
		if err != nil || got != value.Value {
			t.Fatalf("secret %s=%q err=%v", value.Name, got, err)
		}
	}
}

func TestFileBackendRejectsBroadSecretPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission bits are not equivalent")
	}
	root := t.TempDir()
	store := New(root)
	name := Name("oauth", "access-token")
	if err := store.Set(name, "secret-value"); err != nil {
		t.Fatal(err)
	}
	backend := store.backend.(*fileBackend)
	path, err := backend.path(store.service, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root).Get(name); err == nil || !strings.Contains(err.Error(), "permissions are too broad") {
		t.Fatalf("broad secret permissions err=%v", err)
	}
}

func TestFileBackendRejectsBroadSecretDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission bits are not equivalent")
	}
	root := t.TempDir()
	store := New(root)
	name := AccountName(DomainOAuth, "access-token")
	if err := store.Set(name, "secret-value"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "state", "secrets")
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := New(root).Get(name)
	if err == nil || !strings.Contains(err.Error(), "secret directory permissions are too broad") {
		t.Fatalf("broad secret directory permissions err=%v", err)
	}
	var storeErr *Error
	if !errors.As(err, &storeErr) || storeErr.Operation != "read" {
		t.Fatalf("typed error=%#v err=%v", storeErr, err)
	}
}

func TestFileBackendRejectsBroadMasterKeyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission bits are not equivalent")
	}
	root := t.TempDir()
	first := New(root)
	if err := first.Set(Name("oauth", "first"), "secret-value"); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "state", "secrets", masterKeyName)
	if err := os.Chmod(keyPath, 0644); err != nil {
		t.Fatal(err)
	}
	if err := New(root).Set(Name("oauth", "second"), "another-secret"); err == nil || !strings.Contains(err.Error(), "permissions are too broad") {
		t.Fatalf("broad master-key permissions err=%v", err)
	}
}

func TestFileBackendRejectsSecretStateSymlinkEscape(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "state")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := New(root)
	if err := store.Set(Name("oauth", "access-token"), "secret-value"); err == nil {
		t.Fatal("expected secret write through escaped state symlink to fail")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("secret write escaped config root: %#v", entries)
	}
}

func TestFileBackendRejectsSecretFileSymlink(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	store := New(root)
	backend := store.backend.(*fileBackend)
	name := Name("oauth", "access-token")
	path, err := backend.path(store.service, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.Get(name); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("expected symlink secret file to be rejected, got %v", err)
	}
}

func legacyEncryptedTestBlob(t *testing.T, backend *fileBackend, plaintext []byte) []byte {
	t.Helper()
	key, err := backend.masterKey()
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	for index := range nonce {
		nonce[index] = byte(index + 1)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return []byte(legacyEncryptedPrefix + base64.StdEncoding.EncodeToString(sealed))
}

func TestFileBackendIsolatesConfigRoots(t *testing.T) {
	name := Name("oauth", "alpha", "access-token")
	left, right := New(t.TempDir()), New(t.TempDir())
	if err := left.Set(name, "left"); err != nil {
		t.Fatal(err)
	}
	if _, err := right.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("right err=%v", err)
	}
}

func TestConcurrentStoresShareFirstMasterKey(t *testing.T) {
	root := t.TempDir()
	const count = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range count {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			store := New(root)
			<-start
			if err := store.Set(Name("concurrent", fmt.Sprint(index)), fmt.Sprintf("value-%d", index)); err != nil {
				t.Errorf("set %d: %v", index, err)
			}
		}(index)
	}
	close(start)
	wg.Wait()
	fresh := New(root)
	for index := range count {
		want := fmt.Sprintf("value-%d", index)
		got, err := fresh.Get(Name("concurrent", fmt.Sprint(index)))
		if err != nil || got != want {
			t.Fatalf("get %d value=%q want=%q err=%v", index, got, want, err)
		}
	}
}

func TestSecretMarkerCompatibility(t *testing.T) {
	if !IsMarker(Marker) || !IsMarker(LegacyMarker) {
		t.Fatalf("markers are not recognized")
	}
	if Marker == LegacyMarker || Marker != "<secret-file>" || LegacyMarker != "<os-keyring>" {
		t.Fatalf("marker=%q legacy=%q", Marker, LegacyMarker)
	}
}
