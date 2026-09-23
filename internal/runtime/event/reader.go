package event

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func Path(root string) string { return filepath.Join(root, "logs", "runtime.jsonl") }

func FilesOldestFirst(root string) ([]string, error) {
	logRoot := filepath.Join(root, "logs")
	entries, err := os.ReadDir(logRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type rotated struct {
		index int
		path  string
	}
	var rotations []rotated
	current := ""
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "runtime.jsonl" {
			current = filepath.Join(logRoot, name)
			continue
		}
		if !strings.HasPrefix(name, "runtime.jsonl.") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(name, "runtime.jsonl."))
		if err == nil && index > 0 {
			rotations = append(rotations, rotated{index: index, path: filepath.Join(logRoot, name)})
		}
	}
	sort.Slice(rotations, func(i, j int) bool { return rotations[i].index > rotations[j].index })
	files := make([]string, 0, len(rotations)+1)
	for _, item := range rotations {
		files = append(files, item.path)
	}
	if current != "" {
		files = append(files, current)
	}
	return files, nil
}

func Read(root string, query Query) ([]Event, error) {
	files, err := FilesOldestFirst(root)
	if err != nil {
		return nil, err
	}
	var events []Event
	for _, file := range files {
		if err := ReadFile(file, func(event Event) error {
			if query.Match(event) {
				events = append(events, event)
			}
			return nil
		}); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if query.Tail > 0 && len(events) > query.Tail {
		events = append([]Event(nil), events[len(events)-query.Tail:]...)
	}
	return events, nil
}
