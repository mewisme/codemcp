package state

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"go.mewis.me/codemcp/internal/configformat"
)

func Root() string {
	return filepath.Join(configformat.RootPath(), "state")
}

func WorkspaceID(path string) string {
	hash := sha256.Sum256([]byte(path))
	return hex.EncodeToString(hash[:])[:16]
}

func WorkspacePath(path string) string {
	return filepath.Join(Root(), "workspaces", WorkspaceID(path))
}
