package integrations024

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	currentformat "go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/integrations"
	"go.mewis.me/codemcp/internal/integrations/caveman"
	"go.mewis.me/codemcp/internal/integrations/ponytail"
	migrationformat "go.mewis.me/codemcp/internal/migration/configformat"
	"go.mewis.me/codemcp/internal/state"
)

const (
	SourceRelease      = "0.2.24"
	maxStructuredBytes = 16 << 20
)

type Input struct {
	SourcePath      string
	DestinationPath string
}

type Result struct {
	DestinationPath string                 `json:"destination_path"`
	SourceFormat    migrationformat.Format `json:"source_format"`
	Migrated        bool                   `json:"migrated"`
	AlreadyApplied  bool                   `json:"already_applied"`
}

type Inspection struct {
	SourceRelease       string                 `json:"source_release"`
	SourceFormat        migrationformat.Format `json:"source_format"`
	LegacyFeatures      bool                   `json:"legacy_features"`
	CurrentIntegrations bool                   `json:"current_integrations"`
	PonytailActive      bool                   `json:"ponytail_active"`
	PonytailMode        string                 `json:"ponytail_mode"`
	CavemanActive       bool                   `json:"caveman_active"`
	CavemanMode         string                 `json:"caveman_mode"`
}

func Transform(input Input) (Result, error) {
	input, format, err := normalizeInput(input)
	if err != nil {
		return Result{}, err
	}
	data, err := readSource(input.SourcePath)
	if err != nil {
		return Result{}, err
	}
	raw, err := migrationformat.DecodeGeneric(format, data)
	if err != nil {
		return Result{}, fmt.Errorf("decode released %s config: %w", SourceRelease, err)
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return Result{}, errors.New("released config must be an object")
	}
	migrated, err := migrateIntegrations(root)
	if err != nil {
		return Result{}, err
	}
	if !migrated && samePath(input.SourcePath, input.DestinationPath) {
		return Result{DestinationPath: input.DestinationPath, SourceFormat: format, AlreadyApplied: true}, nil
	}
	encoded, err := currentformat.EncodeGeneric(currentformat.JSON, root)
	if err != nil {
		return Result{}, fmt.Errorf("encode migrated config: %w", err)
	}
	if !samePath(input.SourcePath, input.DestinationPath) {
		if existing, err := readDestination(input.DestinationPath); err == nil {
			if bytes.Equal(existing, encoded) {
				return Result{DestinationPath: input.DestinationPath, SourceFormat: format, Migrated: migrated, AlreadyApplied: true}, nil
			}
			return Result{}, fmt.Errorf("integration migration destination conflict: %s already exists with different content", input.DestinationPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
	}
	if err := state.WriteFileAtomic(input.DestinationPath, encoded, 0600); err != nil {
		return Result{}, fmt.Errorf("write migrated config: %w", err)
	}
	return Result{DestinationPath: input.DestinationPath, SourceFormat: format, Migrated: migrated}, nil
}

func Inspect(sourcePath string) (Inspection, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return Inspection{}, errors.New("released config path is required")
	}
	absolute, err := filepath.Abs(sourcePath)
	if err != nil {
		return Inspection{}, fmt.Errorf("resolve released config path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect released config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Inspection{}, errors.New("released config must be a regular non-symlink file")
	}
	format, err := migrationformat.Detect(absolute)
	if err != nil {
		return Inspection{}, err
	}
	data, err := readSource(absolute)
	if err != nil {
		return Inspection{}, err
	}
	raw, err := migrationformat.DecodeGeneric(format, data)
	if err != nil {
		return Inspection{}, fmt.Errorf("decode released %s config: %w", SourceRelease, err)
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return Inspection{}, errors.New("released config must be an object")
	}
	_, legacy := root["features"]
	_, current := root["integrations"]
	if _, err := migrateIntegrations(root); err != nil {
		return Inspection{}, err
	}
	value := integrations.Default()
	if rawIntegrations, exists := root["integrations"]; exists {
		value, err = decodeIntegrations(rawIntegrations, false, false)
		if err != nil {
			return Inspection{}, fmt.Errorf("inspect released integrations: %w", err)
		}
	}
	return Inspection{
		SourceRelease: SourceRelease, SourceFormat: format,
		LegacyFeatures: legacy, CurrentIntegrations: current,
		PonytailActive: value.Ponytail.Active, PonytailMode: value.Ponytail.Mode,
		CavemanActive: value.Caveman.Active, CavemanMode: value.Caveman.Mode,
	}, nil
}

func normalizeInput(input Input) (Input, migrationformat.Format, error) {
	if strings.TrimSpace(input.SourcePath) == "" || strings.TrimSpace(input.DestinationPath) == "" {
		return Input{}, "", errors.New("source and destination config paths are required")
	}
	for name, value := range map[string]*string{"source": &input.SourcePath, "destination": &input.DestinationPath} {
		absolute, err := filepath.Abs(*value)
		if err != nil {
			return Input{}, "", fmt.Errorf("resolve %s config path: %w", name, err)
		}
		*value = filepath.Clean(absolute)
	}
	if strings.ToLower(filepath.Ext(input.DestinationPath)) != ".json" {
		return Input{}, "", errors.New("integration migration destination must be a JSON file")
	}
	info, err := os.Lstat(input.SourcePath)
	if err != nil {
		return Input{}, "", fmt.Errorf("inspect released config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Input{}, "", errors.New("released config must be a regular non-symlink file")
	}
	format, err := migrationformat.Detect(input.SourcePath)
	if err != nil {
		return Input{}, "", err
	}
	if samePath(input.SourcePath, input.DestinationPath) && format != migrationformat.JSON {
		return Input{}, "", errors.New("non-JSON released config requires a separate JSON migration destination")
	}
	return input, format, nil
}

func readSource(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxStructuredBytes {
		return nil, fmt.Errorf("released config exceeds %d bytes", maxStructuredBytes)
	}
	return os.ReadFile(path)
}

func readDestination(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("integration migration destination is not a regular non-symlink file: %s", path)
	}
	return os.ReadFile(path)
}

func migrateIntegrations(root map[string]any) (bool, error) {
	legacy, hasLegacy := root["features"]
	current, hasCurrent := root["integrations"]
	if hasLegacy && hasCurrent {
		return false, errors.New("released config contains both features and integrations; refusing ambiguous migration")
	}
	if !hasLegacy {
		if hasCurrent {
			if _, err := decodeIntegrations(current, false, false); err != nil {
				return false, fmt.Errorf("validate existing integrations: %w", err)
			}
		}
		return false, nil
	}
	value, err := decodeIntegrations(legacy, true, true)
	if err != nil {
		return false, fmt.Errorf("migrate released features: %w", err)
	}
	root["integrations"] = map[string]any{
		"ponytail": map[string]any{"active": value.Ponytail.Active, "mode": value.Ponytail.Mode},
		"caveman":  map[string]any{"active": value.Caveman.Active, "mode": value.Caveman.Mode},
	}
	delete(root, "features")
	return true, nil
}

func decodeIntegrations(raw any, allowEnabled, rejectUnknownIntegrations bool) (integrations.Config, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return integrations.Config{}, errors.New("integration settings must be an object")
	}
	value := integrations.Default()
	for key, nested := range object {
		switch key {
		case "ponytail":
			active, mode, err := decodeModeSettings(nested, value.Ponytail.Active, value.Ponytail.Mode, allowEnabled, "ponytail")
			if err != nil {
				return integrations.Config{}, err
			}
			normalized, ok := ponytail.NormalizeRuntimeMode(mode)
			if !ok {
				return integrations.Config{}, fmt.Errorf("ponytail mode is invalid: %q", mode)
			}
			value.Ponytail.Active, value.Ponytail.Mode = active, string(normalized)
		case "caveman":
			active, mode, err := decodeModeSettings(nested, value.Caveman.Active, value.Caveman.Mode, allowEnabled, "caveman")
			if err != nil {
				return integrations.Config{}, err
			}
			normalized, ok := caveman.NormalizeRuntimeMode(mode)
			if !ok {
				return integrations.Config{}, fmt.Errorf("caveman mode is invalid: %q", mode)
			}
			value.Caveman.Active, value.Caveman.Mode = active, string(normalized)
		default:
			if rejectUnknownIntegrations {
				return integrations.Config{}, fmt.Errorf("unsupported released feature %q", key)
			}
		}
	}
	return value, nil
}

func decodeModeSettings(raw any, defaultActive bool, defaultMode string, allowEnabled bool, name string) (bool, string, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return false, "", fmt.Errorf("%s settings must be an object", name)
	}
	active, mode := defaultActive, defaultMode
	var enabled *bool
	if allowEnabled {
		if rawEnabled, exists := object["enabled"]; exists {
			value, ok := rawEnabled.(bool)
			if !ok {
				return false, "", fmt.Errorf("%s.enabled must be a boolean", name)
			}
			enabled = &value
		}
	}
	if rawActive, exists := object["active"]; exists {
		value, ok := rawActive.(bool)
		if !ok {
			return false, "", fmt.Errorf("%s.active must be a boolean", name)
		}
		active = value
	} else if enabled != nil {
		active = *enabled
	}
	if rawMode, exists := object["mode"]; exists {
		value, ok := rawMode.(string)
		if !ok {
			return false, "", fmt.Errorf("%s.mode must be a string", name)
		}
		mode = value
	}
	for key := range object {
		if key == "active" || key == "mode" || allowEnabled && key == "enabled" {
			continue
		}
		return false, "", fmt.Errorf("unsupported %s setting %q", name, key)
	}
	return active, mode, nil
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
