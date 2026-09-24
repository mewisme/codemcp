package config

import (
	"os"
	"path/filepath"

	"go.mewis.me/codemcp/internal/configformat"
)

func RootPath() string { return configformat.RootPath() }

func DefaultPath() string { return filepath.Join(RootPath(), "config.json") }

func Source() (configformat.Source, error) { return SourceAt(RootPath()) }

func SourceAt(root string) (configformat.Source, error) {
	path := filepath.Join(root, "config.json")
	_, err := os.Stat(path)
	if err == nil {
		return configformat.Source{Path: path, Format: configformat.JSON, Ext: ".json", Exists: true}, nil
	}
	if os.IsNotExist(err) {
		return configformat.Source{Path: path, Format: configformat.JSON, Ext: ".json", Exists: false}, nil
	}
	return configformat.Source{}, err
}

func Path() string { return DefaultPath() }
