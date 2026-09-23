package instructionpolicy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
)

const Version = 1

type ResourceKind string

const (
	ResourceContext ResourceKind = "context"
	ResourceRules   ResourceKind = "rules"
	ResourceSkills  ResourceKind = "skills"
)

type SourcePolicy struct {
	Enabled *bool `json:"enabled,omitempty"`
	Context *bool `json:"context,omitempty"`
	Rules   *bool `json:"rules,omitempty"`
	Skills  *bool `json:"skills,omitempty"`
}

type GlobalRule struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
	Content string `json:"content"`
}

type Config struct {
	Version int                     `json:"version"`
	Context string                  `json:"context,omitempty"`
	Rules   []GlobalRule            `json:"rules,omitempty"`
	Sources map[string]SourcePolicy `json:"sources,omitempty"`
}

type Store struct{ Path string }

func DefaultPath() string {
	return filepath.Join(configformat.RootPath(), "instructions", "global.json")
}
func DefaultStore() *Store { return &Store{Path: DefaultPath()} }

func DefaultConfig() Config {
	return Config{Version: Version, Rules: []GlobalRule{}, Sources: map[string]SourcePolicy{}}
}

func (c Config) Enabled(provider string, kind ResourceKind) bool {
	policy, ok := c.Sources[ProviderID(provider)]
	if !ok {
		return true
	}
	if policy.Enabled != nil && !*policy.Enabled {
		return false
	}
	var value *bool
	switch kind {
	case ResourceContext:
		value = policy.Context
	case ResourceRules:
		value = policy.Rules
	case ResourceSkills:
		value = policy.Skills
	default:
		return true
	}
	return value == nil || *value
}

func ProviderID(source string) string { return strings.TrimPrefix(strings.TrimSpace(source), ".") }

func (s *Store) Load() (Config, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return Config{}, errors.New("instruction policy path is not configured")
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var value Config
	if err := json.Unmarshal(data, &value); err != nil {
		return Config{}, err
	}
	if value.Version == 0 {
		value.Version = Version
	}
	if value.Version != Version {
		return Config{}, errors.New("unsupported instruction policy version")
	}
	if value.Rules == nil {
		value.Rules = []GlobalRule{}
	}
	if value.Sources == nil {
		value.Sources = map[string]SourcePolicy{}
	}
	return value, nil
}

func (s *Store) Save(value Config) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return errors.New("instruction policy path is not configured")
	}
	value.Version = Version
	if value.Rules == nil {
		value.Rules = []GlobalRule{}
	}
	if value.Sources == nil {
		value.Sources = map[string]SourcePolicy{}
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(s.Path, append(data, '\n'), 0600)
}
