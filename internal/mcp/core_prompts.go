package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/tools"
)

const (
	PromptsListMethod         = "prompts/list"
	PromptsGetMethod          = "prompts/get"
	corePromptsRegistrationID = "codemcp-core-prompts"
)

func ensureCorePrompts(registry *FeatureRegistry, runtime *tools.Runtime) error {
	if registry == nil || runtime == nil {
		return nil
	}
	registry.corePromptsOnce.Do(func() {
		registry.corePromptsErr = registry.Register(FeatureRegistration{
			ID: corePromptsRegistrationID, Family: FeaturePrompts,
			Capabilities: FeatureCapabilities{Prompts: &PromptsCapability{}},
			Methods: []FeatureMethod{
				{Name: PromptsListMethod, Scope: FeatureScopeGlobal, Handler: func(ctx context.Context, request FeatureRequest) (map[string]any, error) {
					if cursor, ok := request.Params["cursor"]; ok && cursor != "" && cursor != nil {
						return nil, NewError(ErrInvalidParams, "prompt cursor is not valid")
					}
					workspaceID, err := authorizedPromptWorkspace(ctx, runtime, "", request.Params)
					if err != nil {
						return nil, err
					}
					store, err := promptStoreFor(runtime, workspaceID)
					if err != nil {
						return nil, err
					}
					prompts, err := store.List()
					if err != nil {
						return nil, err
					}
					items := make([]map[string]any, 0, len(prompts))
					for _, prompt := range prompts {
						items = append(items, promptWireDescriptor(prompt.Definition))
					}
					return map[string]any{"prompts": items}, nil
				}},
				{Name: PromptsGetMethod, Scope: FeatureScopeGlobal, Handler: func(ctx context.Context, request FeatureRequest) (map[string]any, error) {
					workspaceID, err := authorizedPromptWorkspace(ctx, runtime, "", request.Params)
					if err != nil {
						return nil, err
					}
					name, ok := request.Params["name"].(string)
					if !ok || strings.TrimSpace(name) == "" {
						return nil, NewError(ErrInvalidParams, "prompt name is required")
					}
					arguments := map[string]string{}
					if raw, exists := request.Params["arguments"]; exists {
						values, ok := raw.(map[string]any)
						if !ok {
							return nil, NewError(ErrInvalidParams, "prompt arguments must be an object")
						}
						for key, value := range values {
							text, ok := value.(string)
							if !ok {
								return nil, NewError(ErrInvalidParams, "prompt argument values must be strings")
							}
							arguments[key] = text
						}
					}
					store, err := promptStoreFor(runtime, workspaceID)
					if err != nil {
						return nil, err
					}
					prompt, err := store.Get(name)
					if err != nil {
						return nil, NewError(ErrInvalidParams, err.Error())
					}
					messages, err := instructioncontext.RenderPrompt(prompt.Definition, arguments)
					if err != nil {
						return nil, NewError(ErrInvalidParams, err.Error())
					}
					return map[string]any{"description": prompt.Definition.Description, "messages": messages}, nil
				}},
			},
		})
	})
	return registry.corePromptsErr
}

func authorizedPromptWorkspace(ctx context.Context, runtime *tools.Runtime, bound string, params map[string]any) (string, error) {
	if runtime == nil {
		return "", NewError(ErrInternal, "prompt runtime is unavailable")
	}
	if bound != "" {
		ctx = tools.WithBoundWorkspace(ctx, bound)
	}
	requested := ""
	if value, exists := params["workspace_id"]; exists {
		var ok bool
		requested, ok = value.(string)
		if !ok {
			return "", NewError(ErrInvalidParams, "workspace_id must be a string")
		}
	}
	if requested == "" && tools.BoundWorkspace(ctx) == "" {
		return "", nil
	}
	resolution, err := runtime.ResolveWorkspaceAccess(ctx, requested)
	if err != nil {
		return "", NewError(ErrInvalidParams, "prompt workspace access denied")
	}
	return resolution.WorkspaceID, nil
}

func promptStoreFor(runtime *tools.Runtime, workspaceID string) (*instructioncontext.PromptStore, error) {
	if workspaceID == "" {
		return instructioncontext.NewPromptStore(""), nil
	}
	if runtime == nil || runtime.Workspaces == nil {
		return nil, errors.New("prompt workspace manager is unavailable")
	}
	selected, err := runtime.Workspaces.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	return instructioncontext.NewPromptStore(selected.Path), nil
}

func promptWireDescriptor(value instructioncontext.PromptDefinition) map[string]any {
	arguments := make([]map[string]any, 0, len(value.Arguments))
	for _, argument := range value.Arguments {
		arguments = append(arguments, map[string]any{"name": argument.Name, "description": argument.Description, "required": argument.Required})
	}
	result := map[string]any{"name": value.Name}
	if value.Description != "" {
		result["description"] = value.Description
	}
	if len(arguments) > 0 {
		result["arguments"] = arguments
	}
	return result
}

// sdkPromptProjection refreshes SDK method handlers from the same Prompt service used by other interfaces.
type sdkPromptProjection struct {
	mu       sync.Mutex
	server   *sdkmcp.Server
	executor *FeatureExecutor
	known    map[string]string
}

func installPromptProjection(server *sdkmcp.Server, executor *FeatureExecutor) *sdkPromptProjection {
	projection := &sdkPromptProjection{server: server, executor: executor, known: map[string]string{}}
	server.AddReceivingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, request sdkmcp.Request) (sdkmcp.Result, error) {
			if method == PromptsListMethod || method == PromptsGetMethod {
				if err := projection.refresh(ctx); err != nil {
					return nil, featureJSONRPCError(err)
				}
			}
			return next(ctx, method, request)
		}
	})
	return projection
}

func (p *sdkPromptProjection) refresh(ctx context.Context) error {
	if p == nil || p.executor == nil || p.executor.Tools == nil {
		return errors.New("prompt projection is unavailable")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	workspaceID, err := authorizedPromptWorkspace(ctx, p.executor.Tools, p.executor.BoundWorkspace, nil)
	if err != nil {
		return err
	}
	store, err := promptStoreFor(p.executor.Tools, workspaceID)
	if err != nil {
		return err
	}
	values, err := store.List()
	if err != nil {
		return err
	}
	next := make(map[string]string, len(values))
	for _, prompt := range values {
		definition := prompt.Definition
		fingerprint := fmt.Sprintf("%s:%#v", prompt.Scope, definition)
		next[definition.Name] = fingerprint
		if p.known[definition.Name] == fingerprint {
			continue
		}
		arguments := make([]*sdkmcp.PromptArgument, 0, len(definition.Arguments))
		for _, argument := range definition.Arguments {
			arguments = append(arguments, &sdkmcp.PromptArgument{Name: argument.Name, Description: argument.Description, Required: argument.Required})
		}
		p.server.AddPrompt(&sdkmcp.Prompt{Name: definition.Name, Description: definition.Description, Arguments: arguments}, p.get)
	}
	var removed []string
	for name := range p.known {
		if _, exists := next[name]; !exists {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	if len(removed) > 0 {
		p.server.RemovePrompts(removed...)
	}
	p.known = next
	return nil
}

func (p *sdkPromptProjection) get(ctx context.Context, request *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
	if request == nil || request.Params == nil {
		return nil, featureJSONRPCError(NewError(ErrInvalidParams, "prompt name is required"))
	}
	if request.Session != nil {
		ctx = tools.WithMCPSessionID(ctx, request.Session.ID())
	}
	workspaceID, err := authorizedPromptWorkspace(ctx, p.executor.Tools, p.executor.BoundWorkspace, nil)
	if err != nil {
		return nil, featureJSONRPCError(err)
	}
	store, err := promptStoreFor(p.executor.Tools, workspaceID)
	if err != nil {
		return nil, featureJSONRPCError(err)
	}
	prompt, err := store.Get(request.Params.Name)
	if err != nil {
		return nil, featureJSONRPCError(NewError(ErrInvalidParams, err.Error()))
	}
	messages, err := instructioncontext.RenderPrompt(prompt.Definition, request.Params.Arguments)
	if err != nil {
		return nil, featureJSONRPCError(NewError(ErrInvalidParams, err.Error()))
	}
	result := &sdkmcp.GetPromptResult{Description: prompt.Definition.Description, Messages: make([]*sdkmcp.PromptMessage, 0, len(messages))}
	for _, message := range messages {
		result.Messages = append(result.Messages, &sdkmcp.PromptMessage{Role: sdkmcp.Role(message.Role), Content: &sdkmcp.TextContent{Text: message.Content.Text}})
	}
	return result, nil
}
