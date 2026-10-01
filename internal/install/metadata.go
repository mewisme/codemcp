package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/state"
)

const MetadataSchema = 1

var ErrMetadataNotFound = errors.New("install metadata not found")

type Method string

const (
	MethodDirect      Method = "direct"
	MethodHomebrew    Method = "homebrew"
	MethodScoop       Method = "scoop"
	MethodDebian      Method = "debian"
	MethodRPM         Method = "rpm"
	MethodGo          Method = "go"
	MethodStandalone  Method = "standalone"
	MethodDevelopment Method = "development"
	MethodUnknown     Method = "unknown"
)

type Metadata struct {
	Schema     int    `json:"schema"`
	Method     Method `json:"method"`
	Version    string `json:"version"`
	InstallDir string `json:"install_dir"`
	BinDir     string `json:"bin_dir,omitempty"`
}

func (m Metadata) Validate() error {
	if m.Schema != MetadataSchema {
		return fmt.Errorf("unsupported install metadata schema %d", m.Schema)
	}
	if !m.Method.Valid() {
		return fmt.Errorf("invalid install method %q", m.Method)
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("install metadata version is required")
	}
	if strings.TrimSpace(m.InstallDir) == "" {
		return errors.New("install metadata install_dir is required")
	}
	return nil
}

func (m Method) Valid() bool {
	switch m {
	case MethodDirect, MethodHomebrew, MethodScoop, MethodDebian, MethodRPM, MethodGo, MethodStandalone, MethodDevelopment, MethodUnknown:
		return true
	default:
		return false
	}
}

func ReadMetadata(path string) (Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Metadata{}, ErrMetadataNotFound
		}
		return Metadata{}, err
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("decode install metadata: %w", err)
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func WriteMetadata(path string, metadata Metadata) error {
	if err := metadata.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return state.WriteFileAtomic(path, data, 0600)
}
