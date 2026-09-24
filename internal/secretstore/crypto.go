package secretstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	secretEnvelopeVersion = 1
	secretAlgorithm       = "AES-256-GCM"
	masterKeyName         = ".master.key"
	masterKeySize         = 32
	maxSecretEnvelopeSize = 1 << 20
)

type secretEnvelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	KeyID      string `json:"key_id"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func (b *fileBackend) seal(plaintext []byte, keyID string) ([]byte, error) {
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
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, secretAAD(keyID))
	envelope := secretEnvelope{
		Version:    secretEnvelopeVersion,
		Algorithm:  secretAlgorithm,
		KeyID:      keyID,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > maxSecretEnvelopeSize {
		return nil, errors.New("encrypted secret envelope exceeds size limit")
	}
	return data, nil
}

func (b *fileBackend) open(data []byte, keyID string) ([]byte, error) {
	envelope, err := decodeSecretEnvelope(data)
	if err != nil {
		return nil, err
	}
	if envelope.KeyID != keyID {
		return nil, errors.New("encrypted secret envelope key id does not match its path")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted secret nonce: %w", err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted secret ciphertext: %w", err)
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
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("encrypted secret nonce has invalid length %d", len(nonce))
	}
	if len(ciphertext) < gcm.Overhead() {
		return nil, errors.New("encrypted secret ciphertext is truncated")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, secretAAD(keyID))
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}
	return plaintext, nil
}

func decodeSecretEnvelope(data []byte) (secretEnvelope, error) {
	if len(data) == 0 || len(data) > maxSecretEnvelopeSize {
		return secretEnvelope{}, errors.New("encrypted secret envelope has invalid size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope secretEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return secretEnvelope{}, fmt.Errorf("decode encrypted secret envelope: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return secretEnvelope{}, errors.New("encrypted secret envelope contains multiple JSON values")
		}
		return secretEnvelope{}, fmt.Errorf("decode encrypted secret envelope trailing data: %w", err)
	}
	if envelope.Version != secretEnvelopeVersion {
		return secretEnvelope{}, fmt.Errorf("unsupported encrypted secret envelope version: %d", envelope.Version)
	}
	if envelope.Algorithm != secretAlgorithm {
		return secretEnvelope{}, fmt.Errorf("unsupported encrypted secret algorithm: %q", envelope.Algorithm)
	}
	if envelope.KeyID == "" || envelope.Nonce == "" || envelope.Ciphertext == "" {
		return secretEnvelope{}, errors.New("encrypted secret envelope is incomplete")
	}
	return envelope, nil
}

func secretAAD(keyID string) []byte {
	return []byte(fmt.Sprintf("codemcp-secret-envelope-v%d\x00%s", secretEnvelopeVersion, keyID))
}

func (b *fileBackend) masterKey() ([]byte, error) {
	b.keyMu.Lock()
	defer b.keyMu.Unlock()
	if len(b.key) == masterKeySize {
		return b.key, nil
	}
	root, err := b.openConfigRoot(true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir := filepath.Join("state", "secrets")
	if err := root.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, masterKeyName)
	data, err := readMasterKey(root, path)
	if err == nil {
		b.key = data
		return b.key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	data, err = publishMasterKey(root, dir, path, key)
	if err != nil {
		return nil, err
	}
	b.key = data
	return b.key, nil
}

func publishMasterKey(root *os.Root, dir, path string, key []byte) ([]byte, error) {
	tempPath, file, err := createMasterKeyTemp(root, dir)
	if err != nil {
		return nil, err
	}
	defer root.Remove(tempPath)
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write master key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync master key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close master key: %w", err)
	}
	if err := root.Link(tempPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readMasterKey(root, path)
		}
		return nil, fmt.Errorf("publish master key: %w", err)
	}
	return append([]byte(nil), key...), nil
}

func createMasterKeyTemp(root *os.Root, dir string) (string, *os.File, error) {
	for range 8 {
		suffix := make([]byte, 12)
		if _, err := rand.Read(suffix); err != nil {
			return "", nil, err
		}
		path := filepath.Join(dir, "."+masterKeyName+"."+base64.RawURLEncoding.EncodeToString(suffix)+".tmp")
		file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("create master key temp file: %w", err)
		}
		return path, file, nil
	}
	return "", nil, errors.New("create master key temp file: exhausted unique names")
}

func readMasterKey(root *os.Root, path string) ([]byte, error) {
	data, err := readRootedSecretFile(root, path, masterKeySize)
	if err != nil {
		return nil, err
	}
	if len(data) != masterKeySize {
		return nil, fmt.Errorf("master key %s has invalid length %d", path, len(data))
	}
	return append([]byte(nil), data...), nil
}
