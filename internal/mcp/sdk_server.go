package mcp

import (
	"context"
	"encoding/json"
	"errors"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/tools"
)

type SDKServer struct {
	Server          *sdkmcp.Server
	Tools           *tools.Runtime
	Source          string
	SessionID       string
	BoundWorkspace  string
	ApprovalCallers *approval.CallerRegistry
	ModernCallerID  string
	Profile         Profile
}

func NewSDKServerWithTools(toolRuntime *tools.Runtime, source string) (*SDKServer, error) {
	return NewSDKServerWithSession(toolRuntime, source, "", "")
}

func NewSDKServerWithSession(toolRuntime *tools.Runtime, source, sessionID, boundWorkspace string) (*SDKServer, error) {
	return newSDKServerWithProfile(toolRuntime, source, sessionID, boundWorkspace, BaseProfile())
}

func newSDKServerWithProfile(toolRuntime *tools.Runtime, source, sessionID, boundWorkspace string, profile Profile) (*SDKServer, error) {
	if toolRuntime == nil {
		toolRuntime = tools.NewRuntime()
	}
	if profile == nil {
		profile = BaseProfile()
	}
	descriptors := DescribeProtocol(nil)
	implementation, options := ProjectSDKServer(profile, descriptors)
	server := sdkmcp.NewServer(implementation, options)
	callers := approval.NewCallerRegistry()
	adapter := &SDKServer{Server: server, Tools: toolRuntime, Source: source, SessionID: sessionID, BoundWorkspace: boundWorkspace, ApprovalCallers: callers, ModernCallerID: callers.Caller("modern:" + source), Profile: profile}
	for _, schema := range filterHeaderSafeTools(toolRuntime.List()) {
		if err := adapter.addTool(schema); err != nil {
			return nil, err
		}
	}
	return adapter, nil
}

func (s *SDKServer) addTool(schema tools.Schema) error {
	options := ToolProjectionOptions{}
	if s.BoundWorkspace != "" {
		workspaceScoped, err := s.Tools.Registry.WorkspaceScoped(schema.Name)
		if err != nil {
			return err
		}
		options.BoundWorkspace = workspaceScoped
	}
	tool, err := ProjectSDKTool(s.Profile, DescribeTool(schema), options)
	if err != nil {
		return err
	}
	s.Server.AddTool(tool, func(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		args := map[string]any{}
		if request != nil && request.Params != nil && len(request.Params.Arguments) > 0 {
			if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
				return nil, err
			}
		}
		requestContext := RequestContextFromSDK(request)
		ctx = WithRequestContext(ctx, requestContext)
		if requestContext.Modern() {
			if s.ModernCallerID != "" {
				ctx = tools.WithApprovalCorrelation(ctx, s.ModernCallerID, idgen.Must("apr", 8))
			}
		} else {
			sessionID := s.SessionID
			if sessionID == "" && request != nil && request.Session != nil {
				sessionID = request.Session.ID()
			}
			if sessionID != "" {
				ctx = tools.WithMCPSessionID(ctx, sessionID)
				if s.ApprovalCallers != nil {
					ctx = tools.WithApprovalCorrelation(ctx, s.ApprovalCallers.Caller("legacy:sdk:"+sessionID), idgen.Must("apr", 8))
				}
			}
		}
		if s.BoundWorkspace != "" {
			ctx = tools.WithBoundWorkspace(ctx, s.BoundWorkspace)
		}
		ctx = tools.WithCallSource(ctx, s.Source)
		ctx = tools.WithInputRound(ctx, requestContext.RequestState, requestContext.InputResponses)
		result, err := s.Tools.Call(ctx, schema.Name, args)
		if errors.Is(err, tools.ErrToolNotFound) {
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		return sdkCallToolResult(result)
	})
	return nil
}

func sdkCallToolResult(result tools.Result) (*sdkmcp.CallToolResult, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var converted sdkmcp.CallToolResult
	if err := json.Unmarshal(data, &converted); err != nil {
		return nil, err
	}
	return &converted, nil
}

func inputResponses(values sdkmcp.InputResponseMap) map[string]any {
	if len(values) == 0 {
		return nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	result := map[string]any{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}
