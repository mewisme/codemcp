package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestReplaceOwnedIsAtomicAndRemovesStaleTools(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister("native", Schema{Name: "native", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, map[string]any) (Result, error) {
		return TextResult("native"), nil
	})
	if err := registry.ReplaceOwned("upstream:a", map[string]Entry{
		"a__one": {Schema: Schema{Name: "a__one"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("one"), nil }},
		"a__two": {Schema: Schema{Name: "a__two"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("two"), nil }},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceOwned("upstream:a", map[string]Entry{
		"a__two": {Schema: Schema{Name: "a__two"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("two"), nil }},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Call(context.Background(), "a__one", nil); err == nil {
		t.Fatal("stale owned tool still exists")
	}
	if _, err := registry.Call(context.Background(), "native", nil); err != nil {
		t.Fatal("native tool was removed")
	}
}
func TestRegistryOwnedReplacementSignalsChange(t *testing.T) {
	registry := NewRegistry()
	changes := registry.SubscribeChanges()
	defer registry.UnsubscribeChanges(changes)
	if err := registry.ReplaceOwned("upstream:test", map[string]Entry{
		"test__one": {Schema: Schema{Name: "test__one"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("one"), nil }},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
	default:
		t.Fatal("owned replacement did not signal registry change")
	}
}

func TestOwnedReplacementPreservesCapabilityMetadata(t *testing.T) {
	registry := NewRegistry()
	handler := func(context.Context, map[string]any) (Result, error) { return TextResult("ok"), nil }
	if err := registry.ReplaceOwned("upstream:test", map[string]Entry{
		"test__one": {
			Schema:  Schema{Name: "test__one", Capability: &CapabilityMetadata{Domain: "  Upstream  "}},
			Handler: handler,
		},
	}); err != nil {
		t.Fatal(err)
	}
	schema, ok := registry.Schema("test__one")
	if !ok || schema.Capability == nil || schema.Capability.Domain != "upstream" {
		t.Fatalf("owned capability = %#v", schema.Capability)
	}
	if err := registry.ReplaceOwnedPrefix("upstream:", map[string]map[string]Entry{
		"upstream:test": {
			"test__two": {
				Schema:  Schema{Name: "test__two", Capability: &CapabilityMetadata{Domain: "external-tools"}},
				Handler: handler,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	schema, ok = registry.Schema("test__two")
	if !ok || schema.Capability == nil || schema.Capability.Domain != "external-tools" {
		t.Fatalf("prefix replacement capability = %#v", schema.Capability)
	}
}

func TestReplaceOwnedPrefixSwapsMultipleOwnersAtomically(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister("native", Schema{Name: "native"}, func(context.Context, map[string]any) (Result, error) { return TextResult("native"), nil })
	if err := registry.ReplaceOwnedPrefix("upstream:", map[string]map[string]Entry{
		"upstream:a": {"a__one": {Schema: Schema{Name: "a__one"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("a"), nil }}},
		"upstream:b": {"b__one": {Schema: Schema{Name: "b__one"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("b"), nil }}},
	}); err != nil {
		t.Fatal(err)
	}
	changes := registry.SubscribeChanges()
	defer registry.UnsubscribeChanges(changes)
	if err := registry.ReplaceOwnedPrefix("upstream:", map[string]map[string]Entry{
		"upstream:a": {"a__two": {Schema: Schema{Name: "a__two"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("a2"), nil }}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a__one", "b__one"} {
		if _, err := registry.Call(context.Background(), name, nil); !errors.Is(err, ErrToolNotFound) {
			t.Fatalf("stale %s survived: %v", name, err)
		}
	}
	for _, name := range []string{"native", "a__two"} {
		if _, err := registry.Call(context.Background(), name, nil); err != nil {
			t.Fatalf("expected %s after swap: %v", name, err)
		}
	}
	select {
	case <-changes:
	default:
		t.Fatal("bulk owned replacement did not signal change")
	}
	select {
	case <-changes:
		t.Fatal("bulk owned replacement signaled more than once")
	default:
	}
}

func TestReplaceOwnedPrefixRejectsCollisionWithoutMutation(t *testing.T) {
	registry := NewRegistry()
	if err := registry.ReplaceOwned("upstream:old", map[string]Entry{
		"old__tool": {Schema: Schema{Name: "old__tool"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("old"), nil }},
	}); err != nil {
		t.Fatal(err)
	}
	changes := registry.SubscribeChanges()
	defer registry.UnsubscribeChanges(changes)
	err := registry.ReplaceOwnedPrefix("upstream:", map[string]map[string]Entry{
		"upstream:a": {"shared": {Schema: Schema{Name: "shared"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("a"), nil }}},
		"upstream:b": {"shared": {Schema: Schema{Name: "shared"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("b"), nil }}},
	})
	if !errors.Is(err, ErrToolAlreadyRegistered) {
		t.Fatalf("collision error = %v", err)
	}
	if _, err := registry.Call(context.Background(), "old__tool", nil); err != nil {
		t.Fatalf("old registry was mutated after collision: %v", err)
	}
	select {
	case <-changes:
		t.Fatal("failed bulk replacement signaled a change")
	default:
	}
}
