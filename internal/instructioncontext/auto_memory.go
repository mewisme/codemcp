package instructioncontext

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/memory"
)

func LoadAutoMemory(store memory.Store, workspaceID string) (AutoMemorySnapshot, error) {
	return LoadAutoMemorySelected(store, workspaceID, "", 0, 0)
}

func LoadAutoMemorySelected(store memory.Store, workspaceID, query string, maxEntries, maxBytes int) (AutoMemorySnapshot, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return AutoMemorySnapshot{}, errors.New("workspace id is required")
	}
	document, err := store.LoadDocument(workspaceID)
	if err != nil {
		return AutoMemorySnapshot{}, err
	}
	entries := document.Entries
	canonicalBytes := len([]byte(memory.Render(document)))
	optimizationRecommended := memory.CompactionRecommended(document.Entries, canonicalBytes)
	if strings.TrimSpace(query) != "" {
		index := memory.NewMemoryIndex()
		if err := index.Rebuild(workspaceID, entries); err != nil {
			return AutoMemorySnapshot{}, err
		}
		matches, err := index.Search(workspaceID, memory.Query{Text: query, Limit: maxEntries})
		if err != nil {
			return AutoMemorySnapshot{}, err
		}
		entries = make([]memory.Entry, 0, len(matches))
		for _, match := range matches {
			entries = append(entries, match.Entry)
		}
	} else if maxEntries > 0 && len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	selected := memory.Document{Entries: entries}
	content := strings.TrimSpace(memory.Render(selected))
	truncated := len(entries) < len(document.Entries)
	if maxBytes > 0 && len([]byte(content)) > maxBytes {
		kept := make([]memory.Entry, 0, len(entries))
		for _, entry := range entries {
			candidate := strings.TrimSpace(memory.Render(memory.Document{Entries: append(append([]memory.Entry(nil), kept...), entry)}))
			if len([]byte(candidate)) > maxBytes {
				truncated = true
				break
			}
			kept = append(kept, entry)
			content = candidate
		}
		entries = kept
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return AutoMemorySnapshot{}, nil
	}
	return AutoMemorySnapshot{Loaded: true, Content: content, Bytes: len([]byte(content)), Entries: len(entries), Query: strings.TrimSpace(query), Truncated: truncated, OptimizationRecommended: optimizationRecommended}, nil
}
