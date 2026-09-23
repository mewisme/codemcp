package mcp

import "go.mewis.me/codemcp/internal/runtime/activity"

func (h *HTTPRuntime) PublishToolsChanged() {
	if h != nil {
		PublishToolsChanged(h.Activity)
	}
}

func PublishToolsChanged(stream *activity.Stream) {
	if stream != nil {
		stream.Publish(activity.Event{Kind: "mcp.tools.changed", Message: "tools/list cache invalidated"})
	}
}
