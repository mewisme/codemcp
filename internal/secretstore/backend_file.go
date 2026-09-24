package secretstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/state"
)

type fileBackend struct {
	configRoot string
	root       string
	keyMu      sync.Mutex
	txMu       *sync.Mutex
	key        []byte
}

var fileTransactionLocks sync.Map

func newFileBackend(root string) Backend {
	root = filepath.Clean(root)
	lock, _ := fileTransactionLocks.LoadOrStore(root, &sync.Mutex{})
	return &fileBackend{configRoot: root, root: filepath.Join(root, "state", "secrets"), txMu: lock.(*sync.Mutex)}
}

func (b *fileBackend) Set(service, account, value string) error {
	b.txMu.Lock()
	defer b.txMu.Unlock()
	return b.set(service, account, value)
}

func (b *fileBackend) set(service, account, value string) error {
	relative, err := b.relativePath(service, account)
	if err != nil {
		return err
	}
	sealed, err := b.seal([]byte(value), keyIDFromRelative(relative))
	if err != nil {
		return err
	}
	root, err := b.openConfigRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := ensureSecretDirectory(root, true); err != nil {
		return err
	}
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
	if err := ensureSecretDirectory(root, false); errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	data, err := readRootedSecretFile(root, relative, maxSecretEnvelopeSize)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	plaintext, err := b.open(data, keyIDFromRelative(relative))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

type secretMutation struct {
	relative string
	data     []byte
	remove   bool
	previous []byte
	existed  bool
}

func (b *fileBackend) Apply(service string, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	b.txMu.Lock()
	defer b.txMu.Unlock()
	mutations := make([]secretMutation, 0, len(changes))
	hasWrite := false
	for _, change := range changes {
		relative, err := b.relativePath(service, change.Name)
		if err != nil {
			return err
		}
		mutation := secretMutation{relative: relative, remove: change.Value == ""}
		if !mutation.remove {
			mutation.data, err = b.seal([]byte(change.Value), keyIDFromRelative(relative))
			if err != nil {
				return err
			}
			hasWrite = true
		}
		mutations = append(mutations, mutation)
	}
	root, err := b.openConfigRoot(hasWrite)
	if errors.Is(err, os.ErrNotExist) && !hasWrite {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if err := ensureSecretDirectory(root, hasWrite); errors.Is(err, os.ErrNotExist) && !hasWrite {
		return nil
	} else if err != nil {
		return err
	}
	for index := range mutations {
		previous, err := readRootedSecretFile(root, mutations[index].relative, maxSecretEnvelopeSize)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		mutations[index].previous = previous
		mutations[index].existed = true
	}
	return applySecretMutations(root, mutations)
}

func applySecretMutations(root *os.Root, mutations []secretMutation) error {
	applied := 0
	for _, mutation := range mutations {
		var err error
		if mutation.remove {
			err = root.Remove(mutation.relative)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else {
			err = state.WriteFileAtomicRoot(root, mutation.relative, mutation.data, 0600)
		}
		if err != nil {
			return errors.Join(err, rollbackSecretMutations(root, mutations[:applied]))
		}
		applied++
	}
	return nil
}

func rollbackSecretMutations(root *os.Root, mutations []secretMutation) error {
	var result error
	for index := len(mutations) - 1; index >= 0; index-- {
		mutation := mutations[index]
		if mutation.existed {
			result = errors.Join(result, state.WriteFileAtomicRoot(root, mutation.relative, mutation.previous, 0600))
			continue
		}
		if err := root.Remove(mutation.relative); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (b *fileBackend) Delete(service, account string) error {
	b.txMu.Lock()
	defer b.txMu.Unlock()
	return b.delete(service, account)
}

func (b *fileBackend) delete(service, account string) error {
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
	if err := ensureSecretDirectory(root, false); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := root.Remove(relative); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
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
	return filepath.Join(b.root, hex.EncodeToString(sum[:])+".json"), nil
}

func keyIDFromRelative(relative string) string {
	name := filepath.Base(relative)
	return strings.TrimSuffix(name, filepath.Ext(name))
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

func ensureSecretDirectory(root *os.Root, create bool) error {
	if root == nil {
		return errors.New("secret config root is unavailable")
	}
	dir := filepath.Join("state", "secrets")
	if create {
		if err := root.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	info, err := root.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("secret directory is not a directory: %s", dir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("secret directory permissions are too broad: %s has %#o", dir, info.Mode().Perm())
	}
	return nil
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

func readRootedSecretFile(root *os.Root, relative string, limit int) ([]byte, error) {
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret path is not a regular file: %s", relative)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("secret path permissions are too broad: %s has %#o", relative, info.Mode().Perm())
	}
	if info.Size() > int64(limit) {
		return nil, fmt.Errorf("secret path exceeds size limit: %s", relative)
	}
	data, err := readRootedRegularFile(root, relative)
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("secret path exceeds size limit: %s", relative)
	}
	return data, nil
}
