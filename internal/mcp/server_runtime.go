package mcp

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/upstream"
)

type Runtime struct {
	Tools *tools.Runtime
}

func NewRuntime() *Runtime {
	return NewRuntimeWithTools(tools.NewRuntime())
}

func NewRuntimeWithTools(toolRuntime *tools.Runtime) *Runtime {
	if toolRuntime == nil {
		toolRuntime = tools.NewRuntime()
	}
	return &Runtime{Tools: toolRuntime}
}

func (r *Runtime) Handle(ctx context.Context, method string, params map[string]any) (any, error) {
	switch method {
	case "server/discover":
		return Discover(), nil
	case "tools/list":
		return map[string]any{"tools": filterHeaderSafeTools(r.Tools.List()), "ttlMs": defaultCacheTTLMS, "cacheScope": defaultCacheScope, "resultType": "complete"}, nil
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		meta, _ := params["_meta"].(map[string]any)
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
			return nil, NewError(ErrInvalidParams, err.Error())
		}
		return result, err
	default:
		return nil, NewError(ErrMethodNotFound, "method not found")
	}
}
