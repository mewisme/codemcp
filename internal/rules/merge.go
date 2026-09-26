package rules

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func ValidateWorkspaceState(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("workspace rules state must be a real directory")
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace rules state contains symlink: %s", filepath.ToSlash(relative))
		}
		depth := len(strings.Split(filepath.ToSlash(relative), "/")) - 1
		if entry.IsDir() {
			if depth > 3 {
				return fmt.Errorf("workspace rules state exceeds supported depth: %s", filepath.ToSlash(relative))
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if !info.Mode().IsRegular() || (extension != ".md" && extension != ".mdc") {
			return fmt.Errorf("unsupported workspace rules entry: %s", filepath.ToSlash(relative))
		}
		return nil
	})
}
