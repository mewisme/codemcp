package oslock

import (
	"path/filepath"
	"testing"
)

func TestExclusiveLockRejectsSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.lock")
	first, ok, err := TryAcquire(path, Exclusive)
	if err != nil || !ok {
		t.Fatalf("first lock ok=%t err=%v", ok, err)
	}
	defer first.Release()

	if second, ok, err := TryAcquire(path, Exclusive); err != nil {
		t.Fatal(err)
	} else if ok {
		_ = second.Release()
		t.Fatal("second exclusive owner acquired the same lock")
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, ok, err := TryAcquire(path, Exclusive)
	if err != nil || !ok {
		t.Fatalf("lock was not reusable after release: ok=%t err=%v", ok, err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}
