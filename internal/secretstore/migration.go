package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/state"
)

const legacyEncryptedPrefix = "cgmsecret1:"

func (b *fileBackend) MigrateLegacyFiles() (int, error) {
	b.txMu.Lock()
	defer b.txMu.Unlock()
	root, err := b.openConfigRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	dir := filepath.Join("state", "secrets")
	if err := ensureSecretDirectory(root, false); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	directory, err := root.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".secret") {
			continue
		}
		legacyRelative := filepath.Join(dir, entry.Name())
		data, err := readRootedSecretFile(root, legacyRelative, maxSecretEnvelopeSize)
		if err != nil {
			return migrated, err
		}
		plaintext := data
		if strings.HasPrefix(string(data), legacyEncryptedPrefix) {
			plaintext, err = b.openLegacyEncrypted(data)
			if err != nil {
				return migrated, fmt.Errorf("decrypt legacy secret %s: %w", legacyRelative, err)
			}
		}
		keyID := strings.TrimSuffix(entry.Name(), ".secret")
		targetRelative := filepath.Join(dir, keyID+".json")
		if existing, err := readRootedSecretFile(root, targetRelative, maxSecretEnvelopeSize); err == nil {
			opened, openErr := b.open(existing, keyID)
			if openErr != nil {
				return migrated, fmt.Errorf("verify migrated secret %s: %w", targetRelative, openErr)
			}
			if string(opened) != string(plaintext) {
				return migrated, fmt.Errorf("legacy secret conflicts with existing JSON envelope: %s", targetRelative)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return migrated, err
		} else {
			sealed, err := b.seal(plaintext, keyID)
			if err != nil {
				return migrated, err
			}
			if err := state.WriteFileAtomicRoot(root, targetRelative, sealed, 0600); err != nil {
				return migrated, err
			}
			written, err := readRootedSecretFile(root, targetRelative, maxSecretEnvelopeSize)
			if err != nil {
				return migrated, err
			}
			opened, err := b.open(written, keyID)
			if err != nil || string(opened) != string(plaintext) {
				if err == nil {
					err = errors.New("migrated secret verification mismatch")
				}
				return migrated, err
			}
		}
		if err := root.Remove(legacyRelative); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

func (b *fileBackend) openLegacyEncrypted(data []byte) ([]byte, error) {
	raw := strings.TrimSpace(string(data))
	if !strings.HasPrefix(raw, legacyEncryptedPrefix) {
		return nil, errors.New("legacy secret blob header is invalid")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, legacyEncryptedPrefix))
	if err != nil {
		return nil, fmt.Errorf("decode legacy encrypted secret: %w", err)
	}
	key, err := b.masterKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("legacy encrypted secret is truncated")
	}
	nonce, ciphertext := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt legacy secret: %w", err)
	}
	return plaintext, nil
}
