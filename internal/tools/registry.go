package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrToolNotFound          = errors.New("tool not found")
	ErrToolAlreadyRegistered = errors.New("tool already registered")
)

type Handler func(context.Context, map[string]any) (Result, error)

type Entry struct {
	Schema  Schema
	Handler Handler
}

type Registry struct {
	mu       sync.RWMutex
	tools    map[string]Entry
	owners   map[string]string
	watchers map[chan struct{}]struct{}
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Entry{}, owners: map[string]string{}, watchers: map[chan struct{}]struct{}{}}
}

func (r *Registry) Register(name string, schema Schema, handler Handler) error {
	return r.registerOwned("", name, schema, handler)
}

func (r *Registry) registerOwned(owner, name string, schema Schema, handler Handler) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tool name is required")
	}
	if handler == nil {
		return fmt.Errorf("tool %q handler is required", name)
	}
	var err error
	schema, err = normalizeRegisteredSchema(name, schema)
	if err != nil {
		return err
	}
	if schema.Name != name {
		return fmt.Errorf("tool schema name %q does not match registration name %q", schema.Name, name)
	}

	r.mu.Lock()
	if _, exists := r.tools[name]; exists {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrToolAlreadyRegistered, name)
	}
	r.tools[name] = Entry{Schema: schema, Handler: handler}
	if owner != "" {
		r.owners[name] = owner
	}
	r.mu.Unlock()
	r.signalChanged()
	return nil
}

func (r *Registry) MustRegister(name string, schema Schema, handler Handler) {
	if err := r.Register(name, schema, handler); err != nil {
		panic(err)
	}
}

func (r *Registry) ReplaceOwned(owner string, entries map[string]Entry) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return errors.New("owner is required")
	}
	for name, entry := range entries {
		if strings.TrimSpace(name) == "" || entry.Handler == nil {
			return fmt.Errorf("invalid owned tool %q", name)
		}
		var err error
		entry.Schema, err = normalizeRegisteredSchema(name, entry.Schema)
		if err != nil {
			return err
		}
		entries[name] = entry
		if entry.Schema.Name != name {
			return fmt.Errorf("owned schema name %q does not match %q", entry.Schema.Name, name)
		}
	}

	r.mu.Lock()
	for name := range entries {
		if _, exists := r.tools[name]; exists && r.owners[name] != owner {
			r.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrToolAlreadyRegistered, name)
		}
	}
	for name, currentOwner := range r.owners {
		if currentOwner == owner {
			delete(r.tools, name)
			delete(r.owners, name)
		}
	}
	for name, entry := range entries {
		r.tools[name] = entry
		r.owners[name] = owner
	}
	r.mu.Unlock()
	r.signalChanged()
	return nil
}

func (r *Registry) ReplaceOwnedPrefix(prefix string, replacements map[string]map[string]Entry) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return errors.New("owner prefix is required")
	}
	type ownedEntry struct {
		owner string
		entry Entry
	}
	prepared := make(map[string]ownedEntry)
	for owner, entries := range replacements {
		owner = strings.TrimSpace(owner)
		if owner == "" || !strings.HasPrefix(owner, prefix) {
			return fmt.Errorf("owner %q must start with %q", owner, prefix)
		}
		for name, entry := range entries {
			name = strings.TrimSpace(name)
			if name == "" || entry.Handler == nil {
				return fmt.Errorf("invalid owned tool %q", name)
			}
			var err error
			entry.Schema, err = normalizeRegisteredSchema(name, entry.Schema)
			if err != nil {
				return err
			}
			if entry.Schema.Name != name {
				return fmt.Errorf("owned schema name %q does not match %q", entry.Schema.Name, name)
			}
			if current, exists := prepared[name]; exists {
				return fmt.Errorf("%w: %s owned by both %s and %s", ErrToolAlreadyRegistered, name, current.owner, owner)
			}
			prepared[name] = ownedEntry{owner: owner, entry: entry}
		}
	}

	r.mu.Lock()
	for name := range prepared {
		if _, exists := r.tools[name]; exists && !strings.HasPrefix(r.owners[name], prefix) {
			r.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrToolAlreadyRegistered, name)
		}
	}
	for name, owner := range r.owners {
		if strings.HasPrefix(owner, prefix) {
			delete(r.tools, name)
			delete(r.owners, name)
		}
	}
	for name, item := range prepared {
		r.tools[name] = item.entry
		r.owners[name] = item.owner
	}
	r.mu.Unlock()
	r.signalChanged()
	return nil
}

func (r *Registry) SubscribeChanges() chan struct{} {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.watchers[ch] = struct{}{}
	r.mu.Unlock()
	return ch
}

func (r *Registry) UnsubscribeChanges(ch chan struct{}) {
	if ch == nil {
		return
	}
	r.mu.Lock()
	delete(r.watchers, ch)
	r.mu.Unlock()
}

func (r *Registry) signalChanged() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for ch := range r.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (r *Registry) ListSchemas() []Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Schema, 0, len(r.tools))
	for _, entry := range r.tools {
		out = append(out, entry.Schema)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Schema(name string) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.tools[name]
	return entry.Schema, ok
}

func (r *Registry) WorkspaceScoped(name string) (bool, error) {
	r.mu.RLock()
	entry, ok := r.tools[name]
	owner := r.owners[name]
	r.mu.RUnlock()
	if !ok || strings.HasPrefix(owner, "upstream:") {
		return false, nil
	}
	return schemaHasWorkspaceID(entry.Schema)
}

func (r *Registry) Call(ctx context.Context, name string, args map[string]any) (Result, error) {
	r.mu.RLock()
	entry, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrToolNotFound, name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if args == nil {
		args = map[string]any{}
	}
	return entry.Handler(ctx, args)
}
