package instructioncontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/configformat"
	statepkg "go.mewis.me/codemcp/internal/state"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	PromptDefinitionVersion = 1

	maxPromptNameBytes        = 128
	maxPromptDescriptionBytes = 4 << 10
	maxPromptArguments        = 32
	maxPromptArgumentName     = 64
	maxPromptArgumentDesc     = 2 << 10
	maxPromptMessages         = 64
	maxPromptMessageBytes     = 64 << 10
	maxPromptContentBytes     = 256 << 10
	maxPromptArgumentBytes    = 64 << 10
	maxPromptArgumentsBytes   = 256 << 10
	maxRenderedPromptBytes    = 512 << 10
	maxPromptFileBytes        = 512 << 10
)

type PromptScope string

const (
	PromptScopeGlobal    PromptScope = "global"
	PromptScopeWorkspace PromptScope = "workspace"
)

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type PromptMessage struct {
	Role    string            `json:"role"`
	Content PromptTextContent `json:"content"`
}

type PromptDefinition struct {
	Version     int              `json:"version"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Messages    []PromptMessage  `json:"messages"`
}

type ScopedPrompt struct {
	Scope      PromptScope      `json:"scope"`
	Definition PromptDefinition `json:"definition"`
}

type PromptStore struct {
	globalRoot    string
	workspaceRoot string
	workspacePath string
}

var promptStoreWriteMu sync.Mutex

func NewPromptStore(workspaceRoot string) *PromptStore {
	store := &PromptStore{globalRoot: filepath.Join(configformat.RootPath(), "prompts")}
	if strings.TrimSpace(workspaceRoot) != "" {
		store.workspacePath = filepath.Clean(workspaceRoot)
		store.workspaceRoot = workspacestate.New(store.workspacePath).PromptRoot()
	}
	return store
}

func (s *PromptStore) GlobalRoot() string {
	if s == nil {
		return ""
	}
	return s.globalRoot
}

func (s *PromptStore) WorkspaceRoot() string {
	if s == nil {
		return ""
	}
	return s.workspaceRoot
}

func ValidatePromptDefinition(value PromptDefinition) error {
	_, err := validateAndClonePrompt(value)
	return err
}

func RenderPrompt(value PromptDefinition, values map[string]string) ([]PromptMessage, error) {
	definition, err := validateAndClonePrompt(value)
	if err != nil {
		return nil, err
	}

	arguments := make(map[string]PromptArgument, len(definition.Arguments))
	for _, argument := range definition.Arguments {
		arguments[argument.Name] = argument
	}
	totalArgumentBytes := 0
	for name, value := range values {
		if _, ok := arguments[name]; !ok {
			return nil, fmt.Errorf("unknown prompt argument %q", name)
		}
		if len(value) > maxPromptArgumentBytes {
			return nil, fmt.Errorf("prompt argument %q exceeds %d bytes", name, maxPromptArgumentBytes)
		}
		totalArgumentBytes += len(value)
		if totalArgumentBytes > maxPromptArgumentsBytes {
			return nil, fmt.Errorf("prompt arguments exceed %d bytes", maxPromptArgumentsBytes)
		}
	}
	for _, argument := range definition.Arguments {
		if argument.Required {
			if _, ok := values[argument.Name]; !ok {
				return nil, fmt.Errorf("required prompt argument %q is missing", argument.Name)
			}
		}
	}

	result := make([]PromptMessage, len(definition.Messages))
	totalRendered := 0
	for index, message := range definition.Messages {
		rendered, err := renderPromptText(message.Content.Text, values)
		if err != nil {
			return nil, err
		}
		totalRendered += len(rendered)
		if totalRendered > maxRenderedPromptBytes {
			return nil, fmt.Errorf("rendered prompt exceeds %d bytes", maxRenderedPromptBytes)
		}
		result[index] = PromptMessage{
			Role: message.Role,
			Content: PromptTextContent{
				Type: "text",
				Text: rendered,
			},
		}
	}
	return result, nil
}

func (s *PromptStore) List() ([]ScopedPrompt, error) {
	if s == nil {
		return nil, errors.New("prompt store is unavailable")
	}
	merged := map[string]ScopedPrompt{}
	global, err := loadPromptRoot(s.globalRoot, PromptScopeGlobal, false)
	if err != nil {
		return nil, fmt.Errorf("load global prompts: %w", err)
	}
	for _, prompt := range global {
		merged[prompt.Definition.Name] = prompt
	}
	if s.workspaceRoot != "" {
		if err := validatePromptWorkspaceIdentity(s.workspacePath); err != nil {
			return nil, err
		}
		workspace, err := loadPromptRoot(s.workspaceRoot, PromptScopeWorkspace, false)
		if err != nil {
			return nil, fmt.Errorf("load workspace prompts: %w", err)
		}
		for _, prompt := range workspace {
			merged[prompt.Definition.Name] = prompt
		}
	}
	result := make([]ScopedPrompt, 0, len(merged))
	for _, prompt := range merged {
		result = append(result, prompt)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Definition.Name < result[j].Definition.Name
	})
	return result, nil
}

func (s *PromptStore) Get(name string) (ScopedPrompt, error) {
	if s == nil {
		return ScopedPrompt{}, errors.New("prompt store is unavailable")
	}
	if err := validatePromptIdentifier(name, maxPromptNameBytes, "prompt name"); err != nil {
		return ScopedPrompt{}, err
	}
	if s.workspaceRoot != "" {
		if err := validatePromptWorkspaceIdentity(s.workspacePath); err != nil {
			return ScopedPrompt{}, err
		}
		prompt, found, err := loadPromptFile(s.workspaceRoot, PromptScopeWorkspace, name)
		if err != nil {
			return ScopedPrompt{}, fmt.Errorf("load workspace prompt: %w", err)
		}
		if found {
			return prompt, nil
		}
	}
	prompt, found, err := loadPromptFile(s.globalRoot, PromptScopeGlobal, name)
	if err != nil {
		return ScopedPrompt{}, fmt.Errorf("load global prompt: %w", err)
	}
	if !found {
		return ScopedPrompt{}, fmt.Errorf("prompt %q not found", name)
	}
	return prompt, nil
}

// GetInScope reads one definition without applying workspace shadowing.
func (s *PromptStore) GetInScope(scope PromptScope, name string) (ScopedPrompt, error) {
	rootPath, err := s.scopeRoot(scope)
	if err != nil {
		return ScopedPrompt{}, err
	}
	if err := validatePromptIdentifier(name, maxPromptNameBytes, "prompt name"); err != nil {
		return ScopedPrompt{}, err
	}
	prompt, found, err := loadPromptFile(rootPath, scope, name)
	if err != nil {
		return ScopedPrompt{}, err
	}
	if !found {
		return ScopedPrompt{}, fmt.Errorf("prompt %q not found in %s scope: %w", name, scope, os.ErrNotExist)
	}
	return prompt, nil
}

func (s *PromptStore) scopeRoot(scope PromptScope) (string, error) {
	if s == nil {
		return "", errors.New("prompt store is unavailable")
	}
	switch scope {
	case PromptScopeGlobal:
		return s.globalRoot, nil
	case PromptScopeWorkspace:
		if s.workspaceRoot == "" {
			return "", errors.New("workspace prompt scope requires a workspace root")
		}
		if err := validatePromptWorkspaceIdentity(s.workspacePath); err != nil {
			return "", err
		}
		return s.workspaceRoot, nil
	default:
		return "", fmt.Errorf("unsupported prompt scope %q", scope)
	}
}

// Put enforces create and update semantics at the same rooted file authority as Save.
func (s *PromptStore) Put(scope PromptScope, mode string, value PromptDefinition) (ScopedPrompt, error) {
	if mode != "create" && mode != "update" {
		return ScopedPrompt{}, errors.New("prompt mode must be create or update")
	}
	promptStoreWriteMu.Lock()
	defer promptStoreWriteMu.Unlock()
	_, err := s.GetInScope(scope, value.Name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ScopedPrompt{}, err
	}
	if mode == "create" && err == nil {
		return ScopedPrompt{}, fmt.Errorf("prompt %q already exists in %s scope", value.Name, scope)
	}
	if mode == "update" && errors.Is(err, os.ErrNotExist) {
		return ScopedPrompt{}, err
	}
	return s.Save(scope, value)
}

// Delete removes an exact scoped definition, never a shadowed definition in another scope.
func (s *PromptStore) Delete(scope PromptScope, name string) error {
	promptStoreWriteMu.Lock()
	defer promptStoreWriteMu.Unlock()
	rootPath, err := s.scopeRoot(scope)
	if err != nil {
		return err
	}
	if err := validatePromptIdentifier(name, maxPromptNameBytes, "prompt name"); err != nil {
		return err
	}
	root, exists, err := openStablePromptRoot(rootPath, false)
	if err != nil {
		return err
	}
	if !exists {
		return os.ErrNotExist
	}
	defer root.Close()
	filename := promptFilename(name)
	info, err := root.Lstat(filename)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("prompt target is not a regular non-symlink file: %s", filename)
	}
	return root.Remove(filename)
}

func (s *PromptStore) Save(scope PromptScope, value PromptDefinition) (ScopedPrompt, error) {
	if s == nil {
		return ScopedPrompt{}, errors.New("prompt store is unavailable")
	}
	definition, err := validateAndClonePrompt(value)
	if err != nil {
		return ScopedPrompt{}, err
	}
	var rootPath string
	switch scope {
	case PromptScopeGlobal:
		rootPath = s.globalRoot
	case PromptScopeWorkspace:
		if s.workspaceRoot == "" {
			return ScopedPrompt{}, errors.New("workspace prompt scope requires a workspace root")
		}
		if err := validatePromptWorkspaceIdentity(s.workspacePath); err != nil {
			return ScopedPrompt{}, err
		}
		rootPath = s.workspaceRoot
	default:
		return ScopedPrompt{}, fmt.Errorf("unsupported prompt scope %q", scope)
	}

	root, exists, err := openStablePromptRoot(rootPath, true)
	if err != nil {
		return ScopedPrompt{}, err
	}
	if !exists {
		return ScopedPrompt{}, errors.New("prompt root was not created")
	}
	defer root.Close()

	filename := promptFilename(definition.Name)
	if info, err := root.Lstat(filename); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return ScopedPrompt{}, fmt.Errorf("prompt target is not a regular non-symlink file: %s", filename)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ScopedPrompt{}, err
	}

	data, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return ScopedPrompt{}, fmt.Errorf("encode prompt definition: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxPromptFileBytes {
		return ScopedPrompt{}, fmt.Errorf("prompt definition exceeds %d bytes", maxPromptFileBytes)
	}
	if err := statepkg.WriteFileAtomicRoot(root, filename, data, 0600); err != nil {
		return ScopedPrompt{}, fmt.Errorf("persist prompt definition: %w", err)
	}
	return ScopedPrompt{Scope: scope, Definition: definition}, nil
}

func validateAndClonePrompt(value PromptDefinition) (PromptDefinition, error) {
	if value.Version != PromptDefinitionVersion {
		return PromptDefinition{}, fmt.Errorf("prompt version must be %d", PromptDefinitionVersion)
	}
	if err := validatePromptIdentifier(value.Name, maxPromptNameBytes, "prompt name"); err != nil {
		return PromptDefinition{}, err
	}
	if len(value.Description) > maxPromptDescriptionBytes {
		return PromptDefinition{}, fmt.Errorf("prompt description exceeds %d bytes", maxPromptDescriptionBytes)
	}
	if len(value.Arguments) > maxPromptArguments {
		return PromptDefinition{}, fmt.Errorf("prompt arguments exceed %d entries", maxPromptArguments)
	}
	if len(value.Messages) == 0 {
		return PromptDefinition{}, errors.New("prompt must contain at least one message")
	}
	if len(value.Messages) > maxPromptMessages {
		return PromptDefinition{}, fmt.Errorf("prompt messages exceed %d entries", maxPromptMessages)
	}

	clone := PromptDefinition{
		Version:     value.Version,
		Name:        value.Name,
		Description: value.Description,
		Arguments:   append([]PromptArgument(nil), value.Arguments...),
		Messages:    make([]PromptMessage, len(value.Messages)),
	}
	knownArguments := make(map[string]struct{}, len(clone.Arguments))
	for index, argument := range clone.Arguments {
		if err := validatePromptIdentifier(argument.Name, maxPromptArgumentName, "prompt argument name"); err != nil {
			return PromptDefinition{}, err
		}
		if _, exists := knownArguments[argument.Name]; exists {
			return PromptDefinition{}, fmt.Errorf("duplicate prompt argument %q", argument.Name)
		}
		knownArguments[argument.Name] = struct{}{}
		if len(argument.Description) > maxPromptArgumentDesc {
			return PromptDefinition{}, fmt.Errorf("prompt argument %q description exceeds %d bytes", argument.Name, maxPromptArgumentDesc)
		}
		clone.Arguments[index] = argument
	}

	totalContent := 0
	for index, message := range value.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return PromptDefinition{}, fmt.Errorf("prompt message %d has unsupported role %q", index, message.Role)
		}
		if message.Content.Type != "text" {
			return PromptDefinition{}, fmt.Errorf("prompt message %d content type must be text", index)
		}
		if len(message.Content.Text) > maxPromptMessageBytes {
			return PromptDefinition{}, fmt.Errorf("prompt message %d exceeds %d bytes", index, maxPromptMessageBytes)
		}
		totalContent += len(message.Content.Text)
		if totalContent > maxPromptContentBytes {
			return PromptDefinition{}, fmt.Errorf("prompt content exceeds %d bytes", maxPromptContentBytes)
		}
		if err := validatePromptPlaceholders(message.Content.Text, knownArguments); err != nil {
			return PromptDefinition{}, fmt.Errorf("prompt message %d: %w", index, err)
		}
		clone.Messages[index] = PromptMessage{
			Role: message.Role,
			Content: PromptTextContent{
				Type: "text",
				Text: message.Content.Text,
			},
		}
	}
	return clone, nil
}

func validatePromptIdentifier(value string, maxBytes int, label string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not contain surrounding whitespace", label)
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		if index > 0 && (r == '_' || r == '-' || r == '.') {
			continue
		}
		return fmt.Errorf("%s contains unsupported character %q", label, r)
	}
	return nil
}

func validatePromptPlaceholders(text string, arguments map[string]struct{}) error {
	for offset := 0; offset < len(text); {
		open := strings.Index(text[offset:], "{{")
		close := strings.Index(text[offset:], "}}")
		if open < 0 {
			if close >= 0 {
				return errors.New("prompt content contains an unmatched closing placeholder")
			}
			return nil
		}
		open += offset
		if close >= 0 && offset+close < open {
			return errors.New("prompt content contains an unmatched closing placeholder")
		}
		end := strings.Index(text[open+2:], "}}")
		if end < 0 {
			return errors.New("prompt content contains an unterminated placeholder")
		}
		end += open + 2
		name := text[open+2 : end]
		if err := validatePromptIdentifier(name, maxPromptArgumentName, "prompt placeholder"); err != nil {
			return err
		}
		if _, ok := arguments[name]; !ok {
			return fmt.Errorf("prompt placeholder %q has no matching argument", name)
		}
		offset = end + 2
	}
	return nil
}

func renderPromptText(text string, values map[string]string) (string, error) {
	var builder strings.Builder
	for offset := 0; offset < len(text); {
		open := strings.Index(text[offset:], "{{")
		if open < 0 {
			builder.WriteString(text[offset:])
			break
		}
		open += offset
		builder.WriteString(text[offset:open])
		end := strings.Index(text[open+2:], "}}")
		if end < 0 {
			return "", errors.New("prompt content contains an unterminated placeholder")
		}
		end += open + 2
		name := text[open+2 : end]
		builder.WriteString(values[name])
		offset = end + 2
		if builder.Len() > maxRenderedPromptBytes {
			return "", fmt.Errorf("rendered prompt exceeds %d bytes", maxRenderedPromptBytes)
		}
	}
	return builder.String(), nil
}

func promptFilename(name string) string {
	return name + ".json"
}

func validatePromptWorkspaceIdentity(workspaceRoot string) error {
	if strings.TrimSpace(workspaceRoot) == "" {
		return errors.New("workspace root is required")
	}
	if _, err := workspacestate.New(workspaceRoot).LoadIdentity(); err != nil {
		return fmt.Errorf("validate workspace prompt scope: %w", err)
	}
	return nil
}

func loadPromptRoot(rootPath string, scope PromptScope, require bool) ([]ScopedPrompt, error) {
	root, exists, err := openStablePromptRoot(rootPath, false)
	if err != nil {
		return nil, err
	}
	if !exists {
		if require {
			return nil, os.ErrNotExist
		}
		return []ScopedPrompt{}, nil
	}
	defer root.Close()

	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	result := make([]ScopedPrompt, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.ToLower(filepath.Ext(name)) != ".json" {
			return nil, fmt.Errorf("unsupported prompt entry: %s", name)
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		prompt, found, err := loadPromptFileRoot(root, scope, base)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("prompt entry disappeared while loading: %s", name)
		}
		result = append(result, prompt)
	}
	return result, nil
}

func loadPromptFile(rootPath string, scope PromptScope, name string) (ScopedPrompt, bool, error) {
	root, exists, err := openStablePromptRoot(rootPath, false)
	if err != nil || !exists {
		return ScopedPrompt{}, false, err
	}
	defer root.Close()
	return loadPromptFileRoot(root, scope, name)
}

func loadPromptFileRoot(root *os.Root, scope PromptScope, name string) (ScopedPrompt, bool, error) {
	filename := promptFilename(name)
	info, err := root.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return ScopedPrompt{}, false, nil
	}
	if err != nil {
		return ScopedPrompt{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ScopedPrompt{}, false, fmt.Errorf("prompt entry is not a regular non-symlink file: %s", filename)
	}
	file, err := root.Open(filename)
	if err != nil {
		return ScopedPrompt{}, false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return ScopedPrompt{}, false, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return ScopedPrompt{}, false, fmt.Errorf("prompt entry changed while opening: %s", filename)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPromptFileBytes+1))
	if err != nil {
		return ScopedPrompt{}, false, err
	}
	if len(data) > maxPromptFileBytes {
		return ScopedPrompt{}, false, fmt.Errorf("prompt definition %s exceeds %d bytes", filename, maxPromptFileBytes)
	}

	var definition PromptDefinition
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return ScopedPrompt{}, false, fmt.Errorf("decode prompt %s: %w", filename, err)
	}
	if err := ensurePromptJSONEOF(decoder); err != nil {
		return ScopedPrompt{}, false, fmt.Errorf("decode prompt %s: %w", filename, err)
	}
	validated, err := validateAndClonePrompt(definition)
	if err != nil {
		return ScopedPrompt{}, false, fmt.Errorf("validate prompt %s: %w", filename, err)
	}
	if validated.Name != name {
		return ScopedPrompt{}, false, fmt.Errorf("prompt filename %s does not match definition name %q", filename, validated.Name)
	}
	return ScopedPrompt{Scope: scope, Definition: validated}, true, nil
}

func ensurePromptJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("prompt definition contains multiple JSON values")
}

func openStablePromptRoot(path string, create bool) (*os.Root, bool, error) {
	path = filepath.Clean(path)
	parentPath := filepath.Dir(path)
	base := filepath.Base(path)
	if create {
		if err := os.MkdirAll(parentPath, 0700); err != nil {
			return nil, false, err
		}
	}
	parentInfo, err := os.Lstat(parentPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("prompt parent is not a stable directory: %s", parentPath)
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, false, err
	}
	parentOpened, err := parent.Stat(".")
	if err != nil || !parentOpened.IsDir() || !os.SameFile(parentInfo, parentOpened) {
		_ = parent.Close()
		if err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("prompt parent changed while opening: %s", parentPath)
	}
	info, err := parent.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			_ = parent.Close()
			return nil, false, nil
		}
		if err := parent.Mkdir(base, 0700); err != nil {
			_ = parent.Close()
			return nil, false, err
		}
		info, err = parent.Lstat(base)
	}
	if err != nil {
		_ = parent.Close()
		return nil, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		_ = parent.Close()
		return nil, false, fmt.Errorf("prompt root is not a stable directory: %s", path)
	}
	root, err := parent.OpenRoot(base)
	_ = parent.Close()
	if err != nil {
		return nil, false, err
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		_ = root.Close()
		if err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("prompt root changed while opening: %s", path)
	}
	return root, true, nil
}

func validatePromptRoot(path string) error {
	_, err := loadPromptRoot(path, PromptScopeWorkspace, false)
	return err
}

func discoverPromptDefinitionPaths(path string) []string {
	values, err := loadPromptRoot(path, PromptScopeWorkspace, false)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, filepath.Join(path, promptFilename(value.Definition.Name)))
	}
	return result
}
