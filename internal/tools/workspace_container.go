package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/workspace"
)

type WorkspaceContainerListItem struct {
	ContainerID    string `json:"container_id"`
	Name           string `json:"name"`
	WorkspaceCount int    `json:"workspace_count"`
}

type WorkspaceContainerListResult struct {
	Containers []WorkspaceContainerListItem `json:"containers"`
	Count      int                          `json:"count"`
}

type WorkspaceContainerMember struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceRoot string `json:"workspace_root"`
}

type WorkspaceContainerStatusResult struct {
	ContainerID    string                     `json:"container_id"`
	Name           string                     `json:"name"`
	Workspaces     []WorkspaceContainerMember `json:"workspaces"`
	WorkspaceCount int                        `json:"workspace_count"`
}

type WorkspaceContainerContextResult struct {
	ContainerID    string                     `json:"container_id"`
	Name           string                     `json:"name"`
	Workspaces     []WorkspaceContainerMember `json:"workspaces"`
	WorkspaceCount int                        `json:"workspace_count"`
	Instructions   string                     `json:"instructions"`
}

func RegisterWorkspaceContainerTools(registry *Registry, manager *workspace.Manager) {
	registry.MustRegister("workspace_container_list", Schema{
		Name:         "workspace_container_list",
		Title:        "List Workspace Containers",
		Description:  "List registered workspace containers. Containers are orchestration scopes; concrete tools still require a member ws_* workspace_id.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"containers":{"type":"array","items":{"type":"object","properties":{"container_id":{"type":"string"},"name":{"type":"string"},"workspace_count":{"type":"integer"}},"required":["container_id","name","workspace_count"],"additionalProperties":false}},"count":{"type":"integer"}},"required":["containers","count"],"additionalProperties":false}`),
		Annotations:  ToolAnnotations(RiskRead),
		Capability:   toolCapability(CapabilityDomainWorkspace),
	}, func(ctx context.Context, _ map[string]any) (Result, error) {
		span := tracepkg.Start(ctx, "WORKSPACE", "workspace.container.list", "Listing workspace containers")
		values, err := manager.ListContainers()
		if err != nil {
			span.FailMessage("Workspace container list failed", err)
			return Result{}, err
		}
		items := make([]WorkspaceContainerListItem, 0, len(values))
		for _, value := range values {
			items = append(items, WorkspaceContainerListItem{ContainerID: value.ID, Name: value.Name, WorkspaceCount: len(value.WorkspaceIDs)})
		}
		span.EndMessage("Workspace containers listed", tracepkg.Int("count", len(items)))
		return JSONResult(WorkspaceContainerListResult{Containers: items, Count: len(items)}), nil
	})

	registry.MustRegister("workspace_container_status", Schema{
		Name:         "workspace_container_status",
		Title:        "Workspace Container Status",
		Description:  "Resolve one workspace container and list its concrete member workspaces.",
		InputSchema:  containerOnlySchema(),
		OutputSchema: workspaceContainerStatusSchema(),
		Annotations:  ToolAnnotations(RiskRead),
		Capability:   toolCapability(CapabilityDomainWorkspace),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		containerID, err := requiredString(args, "container_id")
		if err != nil {
			return Result{}, err
		}
		span := tracepkg.Start(ctx, "WORKSPACE", "workspace.container.status", "Resolving workspace container", tracepkg.String("container_id", strings.TrimSpace(containerID)))
		value, err := manager.ResolveContainer(containerID)
		if err != nil {
			span.FailMessage("Workspace container status failed", err, tracepkg.String("container_id", strings.TrimSpace(containerID)))
			return Result{}, err
		}
		result := workspaceContainerStatus(value)
		span.EndMessage("Workspace container resolved", tracepkg.String("container_id", result.ContainerID), tracepkg.String("name", result.Name), tracepkg.Int("workspace_count", result.WorkspaceCount), tracepkg.Any("member_workspace_ids", containerMemberIDs(result.Workspaces)))
		return JSONResult(result), nil
	})

	registry.MustRegister("workspace_container_context", Schema{
		Name:         "workspace_container_context",
		Title:        "Workspace Container Context",
		Description:  "Bootstrap Agent orchestration for a workspace container without merging member project contexts.",
		InputSchema:  containerOnlySchema(),
		OutputSchema: workspaceContainerContextSchema(),
		Annotations:  ToolAnnotations(RiskRead),
		Capability:   toolCapability(CapabilityDomainWorkspace),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		containerID, err := requiredString(args, "container_id")
		if err != nil {
			return Result{}, err
		}
		span := tracepkg.Start(ctx, "WORKSPACE", "workspace.container.context", "Building workspace container context", tracepkg.String("container_id", strings.TrimSpace(containerID)))
		value, err := manager.ResolveContainer(containerID)
		if err != nil {
			span.FailMessage("Workspace container context failed", err, tracepkg.String("container_id", strings.TrimSpace(containerID)))
			return Result{}, err
		}
		status := workspaceContainerStatus(value)
		result := WorkspaceContainerContextResult{ContainerID: status.ContainerID, Name: status.Name, Workspaces: status.Workspaces, WorkspaceCount: status.WorkspaceCount, Instructions: workspaceContainerInstructions(status)}
		span.EndMessage("Workspace container context built", tracepkg.String("container_id", result.ContainerID), tracepkg.String("name", result.Name), tracepkg.Int("workspace_count", result.WorkspaceCount), tracepkg.Any("member_workspace_ids", containerMemberIDs(result.Workspaces)))
		return JSONResult(result), nil
	})
}

func containerOnlySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"container_id":{"type":"string"}},"required":["container_id"],"additionalProperties":false}`)
}

func workspaceContainerStatusSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"container_id":{"type":"string"},"name":{"type":"string"},"workspaces":{"type":"array","items":{"type":"object","properties":{"workspace_id":{"type":"string"},"workspace_root":{"type":"string"}},"required":["workspace_id","workspace_root"],"additionalProperties":false}},"workspace_count":{"type":"integer"}},"required":["container_id","name","workspaces","workspace_count"],"additionalProperties":false}`)
}

func workspaceContainerContextSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"container_id":{"type":"string"},"name":{"type":"string"},"workspaces":{"type":"array","items":{"type":"object","properties":{"workspace_id":{"type":"string"},"workspace_root":{"type":"string"}},"required":["workspace_id","workspace_root"],"additionalProperties":false}},"workspace_count":{"type":"integer"},"instructions":{"type":"string"}},"required":["container_id","name","workspaces","workspace_count","instructions"],"additionalProperties":false}`)
}

func workspaceContainerStatus(value workspace.ContainerContext) WorkspaceContainerStatusResult {
	members := make([]WorkspaceContainerMember, 0, len(value.Workspaces))
	for _, item := range value.Workspaces {
		members = append(members, WorkspaceContainerMember{WorkspaceID: item.ID, WorkspaceRoot: item.Path})
	}
	return WorkspaceContainerStatusResult{ContainerID: value.Container.ID, Name: value.Container.Name, Workspaces: members, WorkspaceCount: len(members)}
}

func workspaceContainerInstructions(value WorkspaceContainerStatusResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "This session targets workspace container %s (%s).\n\n", value.ContainerID, value.Name)
	builder.WriteString("A workspace container is an orchestration scope, not a filesystem workspace.\n")
	if len(value.Workspaces) == 0 {
		builder.WriteString("This container has no member workspaces. Do not invent a workspace target; ask the user to add or register a workspace before concrete project work.\n")
	} else {
		builder.WriteString("Member workspaces:\n")
		for _, item := range value.Workspaces {
			fmt.Fprintf(&builder, "- %s: %s\n", item.WorkspaceID, item.WorkspaceRoot)
		}
	}
	builder.WriteString("\nFor every filesystem, Git, shell, checkpoint, memory, rule, or project operation, explicitly choose one member ws_* workspace_id. Before substantial work in a selected member workspace, call project_context for that workspace with memory enabled. Keep cwd, rules, memory, checkpoints, permissions, and assumptions isolated between member workspaces. Do not pass this wsc_* container_id as workspace_id to workspace-scoped tools.")
	return builder.String()
}

func containerMemberIDs(values []WorkspaceContainerMember) []string {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.WorkspaceID)
	}
	return ids
}
