package memory

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/workspace"
)

const (
	maxBytes = 25_000
	maxLines = 200
)

type Entry struct {
	Scope string `json:"scope"`
	Key   string `json:"key,omitempty"`
	Note  string `json:"note"`
}

type Document struct {
	Entries []Entry `json:"entries"`
}

type Store struct {
	Path       string
	Root       string
	Workspaces *workspace.Manager
}

func DefaultRoot() string        { return configformat.RootPath() }
func NewStore(root string) Store { return Store{Root: root} }
func NewWorkspaceStore(root string, workspaces *workspace.Manager) Store {
	return Store{Root: root, Workspaces: workspaces}
}

func (s Store) Read() (string, error) {
	path := s.Path
	if path == "" {
		return "", errors.New("memory path is not configured")
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func (s Store) Write(value string) error {
	path := s.Path
	if path == "" {
		return errors.New("memory path is not configured")
	}
	return state.WriteFileAtomic(path, []byte(value), 0600)
}

func (s Store) WorkspacePath(workspaceID string) string {
	if s.Workspaces != nil {
		if local, err := s.Workspaces.LocalState(workspaceID); err == nil {
			return filepath.Join(local.MemoryRoot(), "MEMORY.md")
		}
	}
	root := s.Root
	if root == "" {
		root = DefaultRoot()
	}
	return filepath.Join(root, "workspaces", workspaceID, "MEMORY.md")
}

func (s Store) Load(workspaceID string) (string, error) {
	data, err := os.ReadFile(s.WorkspacePath(workspaceID))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	value := strings.Join(lines, "\n")
	if len([]byte(value)) > maxBytes {
		value = string([]byte(value)[:maxBytes])
	}
	return strings.TrimSpace(value), nil
}

func (s Store) LoadDocument(workspaceID string) (Document, error) {
	data, err := os.ReadFile(s.WorkspacePath(workspaceID))
	if os.IsNotExist(err) {
		return Document{}, nil
	}
	if err != nil {
		return Document{}, err
	}
	return Parse(string(data)), nil
}

func (s Store) SaveDocument(workspaceID string, document Document) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", errors.New("workspace id is required")
	}
	document = normalizeDocument(document)
	path := s.WorkspacePath(workspaceID)
	if err := state.WriteFileAtomic(path, []byte(Render(document)), 0600); err != nil {
		return "", err
	}
	return path, nil
}

func (s Store) Upsert(workspaceID, scope, key, note string) (string, error) {
	scope, note = normalizeName(scope), normalizeNote(note)
	if scope == "" {
		return "", errors.New("scope is required")
	}
	key = canonicalKey(scope, key)
	if note == "" {
		return "", errors.New("note is required")
	}
	document, err := s.LoadDocument(workspaceID)
	if err != nil {
		return "", err
	}
	updated := false
	for index := range document.Entries {
		if strings.EqualFold(document.Entries[index].Scope, scope) && strings.EqualFold(document.Entries[index].Key, key) {
			document.Entries[index] = Entry{Scope: document.Entries[index].Scope, Key: document.Entries[index].Key, Note: note}
			updated = true
			break
		}
	}
	if !updated {
		document.Entries = append(document.Entries, Entry{Scope: scope, Key: key, Note: note})
	}
	return s.SaveDocument(workspaceID, document)
}

func (s Store) Get(workspaceID, scope, key string) ([]Entry, error) {
	document, err := s.LoadDocument(workspaceID)
	if err != nil {
		return nil, err
	}
	scope = normalizeName(scope)
	hasKey := strings.TrimSpace(key) != ""
	if hasKey && scope == "" {
		return nil, errors.New("scope is required when key is provided")
	}
	key = canonicalKey(scope, key)
	entries := make([]Entry, 0, len(document.Entries))
	for _, item := range document.Entries {
		if scope != "" && !strings.EqualFold(item.Scope, scope) {
			continue
		}
		if hasKey && !strings.EqualFold(item.Key, key) {
			continue
		}
		entries = append(entries, item)
	}
	return entries, nil
}

func (s Store) Remove(workspaceID, scope, key string) (int, string, error) {
	scope = normalizeName(scope)
	if scope == "" {
		return 0, "", errors.New("scope is required")
	}
	hasKey := strings.TrimSpace(key) != ""
	key = canonicalKey(scope, key)
	document, err := s.LoadDocument(workspaceID)
	if err != nil {
		return 0, "", err
	}
	kept := make([]Entry, 0, len(document.Entries))
	removed := 0
	for _, item := range document.Entries {
		match := strings.EqualFold(item.Scope, scope) && (!hasKey || strings.EqualFold(item.Key, key))
		if match {
			removed++
			continue
		}
		kept = append(kept, item)
	}
	if removed == 0 {
		return 0, s.WorkspacePath(workspaceID), nil
	}
	document.Entries = kept
	path, err := s.SaveDocument(workspaceID, document)
	return removed, path, err
}

func Parse(value string) Document {
	document := Document{}
	currentScope, currentKey := "", ""
	lastScope, lastKey := "", ""
	for _, raw := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "### "):
			currentScope = normalizeName(strings.TrimPrefix(line, "## "))
			currentKey, lastScope, lastKey = "", "", ""
		case strings.HasPrefix(line, "### "):
			if currentScope != "" {
				currentKey = canonicalKey(currentScope, strings.TrimPrefix(line, "### "))
			}
			lastScope, lastKey = "", ""
		case line == "" || strings.HasPrefix(line, "# "):
			continue
		case strings.HasPrefix(line, "- "):
			note := normalizeNote(strings.TrimPrefix(line, "- "))
			scope, key := currentScope, currentKey
			if scope == "" {
				scope, key, note = "general", "", stripLegacyDate(note)
			}
			if scope != "" && note != "" {
				document.Entries = mergeEntry(document.Entries, Entry{Scope: scope, Key: key, Note: note})
				lastScope, lastKey = scope, key
			}
		default:
			if lastScope != "" {
				document.Entries = mergeEntry(document.Entries, Entry{Scope: lastScope, Key: lastKey, Note: normalizeNote(line)})
			}
		}
	}
	return normalizeDocument(document)
}

func Render(document Document) string {
	document = normalizeDocument(document)
	if len(document.Entries) == 0 {
		return ""
	}
	var builder strings.Builder
	currentScope := ""
	for _, item := range document.Entries {
		if !strings.EqualFold(currentScope, item.Scope) {
			builder.WriteString("## ")
			builder.WriteString(item.Scope)
			builder.WriteString("\n\n")
			currentScope = item.Scope
		}
		if item.Key != "" {
			builder.WriteString("### ")
			builder.WriteString(item.Key)
			builder.WriteByte('\n')
		}
		builder.WriteString("- ")
		builder.WriteString(item.Note)
		builder.WriteString("\n\n")
	}
	return strings.TrimRight(builder.String(), "\n") + "\n"
}

func normalizeDocument(document Document) Document {
	entries := make([]Entry, 0, len(document.Entries))
	for _, item := range document.Entries {
		item.Scope, item.Note = normalizeName(item.Scope), normalizeNote(item.Note)
		item.Key = canonicalKey(item.Scope, item.Key)
		if item.Scope == "" || item.Note == "" {
			continue
		}
		entries = mergeEntry(entries, item)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		leftScope, rightScope := strings.ToLower(entries[i].Scope), strings.ToLower(entries[j].Scope)
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		leftKey, rightKey := strings.ToLower(entries[i].Key), strings.ToLower(entries[j].Key)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		if entries[i].Scope != entries[j].Scope {
			return entries[i].Scope < entries[j].Scope
		}
		return entries[i].Key < entries[j].Key
	})
	return Document{Entries: entries}
}

func mergeEntry(entries []Entry, incoming Entry) []Entry {
	if incoming.Scope == "" || incoming.Note == "" {
		return entries
	}
	for index := range entries {
		if strings.EqualFold(entries[index].Scope, incoming.Scope) && strings.EqualFold(entries[index].Key, incoming.Key) {
			entries[index].Note = mergeNotes(entries[index].Note, incoming.Note)
			return entries
		}
	}
	return append(entries, incoming)
}

func mergeNotes(current, incoming string) string {
	current, incoming = normalizeNote(current), normalizeNote(incoming)
	if current == "" {
		return incoming
	}
	if incoming == "" || strings.EqualFold(current, incoming) {
		return current
	}
	return normalizeNote(current + " " + incoming)
}

func normalizeName(value string) string {
	return normalizeNote(strings.TrimLeft(strings.TrimSpace(value), "#"))
}

func canonicalKey(scope, key string) string {
	key = normalizeName(key)
	if strings.EqualFold(normalizeName(scope), key) {
		return ""
	}
	return key
}

func normalizeNote(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")), " ")
}

func stripLegacyDate(note string) string {
	if len(note) >= 12 && note[4] == '-' && note[7] == '-' && note[10] == ':' {
		return normalizeNote(note[11:])
	}
	return note
}
