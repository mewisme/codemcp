package secretstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/state"
)

type fileBackend struct {
	configRoot string
	root       string
	mu         sync.Mutex
	key        []byte
}

func newFileBackend(root string) Backend {
	root = filepath.Clean(root)
	return &fileBackend{configRoot: root, root: filepath.Join(root, "state", "secrets")}
}

func (b *fileBackend) Set(service, account, value string) error {
	relative, err := b.relativePath(service, account)
	if err != nil {
		return err
	}
	sealed, err := b.seal([]byte(value))
	if err != nil {
		return err
	}
	root, err := b.openConfigRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	return state.WriteFileAtomicRoot(root, relative, sealed, 0600)
}

func (b *fileBackend) Get(service, account string) (string, error) {
	relative, err := b.relativePath(service, account)
	if err != nil {
		return "", err
	}
	root, err := b.openConfigRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	defer root.Close()
	data, err := readRootedRegularFile(root, relative)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if isEncryptedBlob(data) {
		plaintext, err := b.open(data)
		if err != nil {
			return "", err
		}
		return string(plaintext), nil
	}
	value := string(data)
	// Legacy plaintext: keep readable and rewrite encrypted when possible.
	_ = b.Set(service, account, value)
	return value, nil
}

func (b *fileBackend) Delete(service, account string) error {
	relative, err := b.relativePath(service, account)
	if err != nil {
		return err
	}
	root, err := b.openConfigRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(relative); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

func (b *fileBackend) MigratePlaintext() (int, error) {
	root, err := b.openConfigRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	dir := filepath.Join("state", "secrets")
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
		relative := filepath.Join(dir, entry.Name())
		info, err := root.Lstat(relative)
		if err != nil {
			return migrated, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := readRootedRegularFile(root, relative)
		if err != nil {
			return migrated, err
		}
		if isEncryptedBlob(data) {
			continue
		}
		sealed, err := b.seal(data)
		if err != nil {
			return migrated, err
		}
		if err := state.WriteFileAtomicRoot(root, relative, sealed, 0600); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

func (b *fileBackend) path(service, account string) (string, error) {
	service = strings.TrimSpace(service)
	account = strings.TrimSpace(account)
	if service == "" {
		return "", errors.New("secret service is required")
	}
	if account == "" {
		return "", errors.New("secret account is required")
	}
	sum := sha256.Sum256([]byte(service + "\x00" + account))
	return filepath.Join(b.root, hex.EncodeToString(sum[:])+".secret"), nil
}

func (b *fileBackend) relativePath(service, account string) (string, error) {
	path, err := b.path(service, account)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(b.configRoot, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("secret path escapes config root")
	}
	return relative, nil
}

func (b *fileBackend) openConfigRoot(create bool) (*os.Root, error) {
	if strings.TrimSpace(b.configRoot) == "" {
		return nil, errors.New("secret config root is unavailable")
	}
	if create {
		if err := os.MkdirAll(b.configRoot, 0700); err != nil {
			return nil, err
		}
	}
	return os.OpenRoot(b.configRoot)
}

func readRootedRegularFile(root *os.Root, relative string) ([]byte, error) {
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret path is not a regular file: %s", relative)
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("secret path changed while opening: %s", relative)
	}
	return io.ReadAll(file)
}
