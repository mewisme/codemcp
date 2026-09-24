package action

import (
	"context"
	"reflect"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/capability"
)

func TestRegistryRejectsInvalidAndDuplicateActions(t *testing.T) {
	if _, err := NewRegistry(Action{}); err == nil {
		t.Fatal("empty action unexpectedly accepted")
	}
	if _, err := NewRegistry(Action{ID: "a", Title: "A"}, Action{ID: "a", Title: "B"}); err == nil {
		t.Fatal("duplicate action unexpectedly accepted")
	}
	if _, err := NewRegistry(Action{ID: "cap", Title: "Capability", Capabilities: []capability.ID{"unknown"}}); err == nil {
		t.Fatal("unknown capability unexpectedly accepted")
	}
	if _, err := NewRegistry(Action{ID: "cap", Title: "Capability", Capabilities: []capability.ID{capability.VersionAbout, capability.VersionAbout}}); err == nil {
		t.Fatal("duplicate capability unexpectedly accepted")
	}
	if _, err := NewRegistry(Action{ID: "operation", Title: "Operation", Operation: "unknown"}); err == nil {
		t.Fatal("unknown operation unexpectedly accepted")
	}
}

func TestRegistryOperationIsCanonicalCapability(t *testing.T) {
	registry, err := NewRegistry(Action{ID: "about", Title: "About", Operation: capability.VersionAbout})
	if err != nil {
		t.Fatal(err)
	}
	item, ok := registry.Get("about")
	if !ok || item.Operation != capability.VersionAbout || !reflect.DeepEqual(item.Capabilities, []capability.ID{capability.VersionAbout}) {
		t.Fatalf("action=%#v ok=%t", item, ok)
	}
}

func TestRegistryOrderingAvailabilityAndExecution(t *testing.T) {
	registry, err := NewRegistry(
		Action{ID: "z", Title: "Zoo", Category: "B", Run: func(context.Context, Context) tea.Cmd { return nil }},
		Action{ID: "a", Title: "Alpha", Category: "A", Run: func(context.Context, Context) tea.Cmd { return nil }},
		Action{ID: "hidden", Title: "Hidden", Category: "A", Available: func(ctx Context) bool { return ctx.ResourceID != "" }, Run: func(context.Context, Context) tea.Cmd { return nil }},
	)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(actions []Action) []string {
		result := make([]string, 0, len(actions))
		for _, action := range actions {
			result = append(result, action.ID)
		}
		return result
	}
	if got, want := ids(registry.Actions(Context{})), []string{"a", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v want %v", got, want)
	}
	if got, want := ids(registry.Actions(Context{ResourceID: "x"})), []string{"a", "hidden", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resource actions = %v want %v", got, want)
	}
	if _, err := registry.Execute(context.Background(), "missing", Context{}); err == nil {
		t.Fatal("missing action unexpectedly executed")
	}
}

func TestRegistryMatchesShortcut(t *testing.T) {
	registry, err := NewRegistry(Action{ID: "open", Title: "Open", Shortcut: key.NewBinding(key.WithKeys("o")), Run: func(context.Context, Context) tea.Cmd { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := registry.MatchShortcut(tea.KeyPressMsg{Text: "o", Code: 'o'}, Context{})
	if !ok || action.ID != "open" {
		t.Fatalf("shortcut action = %#v ok=%t", action, ok)
	}
}
