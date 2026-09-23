package config

import (
	"path/filepath"

	"go.mewis.me/codemcp/internal/configformat"
)

func RootPath() string { return configformat.RootPath() }

func Source() (configformat.Source, error) { return configformat.Discover(RootPath()) }

func DefaultPath() string { return filepath.Join(RootPath(), "config.json") }

func PathForFormat(format configformat.Format) string {
	return configformat.PathFor(RootPath(), "config", format)
}

func Path() string {
	source, err := Source()
	if err != nil {
		return DefaultPath()
	}
	return source.Path
}
