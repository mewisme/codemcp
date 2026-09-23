package state

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".cm-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replaceFile(temp, path)
}

func WriteFileAtomicRoot(root *os.Root, path string, data []byte, perm os.FileMode) error {
	if root == nil {
		return errors.New("state root is unavailable")
	}
	clean := filepath.Clean(path)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("state path escapes root: %s", path)
	}
	parent := filepath.Dir(clean)
	if parent != "." {
		if err := root.MkdirAll(parent, 0700); err != nil {
			return err
		}
	}
	file, temp, err := createRootTemp(root, parent, filepath.Base(clean), perm)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return root.Rename(temp, clean)
}

func createRootTemp(root *os.Root, parent, base string, perm os.FileMode) (*os.File, string, error) {
	for range 100 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := "." + base + "-" + hex.EncodeToString(random[:])
		if parent != "." {
			name = filepath.Join(parent, name)
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if err := file.Chmod(perm); err != nil {
			_ = file.Close()
			_ = root.Remove(name)
			return nil, "", err
		}
		return file, name, nil
	}
	return nil, "", errors.New("create rooted state temp file: too many collisions")
}
