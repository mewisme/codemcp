package mcp

import "go.mewis.me/codemcp/internal/tools"

func ToolList(r *tools.Registry) map[string]any {
	descriptors := DescribeProtocol(r.ListSchemas()).Tools
	projected, err := ProjectTools(BaseProfile(), descriptors, ToolProjectionOptions{})
	if err != nil {
		return map[string]any{"tools": []ProjectedTool{}}
	}
	return map[string]any{"tools": projected}
}

func ToolChanged() map[string]any {
	return map[string]any{"method": "notifications/tools/list_changed"}
}
