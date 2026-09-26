package mcp

import (
	"context"
	"strings"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/tools"
)

func (e *FeatureExecutor) AuthorizeResourceSubscription(ctx context.Context, rawURI string) (ParsedResourceURI, error) {
	if e == nil || e.Registry == nil {
		return ParsedResourceURI{}, NewError(ErrInternal, "feature registry is unavailable")
	}
	parsed, err := ParseResourceURI(rawURI)
	if err != nil {
		return ParsedResourceURI{}, NewErrorData(ErrInvalidParams, "Invalid resource URI", map[string]any{"uri": strings.TrimSpace(rawURI)})
	}
	_, exact, template, ok := e.Registry.resourceRegistration(parsed)
	if !ok {
		return ParsedResourceURI{}, ResourceNotFoundError(parsed.URI)
	}
	policy := exact.Policy
	if parsed.Scope == FeatureScopeWorkspace {
		policy = template.Policy
	}
	if !policy.Subscription.Allowed {
		return ParsedResourceURI{}, NewErrorData(ErrInvalidParams, "Resource does not support change subscriptions", map[string]any{"uri": parsed.URI})
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.Source != "" {
		ctx = tools.WithCallSource(ctx, e.Source)
	}
	if e.BoundWorkspace != "" {
		ctx = tools.WithBoundWorkspace(ctx, e.BoundWorkspace)
	}
	if parsed.Scope == FeatureScopeWorkspace {
		if e.Tools == nil {
			return ParsedResourceURI{}, NewError(ErrInternal, "tool runtime is unavailable")
		}
		resolution, accessErr := e.Tools.ResolveWorkspaceAccess(ctx, parsed.WorkspaceID)
		if accessErr != nil {
			return ParsedResourceURI{}, NewErrorData(ErrInvalidParams, "Resource access denied", map[string]any{"uri": parsed.URI})
		}
		parsed.WorkspaceID = resolution.WorkspaceID
		canonical, uriErr := WorkspaceResourceURI(parsed.WorkspaceID, parsed.Path)
		if uriErr != nil {
			return ParsedResourceURI{}, NewError(ErrInternal, "resource URI normalization failed")
		}
		parsed.URI = canonical
	}
	return parsed, nil
}

func instructionChangeAffectsResource(change instructioncontext.Change, resource ParsedResourceURI) bool {
	scope := strings.TrimSpace(change.Scope)
	kind := strings.TrimSpace(change.Kind)
	if scope == "workspace" {
		if resource.Scope != FeatureScopeWorkspace || resource.WorkspaceID != strings.TrimSpace(change.WorkspaceID) {
			return false
		}
		switch kind {
		case "rule":
			return resource.Path == resourcePathProjectContext || resource.Path == resourcePathInstructionSources
		case "skill":
			return resource.Path == resourcePathProjectContext || resource.Path == resourcePathInstructionSources || resource.Path == resourcePathSkillCatalog
		}
		return false
	}
	if scope != "global" {
		return false
	}
	switch kind {
	case "rule":
		if resource.Scope == FeatureScopeGlobal {
			return resource.Path == resourcePathInstructionSources
		}
		return resource.Path == resourcePathProjectContext || resource.Path == resourcePathInstructionSources
	case "skill":
		if resource.Scope == FeatureScopeGlobal {
			return resource.Path == resourcePathInstructionSources || resource.Path == resourcePathSkillCatalog
		}
		return resource.Path == resourcePathProjectContext || resource.Path == resourcePathInstructionSources || resource.Path == resourcePathSkillCatalog
	default:
		return false
	}
}
