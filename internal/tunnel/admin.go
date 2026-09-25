package tunnel

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	tunnelclient "github.com/openai/tunnel-client"
	tcconfig "github.com/openai/tunnel-client/pkg/config"
	tcadmin "github.com/openai/tunnel-client/pkg/controlplane/admin"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type UpdateRequest struct {
	Name            *string
	Description     *string
	TenantIDs       *[]string
	WorkspaceIDs    *[]string
	OrganizationIDs *[]string
}

func AdminScopeFromConfig(cfg Config) AdminScope {
	return AdminScope{OrganizationID: strings.TrimSpace(cfg.Admin.OrganizationID), WorkspaceID: strings.TrimSpace(cfg.Admin.WorkspaceID), TenantID: strings.TrimSpace(cfg.Admin.TenantID)}
}

func ApplyAdminScope(cfg *Config, scope AdminScope) {
	if cfg == nil {
		return
	}
	cfg.Admin.OrganizationID = strings.TrimSpace(scope.OrganizationID)
	cfg.Admin.WorkspaceID = strings.TrimSpace(scope.WorkspaceID)
	cfg.Admin.TenantID = strings.TrimSpace(scope.TenantID)
}

func AdminEnabled(cfg Config) bool {
	if !cfg.Admin.EnabledSet {
		return true
	}
	return cfg.Admin.Enabled
}

func SetAdminEnabled(cfg *Config, enabled bool) {
	if cfg == nil {
		return
	}
	cfg.Admin.Enabled = enabled
	cfg.Admin.EnabledSet = true
}

func SetAdminScope(cfg *Config, scope AdminScope) error {
	if cfg == nil {
		return errors.New("tunnel admin configuration is required")
	}
	if err := ValidateAdminScope(scope); err != nil {
		return err
	}
	ApplyAdminScope(cfg, scope)
	InvalidateAdminVerification(cfg)
	return nil
}

func ValidateAdminScope(scope AdminScope) error {
	count := 0
	if strings.TrimSpace(scope.OrganizationID) != "" {
		count++
	}
	if strings.TrimSpace(scope.WorkspaceID) != "" {
		count++
	}
	if strings.TrimSpace(scope.TenantID) != "" {
		count++
	}
	if count != 1 {
		return errors.New("provide exactly one admin scope: organization, workspace, or tenant")
	}
	return nil
}

func AdminConfigured(cfg Config) bool {
	return strings.TrimSpace(cfg.Admin.Key) != "" && ValidateAdminScope(AdminScopeFromConfig(cfg)) == nil
}

func AdminVerified(cfg Config) bool {
	return cfg.Admin.Verified || cfg.Admin.ReadAccess || cfg.Admin.ManageAccess
}

func AdminAccessFromConfig(cfg Config) AdminAccess {
	return AdminAccess{Read: cfg.Admin.ReadAccess, Manage: cfg.Admin.ManageAccess}
}

func AdminStateFromConfig(cfg Config) AdminState {
	scope := AdminScopeFromConfig(cfg)
	return AdminState{
		Enabled:        AdminEnabled(cfg),
		KeyConfigured:  strings.TrimSpace(cfg.Admin.Key) != "",
		Configured:     AdminConfigured(cfg),
		OrganizationID: scope.OrganizationID,
		WorkspaceID:    scope.WorkspaceID,
		TenantID:       scope.TenantID,
		Verified:       AdminVerified(cfg),
		ReadAccess:     cfg.Admin.ReadAccess,
		ManageAccess:   cfg.Admin.ManageAccess,
	}
}

func ApplyAdminAccess(cfg *Config, access AdminAccess) {
	if cfg == nil {
		return
	}
	cfg.Admin.ReadAccess = access.Read
	cfg.Admin.ManageAccess = access.Manage
}

func MarkAdminVerified(cfg *Config, access AdminAccess) {
	if cfg == nil {
		return
	}
	cfg.Admin.Verified = true
	ApplyAdminAccess(cfg, access)
}

func InvalidateAdminVerification(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.Admin.Verified = false
	cfg.Admin.ReadAccess = false
	cfg.Admin.ManageAccess = false
}

func VerifyAdminKey(ctx context.Context, cfg Config) (AdminAccess, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	scope := AdminScopeFromConfig(cfg)
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.verify", "Verifying tunnel admin access", append(adminScopeTraceFields(scope), tracepkg.URL("control_plane_url", adminAPIURL(cfg, "/v1/tunnels", adminScopeQuery(scope))))...)
	items, err := ListManaged(ctx, cfg, AdminScopeFromConfig(cfg))
	if err == nil {
		span.EndMessage("Tunnel admin access verified", tracepkg.Bool("read_access", true), tracepkg.Bool("manage_access", true), tracepkg.Int("tunnel_count", len(items)), tracepkg.String("verification_method", "list"))
		return AdminAccess{Read: true, Manage: true}, len(items), nil
	}
	var requestErr *tcadmin.RequestError
	if !errors.As(err, &requestErr) || requestErr.StatusCode != http.StatusForbidden {
		span.FailMessage("Tunnel admin access verification failed", safeAdminTraceError(err), adminRequestErrorTraceFields(err)...)
		return AdminAccess{}, 0, err
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.admin.verify.manage-denied", "Tunnel admin Manage access denied; probing Read access", append(adminRequestErrorTraceFields(err), tracepkg.Bool("manage_access", false))...)
	client, clientErr := adminTunnelClient(cfg, cfg.Admin.Key)
	if clientErr != nil {
		span.FailMessage("Tunnel admin Read access probe setup failed", clientErr)
		return AdminAccess{}, 0, clientErr
	}
	const probeID = "tunnel_00000000000000000000000000000000"
	readSpan := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.read-probe", "Probing tunnel admin Read access", tracepkg.String("method", http.MethodGet), tracepkg.URL("url", adminAPIURL(cfg, "/v1/tunnels/"+probeID, nil)))
	_, readErr := client.GetTunnel(ctx, probeID)
	if readErr == nil {
		readSpan.EndMessage("Tunnel admin Read access verified", tracepkg.Bool("read_access", true))
		span.EndMessage("Tunnel admin access verified", tracepkg.Bool("read_access", true), tracepkg.Bool("manage_access", false), tracepkg.Int("tunnel_count", 0), tracepkg.String("verification_method", "read_probe"))
		return AdminAccess{Read: true}, 0, nil
	}
	var readRequestErr *tcadmin.RequestError
	if errors.As(readErr, &readRequestErr) && (readRequestErr.StatusCode == http.StatusNotFound || readRequestErr.StatusCode == http.StatusBadRequest) {
		readSpan.EndMessage("Tunnel admin Read access verified", append(adminRequestErrorTraceFields(readErr), tracepkg.Bool("read_access", true))...)
		span.EndMessage("Tunnel admin access verified", tracepkg.Bool("read_access", true), tracepkg.Bool("manage_access", false), tracepkg.Int("tunnel_count", 0), tracepkg.String("verification_method", "read_probe"))
		return AdminAccess{Read: true}, 0, nil
	}
	readSpan.FailMessage("Tunnel admin Read access probe failed", safeAdminTraceError(readErr), adminRequestErrorTraceFields(readErr)...)
	span.FailMessage("Tunnel admin access verification failed", safeAdminTraceError(err), tracepkg.Bool("read_access", false), tracepkg.Bool("manage_access", false))
	return AdminAccess{}, 0, err
}

func ListManaged(ctx context.Context, cfg Config, scope AdminScope) ([]Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.list", "Listing managed tunnels", append(adminScopeTraceFields(scope), tracepkg.String("method", http.MethodGet), tracepkg.URL("url", adminAPIURL(cfg, "/v1/tunnels", adminScopeQuery(scope))))...)
	if strings.TrimSpace(cfg.Admin.Key) == "" {
		err := errors.New("OpenAI tunnel admin key is not configured")
		span.FailMessage("Managed tunnel listing failed", err)
		return nil, err
	}
	if err := ValidateAdminScope(scope); err != nil {
		span.FailMessage("Managed tunnel listing scope invalid", err)
		return nil, err
	}
	client, err := adminTunnelClient(cfg, cfg.Admin.Key)
	if err != nil {
		span.FailMessage("Managed tunnel client setup failed", err)
		return nil, err
	}
	response, err := client.ListTunnels(ctx, strings.TrimSpace(scope.OrganizationID), strings.TrimSpace(scope.WorkspaceID), strings.TrimSpace(scope.TenantID))
	if err != nil {
		span.FailMessage("Managed tunnel listing failed", safeAdminTraceError(err), adminRequestErrorTraceFields(err)...)
		return nil, err
	}
	items := make([]Metadata, 0, len(response.Tunnels))
	for index := range response.Tunnels {
		items = append(items, metadataFromTunnel(&response.Tunnels[index]))
	}
	span.EndMessage("Managed tunnels listed", tracepkg.Int("tunnel_count", len(items)))
	return items, nil
}

func GetManaged(ctx context.Context, cfg Config, id string) (Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.get", "Fetching managed tunnel", tracepkg.String("tunnel_id", id), tracepkg.String("method", http.MethodGet), tracepkg.URL("url", adminAPIURL(cfg, "/v1/tunnels/"+url.PathEscape(id), nil)))
	if strings.TrimSpace(cfg.Admin.Key) == "" {
		err := errors.New("OpenAI tunnel admin key is not configured")
		span.FailMessage("Managed tunnel fetch failed", err)
		return Metadata{}, err
	}
	client, err := adminTunnelClient(cfg, cfg.Admin.Key)
	if err != nil {
		span.FailMessage("Managed tunnel client setup failed", err)
		return Metadata{}, err
	}
	value, err := client.GetTunnel(ctx, id)
	if err != nil {
		span.FailMessage("Managed tunnel fetch failed", safeAdminTraceError(err), adminRequestErrorTraceFields(err)...)
		return Metadata{}, err
	}
	metadata := metadataFromTunnel(value)
	span.EndMessage("Managed tunnel fetched", tracepkg.String("tunnel_id", metadata.ID), tracepkg.Int("organization_count", len(metadata.OrganizationIDs)), tracepkg.Int("workspace_count", len(metadata.WorkspaceIDs)), tracepkg.Int("tenant_count", len(metadata.TenantIDs)))
	return metadata, nil
}

func CreateManaged(ctx context.Context, cfg Config, req CreateRequest) (Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.create", "Creating managed tunnel", tracepkg.String("method", http.MethodPost), tracepkg.URL("url", adminAPIURL(cfg, "/v1/tunnels", nil)), tracepkg.Int("organization_count", len(req.OrganizationIDs)), tracepkg.Int("workspace_count", len(req.WorkspaceIDs)), tracepkg.Int("tenant_count", len(req.TenantIDs)))
	metadata, err := createWithAdminKey(ctx, cfg, cfg.Admin.Key, req)
	if err != nil {
		span.FailMessage("Managed tunnel creation failed", safeAdminTraceError(err), adminRequestErrorTraceFields(err)...)
		return Metadata{}, err
	}
	span.EndMessage("Managed tunnel created", tracepkg.String("tunnel_id", metadata.ID), tracepkg.Int("organization_count", len(metadata.OrganizationIDs)), tracepkg.Int("workspace_count", len(metadata.WorkspaceIDs)), tracepkg.Int("tenant_count", len(metadata.TenantIDs)))
	return metadata, nil
}

func UpdateManaged(ctx context.Context, cfg Config, id string, req UpdateRequest) (Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.update", "Updating managed tunnel", tracepkg.String("tunnel_id", id), tracepkg.String("method", http.MethodPost), tracepkg.URL("url", adminAPIURL(cfg, "/v1/tunnels/"+url.PathEscape(id), nil)), tracepkg.Any("changed_fields", managedTunnelUpdateFields(req)))
	if strings.TrimSpace(cfg.Admin.Key) == "" {
		err := errors.New("OpenAI tunnel admin key is not configured")
		span.FailMessage("Managed tunnel update failed", err)
		return Metadata{}, err
	}
	if req.Name == nil && req.Description == nil && req.TenantIDs == nil && req.WorkspaceIDs == nil && req.OrganizationIDs == nil {
		err := errors.New("provide at least one field to update")
		span.FailMessage("Managed tunnel update validation failed", err)
		return Metadata{}, err
	}
	client, err := adminTunnelClient(cfg, cfg.Admin.Key)
	if err != nil {
		span.FailMessage("Managed tunnel client setup failed", err)
		return Metadata{}, err
	}
	value, err := client.UpdateTunnel(ctx, id, tcadmin.TunnelUpdateRequest{
		Name: req.Name, Description: req.Description, TenantIDs: req.TenantIDs, WorkspaceIDs: req.WorkspaceIDs, OrganizationIDs: req.OrganizationIDs,
	})
	if err != nil {
		span.FailMessage("Managed tunnel update failed", safeAdminTraceError(err), adminRequestErrorTraceFields(err)...)
		return Metadata{}, err
	}
	metadata := metadataFromTunnel(value)
	span.EndMessage("Managed tunnel updated", tracepkg.String("tunnel_id", metadata.ID), tracepkg.Any("changed_fields", managedTunnelUpdateFields(req)))
	return metadata, nil
}

func DeleteManaged(ctx context.Context, cfg Config, id string) (Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.delete", "Deleting managed tunnel", tracepkg.String("tunnel_id", id), tracepkg.String("method", http.MethodDelete), tracepkg.URL("url", adminAPIURL(cfg, "/v1/tunnels/"+url.PathEscape(id), nil)))
	if strings.TrimSpace(cfg.Admin.Key) == "" {
		err := errors.New("OpenAI tunnel admin key is not configured")
		span.FailMessage("Managed tunnel deletion failed", err)
		return Metadata{}, err
	}
	client, err := adminTunnelClient(cfg, cfg.Admin.Key)
	if err != nil {
		span.FailMessage("Managed tunnel client setup failed", err)
		return Metadata{}, err
	}
	value, err := client.DeleteTunnel(ctx, id)
	if err != nil {
		span.FailMessage("Managed tunnel deletion failed", safeAdminTraceError(err), adminRequestErrorTraceFields(err)...)
		return Metadata{}, err
	}
	metadata := metadataFromTunnel(value)
	span.EndMessage("Managed tunnel deleted", tracepkg.String("tunnel_id", metadata.ID))
	return metadata, nil
}

func adminScopeQuery(scope AdminScope) url.Values {
	query := url.Values{}
	if value := strings.TrimSpace(scope.OrganizationID); value != "" {
		query.Set("organization_id", value)
	}
	if value := strings.TrimSpace(scope.WorkspaceID); value != "" {
		query.Set("workspace_id", value)
	}
	if value := strings.TrimSpace(scope.TenantID); value != "" {
		query.Set("tenant_id", value)
	}
	return query
}

func adminScopeTraceFields(scope AdminScope) []tracepkg.Field {
	fields := []tracepkg.Field{}
	if value := strings.TrimSpace(scope.OrganizationID); value != "" {
		fields = append(fields, tracepkg.String("scope_type", "organization"), tracepkg.String("scope_id", value))
	}
	if value := strings.TrimSpace(scope.WorkspaceID); value != "" {
		fields = append(fields, tracepkg.String("scope_type", "workspace"), tracepkg.String("scope_id", value))
	}
	if value := strings.TrimSpace(scope.TenantID); value != "" {
		fields = append(fields, tracepkg.String("scope_type", "tenant"), tracepkg.String("scope_id", value))
	}
	if len(fields) == 0 {
		fields = append(fields, tracepkg.String("scope_type", "unresolved"))
	}
	return fields
}

func adminAPIURL(cfg Config, path string, query url.Values) string {
	baseURL := strings.TrimSpace(cfg.ControlPlaneBaseURL)
	if baseURL == "" {
		baseURL = tunnelclient.DefaultControlPlaneBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return baseURL
	}
	target := tcconfig.ResolveControlPlanePath(parsed, "", path)
	if len(query) > 0 {
		target.RawQuery = query.Encode()
	}
	return target.String()
}

func adminRequestErrorTraceFields(err error) []tracepkg.Field {
	var requestErr *tcadmin.RequestError
	if !errors.As(err, &requestErr) {
		return nil
	}
	fields := []tracepkg.Field{tracepkg.String("method", requestErr.Method), tracepkg.Int("status", requestErr.StatusCode), tracepkg.Int64("response_bytes", int64(len(requestErr.ResponseBody)))}
	if requestErr.RequestID != "" {
		fields = append(fields, tracepkg.String("request_id", requestErr.RequestID))
	}
	if requestErr.Code != "" {
		fields = append(fields, tracepkg.String("error_code", requestErr.Code))
	}
	return fields
}

func safeAdminTraceError(err error) error {
	if err == nil {
		return nil
	}
	var requestErr *tcadmin.RequestError
	if errors.As(err, &requestErr) {
		return errors.New("OpenAI tunnel management API request failed")
	}
	return err
}

func managedTunnelUpdateFields(req UpdateRequest) []string {
	fields := []string{}
	if req.Name != nil {
		fields = append(fields, "name")
	}
	if req.Description != nil {
		fields = append(fields, "description")
	}
	if req.TenantIDs != nil {
		fields = append(fields, "tenant_ids")
	}
	if req.WorkspaceIDs != nil {
		fields = append(fields, "workspace_ids")
	}
	if req.OrganizationIDs != nil {
		fields = append(fields, "organization_ids")
	}
	return fields
}
