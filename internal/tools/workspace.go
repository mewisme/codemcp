package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"go.mewis.me/codemcp/internal/controlguard"
	shellruntime "go.mewis.me/codemcp/internal/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

type WorkspaceRegistrationResult struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceRoot string `json:"workspace_root"`
	InstanceID    string `json:"instance_id"`
	InstanceName  string `json:"instance_name"`
}

type WorkspaceStatusResult struct {
	WorkspaceID        string   `json:"workspace_id"`
	WorkspaceRoot      string   `json:"workspace_root"`
	ShellCWD           string   `json:"shell_cwd"`
	AllowedDirectories []string `json:"allowed_directories"`
	InstanceID         string   `json:"instance_id"`
	InstanceName       string   `json:"instance_name"`
	Online             bool     `json:"online"`
}

func RegisterWorkspaceTools(registry *Registry, manager *workspace.Manager, shells ...*shellruntime.Manager) {
	registry.MustRegister("workspace_register", Schema{
		Name:         "workspace_register",
		Title:        "Register Workspace",
		Description:  "Register a local workspace root. Re-registering the same canonical path returns the same workspace_id.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"workspace_root":{"type":"string"},"instance_id":{"type":"string"},"instance_name":{"type":"string"}},"required":["workspace_id","workspace_root","instance_id","instance_name"],"additionalProperties":false}`),
		Annotations:  ToolAnnotations(RiskEdit),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		path, err := requiredString(args, "path")
		if err != nil {
			return Result{}, err
		}
		if grant, ok := controlguard.GrantFromContext(ctx); !ok || grant.Code != controlguard.CodeControlPlaneMutation {
			return Result{}, controlguard.New(controlguard.CodeControlPlaneMutation, "registering a local workspace requires local approval", true, nil)
		}
		item, err := manager.Register(path)
		if err != nil {
			return Result{}, err
		}
		if err := manager.Reload(); err != nil {
			return Result{}, fmt.Errorf("workspace registered but runtime workspace reload failed: %w", err)
		}
		identity, err := manager.Instance()
		if err != nil {
			return Result{}, err
		}
		return JSONResult(WorkspaceRegistrationResult{WorkspaceID: item.ID, WorkspaceRoot: item.Path, InstanceID: identity.ID, InstanceName: identity.Name}), nil
	})

	registry.MustRegister("workspace_status", Schema{
		Name:         "workspace_status",
		Title:        "Workspace Status",
		Description:  "Resolve a registered workspace, its filesystem root, persisted shell cwd, and allowed directories.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"workspace_root":{"type":"string"},"shell_cwd":{"type":"string"},"allowed_directories":{"type":"array","items":{"type":"string"}},"instance_id":{"type":"string"},"instance_name":{"type":"string"},"online":{"type":"boolean"}},"required":["workspace_id","workspace_root","shell_cwd","allowed_directories","instance_id","instance_name","online"],"additionalProperties":false}`),
		Annotations:  ToolAnnotations(RiskRead),
	}, func(_ context.Context, args map[string]any) (Result, error) {
		id, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		ctx, err := manager.ResolveContext(id)
		if err != nil {
			return Result{}, err
		}
		roots, err := manager.EffectiveRoots(id)
		if err != nil {
			return Result{}, err
		}
		shellCWD := ctx.Root
		if len(shells) > 0 && shells[0] != nil {
			status, err := shells[0].Status(id)
			if err != nil {
				return Result{}, err
			}
			shellCWD = status.CWD
		}
		identity, err := manager.Instance()
		if err != nil {
			return Result{}, err
		}
		return JSONResult(WorkspaceStatusResult{WorkspaceID: ctx.Workspace.ID, WorkspaceRoot: ctx.Root, ShellCWD: shellCWD, AllowedDirectories: roots, InstanceID: identity.ID, InstanceName: identity.Name, Online: true}), nil
	})
}

func workspaceContext(manager *workspace.Manager, args map[string]any) (workspace.Workspace, string, error) {
	id, err := requiredString(args, "workspace_id")
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	ctx, err := manager.ResolveContext(id)
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	return ctx.Workspace, ctx.Root, nil
}

func workspacePath(manager *workspace.Manager, args map[string]any, key string, mustExist bool) (workspace.Workspace, string, string, error) {
	item, cwd, err := workspaceContext(manager, args)
	if err != nil {
		return workspace.Workspace{}, "", "", err
	}
	value, err := requiredString(args, key)
	if err != nil {
		return workspace.Workspace{}, "", "", err
	}
	resolved, err := manager.ResolvePath(item.ID, cwd, value, mustExist)
	if err != nil {
		return workspace.Workspace{}, "", "", fmt.Errorf("%s: %w", key, err)
	}
	return item, cwd, resolved, nil
}

func optionalString(args map[string]any, key string) (string, error) {
	value, exists := args[key]
	if !exists {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}
