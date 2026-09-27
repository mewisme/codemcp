package executable

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ResolveExternal validates configured or PATH-resolved executables.
// Managed assets use their own integrity verifier and must not call this helper.
func ResolveExternal(path, goos string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("executable path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", err
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("executable target must be a regular file")
	}
	if info.Size() <= 0 {
		return "", errors.New("executable target must not be empty")
	}
	if strings.TrimSpace(goos) != "windows" && info.Mode().Perm()&0111 == 0 {
		return "", errors.New("executable target is not executable")
	}
	return canonical, nil
}
