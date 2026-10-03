package chatgptweb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func AuthMarkerPath(stateRoot string) (string, error) {
	root := strings.TrimSpace(stateRoot)
	if root == "" {
		return "", errors.New("codemcp state root is required for ChatGPT Web authentication state")
	}
	return filepath.Join(root, "browser", "chatgpt-auth.json"), nil
}

func LoadAuthMarker(stateRoot string) (AuthMarker, bool, error) {
	path, err := AuthMarkerPath(stateRoot)
	if err != nil {
		return AuthMarker{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AuthMarker{}, false, nil
	}
	if err != nil {
		return AuthMarker{}, false, fmt.Errorf("read ChatGPT Web auth marker: %w", err)
	}
	var marker AuthMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return AuthMarker{}, false, errors.New("ChatGPT Web auth marker is invalid")
	}
	if marker.Version != AuthMarkerVersion || marker.VerifiedAt.IsZero() {
		return AuthMarker{}, false, errors.New("ChatGPT Web auth marker is unsupported or incomplete")
	}
	return marker, true, nil
}

func WriteAuthMarker(stateRoot string, verifiedAt time.Time) error {
	path, err := AuthMarkerPath(stateRoot)
	if err != nil {
		return err
	}
	if verifiedAt.IsZero() {
		return errors.New("ChatGPT Web auth verification time is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("prepare ChatGPT Web auth state directory: %w", err)
	}
	data, err := json.Marshal(AuthMarker{Version: AuthMarkerVersion, VerifiedAt: verifiedAt.UTC()})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".chatgpt-auth-*.tmp")
	if err != nil {
		return fmt.Errorf("create ChatGPT Web auth marker: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("commit ChatGPT Web auth marker: %w", err)
	}
	return nil
}

func RemoveAuthMarker(stateRoot string) error {
	path, err := AuthMarkerPath(stateRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove ChatGPT Web auth marker: %w", err)
	}
	return nil
}
