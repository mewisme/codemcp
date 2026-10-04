package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

type PromptProvider interface {
	ListPromptDefinitions(context.Context, string) (any, error)
	GetPromptDefinition(context.Context, string, string, map[string]string) (any, error)
	WritePromptDefinition(context.Context, string, string, any) (any, error)
	DeletePromptDefinition(context.Context, string, string) (any, error)
}

type promptProviderSlot struct {
	mu       sync.RWMutex
	provider PromptProvider
}

func (r *Runtime) SetPromptProvider(provider PromptProvider) {
	if r == nil {
		return
	}
	r.prompts.mu.Lock()
	r.prompts.provider = provider
	r.prompts.mu.Unlock()
}

func (r *Runtime) promptProvider() PromptProvider {
	if r == nil {
		return nil
	}
	r.prompts.mu.RLock()
	defer r.prompts.mu.RUnlock()
	return r.prompts.provider
}

func RegisterPromptTools(registry *Registry, runtime *Runtime) {
	if registry == nil {
		return
	}
	const workspaceSchema = `"workspace_id":{"type":"string"}`
	const promptSchema = `"definition":{"type":"object"}`
	register := func(name, description, properties, required string, risk Risk, handler Handler) {
		if properties != "" {
			properties = "," + properties
		}
		registry.MustRegister(name, Schema{Name: name, Description: description, InputSchema: json.RawMessage(`{"type":"object","properties":{` + workspaceSchema + properties + `},"required":["workspace_id"` + required + `],"additionalProperties":false}`), Annotations: ToolAnnotations(risk), Capability: toolCapability(CapabilityDomainPrompts)}, handler)
	}
	fail := func(err error) (Result, error) {
		if err == nil {
			err = errors.New("prompt operation failed")
		}
		message := []rune(err.Error())
		if len(message) > 1024 {
			message = append(message[:1024], '…')
		}
		return ErrorResult(errors.New(string(message))), nil
	}
	stringField := func(args map[string]any, name string) (string, error) {
		value, ok := args[name].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return "", errors.New(name + " must be a non-empty string")
		}
		return value, nil
	}
	register("list_prompts", "List resolved Prompt definitions for an authorized workspace.", "", "", RiskRead, func(ctx context.Context, args map[string]any) (Result, error) {
		provider := runtime.promptProvider()
		if provider == nil {
			return fail(errors.New("prompt service is unavailable"))
		}
		workspaceID, err := stringField(args, "workspace_id")
		if err != nil {
			return fail(err)
		}
		value, err := provider.ListPromptDefinitions(ctx, workspaceID)
		if err != nil {
			return fail(err)
		}
		return JSONResult(value), nil
	})
	register("get_prompt", "Render a Prompt for an authorized workspace without side effects.", `"name":{"type":"string"},"arguments":{"type":"object","additionalProperties":{"type":"string"}}`, `,"name"`, RiskRead, func(ctx context.Context, args map[string]any) (Result, error) {
		provider := runtime.promptProvider()
		if provider == nil {
			return fail(errors.New("prompt service is unavailable"))
		}
		workspaceID, err := stringField(args, "workspace_id")
		if err != nil {
			return fail(err)
		}
		name, err := stringField(args, "name")
		if err != nil {
			return fail(err)
		}
		values := map[string]string{}
		if rawValue, exists := args["arguments"]; exists {
			raw, ok := rawValue.(map[string]any)
			if !ok {
				return fail(errors.New("arguments must be an object"))
			}
			for key, value := range raw {
				text, ok := value.(string)
				if !ok {
					return fail(errors.New("prompt argument values must be strings"))
				}
				values[key] = text
			}
		}
		value, err := provider.GetPromptDefinition(ctx, workspaceID, name, values)
		if err != nil {
			return fail(err)
		}
		return JSONResult(value), nil
	})
	for _, mode := range []string{"create", "update"} {
		mode := mode
		register(mode+"_prompt", strings.ToUpper(mode[:1])+mode[1:]+" a workspace Prompt through canonical application ownership.", promptSchema, `,"definition"`, RiskEdit, func(ctx context.Context, args map[string]any) (Result, error) {
			provider := runtime.promptProvider()
			if provider == nil {
				return fail(errors.New("prompt service is unavailable"))
			}
			workspaceID, err := stringField(args, "workspace_id")
			if err != nil {
				return fail(err)
			}
			if _, ok := args["definition"].(map[string]any); !ok {
				return fail(errors.New("definition must be an object"))
			}
			value, err := provider.WritePromptDefinition(ctx, workspaceID, mode, args["definition"])
			if err != nil {
				return fail(err)
			}
			return JSONResult(value), nil
		})
	}
	register("delete_prompt", "Delete one exact workspace Prompt definition.", `"name":{"type":"string"}`, `,"name"`, RiskDestructive, func(ctx context.Context, args map[string]any) (Result, error) {
		provider := runtime.promptProvider()
		if provider == nil {
			return fail(errors.New("prompt service is unavailable"))
		}
		workspaceID, err := stringField(args, "workspace_id")
		if err != nil {
			return fail(err)
		}
		name, err := stringField(args, "name")
		if err != nil {
			return fail(err)
		}
		value, err := provider.DeletePromptDefinition(ctx, workspaceID, name)
		if err != nil {
			return fail(err)
		}
		return JSONResult(value), nil
	})
}
