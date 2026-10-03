package browser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/oslock"
)

type ProfileLock struct {
	lock *oslock.Lock
}

func TryAcquireProfile(profile ProfileRef) (*ProfileLock, bool, error) {
	path := strings.TrimSpace(profile.LockPath)
	if path == "" {
		return nil, false, errors.New("browser profile lock path is required")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	lock, ok, err := oslock.TryAcquire(path, oslock.Exclusive)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &ProfileLock{lock: lock}, true, nil
}

func (lock *ProfileLock) Release() error {
	if lock == nil || lock.lock == nil {
		return nil
	}
	err := lock.lock.Release()
	lock.lock = nil
	return err
}
