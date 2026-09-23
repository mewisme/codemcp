package mcp

import "go.mewis.me/codemcp/internal/tools"

func ToolList(r *tools.Registry) map[string]any {
	return map[string]any{"tools": r.ListSchemas()}
}

func ToolChanged() map[string]any {
	return map[string]any{"method": "notifications/tools/list_changed"}
}
