package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

type TunnelDashboard struct {
	Config         tunnel.Config
	Status         tunnel.Status
	MCPHTTPEnabled bool
}

type TunnelRuntimeInput struct {
	Enabled             *bool
	ID                  *string
	APIKey              *string
	ControlPlaneBaseURL *string
	OrganizationID      *string
}

type TunnelAdminStatus struct {
	Configured bool
	Scope      tunnel.AdminScope
	Access     tunnel.AdminAccess
}

type TunnelAdminKeyInput struct {
	Key       string
	KeySource string
	Scope     *tunnel.AdminScope
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
	client := tunnel.NewConfigured(cfg.Tunnel, nil)
	if metadata, err := config.LoadTunnelMetadata(cfg.Tunnel.ID); err == nil {
		_ = client.SeedMetadata(metadata)
	}
	return TunnelDashboard{Config: cfg.Tunnel, Status: client.Status(), MCPHTTPEnabled: cfg.Server.Enabled}, nil
}

func ConfigureTunnelRuntime(ctx context.Context, input TunnelRuntimeInput) (TunnelDashboard, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.runtime.configure", "Configuring local tunnel runtime", tracepkg.Any("changed_fields", tunnelRuntimeInputFields(input)), tracepkg.Bool("runtime_key_replacement", input.APIKey != nil))
	load := config.Load
	if input.APIKey != nil {
		load = config.LoadForTunnelRuntimeKeyReplacement
	}
	cfg, _, err := loadConfigWithTracedLoader(ctx, "tunnel.config.load", "Loading tunnel runtime configuration", load)
	if err != nil {
		span.FailMessage("Tunnel runtime configuration load failed", err)
		return TunnelDashboard{}, err
	}
	previousConfig := cfg
	previous := cfg.Tunnel
	next := previous
	if input.Enabled != nil {
		next.Enabled = *input.Enabled
	}
	if input.ID != nil {
		next.ID = strings.TrimSpace(*input.ID)
	}
	if input.APIKey != nil {
		next.APIKey = strings.TrimSpace(*input.APIKey)
	}
	if input.ControlPlaneBaseURL != nil {
		next.ControlPlaneBaseURL = strings.TrimSpace(*input.ControlPlaneBaseURL)
	}
	if input.OrganizationID != nil {
		next.OrganizationID = strings.TrimSpace(*input.OrganizationID)
	}
	cfg.Tunnel = next
	if err := config.Validate(cfg); err != nil {
		span.FailMessage("Tunnel runtime configuration validation failed", err, tracepkg.String("tunnel_id", next.ID), tracepkg.Bool("enabled", next.Enabled))
		return TunnelDashboard{}, err
	}
	metadataSync := tunnel.Configured(next) && (previous.ID != next.ID || previous.APIKey != next.APIKey || previous.ControlPlaneBaseURL != next.ControlPlaneBaseURL)
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.runtime.metadata-sync-decision", "Resolved tunnel metadata synchronization decision", tracepkg.Bool("metadata_sync", metadataSync), tracepkg.String("tunnel_id", next.ID), tracepkg.URL("control_plane_base_url", next.ControlPlaneBaseURL), tracepkg.Bool("runtime_auth_present", strings.TrimSpace(next.APIKey) != ""))
	if metadataSync {
		if _, _, err := config.SyncTunnelMetadata(ctx, next); err != nil {
			span.FailMessage("Tunnel runtime metadata synchronization failed", err, tracepkg.Bool("metadata_sync", true))
			return TunnelDashboard{}, fmt.Errorf("persist tunnel metadata: %w", err)
		}
	}
	if _, _, err := saveConfigMutation(ctx, previousConfig, cfg); err != nil {
		span.FailMessage("Tunnel runtime configuration persistence failed", err, tracepkg.Bool("metadata_sync", metadataSync))
		return TunnelDashboard{}, err
	}
	dashboard, err := TunnelStatus()
	if err != nil {
		span.FailMessage("Tunnel runtime status reload failed", err)
		return TunnelDashboard{}, err
	}
	span.EndMessage("Local tunnel runtime configured", tracepkg.String("tunnel_id", dashboard.Config.ID), tracepkg.Bool("enabled", dashboard.Config.Enabled), tracepkg.Bool("metadata_sync", metadataSync), tracepkg.URL("control_plane_base_url", dashboard.Config.ControlPlaneBaseURL), tracepkg.Bool("runtime_auth_present", strings.TrimSpace(dashboard.Config.APIKey) != ""))
	return dashboard, nil
}

func SetTunnelEnabled(ctx context.Context, enabled bool) (TunnelDashboard, error) {
	previous, err := config.Load()
	if err != nil {
		return TunnelDashboard{}, err
	}
	cfg := previous
	cfg.Tunnel.Enabled = enabled
	if err := config.Validate(cfg); err != nil {
		return TunnelDashboard{}, err
	}
	if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
		return TunnelDashboard{}, err
	}
	return TunnelStatus()
}

func SyncConfiguredTunnel(ctx context.Context) (tunnel.Metadata, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return tunnel.Metadata{}, "", err
	}
	if !tunnel.Configured(cfg.Tunnel) {
		return tunnel.Metadata{}, "", errors.New("configured tunnel id and runtime API key are required")
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
	status := TunnelAdminStatus{Configured: tunnel.AdminConfigured(cfg.Tunnel), Scope: scope, Access: tunnel.AdminAccessFromConfig(cfg.Tunnel)}
	fields := append(tunnelAdminScopeFields(scope), tracepkg.Bool("configured", status.Configured), tracepkg.Bool("read_access", status.Access.Read), tracepkg.Bool("manage_access", status.Access.Manage))
	span.EndMessage("Stored tunnel admin key status loaded", fields...)
	return status, nil
}

func SetTunnelAdminKey(ctx context.Context, input TunnelAdminKeyInput) (int, tunnel.AdminScope, error) {
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
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin-key.set", "Verifying and storing tunnel admin access", fields...)
	previous, _, err := loadConfigWithTracedLoader(ctx, "tunnel.admin.config.load", "Loading tunnel admin configuration", config.LoadForTunnelAdminKeyReplacement)
	if err != nil {
		span.FailMessage("Tunnel admin configuration load failed", err)
		return 0, tunnel.AdminScope{}, err
	}
	cfg := previous
	key := strings.TrimSpace(input.Key)
	if key == "" {
		err := errors.New("OpenAI admin key is required")
		span.FailMessage("Tunnel admin key validation failed", err, tracepkg.String("key_source", keySource))
		return 0, tunnel.AdminScope{}, err
	}
	candidate := cfg.Tunnel
	candidate.AdminKey = key
	scope, derivation, err := resolveTunnelAdminSetScope(ctx, candidate, input.Scope)
	if err != nil {
		span.FailMessage("Tunnel admin verification scope resolution failed", err, tracepkg.String("scope_derivation", derivation))
		return 0, tunnel.AdminScope{}, fmt.Errorf("admin key verification scope: %w", err)
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.admin-key.scope-resolved", "Resolved tunnel admin verification scope", append(tunnelAdminScopeFields(scope), tracepkg.String("scope_derivation", derivation))...)
	tunnel.ApplyAdminScope(&candidate, scope)
	access, count, err := tunnel.VerifyAdminKey(ctx, candidate)
	if err != nil {
		span.FailMessage("Tunnel admin key verification failed", errors.New("tunnel admin verification failed"), append(tunnelAdminScopeFields(scope), tracepkg.String("scope_derivation", derivation))...)
		return 0, tunnel.AdminScope{}, fmt.Errorf("admin key verification failed: %w", err)
	}
	tunnel.ApplyAdminAccess(&candidate, access)
	cfg.Tunnel = candidate
	if _, _, err := saveConfigMutation(ctx, previous, cfg); err != nil {
		span.FailMessage("Tunnel admin access persistence failed", err, tracepkg.Bool("read_access", access.Read), tracepkg.Bool("manage_access", access.Manage), tracepkg.Int("tunnel_count", count))
		return 0, tunnel.AdminScope{}, err
	}
	span.EndMessage("Tunnel admin access verified and stored", append(tunnelAdminScopeFields(scope), tracepkg.String("key_source", keySource), tracepkg.String("scope_derivation", derivation), tracepkg.Bool("read_access", access.Read), tracepkg.Bool("manage_access", access.Manage), tracepkg.Int("tunnel_count", count))...)
	return count, scope, nil
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
		err := errors.New("tunnel admin key is not configured; set it first")
		span.FailMessage("Stored tunnel admin access is not configured", err)
		return 0, tunnel.AdminScope{}, err
	}
	scope := tunnel.AdminScopeFromConfig(cfg.Tunnel)
	access, count, err := tunnel.VerifyAdminKey(ctx, cfg.Tunnel)
	if err != nil {
		span.FailMessage("Stored tunnel admin access verification failed", errors.New("tunnel admin verification failed"), tunnelAdminScopeFields(scope)...)
		return 0, scope, err
	}
	previous := cfg
	tunnel.ApplyAdminAccess(&cfg.Tunnel, access)
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
	previous, _, err := loadConfigWithTracedLoader(ctx, "tunnel.admin.config.load", "Loading tunnel admin configuration", config.LoadForTunnelAdminKeyReplacement)
	if err != nil {
		span.FailMessage("Tunnel admin key removal configuration load failed", err)
		return err
	}
	cfg := previous
	cfg.Tunnel.AdminKey = ""
	tunnel.ApplyAdminScope(&cfg.Tunnel, tunnel.AdminScope{})
	tunnel.ApplyAdminAccess(&cfg.Tunnel, tunnel.AdminAccess{})
	_, reloaded, err := saveConfigMutation(ctx, previous, cfg)
	if err != nil {
		span.FailMessage("Tunnel admin key removal failed", err)
		return err
	}
	span.EndMessage("Stored tunnel admin key removed", tracepkg.Bool("runtime_reloaded", reloaded))
	return nil
}

func ListManagedTunnels(ctx context.Context) ([]tunnel.Metadata, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return nil, errors.New("verified tunnel admin key is required")
	}
	if !cfg.Tunnel.AdminManageAccess {
		return nil, errors.New("tunnel admin key does not have verified Manage access")
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
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, errors.New("verified tunnel admin key is required")
	}
	if !cfg.Tunnel.AdminReadAccess && !cfg.Tunnel.AdminManageAccess {
		return ManagedTunnelResult{}, errors.New("tunnel admin key does not have verified Read access")
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
		err := errors.New("runtime API key is required to use this tunnel; provide one or enable automatic generation with a sufficiently privileged admin key")
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
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, errors.New("verified tunnel admin key is required")
	}
	if !cfg.Tunnel.AdminManageAccess {
		return ManagedTunnelResult{}, errors.New("tunnel admin key does not have verified Manage access")
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
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, errors.New("verified tunnel admin key is required")
	}
	if !cfg.Tunnel.AdminManageAccess {
		return ManagedTunnelResult{}, errors.New("tunnel admin key does not have verified Manage access")
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
	if !tunnel.AdminConfigured(cfg.Tunnel) {
		return ManagedTunnelResult{}, errors.New("verified tunnel admin key is required")
	}
	if !cfg.Tunnel.AdminManageAccess {
		return ManagedTunnelResult{}, errors.New("tunnel admin key does not have verified Manage access")
	}
	id = strings.TrimSpace(id)
	configuredTunnel := id != "" && id == strings.TrimSpace(cfg.Tunnel.ID)
	if configuredTunnel && !cfg.Server.Enabled {
		return ManagedTunnelResult{}, errors.New("cannot delete the configured OpenAI tunnel while MCP HTTP is disabled")
	}
	clearConfigured := clearConfig && configuredTunnel
	var clearedConfig config.Config
	if clearConfigured {
		clearedConfig = cfg
		clearedConfig.Tunnel.Enabled = false
		clearedConfig.Tunnel.ID = ""
		clearedConfig.Tunnel.APIKey = ""
		clearedConfig.Tunnel.OrganizationID = ""
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
		return errors.New("runtime API key is required to configure cgm")
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

func resolveTunnelAdminSetScope(ctx context.Context, cfg tunnel.Config, explicit *tunnel.AdminScope) (tunnel.AdminScope, string, error) {
	if explicit != nil {
		scope := tunnel.AdminScope{OrganizationID: strings.TrimSpace(explicit.OrganizationID), WorkspaceID: strings.TrimSpace(explicit.WorkspaceID), TenantID: strings.TrimSpace(explicit.TenantID)}
		return scope, "explicit", tunnel.ValidateAdminScope(scope)
	}
	if scope := tunnel.AdminScopeFromConfig(cfg); tunnel.ValidateAdminScope(scope) == nil {
		return scope, "stored", nil
	}
	if strings.TrimSpace(cfg.ID) == "" {
		return tunnel.AdminScope{}, "unresolved", errors.New("provide exactly one admin scope or configure a tunnel first")
	}
	metadata, err := tunnel.GetManaged(ctx, cfg, cfg.ID)
	if err != nil {
		return tunnel.AdminScope{}, "configured_tunnel", fmt.Errorf("derive admin scope from configured tunnel: %w", err)
	}
	for _, candidate := range []tunnel.AdminScope{
		{OrganizationID: singleID(metadata.OrganizationIDs)},
		{WorkspaceID: singleID(metadata.WorkspaceIDs)},
		{TenantID: singleID(metadata.TenantIDs)},
	} {
		if tunnel.ValidateAdminScope(candidate) == nil {
			return candidate, "configured_tunnel", nil
		}
	}
	return tunnel.AdminScope{}, "configured_tunnel", errors.New("configured tunnel does not expose one unambiguous admin scope")
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

func singleID(values []string) string {
	if len(values) == 1 {
		return strings.TrimSpace(values[0])
	}
	return ""
}
