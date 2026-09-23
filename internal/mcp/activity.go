package mcp

import (
	"time"

	"go.mewis.me/codemcp/internal/runtime/activity"
)

func (h *HTTPRuntime) EmitActivity(event activity.Event) {
	if h.Activity == nil {
		return
	}
	h.Activity.Publish(event)
}

func requestActivity(method string, params map[string]any, status, message string, duration time.Duration) activity.Event {
	event := activity.Event{
		Kind:       string(activity.EventRequest),
		Method:     method,
		Status:     status,
		DurationMS: duration.Milliseconds(),
		Message:    message,
		Raw:        map[string]any{"method": method, "params": params},
	}
	if method == "tools/call" {
		event.Tool, _ = params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		event.WorkspaceID, _ = args["workspace_id"].(string)
		event.Raw["tool"] = event.Tool
		event.Raw["arguments"] = args
	}
	return event
}
