package telegram

import (
	"context"
	"strings"
	"sync"
)

type RouteHandler func(context.Context, Update)

type routedHandler struct {
	id      uint64
	key     string
	handler RouteHandler
}

type Router struct {
	mu        sync.RWMutex
	nextID    uint64
	commands  []routedHandler
	callbacks []routedHandler
}

func NewRouter() *Router { return &Router{} }

func (r *Router) RegisterCommand(command string, handler RouteHandler) func() {
	command = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(command)), "/")
	return r.register(&r.commands, command, handler)
}

func (r *Router) RegisterCallback(handler RouteHandler) func() {
	return r.register(&r.callbacks, "*", handler)
}

func (r *Router) register(target *[]routedHandler, key string, handler RouteHandler) func() {
	if r == nil || key == "" || handler == nil {
		return func() {}
	}
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	*target = append(*target, routedHandler{id: id, key: key, handler: handler})
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for index, item := range *target {
			if item.id == id {
				*target = append((*target)[:index], (*target)[index+1:]...)
				return
			}
		}
	}
}

func (r *Router) Dispatch(ctx context.Context, update Update) bool {
	if r == nil {
		return false
	}
	if update.CallbackQuery != nil {
		for _, item := range r.snapshot(false) {
			item.handler(ctx, update)
			return true
		}
		return false
	}
	if update.Message == nil {
		return false
	}
	command := messageCommand(update.Message.Text)
	if command == "" {
		return false
	}
	for _, item := range r.snapshot(true) {
		if item.key == command {
			item.handler(ctx, update)
			return true
		}
	}
	return false
}

func (r *Router) snapshot(commands bool) []routedHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	source := r.callbacks
	if commands {
		source = r.commands
	}
	return append([]routedHandler(nil), source...)
}

func messageCommand(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return ""
	}
	command := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	if at := strings.IndexByte(command, '@'); at >= 0 {
		command = command[:at]
	}
	return command
}
