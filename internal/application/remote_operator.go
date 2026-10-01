package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	mcpnetwork "go.mewis.me/codemcp/internal/network"
	"go.mewis.me/codemcp/internal/notification"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

type CompletionWorkspaceInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type ConfigPatchInput struct {
	Changes []SettingChange `json:"changes"`
}

type HealthStatus struct {
	OK               bool `json:"ok"`
	AdminAuthEnabled bool `json:"admin_auth_enabled"`
}

type ProcessInput struct {
	WorkspaceID string `json:"workspace_id"`
	ID          string `json:"id,omitempty"`
}

type ProcessClearResult struct {
	Deleted bool `json:"deleted"`
}

type RuntimeGrantInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	ID          string `json:"id,omitempty"`
}

type ManagedTunnelGetInput struct {
	ID string `json:"id"`
}

type ManagedTunnelUseInput struct {
	ID                     string `json:"id"`
	RuntimeAPIKey          string `json:"runtime_api_key,omitempty"`
	AutoGenerateRuntimeKey bool   `json:"auto_generate_runtime_key,omitempty"`
	ProjectID              string `json:"project_id,omitempty"`
}

type ManagedTunnelCreateInput struct {
	Request tunnel.CreateRequest `json:"request"`
}

type ManagedTunnelUpdateInput struct {
	ID      string               `json:"id"`
	Request tunnel.UpdateRequest `json:"request"`
}

type ManagedTunnelDeleteInput struct {
	ID          string `json:"id"`
	ClearConfig bool   `json:"clear_config,omitempty"`
}

type UpstreamOAuthInput struct {
	ID                 string `json:"id"`
	RedirectOrigin     string `json:"redirect_origin,omitempty"`
	Issuer             string `json:"issuer,omitempty"`
	ClientID           string `json:"client_id,omitempty"`
	ClientSecretEnvVar string `json:"client_secret_env_var,omitempty"`
	ClientMetadataURL  string `json:"client_metadata_url,omitempty"`
	Scope              string `json:"scope,omitempty"`
}

type RemoteOperatorServices struct {
	Processes     *shellruntime.ProcessManager
	Notifications *notification.Coordinator
	OAuth         *mcpoauth.Store
	OAuthFlows    *mcpoauth.FlowManager
	Upstream      *upstream.Manager
}

func BindRemoteOperatorOperations(dispatcher *Dispatcher, services RemoteOperatorServices) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	settings := NewSettingService()
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.CompletionCurrent, typedOperation[CompletionWorkspaceInput](capability.CompletionCurrent, func(ctx context.Context, input CompletionWorkspaceInput) (any, error) {
			return CurrentCompletion(ctx, input.WorkspaceID)
		})},
		{capability.CompletionDoctor, typedOperation[CompletionWorkspaceInput](capability.CompletionDoctor, func(ctx context.Context, input CompletionWorkspaceInput) (any, error) {
			return CompletionHealth(ctx, input.WorkspaceID)
		})},
		{capability.CompletionFeed, typedOperation[CompletionWorkspaceInput](capability.CompletionFeed, func(ctx context.Context, input CompletionWorkspaceInput) (any, error) {
			subscription, snapshot, err := SubscribeCompletions(ctx, input.WorkspaceID, input.Limit)
			if subscription != nil {
				_ = subscription.Close()
			}
			return snapshot, err
		})},
		{capability.ConfigSnapshotRead, func(ctx context.Context, _ any) (any, error) {
			return settings.List(ctx, "")
		}},
		{capability.ConfigPatch, typedOperation[ConfigPatchInput](capability.ConfigPatch, func(ctx context.Context, input ConfigPatchInput) (any, error) {
			if len(input.Changes) == 0 {
				return nil, errors.New("configuration patch requires at least one change")
			}
			return settings.Apply(ctx, input.Changes)
		})},
		{capability.ConfigPath, func(ctx context.Context, _ any) (any, error) { return ConfigSource(ctx) }},
		{capability.ConfigVerify, func(ctx context.Context, _ any) (any, error) { return VerifyConfigContext(ctx) }},
		{capability.HealthRead, func(ctx context.Context, _ any) (any, error) {
			cfg, err := config.Load()
			if err != nil {
				return HealthStatus{}, err
			}
			return HealthStatus{OK: true, AdminAuthEnabled: cfg.HTTP.Admin.Auth.Enabled}, nil
		}},
		{capability.LogsClear, func(ctx context.Context, _ any) (any, error) {
			if err := ClearLogs(ctx); err != nil {
				return nil, err
			}
			return LoadLogsInfoContext(ctx)
		}},
		{capability.LogsPath, func(ctx context.Context, _ any) (any, error) { return LoadLogsInfoContext(ctx) }},
		{capability.NetworkInterfacesList, func(context.Context, any) (any, error) { return mcpnetwork.Discover() }},
		{capability.NotificationStatus, func(ctx context.Context, _ any) (any, error) {
			cfg, err := config.Load()
			if err != nil {
				return notification.StatusSnapshot{}, err
			}
			if services.Notifications == nil {
				return notification.StatusSnapshot{}, nil
			}
			return services.Notifications.Status(notificationEnabledMap(cfg)), nil
		}},
		{capability.ProcessList, typedOperation[ProcessInput](capability.ProcessList, func(_ context.Context, input ProcessInput) (any, error) {
			if services.Processes == nil {
				return nil, ErrProcessServiceUnavailable
			}
			return services.Processes.Status(strings.TrimSpace(input.WorkspaceID), "")
		})},
		{capability.ProcessView, typedOperation[ProcessInput](capability.ProcessView, func(_ context.Context, input ProcessInput) (any, error) {
			if services.Processes == nil {
				return nil, ErrProcessServiceUnavailable
			}
			values, err := services.Processes.Status(strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ID))
			if err != nil {
				return nil, err
			}
			if len(values) != 1 {
				return nil, fmt.Errorf("process %q was not found", strings.TrimSpace(input.ID))
			}
			return values[0], nil
		})},
		{capability.ProcessClear, typedOperation[ProcessInput](capability.ProcessClear, func(_ context.Context, input ProcessInput) (any, error) {
			if err := NewProcessService(services.Processes).ClearFinished(strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ID)); err != nil {
				return nil, err
			}
			return ProcessClearResult{Deleted: true}, nil
		})},
		{capability.RequestGrantList, typedOperation[RuntimeGrantInput](capability.RequestGrantList, func(ctx context.Context, input RuntimeGrantInput) (any, error) {
			return ListRuntimeGrants(ctx, input.WorkspaceID)
		})},
		{capability.RequestGrantRevoke, typedOperation[RuntimeGrantInput](capability.RequestGrantRevoke, func(ctx context.Context, input RuntimeGrantInput) (any, error) {
			return RevokeRuntimeGrant(ctx, input.ID)
		})},
		{capability.RequestStream, func(ctx context.Context, _ any) (any, error) {
			subscription, snapshot, err := SubscribeApprovalRequests(ctx)
			if subscription != nil {
				_ = subscription.Close()
			}
			return snapshot, err
		}},
		{capability.TunnelConfigRead, func(context.Context, any) (any, error) {
			status, err := TunnelStatus()
			if err != nil {
				return TunnelView{}, err
			}
			return safeTunnelView(status), nil
		}},
		{capability.TunnelList, func(ctx context.Context, _ any) (any, error) { return RefreshManagedTunnels(ctx) }},
		{capability.TunnelGet, typedOperation[ManagedTunnelGetInput](capability.TunnelGet, func(ctx context.Context, input ManagedTunnelGetInput) (any, error) {
			return GetManagedTunnel(ctx, input.ID, ManagedTunnelOptions{})
		})},
		{capability.TunnelUse, typedOperation[ManagedTunnelUseInput](capability.TunnelUse, func(ctx context.Context, input ManagedTunnelUseInput) (any, error) {
			return UseManagedTunnel(ctx, input.ID, ManagedTunnelUseOptions{RuntimeAPIKey: input.RuntimeAPIKey, AutoGenerateRuntimeKey: input.AutoGenerateRuntimeKey, ProjectID: input.ProjectID})
		})},
		{capability.TunnelCreate, typedOperation[ManagedTunnelCreateInput](capability.TunnelCreate, func(ctx context.Context, input ManagedTunnelCreateInput) (any, error) {
			return CreateManagedTunnel(ctx, input.Request, ManagedTunnelOptions{})
		})},
		{capability.TunnelUpdate, typedOperation[ManagedTunnelUpdateInput](capability.TunnelUpdate, func(ctx context.Context, input ManagedTunnelUpdateInput) (any, error) {
			return UpdateManagedTunnel(ctx, input.ID, input.Request, ManagedTunnelOptions{})
		})},
		{capability.TunnelDelete, typedOperation[ManagedTunnelDeleteInput](capability.TunnelDelete, func(ctx context.Context, input ManagedTunnelDeleteInput) (any, error) {
			return DeleteManagedTunnel(ctx, input.ID, input.ClearConfig)
		})},
		{capability.UpstreamAuthStatus, typedOperation[UpstreamOAuthInput](capability.UpstreamAuthStatus, func(_ context.Context, input UpstreamOAuthInput) (any, error) {
			if services.OAuth == nil {
				return nil, errors.New("OAuth store is unavailable")
			}
			return services.OAuth.Status(strings.TrimSpace(input.ID))
		})},
		{capability.UpstreamAuthLogin, typedOperation[UpstreamOAuthInput](capability.UpstreamAuthLogin, func(ctx context.Context, input UpstreamOAuthInput) (any, error) {
			return beginUpstreamOAuth(ctx, services, input)
		})},
		{capability.UpstreamAuthLogout, typedOperation[UpstreamOAuthInput](capability.UpstreamAuthLogout, func(_ context.Context, input UpstreamOAuthInput) (any, error) {
			id := strings.TrimSpace(input.ID)
			if services.OAuth == nil {
				return nil, errors.New("OAuth store is unavailable")
			}
			if err := services.OAuth.Delete(id); err != nil {
				return nil, err
			}
			if services.Upstream != nil {
				_ = services.Upstream.Disconnect(id)
			}
			return services.OAuth.Status(id)
		})},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}

func beginUpstreamOAuth(ctx context.Context, services RemoteOperatorServices, input UpstreamOAuthInput) (mcpoauth.FlowSession, error) {
	if services.Upstream == nil || services.OAuthFlows == nil {
		return mcpoauth.FlowSession{}, errors.New("upstream OAuth flow is unavailable")
	}
	id := strings.TrimSpace(input.ID)
	server, ok := services.Upstream.Get(id)
	if !ok {
		return mcpoauth.FlowSession{}, fmt.Errorf("upstream server %q was not found", id)
	}
	if server.Transport != "http" {
		return mcpoauth.FlowSession{}, errors.New("OAuth login requires an HTTP upstream server")
	}
	if server.Auth.Type == "none" {
		return mcpoauth.FlowSession{}, errors.New("OAuth is disabled for this upstream server")
	}
	if strings.TrimSpace(server.BearerTokenEnvVar) != "" {
		return mcpoauth.FlowSession{}, errors.New("server uses a static credential; remove it before managed OAuth login")
	}
	for key := range server.Headers {
		if strings.EqualFold(key, "Authorization") {
			return mcpoauth.FlowSession{}, errors.New("server uses static Authorization; remove it before managed OAuth login")
		}
	}
	origin, err := mcpoauth.ValidateRedirectOrigin(input.RedirectOrigin)
	if err != nil {
		return mcpoauth.FlowSession{}, err
	}
	flowCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return services.OAuthFlows.Begin(flowCtx, mcpoauth.LoginConfig{
		ServerID: id, ServerURL: server.URL, Scope: server.Auth.Scope, Issuer: input.Issuer,
		ClientID: input.ClientID, ClientSecretEnvVar: input.ClientSecretEnvVar, ClientMetadataURL: input.ClientMetadataURL,
	}, strings.TrimRight(origin, "/")+"/oauth/callback", input.Scope)
}
