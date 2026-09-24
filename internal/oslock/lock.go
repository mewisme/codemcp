package oslock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Mode int

const (
	Shared Mode = iota
	Exclusive
)

var ErrBusy = errors.New("file lock is busy")

type Lock struct {
	file *os.File
}

func Acquire(path string, mode Mode) (*Lock, error) {
	return acquire(path, mode, false)
}

func TryAcquire(path string, mode Mode) (*Lock, bool, error) {
	lock, err := acquire(path, mode, true)
	if errors.Is(err, ErrBusy) {
		return nil, false, nil
	}
	return lock, err == nil, err
}

func TryAcquireExisting(path string, mode Mode) (*Lock, bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, false, err
	}
	if err := lockFile(file, mode, true); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrBusy) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &Lock{file: file}, true, nil
}

func acquire(path string, mode Mode, nonBlocking bool) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file, mode, nonBlocking); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

func (l *Lock) ReplaceContent(data []byte) error {
	if l == nil || l.file == nil {
		return errors.New("file lock is not held")
	}
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	if len(data) > 0 {
		written, err := l.file.Write(data)
		if err != nil {
			return err
		}
		if written != len(data) {
			return fmt.Errorf("short lock metadata write: wrote %d of %d bytes", written, len(data))
		}
	}
	return l.file.Sync()
}

func (l *Lock) SameFile(path string) (bool, error) {
	if l == nil || l.file == nil {
		return false, errors.New("file lock is not held")
	}
	held, err := l.file.Stat()
	if err != nil {
		return false, err
	}
	target, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return os.SameFile(held, target), nil
}
