package state

import (
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestWorkspaceStatePaths(t *testing.T) {
	oldRoot := configformat.RootPath()
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = configformat.SetRootPath(oldRoot) }()
	if got := Root(); got != filepath.Join(root, "state") {
		t.Fatalf("root = %q", got)
	}
	one, two := WorkspaceID("/workspace/one"), WorkspaceID("/workspace/two")
	if len(one) != 16 || one == two || one != WorkspaceID("/workspace/one") {
		t.Fatalf("workspace ids = %q %q", one, two)
	}
	if got := WorkspacePath("/workspace/one"); got != filepath.Join(root, "state", "workspaces", one) {
		t.Fatalf("workspace path = %q", got)
	}
}
