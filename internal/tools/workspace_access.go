package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type WorkspaceAccessResolution struct {
	WorkspaceID           string
	SessionAccess         SessionWorkspaceAccessDecision
	SessionWorkspaceCount int
}

func (r *Runtime) ResolveWorkspaceAccess(ctx context.Context, requested string) (WorkspaceAccessResolution, error) {
	requested = strings.TrimSpace(requested)
	bound := strings.TrimSpace(BoundWorkspace(ctx))
	if requested == "" {
		requested = bound
	}
	if requested == "" {
		return WorkspaceAccessResolution{}, errors.New("workspace_id is required")
	}
	if r == nil || r.Workspaces == nil {
		return WorkspaceAccessResolution{}, errors.New("workspace manager is unavailable")
	}

	canonical, err := r.Workspaces.CanonicalID(requested)
	if err != nil {
		return WorkspaceAccessResolution{}, workspaceScopePreflightError(r.Workspaces, requested, err)
	}
	if bound != "" {
		boundCanonical, boundErr := r.Workspaces.CanonicalID(bound)
		if boundErr != nil {
			return WorkspaceAccessResolution{}, boundErr
		}
		if canonical != boundCanonical {
			return WorkspaceAccessResolution{}, fmt.Errorf("tool call is bound to workspace %s and cannot access workspace %s", boundCanonical, canonical)
		}
	}

	resolution := WorkspaceAccessResolution{
		WorkspaceID:   canonical,
		SessionAccess: SessionWorkspaceAccessDecision(""),
	}
	if sessionID := strings.TrimSpace(MCPSessionID(ctx)); sessionID != "" {
		if r.Agents != nil {
			if binding, claimed := r.Agents.SessionBinding(sessionID); claimed {
				boundCanonical, bindErr := r.Workspaces.CanonicalID(binding.WorkspaceID)
				if bindErr != nil {
					return WorkspaceAccessResolution{}, bindErr
				}
				if !binding.Active {
					return WorkspaceAccessResolution{}, fmt.Errorf("managed child session for agent %s is no longer active", binding.AgentID)
				}
				if canonical != boundCanonical {
					return WorkspaceAccessResolution{}, fmt.Errorf("managed child session for agent %s is bound to workspace %s and cannot access workspace %s", binding.AgentID, boundCanonical, canonical)
				}
				resolution.WorkspaceID = boundCanonical
				resolution.SessionAccess = SessionWorkspaceAccessClaimed
				resolution.SessionWorkspaceCount = 1
				return resolution, nil
			}
		}
		_, decision, count, accessErr := r.sessionAccessManager().CheckOrGrant(sessionID, canonical)
		resolution.SessionAccess = decision
		resolution.SessionWorkspaceCount = count
		if accessErr != nil {
			return WorkspaceAccessResolution{}, accessErr
		}
	}
	return resolution, nil
}
