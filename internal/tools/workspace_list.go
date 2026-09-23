package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
)

type WorkspaceListItem struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceRoot string `json:"workspace_root"`
}

type WorkspaceListResult struct {
	Workspaces []WorkspaceListItem `json:"workspaces"`
	Count      int                 `json:"count"`
}

func RegisterWorkspaceListTool(registry *Registry, runtime *Runtime) {
	registry.MustRegister("workspace_list", Schema{
		Name:         "workspace_list",
		Title:        "List Workspaces",
		Description:  "List workspaces registered on this CodeMCP runtime.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"workspaces":{"type":"array","items":{"type":"object","properties":{"workspace_id":{"type":"string"},"workspace_root":{"type":"string"}},"required":["workspace_id","workspace_root"],"additionalProperties":false}},"count":{"type":"integer"}},"required":["workspaces","count"],"additionalProperties":false}`),
		Annotations:  ToolAnnotations(RiskRead),
	}, func(context.Context, map[string]any) (Result, error) {
		value, err := runtime.localWorkspaceList()
		if err != nil {
			return Result{}, err
		}
		return JSONResult(value), nil
	})
}

func (r *Runtime) localWorkspaceList() (WorkspaceListResult, error) {
	if r == nil || r.Workspaces == nil {
		return WorkspaceListResult{}, errors.New("workspace manager is unavailable")
	}
	items, err := r.Workspaces.List()
	if err != nil {
		return WorkspaceListResult{}, err
	}
	result := WorkspaceListResult{Workspaces: make([]WorkspaceListItem, 0, len(items))}
	for _, item := range items {
		result.Workspaces = append(result.Workspaces, WorkspaceListItem{WorkspaceID: item.ID, WorkspaceRoot: item.Path})
	}
	sort.Slice(result.Workspaces, func(i, j int) bool { return result.Workspaces[i].WorkspaceID < result.Workspaces[j].WorkspaceID })
	result.Count = len(result.Workspaces)
	return result, nil
}
