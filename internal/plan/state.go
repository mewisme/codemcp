package plan

import (
	"errors"
	"fmt"
	"io"
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
		return errors.New("workspace plans state must be a real directory")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(root, name)
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace plans state contains symlink: %s", name)
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("workspace plans state contains non-regular entry: %s", name)
		}
		if filepath.Base(path) != name || filepath.Ext(name) != ".md" {
			return fmt.Errorf("workspace plans state contains unsupported plan path: %s", name)
		}
		planName := strings.TrimSuffix(name, ".md")
		if err := ValidateName(planName); err != nil {
			return fmt.Errorf("workspace plans state contains invalid plan name %q: %w", planName, err)
		}
		if entryInfo.Size() > int64(MaxDocumentBytes) {
			return fmt.Errorf("workspace plan %q exceeds size limit", planName)
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(entryInfo, opened) {
			_ = file.Close()
			if statErr != nil {
				return statErr
			}
			return fmt.Errorf("workspace plan changed while opening: %s", name)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(MaxDocumentBytes)+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > MaxDocumentBytes {
			return fmt.Errorf("workspace plan %q exceeds size limit", planName)
		}
		if _, err := Parse(data); err != nil {
			return fmt.Errorf("workspace plan %q is invalid: %w", planName, err)
		}
	}
	return nil
}

func EquivalentDocuments(left, right []byte) (bool, error) {
	leftDocument, err := Parse(left)
	if err != nil {
		return false, err
	}
	rightDocument, err := Parse(right)
	if err != nil {
		return false, err
	}
	return leftDocument.ContentID() == rightDocument.ContentID(), nil
}
