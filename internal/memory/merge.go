package memory

import (
	"errors"
	"fmt"
	"strings"
)

var ErrMergeConflict = errors.New("memory merge conflict")

func MergeDocuments(registered, destination Document) (Document, error) {
	result := Document{}
	byKey := map[string]Entry{}
	order := make([]string, 0, len(registered.Entries)+len(destination.Entries))
	appendDocument := func(document Document) error {
		for _, entry := range normalizeDocument(document).Entries {
			key := strings.ToLower(strings.TrimSpace(entry.Scope)) + "\x00" + strings.ToLower(strings.TrimSpace(entry.Key))
			if current, ok := byKey[key]; ok {
				if current.Note != entry.Note {
					return fmt.Errorf("%w: scope=%q key=%q", ErrMergeConflict, entry.Scope, entry.Key)
				}
				continue
			}
			byKey[key] = entry
			order = append(order, key)
		}
		return nil
	}
	if err := appendDocument(registered); err != nil {
		return Document{}, err
	}
	if err := appendDocument(destination); err != nil {
		return Document{}, err
	}
	for _, key := range order {
		result.Entries = append(result.Entries, byKey[key])
	}
	return normalizeDocument(result), nil
}
