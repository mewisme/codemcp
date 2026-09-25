package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryNormalizesAndValidatesToolSchemas(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("normalized", Schema{Name: "normalized"}, func(context.Context, map[string]any) (Result, error) {
		return TextResult("ok"), nil
	}); err != nil {
		t.Fatal(err)
	}
	schema, ok := registry.Schema("normalized")
	if !ok || string(schema.InputSchema) != `{"type":"object"}` {
		t.Fatalf("normalized schema = %#v", schema)
	}

	cases := map[string]Schema{
		"malformed-input": {
			Name:        "malformed-input",
			InputSchema: json.RawMessage(`{"type":"object"`),
		},
		"wrong-input-type": {
			Name:        "wrong-input-type",
			InputSchema: json.RawMessage(`{"type":"array"}`),
		},
		"unsupported-dialect": {
			Name:        "unsupported-dialect",
			InputSchema: json.RawMessage(`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`),
		},
		"external-ref": {
			Name:        "external-ref",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"$ref":"https://example.com/schema.json"}}}`),
		},
		"malformed-output": {
			Name:         "malformed-output",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":`),
		},
		"invalid-annotation": {
			Name:        "invalid-annotation",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Annotations: map[string]any{"readOnlyHint": "yes"},
		},
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			if err := registry.Register(name, schema, func(context.Context, map[string]any) (Result, error) {
				return TextResult("no"), nil
			}); err == nil {
				t.Fatal("invalid schema unexpectedly registered")
			}
			if _, ok := registry.Schema(name); ok {
				t.Fatal("invalid schema leaked into registry")
			}
		})
	}
}

func TestValidateSchemaDocumentAcceptsLocalDraft202012Refs(t *testing.T) {
	raw := json.RawMessage(`{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"$defs":{"identifier":{"type":"string","minLength":1}},
		"properties":{"id":{"$ref":"#/$defs/identifier"}},
		"required":["id"],
		"additionalProperties":false
	}`)
	if err := ValidateSchemaDocument(raw, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchemaDocument(json.RawMessage(`true`), false); err != nil {
		t.Fatalf("boolean output schema: %v", err)
	}
	if err := ValidateSchemaDocument(json.RawMessage(`true`), true); err == nil {
		t.Fatal("boolean input schema unexpectedly accepted")
	}
}

func TestValidateSchemaDocumentBoundsComplexity(t *testing.T) {
	tooLarge := json.RawMessage(`{"type":"object","description":"` + strings.Repeat("x", maxToolSchemaBytes) + `"}`)
	if err := ValidateSchemaDocument(tooLarge, true); err == nil {
		t.Fatal("oversized schema unexpectedly accepted")
	}

	value := any(map[string]any{"type": "object"})
	for range maxToolSchemaDepth + 1 {
		value = map[string]any{"allOf": []any{value}}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchemaDocument(raw, false); err == nil {
		t.Fatal("over-deep schema unexpectedly accepted")
	}
}

func TestOwnedReplacementValidatesBeforeMutation(t *testing.T) {
	registry := NewRegistry()
	handler := func(context.Context, map[string]any) (Result, error) { return TextResult("ok"), nil }
	if err := registry.ReplaceOwned("owner", map[string]Entry{
		"stable": {Schema: Schema{Name: "stable"}, Handler: handler},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceOwned("owner", map[string]Entry{
		"broken": {
			Schema:  Schema{Name: "broken", InputSchema: json.RawMessage(`{"type":"array"}`)},
			Handler: handler,
		},
	}); err == nil {
		t.Fatal("invalid replacement unexpectedly accepted")
	}
	if _, ok := registry.Schema("stable"); !ok {
		t.Fatal("failed replacement mutated the existing owned registry")
	}
}
