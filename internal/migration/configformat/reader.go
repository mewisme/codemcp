package configformat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type Format string

const (
	JSON Format = "json"
	YAML Format = "yaml"
	TOML Format = "toml"
)

func Detect(path string) (Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return JSON, nil
	case ".yaml", ".yml":
		return YAML, nil
	case ".toml":
		return TOML, nil
	default:
		return "", fmt.Errorf("unsupported released structured file extension: %s", filepath.Ext(path))
	}
}

func Unmarshal(format Format, data []byte, value any) error {
	if format == JSON {
		return json.Unmarshal(data, value)
	}
	raw, err := DecodeGeneric(format, data)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, value)
}

func DecodeGeneric(format Format, data []byte) (any, error) {
	switch format {
	case JSON:
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var raw any
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		return normalizeJSONNumbers(raw), nil
	case YAML:
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		return normalizeYAML(raw), nil
	case TOML:
		var raw map[string]any
		if err := toml.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported released format: %s", format)
	}
}

func normalizeJSONNumbers(value any) any {
	switch current := value.(type) {
	case json.Number:
		text := current.String()
		if !strings.ContainsAny(text, ".eE") {
			if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
				return integer
			}
		}
		if decimal, err := strconv.ParseFloat(text, 64); err == nil {
			return decimal
		}
		return text
	case []any:
		for index := range current {
			current[index] = normalizeJSONNumbers(current[index])
		}
		return current
	case map[string]any:
		for key := range current {
			current[key] = normalizeJSONNumbers(current[key])
		}
		return current
	default:
		return current
	}
}

func normalizeYAML(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key := range current {
			current[key] = normalizeYAML(current[key])
		}
		return current
	case map[any]any:
		result := make(map[string]any, len(current))
		for key, nested := range current {
			result[fmt.Sprint(key)] = normalizeYAML(nested)
		}
		return result
	case []any:
		for index := range current {
			current[index] = normalizeYAML(current[index])
		}
		return current
	default:
		return current
	}
}
