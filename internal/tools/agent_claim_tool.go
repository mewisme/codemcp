package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	managedagent "go.mewis.me/codemcp/internal/agent"
)

const AgentClaimToolName = "agent_claim"

type AgentClaimResult struct {
	AgentID     managedagent.ID        `json:"agent_id"`
	WorkspaceID string                 `json:"workspace_id"`
	Backend     managedagent.BackendID `json:"backend"`
	Claimed     bool                   `json:"claimed"`
}

func RegisterAgentClaimTool(registry *Registry, runtime *Runtime) {
	if registry == nil {
		return
	}
	annotations := ToolAnnotations(RiskEdit)
	annotations["idempotentHint"] = false
	registry.MustRegister(AgentClaimToolName, Schema{
		Name:         AgentClaimToolName,
		Title:        "Claim Managed Agent Session",
		Description:  "Bind the current trusted MCP session to one pre-authorized managed child agent. This is a single-use bootstrap capability. The claim determines the exact workspace; workspace authority is never taken from prompt text or caller-supplied workspace arguments.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string","pattern":"^agent_[0-9a-f]{16}$"},"token":{"type":"string","minLength":40,"maxLength":80}},"required":["agent_id","token"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string","pattern":"^agent_[0-9a-f]{16}$"},"workspace_id":{"type":"string"},"backend":{"type":"string"},"claimed":{"type":"boolean"}},"required":["agent_id","workspace_id","backend","claimed"],"additionalProperties":false}`),
		Annotations:  annotations,
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		if runtime == nil || runtime.Agents == nil {
			return Result{}, errors.New("managed agent service is unavailable")
		}
		for key := range args {
			switch key {
			case "agent_id", "token":
			default:
				return Result{}, errors.New("unsupported managed agent claim argument")
			}
		}
		agentID, err := requiredString(args, "agent_id")
		if err != nil {
			return Result{}, err
		}
		token, err := requiredString(args, "token")
		if err != nil {
			return Result{}, err
		}
		if len(token) < 40 || len(token) > 80 {
			return Result{}, errors.New("managed agent claim token has invalid length")
		}
		sessionID := strings.TrimSpace(MCPSessionID(ctx))
		if sessionID == "" {
			return Result{}, errors.New("managed agent claim requires trusted MCP session identity")
		}
		binding, err := runtime.Agents.ConsumeClaim(managedagent.ID(agentID), token, sessionID)
		if err != nil {
			return Result{}, err
		}
		// Any ordinary grants accumulated by this session are discarded once it
		// becomes a managed child. Future workspace access is exact-bound.
		runtime.sessionAccessManager().Delete(sessionID)
		return JSONResult(AgentClaimResult{
			AgentID: binding.AgentID, WorkspaceID: binding.WorkspaceID,
			Backend: binding.Backend, Claimed: true,
		}), nil
	})
}
