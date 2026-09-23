package action

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/capability"
)

type Registry struct {
	items map[string]Action
	order []string
}

func NewRegistry(actions ...Action) (*Registry, error) {
	registry := &Registry{items: make(map[string]Action, len(actions))}
	for _, action := range actions {
		if err := registry.Register(action); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (registry *Registry) Register(action Action) error {
	if registry == nil {
		return fmt.Errorf("action registry is nil")
	}
	action.ID = strings.TrimSpace(action.ID)
	action.Title = strings.TrimSpace(action.Title)
	action.Category = strings.TrimSpace(action.Category)
	if action.ID == "" {
		return fmt.Errorf("action id is required")
	}
	if action.Title == "" {
		return fmt.Errorf("action %q title is required", action.ID)
	}
	if _, exists := registry.items[action.ID]; exists {
		return fmt.Errorf("duplicate action id: %s", action.ID)
	}
	seenCapabilities := map[capability.ID]bool{}
	for _, id := range action.Capabilities {
		if _, ok := capability.Lookup(id); !ok {
			return fmt.Errorf("action %q references unknown capability %q", action.ID, id)
		}
		if seenCapabilities[id] {
			return fmt.Errorf("action %q repeats capability %q", action.ID, id)
		}
		seenCapabilities[id] = true
	}
	registry.items[action.ID] = action
	registry.order = append(registry.order, action.ID)
	registry.sort()
	return nil
}

func (registry *Registry) Get(id string) (Action, bool) {
	if registry == nil {
		return Action{}, false
	}
	action, ok := registry.items[strings.TrimSpace(id)]
	return action, ok
}

func (registry *Registry) Actions(ctx Context) []Action {
	if registry == nil {
		return nil
	}
	result := make([]Action, 0, len(registry.order))
	for _, id := range registry.order {
		action := registry.items[id]
		if action.IsAvailable(ctx) {
			result = append(result, action)
		}
	}
	return result
}

func (registry *Registry) All() []Action {
	if registry == nil {
		return nil
	}
	result := make([]Action, 0, len(registry.order))
	for _, id := range registry.order {
		result = append(result, registry.items[id])
	}
	return result
}

func (registry *Registry) Execute(ctx context.Context, id string, actionContext Context) (tea.Cmd, error) {
	action, ok := registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("unknown action: %s", id)
	}
	if !action.IsAvailable(actionContext) {
		return nil, fmt.Errorf("action is unavailable: %s", id)
	}
	if action.Run == nil {
		return nil, fmt.Errorf("action has no handler: %s", id)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return action.Run(ctx, actionContext), nil
}

func (registry *Registry) MatchShortcut(message tea.KeyPressMsg, ctx Context) (Action, bool) {
	for _, action := range registry.Actions(ctx) {
		if action.Shortcut.Enabled() && key.Matches(message, action.Shortcut) {
			return action, true
		}
	}
	return Action{}, false
}

func (registry *Registry) sort() {
	sort.SliceStable(registry.order, func(i, j int) bool {
		left, right := registry.items[registry.order[i]], registry.items[registry.order[j]]
		leftKey := strings.ToLower(left.Category + "\x00" + left.Title + "\x00" + left.ID)
		rightKey := strings.ToLower(right.Category + "\x00" + right.Title + "\x00" + right.ID)
		return leftKey < rightKey
	})
}
