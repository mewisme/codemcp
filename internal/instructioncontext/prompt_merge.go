package instructioncontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ValidateWorkspacePromptState(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("workspace prompts state must be a real directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.ToLower(filepath.Ext(entry.Name())) != ".json" {
			return fmt.Errorf("unsupported workspace prompt entry: %s", entry.Name())
		}
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace prompt entry is not a regular non-symlink file: %s", entry.Name())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return fmt.Errorf("decode workspace prompt %s: %w", entry.Name(), err)
		}
	}
	return nil
}
