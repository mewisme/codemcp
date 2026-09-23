package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecRejectsOutputRedirectionOutsideWorkspace(t *testing.T) {
	manager, workspaceID, _ := newShellTestManager(t)
	outside := filepath.Join(t.TempDir(), "escape.txt")
	_, err := manager.Exec(context.Background(), workspaceID, "echo escaped > "+outside)
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("error = %v, want workspace escape denial", err)
	}
	if _, statErr := os.Stat(outside); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside file was created or stat failed unexpectedly: %v", statErr)
	}
}

func TestBackgroundValidationRejectsWriteOutsideWorkspace(t *testing.T) {
	manager, workspaceID, _ := newShellTestManager(t)
	outside := filepath.Join(t.TempDir(), "escape.txt")
	_, err := manager.ValidateBackgroundCommand(context.Background(), workspaceID, "echo escaped > "+outside)
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("error = %v, want workspace escape denial", err)
	}
}

func TestWriteMutationUsesPersistedCWD(t *testing.T) {
	manager, workspaceID, root := newShellTestManager(t)
	if _, err := manager.Exec(context.Background(), workspaceID, "echo x > file.txt"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil || strings.TrimSpace(string(data)) != "x" {
		t.Fatalf("content = %q err=%v", data, err)
	}
}

func TestBackgroundWriteMutationUsesPersistedCWD(t *testing.T) {
	manager, workspaceID, root := newShellTestManager(t)
	cwd, err := manager.ValidateBackgroundCommand(context.Background(), workspaceID, "touch file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(cwd) != filepath.Clean(root) {
		t.Fatalf("cwd = %q, want %q", cwd, root)
	}
}
