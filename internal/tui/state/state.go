package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	atomicstate "go.mewis.me/codemcp/internal/state"
)

const Version = 1
const maxRecentActions = 20

type State struct {
	Version       int      `json:"version"`
	RecentActions []string `json:"recent_actions,omitempty"`
}

func Path(root string) string { return filepath.Join(root, "tui-state.json") }

func Default() State { return State{Version: Version, RecentActions: []string{}} }

func Load(root string) (State, error) {
	data, err := os.ReadFile(Path(root))
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}
	var value State
	if err := json.Unmarshal(data, &value); err != nil {
		return Default(), err
	}
	if value.Version != Version {
		return Default(), errors.New("unsupported TUI state version")
	}
	value.RecentActions = normalizeRecent(value.RecentActions)
	return value, nil
}

func Save(root string, value State) error {
	value.Version = Version
	value.RecentActions = normalizeRecent(value.RecentActions)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicstate.WriteFileAtomic(Path(root), data, 0600)
}

func RecordRecent(value *State, id string) {
	if value == nil {
		return
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 {
		return
	}
	next := []string{id}
	for _, current := range value.RecentActions {
		current = strings.TrimSpace(current)
		if current != "" && current != id {
			next = append(next, current)
		}
		if len(next) >= maxRecentActions {
			break
		}
	}
	value.Version = Version
	value.RecentActions = next
}

func normalizeRecent(values []string) []string {
	result := make([]string, 0, min(len(values), maxRecentActions))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 256 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == maxRecentActions {
			break
		}
	}
	return result
}
