package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.mewis.me/codemcp/internal/state"
)

func readConfigFile(path string) ([]byte, os.FileMode, error) {
	root, name, err := openConfigFileRoot(path, false)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("config path is not a regular file: %s", path)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, 0, fmt.Errorf("config path changed while opening: %s", path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	return data, openedInfo.Mode().Perm(), nil
}

func writeConfigFile(path string, data []byte, perm os.FileMode) error {
	root, name, err := openConfigFileRoot(path, true)
	if err != nil {
		return err
	}
	defer root.Close()
	return state.WriteFileAtomicRoot(root, name, data, perm)
}

func removeConfigFile(path string) error {
	root, name, err := openConfigFileRoot(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func openConfigFileRoot(path string, create bool) (*os.Root, string, error) {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, "", err
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	return root, filepath.Base(path), nil
}
