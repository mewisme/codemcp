package tools

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	managedagent "go.mewis.me/codemcp/internal/agent"
)

const (
	AgentSpawnToolName  = "agent_spawn"
	AgentListToolName   = "agent_list"
	AgentWaitToolName   = "agent_wait"
	AgentSendToolName   = "agent_send"
	AgentCancelToolName = "agent_cancel"
)

const managedAgentSnapshotSchema = `{"type":"object","properties":{"id":{"type":"string","pattern":"^agent_[0-9a-f]{16}$"},"backend":{"type":"string"},"workspace_id":{"type":"string"},"parent_id":{"type":"string"},"depth":{"type":"integer"},"model":{"type":"string"},"reasoning_effort":{"type":"string"},"state":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"turn":{"type":"integer"},"revision":{"type":"integer"},"result":{"type":"string"},"error":{"type":"string"}},"required":["id","backend","workspace_id","depth","state","created_at","updated_at","turn","revision"],"additionalProperties":false}`

func RegisterManagedAgentTools(registry *Registry, runtime *Runtime) {
	if registry == nil {
		return
	}
	registerManagedAgentSpawnTool(registry, runtime)
	registerManagedAgentListTool(registry, runtime)
	registerManagedAgentWaitTool(registry, runtime)
	registerManagedAgentSendTool(registry, runtime)
	registerManagedAgentCancelTool(registry, runtime)
}

func registerManagedAgentSpawnTool(registry *Registry, runtime *Runtime) {
	registry.MustRegister(AgentSpawnToolName, Schema{
		Name:         AgentSpawnToolName,
		Title:        "Spawn Managed Agent",
		Description:  "Delegate meaningful, well-scoped independent work to a managed child when delegation or parallelism saves parent work. Make the prompt self-contained with only parent-only decisions, constraints, references, scope, and expected output; do not copy the full transcript or workspace context the child can load canonically. Do not use for trivial or immediately dependent work.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"prompt":{"type":"string","minLength":1,"maxLength":65536},"backend":{"type":"string","maxLength":64},"model":{"type":"string","maxLength":128},"reasoning_effort":{"type":"string","maxLength":64}},"required":["workspace_id","prompt"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(managedAgentSnapshotSchema),
		Annotations:  ToolAnnotations(RiskEdit),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		controller, err := managedAgentMCPController(ctx, runtime, false)
		if err != nil {
			return Result{}, err
		}
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		resolution, err := runtime.ResolveWorkspaceAccess(ctx, workspaceID)
		if err != nil {
			return Result{}, err
		}
		prompt, err := requiredString(args, "prompt")
		if err != nil {
			return Result{}, err
		}
		backend, err := optionalString(args, "backend")
		if err != nil {
			return Result{}, err
		}
		model, err := optionalString(args, "model")
		if err != nil {
			return Result{}, err
		}
		effort, err := optionalString(args, "reasoning_effort")
		if err != nil {
			return Result{}, err
		}
		snapshot, err := runtime.Agents.Spawn(ctx, controller, managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
			WorkspaceID: resolution.WorkspaceID,
			Prompt:      prompt, Backend: managedagent.BackendID(backend),
			Model: model, ReasoningEffort: effort,
		}})
		if err != nil {
			return Result{}, err
		}
		return JSONResult(snapshot), nil
	})
}

func registerManagedAgentListTool(registry *Registry, runtime *Runtime) {
	registry.MustRegister(AgentListToolName, Schema{
		Name: AgentListToolName, Title: "List Managed Agents",
		Description:  "List managed agents owned by the current trusted MCP session.",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":` + managedAgentSnapshotSchema + `}`),
		Annotations:  ToolAnnotations(RiskRead),
	}, func(ctx context.Context, _ map[string]any) (Result, error) {
		controller, err := managedAgentMCPController(ctx, runtime, true)
		if err != nil {
			return Result{}, err
		}
		items, err := runtime.Agents.List(ctx, controller)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(items), nil
	})
}

func registerManagedAgentWaitTool(registry *Registry, runtime *Runtime) {
	registry.MustRegister(AgentWaitToolName, Schema{
		Name: AgentWaitToolName, Title: "Wait for Managed Agent",
		Description:  "Wait briefly for an owned managed agent to change revision or reach a terminal state. Calls are bounded to 10 seconds; repeat only when the child result is actually needed.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string","pattern":"^agent_[0-9a-f]{16}$"},"after_revision":{"type":"integer","minimum":0},"timeout_ms":{"type":"integer","minimum":0,"maximum":10000,"default":10000}},"required":["agent_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(managedAgentSnapshotSchema),
		Annotations:  ToolAnnotations(RiskRead),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		controller, err := managedAgentMCPController(ctx, runtime, true)
		if err != nil {
			return Result{}, err
		}
		id, err := requiredManagedAgentID(args)
		if err != nil {
			return Result{}, err
		}
		afterRevision, err := optionalInt64(args, "after_revision", 0, 0, math.MaxInt64)
		if err != nil {
			return Result{}, err
		}
		timeoutMS, err := optionalInt(args, "timeout_ms", int(managedagent.MaxWaitDuration/time.Millisecond), 0, int(managedagent.MaxWaitDuration/time.Millisecond))
		if err != nil {
			return Result{}, err
		}
		snapshot, err := runtime.Agents.Wait(ctx, controller, id, uint64(afterRevision), time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(snapshot), nil
	})
}

func registerManagedAgentSendTool(registry *Registry, runtime *Runtime) {
	registry.MustRegister(AgentSendToolName, Schema{
		Name: AgentSendToolName, Title: "Send Managed Agent Follow-up",
		Description:  "Send a follow-up only to an owned live idle managed agent. Busy or terminal agents are rejected; messages are never silently queued.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string","pattern":"^agent_[0-9a-f]{16}$"},"message":{"type":"string","minLength":1,"maxLength":65536}},"required":["agent_id","message"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(managedAgentSnapshotSchema),
		Annotations:  ToolAnnotations(RiskEdit),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		controller, err := managedAgentMCPController(ctx, runtime, true)
		if err != nil {
			return Result{}, err
		}
		id, err := requiredManagedAgentID(args)
		if err != nil {
			return Result{}, err
		}
		message, err := requiredString(args, "message")
		if err != nil {
			return Result{}, err
		}
		snapshot, err := runtime.Agents.Send(ctx, controller, id, managedagent.Message{Content: message})
		if err != nil {
			return Result{}, err
		}
		return JSONResult(snapshot), nil
	})
}

func registerManagedAgentCancelTool(registry *Registry, runtime *Runtime) {
	registry.MustRegister(AgentCancelToolName, Schema{
		Name: AgentCancelToolName, Title: "Cancel Managed Agent",
		Description:  "Cancel an owned managed agent that is no longer useful. Cancellation is idempotent for an already terminal agent.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string","pattern":"^agent_[0-9a-f]{16}$"}},"required":["agent_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(managedAgentSnapshotSchema),
		Annotations:  ToolAnnotations(RiskEdit),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		controller, err := managedAgentMCPController(ctx, runtime, true)
		if err != nil {
			return Result{}, err
		}
		id, err := requiredManagedAgentID(args)
		if err != nil {
			return Result{}, err
		}
		snapshot, err := runtime.Agents.Cancel(ctx, controller, id)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(snapshot), nil
	})
}

func managedAgentMCPController(ctx context.Context, runtime *Runtime, allowClaimedChild bool) (managedagent.Controller, error) {
	if runtime == nil || runtime.Agents == nil {
		return managedagent.Controller{}, errors.New("managed agent service is unavailable")
	}
	sessionID := strings.TrimSpace(MCPSessionID(ctx))
	if sessionID == "" {
		return managedagent.Controller{}, errors.New("managed agent tools require trusted MCP session identity")
	}
	if !allowClaimedChild {
		if _, claimed := runtime.Agents.SessionBinding(sessionID); claimed {
			return managedagent.Controller{}, errors.New("nested managed-agent delegation is disabled")
		}
	}
	return managedagent.NewMCPController(mcpSessionStateKey(sessionID))
}

func requiredManagedAgentID(args map[string]any) (managedagent.ID, error) {
	raw, err := requiredString(args, "agent_id")
	if err != nil {
		return "", err
	}
	id := managedagent.ID(strings.TrimSpace(raw))
	if err := managedagent.ValidateID(id); err != nil {
		return "", err
	}
	return id, nil
}
