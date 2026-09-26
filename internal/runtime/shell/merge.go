package shell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	statepkg "go.mewis.me/codemcp/internal/state"
)

var ErrSessionMergeConflict = errors.New("shell session merge conflict")

func MergeSessionState(registeredPath, destinationPath, outputPath, workspaceID, registeredRoot, destinationRoot string, allowedRoots []string) error {
	registered, registeredOK, err := loadMergeSession(registeredPath, workspaceID)
	if err != nil {
		return fmt.Errorf("registered shell state: %w", err)
	}
	destination, destinationOK, err := loadMergeSession(destinationPath, workspaceID)
	if err != nil {
		return fmt.Errorf("destination shell state: %w", err)
	}
	if !registeredOK && !destinationOK {
		return nil
	}
	if registeredOK {
		registered.CWD = relocateSessionPath(registered.CWD, registeredRoot, destinationRoot)
		registered, err = validateMergedSession(registered, workspaceID, destinationRoot, allowedRoots)
		if err != nil {
			return fmt.Errorf("registered shell state: %w", err)
		}
	}
	if destinationOK {
		destination, err = validateMergedSession(destination, workspaceID, destinationRoot, allowedRoots)
		if err != nil {
			return fmt.Errorf("destination shell state: %w", err)
		}
	}
	selected := destination
	if !destinationOK {
		selected = registered
	} else if registeredOK {
		if reflect.DeepEqual(registered, destination) {
			selected = destination
		} else {
			registeredUpdated, regErr := time.Parse(time.RFC3339Nano, registered.UpdatedAt)
			destinationUpdated, dstErr := time.Parse(time.RFC3339Nano, destination.UpdatedAt)
			if regErr != nil || dstErr != nil || registeredUpdated.Equal(destinationUpdated) {
				return ErrSessionMergeConflict
			}
			if registeredUpdated.After(destinationUpdated) {
				selected = registered
			}
		}
	}
	selected.Version = sessionStateVersion
	selected.WorkspaceID = workspaceID
	if selected.RecentCommands == nil {
		selected.RecentCommands = []string{}
	}
	if len(selected.RecentCommands) > maxHistory {
		selected.RecentCommands = append([]string(nil), selected.RecentCommands[len(selected.RecentCommands)-maxHistory:]...)
	}
	return statepkg.WriteJSONAtomic(outputPath, selected, 0600)
}

func loadMergeSession(path, workspaceID string) (SessionState, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return SessionState{}, false, nil
	}
	if err != nil {
		return SessionState{}, false, err
	}
	var value SessionState
	if err := json.Unmarshal(data, &value); err != nil {
		return SessionState{}, false, err
	}
	if value.Version != 0 && value.Version != sessionStateVersion {
		return SessionState{}, false, fmt.Errorf("unsupported shell state version: %d", value.Version)
	}
	if value.WorkspaceID != workspaceID || strings.TrimSpace(value.CWD) == "" {
		return SessionState{}, false, errors.New("shell state workspace binding is invalid")
	}
	value.Version = sessionStateVersion
	return value, true, nil
}

func validateMergedSession(value SessionState, workspaceID, destinationRoot string, allowedRoots []string) (SessionState, error) {
	if value.WorkspaceID != workspaceID {
		return SessionState{}, errors.New("shell state workspace binding is invalid")
	}
	cwd, err := filepath.Abs(value.CWD)
	if err != nil {
		return SessionState{}, err
	}
	cwd = filepath.Clean(cwd)
	roots := append([]string{destinationRoot}, allowedRoots...)
	allowed := false
	for _, root := range roots {
		root, err = filepath.Abs(root)
		if err != nil {
			return SessionState{}, err
		}
		root = filepath.Clean(root)
		relative, relErr := filepath.Rel(root, cwd)
		if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return SessionState{}, fmt.Errorf("shell cwd escapes destination workspace access: %s", cwd)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return SessionState{}, err
	}
	if !info.IsDir() {
		return SessionState{}, fmt.Errorf("shell cwd is not a directory: %s", cwd)
	}
	value.CWD = cwd
	return value, nil
}

func relocateSessionPath(value, oldRoot, newRoot string) string {
	value = filepath.Clean(value)
	oldRoot = filepath.Clean(oldRoot)
	newRoot = filepath.Clean(newRoot)
	relative, err := filepath.Rel(oldRoot, value)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return value
	}
	return filepath.Join(newRoot, relative)
}
