package mcp

import (
	"context"
	"strings"

	"go.mewis.me/codemcp/internal/tools"
)

type IdentityProjection struct {
	ControllerHint string
}

type identityProfile interface {
	ProjectIdentity(map[string]any) IdentityProjection
}

type IngressIdentityOptions struct {
	MCPSessionID           string
	TrustProfileController bool
}

func ProjectIdentity(profile Profile, meta map[string]any) IdentityProjection {
	if profile == nil {
		profile = BaseProfile()
	}
	provider, ok := profile.(identityProfile)
	if !ok {
		return IdentityProjection{}
	}
	projection := provider.ProjectIdentity(meta)
	projection.ControllerHint = strings.TrimSpace(projection.ControllerHint)
	return projection
}

func WithIngressIdentity(ctx context.Context, profile Profile, meta map[string]any, options IngressIdentityOptions) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID := strings.TrimSpace(options.MCPSessionID); sessionID != "" {
		ctx = tools.WithMCPSessionID(ctx, sessionID)
		return tools.WithTrustedControllerID(ctx, "mcp:"+sessionID)
	}
	if !options.TrustProfileController {
		return ctx
	}
	projection := ProjectIdentity(profile, meta)
	if projection.ControllerHint == "" {
		return ctx
	}
	return tools.WithTrustedControllerID(ctx, string(profile.ID())+":"+projection.ControllerHint)
}
