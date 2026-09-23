//go:build windows

package control

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReadStateFileRetriesWindowsSharingViolation(t *testing.T) {
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, "http://127.0.0.1:12345", "runtime-secret")
	path := filepath.Join(root, FileName)
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(pathUTF16, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = windows.CloseHandle(handle)
		close(released)
	}()
	data, retries, err := readStateFile()
	<-released
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || retries == 0 {
		t.Fatalf("state bytes=%d retries=%d, want Windows retry", len(data), retries)
	}
}
