package tools

import (
	"context"
)

type InputRound struct {
	RequestState   string
	InputResponses map[string]any
}

type inputRoundContextKey struct{}
type approvalRequestContextKey struct{}
type boundWorkspaceContextKey struct{}
type backgroundCapabilitiesContextKey struct{}

type BackgroundCapabilities struct {
	TaskObservation    bool
	ServerNotification bool
	ModelContinuation  bool
	InFlightSteering   bool
}

func WithInputRound(ctx context.Context, requestState string, inputResponses map[string]any) context.Context {
	if requestState == "" && inputResponses == nil {
		return ctx
	}
	return context.WithValue(ctx, inputRoundContextKey{}, InputRound{RequestState: requestState, InputResponses: inputResponses})
}

func InputRoundFromContext(ctx context.Context) InputRound {
	value, _ := ctx.Value(inputRoundContextKey{}).(InputRound)
	return value
}

func WithApprovalRequest(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, approvalRequestContextKey{}, requestID)
}

func ApprovalRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(approvalRequestContextKey{}).(string)
	return value
}

func WithBoundWorkspace(ctx context.Context, workspaceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if workspaceID == "" {
		return ctx
	}
	return context.WithValue(ctx, boundWorkspaceContextKey{}, workspaceID)
}

func BoundWorkspace(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(boundWorkspaceContextKey{}).(string)
	return value
}

func WithBackgroundCapabilities(ctx context.Context, capabilities BackgroundCapabilities) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, backgroundCapabilitiesContextKey{}, capabilities)
}

func BackgroundCapabilitiesFromContext(ctx context.Context) BackgroundCapabilities {
	if ctx == nil {
		return BackgroundCapabilities{}
	}
	value, _ := ctx.Value(backgroundCapabilitiesContextKey{}).(BackgroundCapabilities)
	return value
}
