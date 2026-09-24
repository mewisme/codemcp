package configformat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type Format string

const JSON Format = "json"

func Detect(path string) (Format, error) {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return JSON, nil
	}
	return "", fmt.Errorf("unsupported structured file extension: %s", filepath.Ext(path))
}

func Marshal(format Format, value any) ([]byte, error) {
	if format != JSON {
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func Unmarshal(format Format, data []byte, value any) error {
	if format != JSON {
		return fmt.Errorf("unsupported format: %s", format)
	}
	return json.Unmarshal(data, value)
}

func MarshalPath(path string, value any) ([]byte, error) {
	format, err := Detect(path)
	if err != nil {
		return nil, err
	}
	return Marshal(format, value)
}

func UnmarshalPath(path string, data []byte, value any) error {
	format, err := Detect(path)
	if err != nil {
		return err
	}
	return Unmarshal(format, data, value)
}

func DecodeGeneric(format Format, data []byte) (any, error) {
	if format != JSON {
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	return normalizeJSONNumbers(raw), nil
}

func EncodeGeneric(format Format, value any) ([]byte, error) {
	if format != JSON {
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func MergeGeneric(base, overlay any) any {
	baseMap, baseOK := base.(map[string]any)
	overlayMap, overlayOK := overlay.(map[string]any)
	if !baseOK || !overlayOK {
		return overlay
	}
	result := make(map[string]any, len(baseMap)+len(overlayMap))
	for key, value := range baseMap {
		result[key] = value
	}
	for key, value := range overlayMap {
		if previous, exists := result[key]; exists {
			result[key] = MergeGeneric(previous, value)
		} else {
			result[key] = value
		}
	}
	return result
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
		for i := range current {
			current[i] = normalizeJSONNumbers(current[i])
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
