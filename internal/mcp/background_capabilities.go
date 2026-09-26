package mcp

import (
	"context"

	"go.mewis.me/codemcp/internal/tools"
)

type BackgroundCapabilities struct {
	Execution          bool `json:"execution"`
	TaskObservation    bool `json:"task_observation"`
	ServerNotification bool `json:"server_notification"`
	ModelContinuation  bool `json:"model_continuation"`
	InFlightSteering   bool `json:"in_flight_steering"`
}

type backgroundCapabilityProfile interface {
	BackgroundCapabilities() BackgroundCapabilities
}

func ProfileBackgroundCapabilities(profile Profile) BackgroundCapabilities {
	if profile == nil {
		profile = BaseProfile()
	}
	provider, ok := profile.(backgroundCapabilityProfile)
	if !ok {
		return BackgroundCapabilities{}
	}
	return provider.BackgroundCapabilities()
}

func RequestBackgroundCapabilities(profile Profile, request RequestContext) BackgroundCapabilities {
	capabilities := ProfileBackgroundCapabilities(profile)
	capabilities.TaskObservation = capabilities.TaskObservation && requestExtensionNegotiated(request.NegotiatedExtensions, TasksExtensionID)
	capabilities.ModelContinuation = false
	capabilities.InFlightSteering = false
	return capabilities
}

func WithRequestBackgroundCapabilities(ctx context.Context, profile Profile, request RequestContext) context.Context {
	capabilities := RequestBackgroundCapabilities(profile, request)
	return tools.WithBackgroundCapabilities(ctx, tools.BackgroundCapabilities{
		TaskObservation: capabilities.TaskObservation, ServerNotification: capabilities.ServerNotification,
		ModelContinuation: capabilities.ModelContinuation, InFlightSteering: capabilities.InFlightSteering,
	})
}

func ProjectCapabilities(profile Profile, canonical Capabilities, taskTransport bool) Capabilities {
	projected := Capabilities{Tools: canonical.Tools}
	background := ProfileBackgroundCapabilities(profile)
	if taskTransport && background.TaskObservation {
		if value, ok := canonical.Extensions[TasksExtensionID]; ok {
			projected.Extensions = map[string]any{TasksExtensionID: value}
		}
	}
	return projected
}

func requestExtensionNegotiated(extensions map[string]any, extensionID string) bool {
	if len(extensions) == 0 {
		return false
	}
	_, ok := extensions[extensionID]
	return ok
}
