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
	Server                *sdkmcp.Server
	Tools                 *tools.Runtime
	Source                string
	SessionID             string
	BoundWorkspace        string
	ApprovalCallers       *approval.CallerRegistry
	ModernCallerID        string
	Profile               Profile
	AuthRequirements      []AuthRequirement
	Tasks                 *TaskRegistry
	FeatureRegistry       *FeatureRegistry
	Features              *FeatureExecutor
	resourceSubscriptions *sdkResourceSubscriptionTracker
	resourceProjection    *sdkResourceProjectionState
	resourceListSub       *ResourceListChangeSubscription
	resourceEventsCancel  context.CancelFunc
	resourceEventsDone    chan struct{}
}

func (s *SDKServer) Close() {
	if s != nil {
		s.stopResourceEventBridge()
	}
	if s != nil && s.Tasks != nil {
		s.Tasks.Close()
	}
}

func NewSDKServerWithTools(toolRuntime *tools.Runtime, source string) (*SDKServer, error) {
	return NewSDKServerWithSession(toolRuntime, source, "", "")
}

func NewSDKServerWithSession(toolRuntime *tools.Runtime, source, sessionID, boundWorkspace string) (*SDKServer, error) {
	return NewSDKServerWithProfile(toolRuntime, source, sessionID, boundWorkspace, BaseProfile())
}

func NewSDKServerWithProfile(toolRuntime *tools.Runtime, source, sessionID, boundWorkspace string, profile Profile) (*SDKServer, error) {
	return NewSDKServerWithProfileAuth(toolRuntime, source, sessionID, boundWorkspace, profile)
}

func NewSDKServerWithProfileAuth(toolRuntime *tools.Runtime, source, sessionID, boundWorkspace string, profile Profile, authRequirements ...AuthRequirement) (*SDKServer, error) {
	if toolRuntime == nil {
		toolRuntime = tools.NewRuntime()
	}
	if profile == nil {
		profile = BaseProfile()
	}
	features := FeatureRegistryForRuntime(toolRuntime)
	resourceListSub, _ := features.SubscribeResourceListChanges(0)
	descriptors := DescribeProtocolWithFeatures(nil, features, authRequirements...)
	implementation, options := ProjectSDKServer(profile, descriptors)
	featureExecutor := NewFeatureExecutor(features, toolRuntime, boundWorkspace, source)
	featureExecutor.Profile = profile
	resourceSubscriptions := newSDKResourceSubscriptionTracker()
	options.CompletionHandler = sdkCompletionHandler(featureExecutor)
	options.SubscribeHandler = sdkSubscribeHandler(featureExecutor, resourceSubscriptions)
	options.UnsubscribeHandler = sdkUnsubscribeHandler(featureExecutor, resourceSubscriptions)
	server := sdkmcp.NewServer(implementation, options)
	server.AddReceivingMiddleware(rejectDeprecatedResourceSubscriptionMiddleware())
	if err := InstallFeatureMethods(server, featureExecutor); err != nil {
		features.UnsubscribeResourceListChanges(resourceListSub)
		return nil, err
	}
	installPromptProjection(server, featureExecutor)
	resourceProjection := newSDKResourceProjectionState()
	if err := installResourceProjection(server, featureExecutor, resourceProjection); err != nil {
		features.UnsubscribeResourceListChanges(resourceListSub)
		return nil, err
	}
	if err := InstallSkillProjection(server, featureExecutor); err != nil {
		features.UnsubscribeResourceListChanges(resourceListSub)
		return nil, err
	}
	callers := approval.NewCallerRegistry()
	var tasks *TaskRegistry
	if ProfileBackgroundCapabilities(profile).TaskObservation {
		tasks = NewTaskRegistry(toolRuntime.Processes, toolRuntime.BackgroundDeliveries)
		if err := InstallTaskProjection(server, tasks, profile); err != nil {
			features.UnsubscribeResourceListChanges(resourceListSub)
			tasks.Close()
			return nil, err
		}
	}
	adapter := &SDKServer{Server: server, Tools: toolRuntime, Source: source, SessionID: sessionID, BoundWorkspace: boundWorkspace, ApprovalCallers: callers, ModernCallerID: callers.Caller("modern:" + source), Profile: profile, AuthRequirements: cloneAuthRequirements(authRequirements), Tasks: tasks, FeatureRegistry: features, Features: featureExecutor, resourceSubscriptions: resourceSubscriptions, resourceProjection: resourceProjection, resourceListSub: resourceListSub}
	for _, schema := range toolRuntime.List() {
		if err := adapter.addTool(schema); err != nil {
			features.UnsubscribeResourceListChanges(resourceListSub)
			return nil, err
		}
	}
	adapter.startResourceEventBridge()
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
	descriptor := DescribeTool(schema)
	descriptor.Security.AuthRequirements = cloneAuthRequirements(s.AuthRequirements)
	tool, err := ProjectSDKTool(s.Profile, descriptor, options)
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
		ctx = WithRequestBackgroundCapabilities(ctx, s.Profile, requestContext)
		if request != nil && request.Params != nil {
			ctx = withProfileRequestMetadata(ctx, s.Profile, map[string]any(request.Params.Meta))
		}
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
