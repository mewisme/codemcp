package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	managedagent "go.mewis.me/codemcp/internal/agent"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	plandoc "go.mewis.me/codemcp/internal/plan"
)

const AgentCompleteToolName = "agent_complete"

type AgentCompleteResult struct {
	Record  agentcompletion.Record `json:"record"`
	Created bool                   `json:"created"`
}

func RegisterAgentCompletionTool(registry *Registry, service *agentcompletion.Service, planExecutions *plandoc.ExecutionManager, managedAgents *managedagent.Manager) {
	if registry == nil {
		return
	}
	annotations := ToolAnnotations(RiskEdit)
	annotations["idempotentHint"] = true
	registry.MustRegister(AgentCompleteToolName, Schema{
		Name:         AgentCompleteToolName,
		Title:        "Complete Agent Work",
		Description:  "Signal that Agent work for one workspace reached a terminal state. Call this only after verification and, when available in the current tool profile, make it the final CodeMCP tool call immediately before the final user response. For an active persisted-plan implementation binding, completed is accepted only after create_plan mode=update has persisted the bound phase checklist and matching embedded ordered phase; otherwise completed is rejected. Use partial, blocked, or cancelled when work remains incomplete; those outcomes release the ephemeral plan binding without marking progress. This records completion state but does not close the MCP transport or model process.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string","minLength":1},"status":{"type":"string","enum":["completed","partial","blocked","cancelled"]},"title":{"type":"string","minLength":1,"maxLength":120},"summary":{"type":"string","maxLength":2000}},"required":["workspace_id","status","title"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"record":{"type":"object","properties":{"id":{"type":"string"},"sequence":{"type":"integer","minimum":1},"agent_id":{"type":"string","pattern":"^[0-9a-f]{16}$"},"workspace_id":{"type":"string"},"status":{"type":"string","enum":["completed","partial","blocked","cancelled"]},"title":{"type":"string"},"summary":{"type":"string"},"source":{"type":"string"},"supersedes_id":{"type":"string"},"created_at":{"type":"string","format":"date-time"}},"required":["id","sequence","agent_id","workspace_id","status","title","created_at"],"additionalProperties":false},"created":{"type":"boolean"}},"required":["record","created"],"additionalProperties":false}`),
		Annotations:  annotations,
		Capability:   toolCapability(CapabilityDomainAgents),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		if service == nil {
			return Result{}, errors.New("agent completion service is unavailable")
		}
		for key := range args {
			switch key {
			case "workspace_id", "status", "title", "summary":
			default:
				return Result{}, errors.New("unsupported agent completion argument")
			}
		}
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		status, err := requiredString(args, "status")
		if err != nil {
			return Result{}, err
		}
		title, err := requiredString(args, "title")
		if err != nil {
			return Result{}, err
		}
		summary, err := optionalString(args, "summary")
		if err != nil {
			return Result{}, err
		}
		correlation := AgentCompletionCorrelationFromContext(ctx)
		if correlation.AgentID == "" || correlation.Source == "" {
			return Result{}, errors.New("agent completion requires trusted runtime correlation")
		}
		completionStatus := agentcompletion.Status(status)
		var managedBinding managedagent.ClaimBinding
		managedBound := false
		if managedAgents != nil {
			sessionID := strings.TrimSpace(MCPSessionID(ctx))
			if sessionID != "" {
				managedBinding, managedBound = managedAgents.SessionBinding(sessionID)
				if managedBound && managedBinding.Active && managedBinding.WorkspaceID != workspaceID {
					return Result{}, errors.New("claimed managed agent completion workspace mismatch")
				}
			}
		}
		sessionKey := planExecutionSessionKey(ctx)
		if planExecutions != nil {
			if binding, ok := planExecutions.Lookup(sessionKey, workspaceID); ok && completionStatus == agentcompletion.StatusCompleted && !binding.Closed {
				return Result{}, fmt.Errorf("plan %q phase %s (%s) is not persisted as completed; update the canonical plan with create_plan mode=update before agent_complete(status=completed)", binding.PlanName, binding.Phase.ID, binding.Phase.Title)
			}
		}
		record, created, err := service.Accept(
			agentcompletion.Identity{AgentID: correlation.AgentID, Source: correlation.Source},
			agentcompletion.Input{
				WorkspaceID: workspaceID,
				Status:      completionStatus,
				Title:       title,
				Summary:     summary,
			},
		)
		if err != nil {
			return Result{}, err
		}
		if planExecutions != nil {
			planExecutions.Release(sessionKey, workspaceID)
		}
		if managedBound && managedBinding.Active {
			terminal, err := managedCompletionState(completionStatus)
			if err != nil {
				return Result{}, fmt.Errorf("completion persisted but managed-agent status correlation failed: %w", err)
			}
			if err := managedAgents.ObserveAcceptedCompletion(managedagent.CompletionEvent{
				AgentID: managedBinding.AgentID, WorkspaceID: workspaceID, Status: terminal,
			}); err != nil {
				return Result{}, fmt.Errorf("completion persisted but managed-agent correlation failed: %w", err)
			}
		}
		return JSONResult(AgentCompleteResult{Record: record, Created: created}), nil
	})
}

func managedCompletionState(status agentcompletion.Status) (managedagent.State, error) {
	switch status {
	case agentcompletion.StatusCompleted:
		return managedagent.StateCompleted, nil
	case agentcompletion.StatusPartial:
		return managedagent.StatePartial, nil
	case agentcompletion.StatusBlocked:
		return managedagent.StateBlocked, nil
	case agentcompletion.StatusCancelled:
		return managedagent.StateCancelled, nil
	default:
		return "", fmt.Errorf("unsupported accepted completion status %q", status)
	}
}
