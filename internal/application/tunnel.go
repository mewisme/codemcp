package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

var (
	ErrTunnelAdminDisabled         = errors.New("tunnel admin management is disabled")
	ErrTunnelAdminNotConfigured    = errors.New("tunnel admin key is not configured; set it first")
	ErrTunnelAdminKeyRequired      = errors.New("configured tunnel admin key and scope are required")
	ErrTunnelAdminReadRequired     = errors.New("tunnel admin key does not have verified Read access")
	ErrTunnelAdminManageRequired   = errors.New("tunnel admin key does not have verified Manage access")
	ErrTunnelRuntimeConfigRequired = errors.New("configured tunnel id and runtime API key are required")
	ErrTunnelAdminAPIKeyRequired   = errors.New("OpenAI admin key is required")
)

type ManagedTunnelRuntimeKeyRequiredError struct {
	TunnelID string
}

func (err *ManagedTunnelRuntimeKeyRequiredError) Error() string {
	return "runtime API key is required to use this tunnel; provide one or enable automatic generation with a sufficiently privileged admin key"
}

type TunnelDashboard struct {
	Config         tunnel.Config
	Status         tunnel.Status
	MCPHTTPEnabled bool
}

type TunnelRuntimeInput struct {
	Enabled             *bool   `json:"enabled,omitempty"`
	ID                  *string `json:"id,omitempty"`
	APIKey              *string `json:"api_key,omitempty"`
	ControlPlaneBaseURL *string `json:"control_plane_base_url,omitempty"`
	OrganizationID      *string `json:"organization_id,omitempty"`
}

type TunnelAdminStatus struct {
	Enabled       bool
	KeyConfigured bool
	KeyPreview    string
	Configured    bool
	Verified      bool
	Scope         tunnel.AdminScope
	Access        tunnel.AdminAccess
}

type TunnelAdminKeyInput struct {
	Key       string
	KeySource string
	Scope     *tunnel.AdminScope
}

type TunnelView struct {
	Enabled              bool
	Configured           bool
	Connected            bool
	ID                   string
	ControlPlaneBaseURL  string
	OrganizationID       string
	Status               tunnel.Status
	MCPHTTPEnabled       bool
	RuntimeKeyConfigured bool
	RuntimeKeyPreview    string
	Admin                TunnelAdminStatus
}

type TunnelConfigureInput struct {
	Runtime         *TunnelRuntimeInput
	ClearRuntimeKey bool
	AdminEnabled    *bool
	AdminScope      *tunnel.AdminScope
}

type TunnelVerifyResult struct {
	Count  int
	Scope  tunnel.AdminScope
	Status TunnelAdminStatus
}

func safeTunnelView(dashboard TunnelDashboard) TunnelView {
	status := tunnel.PublicStatus(dashboard.Status)
	admin := TunnelAdminStatus{
		Enabled:       status.Admin.Enabled,
		KeyConfigured: status.Admin.KeyConfigured,
		KeyPreview:    tunnel.SecretPreview(dashboard.Config.Admin.Key),
		Configured:    status.Admin.Configured,
		Verified:      status.Admin.Verified,
		Scope:         status.Admin.Scope(),
		Access:        tunnel.AdminAccess{Read: status.Admin.ReadAccess, Manage: status.Admin.ManageAccess},
	}
	return TunnelView{
		Enabled:              dashboard.Config.Enabled,
		Configured:           tunnel.Configured(dashboard.Config),
		Connected:            dashboard.Status.Running && dashboard.Status.Ready,
		ID:                   dashboard.Config.ID,
		ControlPlaneBaseURL:  dashboard.Config.ControlPlaneBaseURL,
		OrganizationID:       dashboard.Config.OrganizationID,
		Status:               status,
		MCPHTTPEnabled:       dashboard.MCPHTTPEnabled,
		RuntimeKeyConfigured: strings.TrimSpace(dashboard.Config.APIKey) != "",
		RuntimeKeyPreview:    tunnel.SecretPreview(dashboard.Config.APIKey),
		Admin:                admin,
	}
}

func BindTunnelOperations(dispatcher *Dispatcher) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.TunnelStatus, func(context.Context, any) (any, error) {
			dashboard, err := TunnelStatus()
			return safeTunnelView(dashboard), err
		}},
		{capability.TunnelSync, func(ctx context.Context, _ any) (any, error) {
			metadata, _, err := SyncConfiguredTunnel(ctx)
			return metadata, err
		}},
		{capability.TunnelEnable, func(ctx context.Context, _ any) (any, error) {
			dashboard, err := SetTunnelEnabled(ctx, true)
			return safeTunnelView(dashboard), err
		}},
		{capability.TunnelDisable, func(ctx context.Context, _ any) (any, error) {
			dashboard, err := SetTunnelEnabled(ctx, false)
			return safeTunnelView(dashboard), err
		}},
		{capability.TunnelConfigure, typedOperation[TunnelConfigureInput](capability.TunnelConfigure, func(ctx context.Context, input TunnelConfigureInput) (any, error) {
			if input.Runtime != nil {
				if _, err := ConfigureTunnelRuntime(ctx, *input.Runtime); err != nil {
					return nil, err
				}
			}
			if input.ClearRuntimeKey {
				if _, err := NewSettingService().Unset(ctx, "tunnel.api_key"); err != nil {
					return nil, err
				}
			}
			if input.AdminEnabled != nil {
				if _, err := SetTunnelAdminEnabled(ctx, *input.AdminEnabled); err != nil {
					return nil, err
				}
			}
			if input.AdminScope != nil {
				if _, err := SetTunnelAdminScope(ctx, *input.AdminScope); err != nil {
					return nil, err
				}
			}
			dashboard, err := TunnelStatus()
			return safeTunnelView(dashboard), err
		})},
		{capability.TunnelAdminKeyStatus, func(ctx context.Context, _ any) (any, error) { return TunnelAdminKeyStatusContext(ctx) }},
		{capability.TunnelAdminKeySet, typedOperation[TunnelAdminKeyInput](capability.TunnelAdminKeySet, func(ctx context.Context, input TunnelAdminKeyInput) (any, error) {
			if _, err := SetTunnelAdminKey(ctx, input); err != nil {
				return nil, err
			}
			return TunnelAdminKeyStatusContext(ctx)
		})},
		{capability.TunnelAdminKeyVerify, func(ctx context.Context, _ any) (any, error) {
			count, scope, err := VerifyTunnelAdminKey(ctx)
			if err != nil {
				return nil, err
			}
			status, err := TunnelAdminKeyStatusContext(ctx)
			return TunnelVerifyResult{Count: count, Scope: scope, Status: status}, err
		}},
		{capability.TunnelAdminKeyRemove, func(ctx context.Context, _ any) (any, error) {
			if err := RemoveTunnelAdminKey(ctx); err != nil {
				return nil, err
			}
			return TunnelAdminKeyStatusContext(ctx)
		}},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}

type ManagedTunnelOptions struct {
	Configure     bool
	RuntimeAPIKey string
	Enable        bool
}

type ManagedTunnelUseOptions struct {
	RuntimeAPIKey          string
	AutoGenerateRuntimeKey bool
	ProjectID              string
}

type ManagedTunnelResult struct {
	Metadata   tunnel.Metadata
	Configured bool
	Cleared    bool
}

func TunnelStatus() (TunnelDashboard, error) {
	cfg, err := config.Load()
	if err != nil {
		return TunnelDashboard{}, err
	}
	var metadata *tunnel.Metadata
	if cached, err := config.LoadTunnelMetadata(cfg.Tunnel.ID); err == nil {
		metadata = &cached
	}
	return TunnelDashboard{Config: cfg.Tunnel, Status: tunnel.StatusFromConfig(cfg.Tunnel, metadata), MCPHTTPEnabled: cfg.HTTP.MCP.Enabled}, nil
}

func ConfigureTunnelRuntime(ctx context.Context, input TunnelRuntimeInput) (TunnelDashboard, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.runtime.configure", "Configuring local tunnel runtime", tracepkg.Any("changed_fields", tunnelRuntimeInputFields(input)), tracepkg.Bool("runtime_key_replacement", input.APIKey != nil))
	changes := make([]SettingChange, 0, 5)
	if input.Enabled != nil {
		changes = append(changes, SettingChange{Key: "tunnel.enabled", Value: fmt.Sprint(*input.Enabled)})
	}
	if input.ID != nil {
		changes = append(changes, SettingChange{Key: "tunnel.id", Value: *input.ID})
	}
	if input.APIKey != nil {
		changes = append(changes, SettingChange{Key: "tunnel.api_key", Value: *input.APIKey})
	}
	if input.ControlPlaneBaseURL != nil {
		changes = append(changes, SettingChange{Key: "tunnel.control_plane_base_url", Value: *input.ControlPlaneBaseURL})
	}
	if input.OrganizationID != nil {
		changes = append(changes, SettingChange{Key: "tunnel.organization_id", Value: *input.OrganizationID})
	}
	applied, err := NewSettingService().Apply(ctx, changes)
	if err != nil {
		span.FailMessage("Tunnel runtime configuration persistence failed", err)
		return TunnelDashboard{}, err
	}
	dashboard, err := TunnelStatus()
	if err != nil {
		span.FailMessage("Tunnel runtime status reload failed", err)
		return TunnelDashboard{}, err
	}
	span.EndMessage("Local tunnel runtime configured", tracepkg.String("tunnel_id", dashboard.Config.ID), tracepkg.Bool("enabled", dashboard.Config.Enabled), tracepkg.Bool("runtime_reloaded", applied.RuntimeReloaded), tracepkg.URL("control_plane_base_url", dashboard.Config.ControlPlaneBaseURL), tracepkg.Bool("runtime_auth_present", strings.TrimSpace(dashboard.Config.APIKey) != ""))
	return dashboard, nil
}

func SetTunnelEnabled(ctx context.Context, enabled bool) (TunnelDashboard, error) {
	return ConfigureTunnelRuntime(ctx, TunnelRuntimeInput{Enabled: &enabled})
}

func SyncConfiguredTunnel(ctx context.Context) (tunnel.Metadata, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return tunnel.Metadata{}, "", err
	}
	if !tunnel.Configured(cfg.Tunnel) {
		return tunnel.Metadata{}, "", ErrTunnelRuntimeConfigRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return config.SyncTunnelMetadata(ctx, cfg.Tunnel)
}

func TunnelAdminKeyStatus() (TunnelAdminStatus, error) {
	return TunnelAdminKeyStatusContext(context.Background())
}

func TunnelAdminKeyStatusContext(ctx context.Context) (TunnelAdminStatus, error) {
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin-key.status", "Loading stored tunnel admin key status")
	cfg, _, err := loadConfigTraced(ctx, "tunnel.admin.config.load", "Loading tunnel admin configuration")
	if err != nil {
		span.FailMessage("Stored tunnel admin key status load failed", err)
		return TunnelAdminStatus{}, err
	}
	scope := tunnel.AdminScopeFromConfig(cfg.Tunnel)
	status := TunnelAdminStatus{Enabled: tunnel.AdminEnabled(cfg.Tunnel), KeyConfigured: strings.TrimSpace(cfg.Tunnel.Admin.Key) != "", KeyPreview: tunnel.SecretPreview(cfg.Tunnel.Admin.Key), Configured: tunnel.AdminConfigured(cfg.Tunnel), Verified: tunnel.AdminVerified(cfg.Tunnel), Scope: scope, Access: tunnel.AdminAccessFromConfig(cfg.Tunnel)}
	fields := append(tunnelAdminScopeFields(scope), tracepkg.Bool("enabled", status.Enabled), tracepkg.Bool("key_configured", status.KeyConfigured), tracepkg.Bool("configured", status.Configured), tracepkg.Bool("verified", status.Verified), tracepkg.Bool("read_access", status.Access.Read), tracepkg.Bool("manage_access", status.Access.Manage))
	span.EndMessage("Stored tunnel admin key status loaded", fields...)
	return status, nil
}

func SetTunnelAdminKey(ctx context.Context, input TunnelAdminKeyInput) (tunnel.AdminScope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	keySource := strings.TrimSpace(input.KeySource)
	if keySource == "" {
		keySource = "provided"
	}
	fields := []tracepkg.Field{tracepkg.String("key_source", keySource), tracepkg.Bool("requested_scope_explicit", input.Scope != nil)}
	if input.Scope != nil {
		fields = append(fields, tunnelAdminScopeFields(*input.Scope)...)
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin-key.set", "Storing tunnel admin key", fields...)
	key := strings.TrimSpace(input.Key)
	if key == "" {
		err := ErrTunnelAdminAPIKeyRequired
		span.FailMessage("Tunnel admin key validation failed", err, tracepkg.String("key_source", keySource))
		return tunnel.AdminScope{}, err
	}
	changes := []SettingChange{{Key: "tunnel.admin.key", Value: key}}
	if input.Scope != nil {
		scopeChange, err := settingChangeForAdminScope(*input.Scope)
		if err != nil {
			span.FailMessage("Tunnel admin scope validation failed", err)
			return tunnel.AdminScope{}, err
		}
		changes = append(changes, scopeChange)
	}
	applied, err := NewSettingService().Apply(ctx, changes)
	if err != nil {
		span.FailMessage("Tunnel admin key persistence failed", err)
		return tunnel.AdminScope{}, err
	}
	scope := tunnel.AdminScopeFromConfig(applied.Config.Tunnel)
	span.EndMessage("Tunnel admin key stored", append(tunnelAdminScopeFields(scope), tracepkg.String("key_source", keySource), tracepkg.Bool("verification_invalidated", true))...)
	return scope, nil
}

func SetTunnelAdminEnabled(ctx context.Context, enabled bool) (TunnelAdminStatus, error) {
	if _, err := NewSettingService().Apply(ctx, []SettingChange{{Key: "tunnel.admin.enabled", Value: fmt.Sprint(enabled)}}); err != nil {
		return TunnelAdminStatus{}, err
	}
	return TunnelAdminKeyStatusContext(ctx)
}

func SetTunnelAdminScope(ctx context.Context, scope tunnel.AdminScope) (TunnelAdminStatus, error) {
	change, err := settingChangeForAdminScope(scope)
	if err != nil {
		return TunnelAdminStatus{}, err
	}
	if _, err := NewSettingService().Apply(ctx, []SettingChange{change}); err != nil {
		return TunnelAdminStatus{}, err
	}
	return TunnelAdminKeyStatusContext(ctx)
}

func VerifyTunnelAdminKey(ctx context.Context) (int, tunnel.AdminScope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin-key.verify-stored", "Verifying stored tunnel admin access", tracepkg.String("key_source", "stored"))
	cfg, _, err := loadConfigTraced(ctx, "tunnel.admin.config.load", "Loading stored tunnel admin configuration")
	if err != nil {
		span.FailMessage("Stored tunnel admin configuration load failed", err)
		return 0, tunnel.AdminScope{}, err
	}
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		err := ErrTunnelAdminNotConfigured
		span.FailMessage("Stored tunnel admin access is not configured", err)
		return 0, tunnel.AdminScope{}, err
	}
	scope := tunnel.AdminScopeFromConfig(cfg.Tunnel)
	access, count, err := tunnel.VerifyAdminKey(ctx, cfg.Tunnel)
	if err != nil {
		span.FailMessage("Stored tunnel admin access verification failed", errors.New("tunnel admin verification failed"), tunnelAdminScopeFields(scope)...)
		if tunnel.AdminVerified(cfg.Tunnel) || cfg.Tunnel.Admin.ReadAccess || cfg.Tunnel.Admin.ManageAccess {
			previous := cfg
			tunnel.InvalidateAdminVerification(&cfg.Tunnel)
			if _, _, persistErr := saveConfigMutation(ctx, previous, cfg); persistErr != nil {
				return 0, scope, errors.Join(err, fmt.Errorf("invalidate stale tunnel admin verification: %w", persistErr))
			}
		}
		return 0, scope, err
	}
	previous := cfg
	tunnel.MarkAdminVerified(&cfg.Tunnel, access)
	if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
		span.FailMessage("Stored tunnel admin access persistence failed", err, tracepkg.Bool("read_access", access.Read), tracepkg.Bool("manage_access", access.Manage))
		return 0, scope, err
	}
	span.EndMessage("Stored tunnel admin access verified", append(tunnelAdminScopeFields(scope), tracepkg.Bool("read_access", access.Read), tracepkg.Bool("manage_access", access.Manage), tracepkg.Int("tunnel_count", count))...)
	return count, scope, nil
}

func RemoveTunnelAdminKey(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin-key.remove", "Removing stored tunnel admin key")
	applied, err := NewSettingService().Apply(ctx, []SettingChange{{Key: "tunnel.admin.key", Unset: true}})
	if err != nil {
		span.FailMessage("Tunnel admin key removal failed", err)
		return err
	}
	span.EndMessage("Stored tunnel admin key removed", tracepkg.Bool("runtime_reloaded", applied.RuntimeReloaded))
	return nil
}

func ListManagedTunnels(ctx context.Context) ([]tunnel.Metadata, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !tunnel.AdminEnabled(cfg.Tunnel) {
		return nil, ErrTunnelAdminDisabled
	}
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return nil, ErrTunnelAdminKeyRequired
	}
	if !cfg.Tunnel.Admin.ManageAccess {
		return nil, ErrTunnelAdminManageRequired
	}
	scope := tunnel.AdminScopeFromConfig(cfg.Tunnel)
	if ctx == nil {
		ctx = context.Background()
	}
	return tunnel.ListManaged(ctx, cfg.Tunnel, scope)
}

func RefreshManagedTunnels(ctx context.Context) ([]tunnel.Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := ListManagedTunnels(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if _, err := config.SaveTunnelMetadataContext(ctx, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func GetManagedTunnel(ctx context.Context, id string, options ManagedTunnelOptions) (ManagedTunnelResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	if !tunnel.AdminEnabled(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminDisabled
	}
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminKeyRequired
	}
	if !cfg.Tunnel.Admin.ReadAccess && !cfg.Tunnel.Admin.ManageAccess {
		return ManagedTunnelResult{}, ErrTunnelAdminReadRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.managed.local-config-decision", "Resolved managed tunnel local configuration decision", tracepkg.String("tunnel_id", strings.TrimSpace(id)), tracepkg.Bool("configure", options.Configure), tracepkg.Bool("enable", options.Enable), tracepkg.Bool("runtime_key_supplied", strings.TrimSpace(options.RuntimeAPIKey) != ""))
	metadata, err := tunnel.GetManaged(ctx, cfg.Tunnel, strings.TrimSpace(id))
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	if _, err := config.SaveTunnelMetadataContext(ctx, metadata); err != nil {
		return ManagedTunnelResult{}, err
	}
	configured := false
	if options.Configure {
		previous := cfg
		if err := configureManagedTunnel(&cfg, metadata, options.RuntimeAPIKey, options.Enable); err != nil {
			return ManagedTunnelResult{}, err
		}
		if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
			return ManagedTunnelResult{}, err
		}
		configured = true
	}
	return ManagedTunnelResult{Metadata: metadata, Configured: configured}, nil
}

func UseManagedTunnel(ctx context.Context, id string, options ManagedTunnelUseOptions) (ManagedTunnelResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.managed.use", "Selecting managed tunnel for local runtime", tracepkg.String("tunnel_id", strings.TrimSpace(id)), tracepkg.Bool("runtime_key_supplied", strings.TrimSpace(options.RuntimeAPIKey) != ""), tracepkg.Bool("automatic_generation_requested", options.AutoGenerateRuntimeKey), tracepkg.Bool("project_explicit", strings.TrimSpace(options.ProjectID) != ""))
	id = strings.TrimSpace(id)
	if id == "" {
		err := errors.New("managed tunnel id is required")
		span.FailMessage("Managed tunnel selection validation failed", err)
		return ManagedTunnelResult{}, err
	}
	cfg, err := config.Load()
	if err != nil {
		span.FailMessage("Managed tunnel configuration load failed", err)
		return ManagedTunnelResult{}, err
	}
	selected, err := GetManagedTunnel(ctx, id, ManagedTunnelOptions{})
	if err != nil {
		span.FailMessage("Managed tunnel selection fetch failed", err)
		return ManagedTunnelResult{}, err
	}
	key := strings.TrimSpace(options.RuntimeAPIKey)
	keySource := "flag"
	if key == "" {
		key = strings.TrimSpace(cfg.Tunnel.APIKey)
		keySource = "stored"
	}
	if key == "" && options.AutoGenerateRuntimeKey {
		generated, err := tunnel.GenerateRuntimeKey(ctx, cfg.Tunnel, options.ProjectID)
		if err != nil {
			span.FailMessage("Managed tunnel runtime key generation failed", errors.New("runtime key generation failed"))
			return ManagedTunnelResult{}, err
		}
		key = generated.Value
		keySource = "generated"
	}
	if key == "" {
		err := &ManagedTunnelRuntimeKeyRequiredError{TunnelID: id}
		span.FailMessage("Managed tunnel runtime key unavailable", err, tracepkg.String("runtime_key_source", "none"))
		return ManagedTunnelResult{}, err
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.managed.runtime-key-resolved", "Resolved managed tunnel runtime key source", tracepkg.String("tunnel_id", id), tracepkg.String("runtime_key_source", keySource), tracepkg.Bool("generated", keySource == "generated"))
	previous := cfg
	if err := configureManagedTunnel(&cfg, selected.Metadata, key, true); err != nil {
		span.FailMessage("Managed tunnel local configuration failed", err, tracepkg.String("runtime_key_source", keySource))
		return ManagedTunnelResult{}, err
	}
	if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
		span.FailMessage("Managed tunnel local configuration persistence failed", err, tracepkg.String("runtime_key_source", keySource))
		return ManagedTunnelResult{}, err
	}
	selected.Configured = true
	span.EndMessage("Managed tunnel selected for local runtime", tracepkg.String("tunnel_id", id), tracepkg.String("runtime_key_source", keySource), tracepkg.Bool("generated", keySource == "generated"), tracepkg.Bool("enabled", true))
	return selected, nil
}

func CreateManagedTunnel(ctx context.Context, request tunnel.CreateRequest, options ManagedTunnelOptions) (ManagedTunnelResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	if !tunnel.AdminEnabled(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminDisabled
	}
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminKeyRequired
	}
	if !cfg.Tunnel.Admin.ManageAccess {
		return ManagedTunnelResult{}, ErrTunnelAdminManageRequired
	}
	request.OrganizationIDs = NormalizeTunnelIDs(request.OrganizationIDs)
	request.WorkspaceIDs = NormalizeTunnelIDs(request.WorkspaceIDs)
	request.TenantIDs = NormalizeTunnelIDs(request.TenantIDs)
	if len(request.OrganizationIDs) == 0 && len(request.WorkspaceIDs) == 0 {
		scope := tunnel.AdminScopeFromConfig(cfg.Tunnel)
		if scope.OrganizationID != "" {
			request.OrganizationIDs = []string{scope.OrganizationID}
		} else if scope.WorkspaceID != "" {
			request.WorkspaceIDs = []string{scope.WorkspaceID}
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.managed.local-config-decision", "Resolved managed tunnel local configuration decision", tracepkg.Bool("configure", options.Configure), tracepkg.Bool("enable", options.Enable), tracepkg.Bool("runtime_key_supplied", strings.TrimSpace(options.RuntimeAPIKey) != ""), tracepkg.String("operation", "create"))
	metadata, err := tunnel.CreateManaged(ctx, cfg.Tunnel, request)
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	if _, err := config.SaveTunnelMetadataContext(ctx, metadata); err != nil {
		return ManagedTunnelResult{}, err
	}
	configured := false
	if options.Configure {
		previous := cfg
		if err := configureManagedTunnel(&cfg, metadata, options.RuntimeAPIKey, options.Enable); err != nil {
			return ManagedTunnelResult{}, err
		}
		if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
			return ManagedTunnelResult{}, err
		}
		configured = true
	}
	return ManagedTunnelResult{Metadata: metadata, Configured: configured}, nil
}

func UpdateManagedTunnel(ctx context.Context, id string, request tunnel.UpdateRequest, options ManagedTunnelOptions) (ManagedTunnelResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	if !tunnel.AdminEnabled(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminDisabled
	}
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminKeyRequired
	}
	if !cfg.Tunnel.Admin.ManageAccess {
		return ManagedTunnelResult{}, ErrTunnelAdminManageRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.managed.local-config-decision", "Resolved managed tunnel local configuration decision", tracepkg.String("tunnel_id", strings.TrimSpace(id)), tracepkg.Bool("configure", options.Configure), tracepkg.Bool("enable", options.Enable), tracepkg.Bool("runtime_key_supplied", strings.TrimSpace(options.RuntimeAPIKey) != ""), tracepkg.String("operation", "update"))
	metadata, err := tunnel.UpdateManaged(ctx, cfg.Tunnel, strings.TrimSpace(id), request)
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	if _, err := config.SaveTunnelMetadataContext(ctx, metadata); err != nil {
		return ManagedTunnelResult{}, err
	}
	configured := false
	if options.Configure {
		previous := cfg
		if err := configureManagedTunnel(&cfg, metadata, options.RuntimeAPIKey, options.Enable); err != nil {
			return ManagedTunnelResult{}, err
		}
		if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
			return ManagedTunnelResult{}, err
		}
		configured = true
	}
	return ManagedTunnelResult{Metadata: metadata, Configured: configured}, nil
}

func DeleteManagedTunnel(ctx context.Context, id string, clearConfig bool) (ManagedTunnelResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	if !tunnel.AdminEnabled(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminDisabled
	}
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, ErrTunnelAdminKeyRequired
	}
	if !cfg.Tunnel.Admin.ManageAccess {
		return ManagedTunnelResult{}, ErrTunnelAdminManageRequired
	}
	id = strings.TrimSpace(id)
	configuredTunnel := id != "" && id == strings.TrimSpace(cfg.Tunnel.ID)
	if configuredTunnel && !cfg.HTTP.MCP.Enabled {
		return ManagedTunnelResult{}, errors.New("cannot delete the configured OpenAI tunnel while MCP HTTP is disabled")
	}
	clearConfigured := clearConfig && configuredTunnel
	var clearedConfig config.Config
	if clearConfigured {
		clearedConfig = cfg
		clearedConfig.Tunnel = tunnel.ClearRuntimeConfig(clearedConfig.Tunnel)
		if err := config.Validate(clearedConfig); err != nil {
			return ManagedTunnelResult{}, err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.managed.local-clear-decision", "Resolved managed tunnel local clear decision", tracepkg.String("tunnel_id", id), tracepkg.Bool("configured_tunnel", configuredTunnel), tracepkg.Bool("clear_requested", clearConfig), tracepkg.Bool("clear_config", clearConfigured))
	metadata, err := tunnel.DeleteManaged(ctx, cfg.Tunnel, id)
	if err != nil {
		return ManagedTunnelResult{}, err
	}
	cleared := clearConfigured && cfg.Tunnel.ID == metadata.ID
	if cleared {
		if _, _, err := saveConfigMutationWithoutRollback(ctx, clearedConfig); err != nil {
			return ManagedTunnelResult{}, err
		}
	}
	if err := config.RemoveTunnelMetadataContext(ctx, metadata.ID); err != nil {
		return ManagedTunnelResult{}, err
	}
	return ManagedTunnelResult{Metadata: metadata, Cleared: cleared}, nil
}

func NormalizeTunnelIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func configureManagedTunnel(cfg *config.Config, metadata tunnel.Metadata, runtimeAPIKey string, enable bool) error {
	if cfg == nil {
		return errors.New("configuration is unavailable")
	}
	key := strings.TrimSpace(runtimeAPIKey)
	if key == "" {
		key = strings.TrimSpace(cfg.Tunnel.APIKey)
	}
	if key == "" {
		return &ManagedTunnelRuntimeKeyRequiredError{TunnelID: metadata.ID}
	}
	cfg.Tunnel.ID = metadata.ID
	cfg.Tunnel.APIKey = key
	if len(metadata.OrganizationIDs) == 1 {
		cfg.Tunnel.OrganizationID = metadata.OrganizationIDs[0]
	}
	if enable {
		cfg.Tunnel.Enabled = true
	}
	return config.Validate(*cfg)
}

func tunnelRuntimeInputFields(input TunnelRuntimeInput) []string {
	fields := []string{}
	if input.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if input.ID != nil {
		fields = append(fields, "id")
	}
	if input.APIKey != nil {
		fields = append(fields, "runtime_key")
	}
	if input.ControlPlaneBaseURL != nil {
		fields = append(fields, "control_plane_base_url")
	}
	if input.OrganizationID != nil {
		fields = append(fields, "organization_id")
	}
	return fields
}

func tunnelAdminScopeFields(scope tunnel.AdminScope) []tracepkg.Field {
	if value := strings.TrimSpace(scope.OrganizationID); value != "" {
		return []tracepkg.Field{tracepkg.String("scope_type", "organization"), tracepkg.String("scope_id", value)}
	}
	if value := strings.TrimSpace(scope.WorkspaceID); value != "" {
		return []tracepkg.Field{tracepkg.String("scope_type", "workspace"), tracepkg.String("scope_id", value)}
	}
	if value := strings.TrimSpace(scope.TenantID); value != "" {
		return []tracepkg.Field{tracepkg.String("scope_type", "tenant"), tracepkg.String("scope_id", value)}
	}
	return []tracepkg.Field{tracepkg.String("scope_type", "unresolved")}
}
