package config

import (
	"os"
	"path/filepath"

	"go.mewis.me/codemcp/internal/configformat"
)

type Store struct{ Path string }

func NewStore(path string) *Store { return &Store{Path: filepath.Clean(path)} }

func DefaultStore() *Store { return NewStore(DefaultPath()) }

func (s *Store) Load() (map[string]any, error) {
	data, _, err := readConfigFile(s.Path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := configformat.UnmarshalPath(s.Path, data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (s *Store) Save(value map[string]any) error {
	data, err := configformat.MarshalPath(s.Path, value)
	if err != nil {
		return err
	}
	return writeConfigFile(s.Path, data, 0600)
}
