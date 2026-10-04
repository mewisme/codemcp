package mcp

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/sequence"
)

type sdkResourceSubscriptionTracker struct {
	mu    sync.Mutex
	refs  map[string]int
	total int
}

func newSDKResourceSubscriptionTracker() *sdkResourceSubscriptionTracker {
	return &sdkResourceSubscriptionTracker{refs: map[string]int{}}
}

func (t *sdkResourceSubscriptionTracker) add(uri string) error {
	if t == nil {
		return errors.New("resource subscription tracker is unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total >= defaultMaxSubscriptions {
		return errors.New("too many active resource subscriptions")
	}
	t.refs[uri]++
	t.total++
	return nil
}

func (t *sdkResourceSubscriptionTracker) remove(uri string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.refs[uri] <= 1 {
		if t.refs[uri] == 1 && t.total > 0 {
			t.total--
		}
		delete(t.refs, uri)
		return
	}
	t.refs[uri]--
	if t.total > 0 {
		t.total--
	}
}

func (t *sdkResourceSubscriptionTracker) uris() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	values := make([]string, 0, len(t.refs))
	for uri := range t.refs {
		values = append(values, uri)
	}
	sort.Strings(values)
	return values
}

func sdkCompletionHandler(executor *FeatureExecutor, profile Profile, configuredSessionID string) func(context.Context, *sdkmcp.CompleteRequest) (*sdkmcp.CompleteResult, error) {
	return func(ctx context.Context, request *sdkmcp.CompleteRequest) (*sdkmcp.CompleteResult, error) {
		if executor == nil || request == nil || request.Params == nil || request.Params.Ref == nil {
			return nil, featureJSONRPCError(NewError(ErrInvalidParams, "completion params are required"))
		}
		meta := map[string]any(request.Params.Meta)
		ctx = withProfileRequestMetadata(ctx, profile, meta)
		requestContext := requestContextFromSDKCompletion(request)
		sessionID := ""
		if !requestContext.Modern() {
			sessionID = strings.TrimSpace(configuredSessionID)
			if sessionID == "" && request.Session != nil {
				sessionID = strings.TrimSpace(request.Session.ID())
			}
		}
		ctx = WithIngressIdentity(ctx, profile, meta, IngressIdentityOptions{MCPSessionID: sessionID})
		arguments := map[string]string{}
		if request.Params.Context != nil {
			for key, value := range request.Params.Context.Arguments {
				arguments[key] = value
			}
		}
		result, err := executor.Complete(ctx, CompletionRequest{
			Ref: CompletionReference{
				Type: request.Params.Ref.Type,
				Name: request.Params.Ref.Name,
				URI:  request.Params.Ref.URI,
			},
			Argument: CompletionArgument{
				Name:  request.Params.Argument.Name,
				Value: request.Params.Argument.Value,
			},
			Arguments: arguments,
		})
		if err != nil {
			return nil, featureJSONRPCError(err)
		}
		return &sdkmcp.CompleteResult{
			Completion: sdkmcp.CompletionResultDetails{
				Values: append([]string(nil), result.Values...),
				Total:  result.Total, HasMore: result.HasMore,
			},
		}, nil
	}
}

func sdkSubscribeHandler(executor *FeatureExecutor, tracker *sdkResourceSubscriptionTracker, profile Profile, configuredSessionID string) func(context.Context, *sdkmcp.SubscribeRequest) error {
	return func(ctx context.Context, request *sdkmcp.SubscribeRequest) error {
		if executor == nil || request == nil || request.Params == nil {
			return featureJSONRPCError(NewError(ErrInvalidParams, "resource subscription params are required"))
		}
		meta := map[string]any(request.Params.Meta)
		ctx = withProfileRequestMetadata(ctx, profile, meta)
		requestContext := requestContextFromSDKSubscribe(request)
		sessionID := ""
		if !requestContext.Modern() {
			sessionID = strings.TrimSpace(configuredSessionID)
			if sessionID == "" && request.Session != nil {
				sessionID = strings.TrimSpace(request.Session.ID())
			}
		}
		ctx = WithIngressIdentity(ctx, profile, meta, IngressIdentityOptions{MCPSessionID: sessionID})
		parsed, err := executor.AuthorizeResourceSubscription(ctx, request.Params.URI)
		if err != nil {
			return featureJSONRPCError(err)
		}
		if err := tracker.add(parsed.URI); err != nil {
			return featureJSONRPCError(NewError(ErrInternal, err.Error()))
		}
		return nil
	}
}

func sdkUnsubscribeHandler(executor *FeatureExecutor, tracker *sdkResourceSubscriptionTracker) func(context.Context, *sdkmcp.UnsubscribeRequest) error {
	return func(ctx context.Context, request *sdkmcp.UnsubscribeRequest) error {
		if executor == nil || request == nil || request.Params == nil {
			return featureJSONRPCError(NewError(ErrInvalidParams, "resource unsubscription params are required"))
		}
		parsed, err := ParseResourceURI(request.Params.URI)
		if err != nil {
			return featureJSONRPCError(NewError(ErrInvalidParams, "invalid resource unsubscription URI"))
		}
		tracker.remove(parsed.URI)
		return nil
	}
}

func rejectDeprecatedResourceSubscriptionMiddleware() sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, request sdkmcp.Request) (sdkmcp.Result, error) {
			switch method {
			case "resources/subscribe", "resources/unsubscribe":
				return nil, featureJSONRPCError(NewError(ErrMethodNotFound, "use subscriptions/listen for resource changes"))
			default:
				return next(ctx, method, request)
			}
		}
	}
}

func (s *SDKServer) startResourceEventBridge() {
	if s == nil || s.Server == nil || s.resourceEventsCancel != nil {
		return
	}
	var instructionSubscription *instructioncontext.ChangeSubscription
	if s.Tools != nil && s.Tools.InstructionChanges != nil {
		instructionSubscription, _ = s.Tools.InstructionChanges.Subscribe(0)
	}
	if instructionSubscription == nil && s.resourceListSub == nil {
		return
	}
	var instructionEvents <-chan instructioncontext.Change
	var instructionOverflow <-chan sequence.Overflow
	if instructionSubscription != nil {
		instructionEvents = instructionSubscription.Events
		instructionOverflow = instructionSubscription.Overflow
	}
	var resourceListEvents <-chan ResourceListChange
	var resourceListOverflow <-chan sequence.Overflow
	if s.resourceListSub != nil {
		resourceListEvents = s.resourceListSub.Events
		resourceListOverflow = s.resourceListSub.Overflow
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.resourceEventsCancel = cancel
	s.resourceEventsDone = make(chan struct{})
	go func() {
		defer close(s.resourceEventsDone)
		if instructionSubscription != nil {
			defer s.Tools.InstructionChanges.Unsubscribe(instructionSubscription)
		}
		if s.resourceListSub != nil {
			defer s.FeatureRegistry.UnsubscribeResourceListChanges(s.resourceListSub)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case change := <-instructionEvents:
				s.notifySDKResourceChange(change)
			case <-instructionOverflow:
				s.notifySDKResourceResync()
				s.Tools.InstructionChanges.AcknowledgeOverflow(instructionSubscription)
			case <-resourceListEvents:
				s.syncSDKResourceProjection()
			case <-resourceListOverflow:
				s.syncSDKResourceProjection()
				s.FeatureRegistry.AcknowledgeResourceListOverflow(s.resourceListSub)
			}
		}
	}()
}

func (s *SDKServer) stopResourceEventBridge() {
	if s == nil || s.resourceEventsCancel == nil {
		return
	}
	s.resourceEventsCancel()
	if s.resourceEventsDone != nil {
		<-s.resourceEventsDone
	}
	s.resourceEventsCancel = nil
	s.resourceEventsDone = nil
	s.resourceListSub = nil
}

func (s *SDKServer) syncSDKResourceProjection() {
	if s == nil || s.Server == nil || s.Features == nil || s.resourceProjection == nil {
		return
	}
	_ = installResourceProjection(s.Server, s.Features, s.resourceProjection)
}

func (s *SDKServer) notifySDKResourceChange(change instructioncontext.Change) {
	if s == nil || s.Server == nil || s.resourceSubscriptions == nil {
		return
	}
	for _, uri := range s.resourceSubscriptions.uris() {
		parsed, err := ParseResourceURI(uri)
		if err != nil || !instructionChangeAffectsResource(change, parsed) {
			continue
		}
		_ = s.Server.ResourceUpdated(context.Background(), &sdkmcp.ResourceUpdatedNotificationParams{URI: uri})
	}
}

func (s *SDKServer) notifySDKResourceResync() {
	if s == nil || s.Server == nil || s.resourceSubscriptions == nil {
		return
	}
	for _, uri := range s.resourceSubscriptions.uris() {
		_ = s.Server.ResourceUpdated(context.Background(), &sdkmcp.ResourceUpdatedNotificationParams{URI: uri})
	}
}
