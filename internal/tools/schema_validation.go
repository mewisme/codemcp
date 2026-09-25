package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	maxToolSchemaBytes   = 256 * 1024
	maxToolSchemaDepth   = 64
	maxToolSchemaNodes   = 8192
	toolSchemaResolveTTL = time.Second
)

const jsonSchema202012 = "https://json-schema.org/draft/2020-12/schema"

// ValidateSchemaDocument validates one bounded JSON Schema document using the
// draft 2020-12 resolver used by the MCP SDK. External references are not
// loaded; tool schemas must be self-contained.
func ValidateSchemaDocument(raw json.RawMessage, requireObjectInstance bool) error {
	if len(raw) == 0 {
		return errors.New("schema is required")
	}
	if len(raw) > maxToolSchemaBytes {
		return fmt.Errorf("schema exceeds %d-byte limit", maxToolSchemaBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("schema must contain exactly one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode schema trailing data: %w", err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		if _, booleanSchema := value.(bool); booleanSchema && !requireObjectInstance {
			return nil
		}
		return errors.New("schema must be a JSON object")
	}
	if requireObjectInstance {
		if kind, _ := root["type"].(string); kind != "object" {
			return errors.New("input schema must have type object")
		}
	}
	if declared, _ := root["$schema"].(string); declared != "" && strings.TrimSuffix(declared, "#") != jsonSchema202012 {
		return fmt.Errorf("unsupported JSON Schema dialect %q", declared)
	}
	nodes := 0
	if err := validateSchemaBounds(value, 1, &nodes); err != nil {
		return err
	}

	var parsed jsonschema.Schema
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("decode draft 2020-12 schema: %w", err)
	}
	type resolveResult struct{ err error }
	done := make(chan resolveResult, 1)
	go func() {
		_, err := parsed.Resolve(&jsonschema.ResolveOptions{})
		done <- resolveResult{err: err}
	}()
	timer := time.NewTimer(toolSchemaResolveTTL)
	defer timer.Stop()
	select {
	case result := <-done:
		if result.err != nil {
			return fmt.Errorf("resolve draft 2020-12 schema: %w", result.err)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("schema resolution exceeded %s", toolSchemaResolveTTL)
	}
}

func validateSchemaBounds(value any, depth int, nodes *int) error {
	if depth > maxToolSchemaDepth {
		return fmt.Errorf("schema exceeds maximum depth %d", maxToolSchemaDepth)
	}
	(*nodes)++
	if *nodes > maxToolSchemaNodes {
		return fmt.Errorf("schema exceeds maximum node count %d", maxToolSchemaNodes)
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if (key == "$ref" || key == "$dynamicRef") && item != nil {
				ref, ok := item.(string)
				if !ok {
					return fmt.Errorf("%s must be a string", key)
				}
				if ref != "" && !strings.HasPrefix(ref, "#") {
					return fmt.Errorf("external %s %q is not allowed", key, ref)
				}
			}
			if err := validateSchemaBounds(item, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := validateSchemaBounds(item, depth+1, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeRegisteredSchema(name string, schema Schema) (Schema, error) {
	name = strings.TrimSpace(name)
	if schema.Name == "" {
		schema.Name = name
	}
	if len(schema.InputSchema) == 0 {
		schema.InputSchema = json.RawMessage(`{"type":"object"}`)
	}
	if err := ValidateSchemaDocument(schema.InputSchema, true); err != nil {
		return Schema{}, fmt.Errorf("tool %q input schema: %w", name, err)
	}
	if len(schema.OutputSchema) > 0 {
		if err := ValidateSchemaDocument(schema.OutputSchema, false); err != nil {
			return Schema{}, fmt.Errorf("tool %q output schema: %w", name, err)
		}
	}
	for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
		if value, exists := schema.Annotations[key]; exists {
			if _, ok := value.(bool); !ok {
				return Schema{}, fmt.Errorf("tool %q annotation %s must be boolean", name, key)
			}
		}
	}
	return schema, nil
}
