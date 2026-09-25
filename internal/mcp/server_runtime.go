package mcp

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/upstream"
)

type Runtime struct {
	Tools   *tools.Runtime
	Profile Profile
}

func NewRuntime() *Runtime {
	return NewRuntimeWithTools(tools.NewRuntime())
}

func NewRuntimeWithTools(toolRuntime *tools.Runtime) *Runtime {
	return NewRuntimeWithProfile(toolRuntime, BaseProfile())
}

func NewRuntimeWithProfile(toolRuntime *tools.Runtime, profile Profile) *Runtime {
	if toolRuntime == nil {
		toolRuntime = tools.NewRuntime()
	}
	if profile == nil {
		profile = BaseProfile()
	}
	return &Runtime{Tools: toolRuntime, Profile: profile}
}

func (r *Runtime) Handle(ctx context.Context, method string, params map[string]any) (any, error) {
	switch method {
	case "server/discover":
		return BuildDiscoverResult(r.Profile), nil
	case "tools/list":
		descriptors := DescribeProtocol(r.Tools.List()).Tools
		projected, err := ProjectTools(r.Profile, descriptors, ToolProjectionOptions{})
		if err != nil {
			return nil, err
		}
		return cacheableCompleteResult(map[string]any{"tools": projected}), nil
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		meta, _ := params["_meta"].(map[string]any)
		ctx = withProfileRequestMetadata(ctx, r.Profile, meta)
		requestContext := RequestContextFromContext(ctx)
		if requestContext.ProtocolVersion == "" {
			if parsed, err := requestContextFromParams(params); err == nil {
				requestContext = parsed
				ctx = WithRequestContext(ctx, parsed)
			}
		}
		ctx = tools.WithInputRound(ctx, requestContext.RequestState, requestContext.InputResponses)
		ctx = tools.WithCallSource(ctx, "http")
		ctx = tools.WithCallDetails(ctx, "tools/call", params)
		ctx = upstream.WithRequestMeta(ctx, meta)
		result, err := r.Tools.Call(ctx, name, args)
		if errors.Is(err, tools.ErrToolNotFound) {
			return nil, ProtocolError(err)
		}
		return result, err
	default:
		return nil, NewError(ErrMethodNotFound, "method not found")
	}
}
