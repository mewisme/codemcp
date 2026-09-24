package credentials024

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/secretstore"
)

func TestTransformReleasedCredentialsStagesRecoverableSecretsAndRetainsSource(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "stage")
	key := bytes.Repeat([]byte{0x42}, legacyMasterKeySize)
	secretDir := filepath.Join(source, "state", "secrets")
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, legacyMasterKeyName), key, 0600); err != nil {
		t.Fatal(err)
	}

	configData := []byte(`{
  "auth": {
    "mcp_token_hash": "sha256$opaque-mcp-hash",
    "admin_token_hash": "opaque-admin-hash"
  },
  "tunnel": {
    "api_key": "<secret-file>"
  }
}`)
	tunnelData := []byte(`{
  "runtime_key_configured": true,
  "admin_key": "inline-admin-secret"
}`)
	oauthData := []byte(`{
  "version": 1,
  "credentials": {
    "server-one": {
      "client_secret": "<secret-file>",
      "access_token": "<secret-file>",
      "refresh_token": "inline-refresh-secret"
    }
  }
}`)
	upstreamData := []byte(`{
  "version": 1,
  "servers": [{
    "id": "server-one",
    "headers": {
      "Authorization": "<secret-file>",
      "X-Test": "visible"
    },
    "env": {
      "API_TOKEN": "inline-env-secret",
      "MODE": "test"
    }
  }]
}`)
	writeFixture(t, filepath.Join(source, "config.json"), configData)
	writeFixture(t, filepath.Join(source, "tunnel.json"), tunnelData)
	writeFixture(t, filepath.Join(source, "oauth.json"), oauthData)
	writeFixture(t, filepath.Join(source, "upstream.json"), upstreamData)

	accounts := map[string]string{
		secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"):                             "runtime-secret",
		secretstore.AccountName(secretstore.DomainOAuth, "server-one", "client-secret"):              "client-secret-value",
		secretstore.AccountName(secretstore.DomainOAuth, "server-one", "access-token"):               "access-secret-value",
		secretstore.AccountName(secretstore.DomainUpstream, "server-one", "header", "Authorization"): "authorization-secret",
		secretstore.AccountName(secretstore.DomainCluster, "relay-token"):                            "relay-secret",
	}
	for account, value := range accounts {
		writeLegacySecret(t, source, account, value, key, true)
	}

	before := snapshotFiles(t, source)
	result, err := Transform(Input{SourceRoot: source, DestinationRoot: destination})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceRelease != SourceRelease || result.DestinationRoot != filepath.Clean(destination) {
		t.Fatalf("result identity = %#v", result)
	}
	if !result.Inventory.AuthHashes.MCPConfigured || !result.Inventory.AuthHashes.AdminConfigured {
		t.Fatalf("auth inventory = %#v", result.Inventory.AuthHashes)
	}
	if result.Inventory.Tunnel != 2 || result.Inventory.OAuth != 3 || result.Inventory.Upstream != 2 || result.Inventory.Cluster != 1 {
		t.Fatalf("family inventory = %#v", result.Inventory)
	}
	if result.Inventory.Recoverable != 8 || result.Inventory.InlinePlaintext != 3 || result.Inventory.LegacyEncrypted != 5 || result.Inventory.LegacyPlaintext != 0 {
		t.Fatalf("credential inventory = %#v", result.Inventory)
	}
	if result.Migrated != 8 || result.AlreadyApplied || !result.Rollback.SourceRetained || result.Rollback.LegacyCredentialFiles != 5 {
		t.Fatalf("result = %#v", result)
	}

	store := secretstore.New(destination)
	expected := map[string]string{
		secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"):                             "runtime-secret",
		secretstore.AccountName(secretstore.DomainTunnel, "admin-key"):                               "inline-admin-secret",
		secretstore.AccountName(secretstore.DomainOAuth, "server-one", "client-secret"):              "client-secret-value",
		secretstore.AccountName(secretstore.DomainOAuth, "server-one", "access-token"):               "access-secret-value",
		secretstore.AccountName(secretstore.DomainOAuth, "server-one", "refresh-token"):              "inline-refresh-secret",
		secretstore.AccountName(secretstore.DomainUpstream, "server-one", "header", "Authorization"): "authorization-secret",
		secretstore.AccountName(secretstore.DomainUpstream, "server-one", "env", "API_TOKEN"):        "inline-env-secret",
		secretstore.AccountName(secretstore.DomainCluster, "relay-token"):                            "relay-secret",
	}
	for account, want := range expected {
		got, err := store.Get(account)
		if err != nil || got != want {
			t.Fatalf("staged account %q = %q, %v; want %q", account, got, err, want)
		}
	}
	assertNoPlaintextSecrets(t, destination, expected)
	assertSnapshotUnchanged(t, source, before)

	again, err := Transform(Input{SourceRoot: source, DestinationRoot: destination})
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyApplied || again.Migrated != 0 || again.Inventory.Recoverable != 8 {
		t.Fatalf("second result = %#v", again)
	}
	assertSnapshotUnchanged(t, source, before)
}

func TestTransformSupportsLegacyPlaintextSecretFiles(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "stage")
	account := secretstore.AccountName(secretstore.DomainTunnel, "runtime-key")
	writeFixture(t, filepath.Join(source, "tunnel.json"), []byte(`{"runtime_key_configured":true}`))
	writeLegacySecret(t, source, account, "legacy-plaintext-value", nil, false)

	result, err := Transform(Input{SourceRoot: source, DestinationRoot: destination})
	if err != nil {
		t.Fatal(err)
	}
	if result.Inventory.LegacyPlaintext != 1 || result.Inventory.LegacyEncrypted != 0 || result.Migrated != 1 {
		t.Fatalf("result = %#v", result)
	}
	got, err := secretstore.New(destination).Get(account)
	if err != nil || got != "legacy-plaintext-value" {
		t.Fatalf("staged plaintext legacy credential = %q, %v", got, err)
	}
	legacyPath := legacySecretPath(source, account)
	data, err := os.ReadFile(legacyPath)
	if err != nil || string(data) != "legacy-plaintext-value" {
		t.Fatalf("legacy source changed: %q, %v", data, err)
	}
}

func TestTransformRefusesDestinationConflictBeforeMutation(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "stage")
	writeFixture(t, filepath.Join(source, "tunnel.json"), []byte(`{"admin_key":"source-secret"}`))
	account := secretstore.AccountName(secretstore.DomainTunnel, "admin-key")
	store := secretstore.New(destination)
	if err := store.Set(account, "destination-secret"); err != nil {
		t.Fatal(err)
	}
	sourceBefore := snapshotFiles(t, source)

	_, err := Transform(Input{SourceRoot: source, DestinationRoot: destination})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("err = %v", err)
	}
	for _, secret := range []string{"source-secret", "destination-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("conflict error leaked credential: %v", err)
		}
	}
	got, getErr := store.Get(account)
	if getErr != nil || got != "destination-secret" {
		t.Fatalf("destination changed on conflict: %q, %v", got, getErr)
	}
	assertSnapshotUnchanged(t, source, sourceBefore)
}

func TestTransformMissingStoredCredentialDoesNotCreateDestination(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "stage")
	writeFixture(t, filepath.Join(source, "oauth.json"), []byte(`{
  "version": 1,
  "credentials": {
    "server-one": {"access_token":"<secret-file>"}
  }
}`))
	_, err := Transform(Input{SourceRoot: source, DestinationRoot: destination})
	if err == nil || !strings.Contains(err.Error(), "missing stored credential") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination created after failed scan: %v", statErr)
	}
}

func TestTransformRejectsSourceDestinationAliasAndSymlinkSource(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	source := t.TempDir()
	if _, err := Transform(Input{SourceRoot: source, DestinationRoot: source}); err == nil {
		t.Fatal("source/destination alias accepted")
	}
	if os.PathSeparator == '\\' {
		return
	}
	root := t.TempDir()
	link := filepath.Join(root, "source-link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(Input{SourceRoot: link, DestinationRoot: filepath.Join(root, "stage")}); err == nil {
		t.Fatal("symlink source accepted")
	}
}

func TestTransformRejectsActiveConfigRootAsStagingDestination(t *testing.T) {
	active := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", active)
	source := t.TempDir()
	writeFixture(t, filepath.Join(source, "tunnel.json"), []byte(`{"admin_key":"source-secret"}`))

	_, err := Transform(Input{SourceRoot: source, DestinationRoot: active})
	if err == nil || !strings.Contains(err.Error(), "active config root") {
		t.Fatalf("err = %v", err)
	}
	entries, readErr := os.ReadDir(active)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("active config root mutated: %#v", entries)
	}
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacySecret(t *testing.T, root, account, value string, key []byte, encrypted bool) {
	t.Helper()
	path := legacySecretPath(root, account)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(value)
	if encrypted {
		block, err := aes.NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		nonce := bytes.Repeat([]byte{0x24}, gcm.NonceSize())
		sealed := gcm.Seal(nonce, nonce, data, nil)
		data = []byte(legacyEncryptedPrefix + base64.StdEncoding.EncodeToString(sealed))
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func legacySecretPath(root, account string) string {
	digest := sha256.Sum256([]byte(legacyService(root) + "\x00" + account))
	return filepath.Join(root, "state", "secrets", hex.EncodeToString(digest[:])+".secret")
}

func snapshotFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertSnapshotUnchanged(t *testing.T, root string, before map[string][]byte) {
	t.Helper()
	after := snapshotFiles(t, root)
	if len(after) != len(before) {
		t.Fatalf("source file count changed: before=%d after=%d", len(before), len(after))
	}
	for path, want := range before {
		if got, ok := after[path]; !ok || !bytes.Equal(got, want) {
			t.Fatalf("source changed at %s", path)
		}
	}
}

func assertNoPlaintextSecrets(t *testing.T, root string, values map[string]string) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(root, "state", "secrets"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range values {
			if strings.Contains(string(data), value) {
				t.Fatalf("staged secret store leaked plaintext in %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
