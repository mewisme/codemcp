package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/tunnel"
)

func (api API) handleTunnelConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if api.Tunnel == nil {
		http.Error(w, "tunnel unavailable", http.StatusServiceUnavailable)
		return
	}
	if api.Config == nil {
		http.Error(w, "config unavailable", http.StatusServiceUnavailable)
		return
	}
	value := api.Config.Snapshot().Tunnel
	view := tunnelConfigView{Config: value, RuntimeKeyConfigured: strings.TrimSpace(value.APIKey) != "", Admin: tunnel.AdminStateFromConfig(value)}
	view.Config.APIKey = ""
	view.Config.Admin.Key = ""
	writeJSON(w, view)
}

func (api API) handleTunnel(w http.ResponseWriter, r *http.Request) {
	if api.Tunnel == nil {
		http.Error(w, "tunnel unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, api.tunnelStatus(r.Context()))
	case http.MethodPost:
		if err := api.Tunnel.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, api.tunnelStatus(r.Context()))
	case http.MethodDelete:
		if api.Config == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		if !api.Config.Snapshot().Server.Enabled {
			http.Error(w, "cannot stop OpenAI Secure MCP Tunnel while MCP HTTP is disabled", http.StatusBadRequest)
			return
		}
		if err := api.Tunnel.Stop(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, api.tunnelStatus(r.Context()))
	case http.MethodPut:
		api.configureTunnel(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) tunnelStatus(_ context.Context) tunnel.Status {
	if api.Tunnel == nil {
		return tunnel.Status{Provider: tunnel.ProviderOpenAI}
	}
	return api.Tunnel.Status()
}

func (api API) configureTunnel(w http.ResponseWriter, r *http.Request) {
	if api.Config == nil || api.Tunnel == nil {
		http.Error(w, "config unavailable", http.StatusServiceUnavailable)
		return
	}
	var next tunnel.Config
	if err := decodeJSONBody(w, r, &next); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	current := api.Config.Snapshot()
	effective := next
	if effective.APIKey == "" {
		effective.APIKey = current.Tunnel.APIKey
	}
	effective.Admin.Key = current.Tunnel.Admin.Key
	effective.Admin.OrganizationID = current.Tunnel.Admin.OrganizationID
	effective.Admin.WorkspaceID = current.Tunnel.Admin.WorkspaceID
	effective.Admin.TenantID = current.Tunnel.Admin.TenantID
	candidate := current
	candidate.Tunnel = effective
	if err := config.Validate(candidate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if tunnel.Configured(effective) && !tunnel.RuntimeConfigEqual(current.Tunnel, effective) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		_, _, err := config.SyncTunnelMetadata(ctx, effective)
		cancel()
		if err != nil {
			http.Error(w, "persist tunnel metadata: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	_, err := api.Config.Update(func(config.Config) (config.Config, error) {
		if err := api.Tunnel.Reconfigure(effective, func() error { return api.persistConfig(candidate) }); err != nil {
			return current, err
		}
		return candidate, nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if metadata, err := config.LoadTunnelMetadata(effective.ID); err == nil {
		_ = api.Tunnel.SeedMetadata(metadata)
	}
	writeJSON(w, api.tunnelStatus(r.Context()))
}

type tunnelConfigView struct {
	tunnel.Config
	RuntimeKeyConfigured bool              `json:"runtime_key_configured"`
	Admin                tunnel.AdminState `json:"admin"`
}

type tunnelAdminKeyRequest struct {
	AdminKey       string `json:"admin_key,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
}

type tunnelAdminKeyStatus struct {
	Enabled       bool               `json:"enabled"`
	KeyConfigured bool               `json:"key_configured"`
	Configured    bool               `json:"configured"`
	Verified      bool               `json:"verified"`
	Scope         tunnel.AdminScope  `json:"scope"`
	Access        tunnel.AdminAccess `json:"access"`
	Tunnels       int                `json:"tunnels,omitempty"`
}

type managedTunnelCreateRequest struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	TenantIDs       []string `json:"tenant_ids,omitempty"`
	WorkspaceIDs    []string `json:"workspace_ids,omitempty"`
	OrganizationIDs []string `json:"organization_ids,omitempty"`
}

type managedTunnelUpdateRequest struct {
	Name            *string   `json:"name,omitempty"`
	Description     *string   `json:"description,omitempty"`
	TenantIDs       *[]string `json:"tenant_ids,omitempty"`
	WorkspaceIDs    *[]string `json:"workspace_ids,omitempty"`
	OrganizationIDs *[]string `json:"organization_ids,omitempty"`
}

type managedTunnelUseRequest struct {
	ID                     string `json:"id"`
	RuntimeAPIKey          string `json:"runtime_api_key,omitempty"`
	AutoGenerateRuntimeKey bool   `json:"auto_generate_runtime_key,omitempty"`
	ProjectID              string `json:"project_id,omitempty"`
}

type managedTunnelUseResult struct {
	Metadata tunnel.Metadata `json:"metadata"`
	Status   tunnel.Status   `json:"status"`
}

func (api API) handleTunnelAdminKey(w http.ResponseWriter, r *http.Request) {
	if api.Config == nil || api.Tunnel == nil {
		http.Error(w, "tunnel configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, tunnelAdminStatus(api.Config.Snapshot().Tunnel, 0))
	case http.MethodPut:
		var request tunnelAdminKeyRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		count, err := api.saveTunnelAdminKey(r.Context(), request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, tunnelAdminStatus(api.Config.Snapshot().Tunnel, count))
	case http.MethodPost:
		cfg := api.Config.Snapshot().Tunnel
		if !tunnel.AdminConfigured(cfg) {
			http.Error(w, "tunnel admin key is not configured", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		access, count, err := tunnel.VerifyAdminKey(ctx, cfg)
		cancel()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, err = api.Config.Update(func(candidate config.Config) (config.Config, error) {
			previous := candidate
			tunnel.MarkAdminVerified(&candidate.Tunnel, access)
			if err := api.persistConfig(candidate); err != nil {
				return previous, err
			}
			if err := api.Tunnel.SyncManagementConfig(candidate.Tunnel); err != nil {
				return previous, errors.Join(err, api.persistConfig(previous))
			}
			return candidate, nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, tunnelAdminStatus(api.Config.Snapshot().Tunnel, count))
	case http.MethodDelete:
		if err := api.removeTunnelAdminKey(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, tunnelAdminStatus(api.Config.Snapshot().Tunnel, 0))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) saveTunnelAdminKey(parent context.Context, request tunnelAdminKeyRequest) (int, error) {
	_ = parent
	_, err := api.Config.Update(func(candidate config.Config) (config.Config, error) {
		previous := candidate
		key := strings.TrimSpace(request.AdminKey)
		if key == "" {
			key = strings.TrimSpace(candidate.Tunnel.Admin.Key)
		}
		candidate.Tunnel.Admin.Key = key
		if key == "" {
			return previous, errors.New("admin key is required")
		}
		scope := tunnel.AdminScope{OrganizationID: request.OrganizationID, WorkspaceID: request.WorkspaceID, TenantID: request.TenantID}
		if strings.TrimSpace(scope.OrganizationID) != "" || strings.TrimSpace(scope.WorkspaceID) != "" || strings.TrimSpace(scope.TenantID) != "" {
			if err := tunnel.SetAdminScope(&candidate.Tunnel, scope); err != nil {
				return previous, err
			}
		} else {
			tunnel.InvalidateAdminVerification(&candidate.Tunnel)
		}
		if err := api.persistConfig(candidate); err != nil {
			return previous, err
		}
		if err := api.Tunnel.SyncManagementConfig(candidate.Tunnel); err != nil {
			return previous, errors.Join(err, api.persistConfig(previous))
		}
		return candidate, nil
	})
	return 0, err
}

func (api API) removeTunnelAdminKey() error {
	_, err := api.Config.Update(func(candidate config.Config) (config.Config, error) {
		previous := candidate
		candidate.Tunnel.Admin.Key = ""
		tunnel.ApplyAdminScope(&candidate.Tunnel, tunnel.AdminScope{})
		tunnel.InvalidateAdminVerification(&candidate.Tunnel)
		if err := api.persistConfig(candidate); err != nil {
			return previous, err
		}
		if err := api.Tunnel.SyncManagementConfig(candidate.Tunnel); err != nil {
			return previous, errors.Join(err, api.persistConfig(previous))
		}
		return candidate, nil
	})
	return err
}

func tunnelAdminStatus(cfg tunnel.Config, count int) tunnelAdminKeyStatus {
	return tunnelAdminKeyStatus{Enabled: tunnel.AdminEnabled(cfg), KeyConfigured: strings.TrimSpace(cfg.Admin.Key) != "", Configured: tunnel.AdminConfigured(cfg), Verified: tunnel.AdminVerified(cfg), Scope: tunnel.AdminScopeFromConfig(cfg), Access: tunnel.AdminAccessFromConfig(cfg), Tunnels: count}
}

func (api API) handleManagedTunnels(w http.ResponseWriter, r *http.Request) {
	if api.Config == nil {
		http.Error(w, "tunnel configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	cfg := api.Config.Snapshot().Tunnel
	if !tunnel.AdminEnabled(cfg) {
		http.Error(w, "tunnel admin management is disabled", http.StatusForbidden)
		return
	}
	if !tunnel.AdminConfigured(cfg) {
		http.Error(w, "configured tunnel admin key and scope are required", http.StatusBadRequest)
		return
	}
	if !cfg.Admin.ManageAccess {
		http.Error(w, "tunnel admin key does not have verified Manage access", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodGet:
		items, err := tunnel.ListManaged(ctx, cfg, tunnel.AdminScopeFromConfig(cfg))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, item := range items {
			if _, err := config.SaveTunnelMetadata(item); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, items)
	case http.MethodPost:
		var request managedTunnelCreateRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		metadata, err := tunnel.CreateManaged(ctx, cfg, tunnel.CreateRequest{Name: request.Name, Description: request.Description, TenantIDs: request.TenantIDs, WorkspaceIDs: request.WorkspaceIDs, OrganizationIDs: request.OrganizationIDs})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := config.SaveTunnelMetadata(metadata); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, metadata)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleManagedTunnel(w http.ResponseWriter, r *http.Request) {
	if api.Config == nil {
		http.Error(w, "tunnel configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/tunnel/managed/"))
	if id == "" || id == "use" {
		http.Error(w, "managed tunnel id is required", http.StatusBadRequest)
		return
	}
	cfg := api.Config.Snapshot().Tunnel
	if !tunnel.AdminEnabled(cfg) {
		http.Error(w, "tunnel admin management is disabled", http.StatusForbidden)
		return
	}
	if !tunnel.AdminConfigured(cfg) {
		http.Error(w, "configured tunnel admin key and scope are required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodGet:
		if !cfg.Admin.ReadAccess && !cfg.Admin.ManageAccess {
			http.Error(w, "tunnel admin key does not have verified Read access", http.StatusForbidden)
			return
		}
		metadata, err := tunnel.GetManaged(ctx, cfg, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := config.SaveTunnelMetadata(metadata); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, metadata)
	case http.MethodPut:
		if !cfg.Admin.ManageAccess {
			http.Error(w, "tunnel admin key does not have verified Manage access", http.StatusForbidden)
			return
		}
		var request managedTunnelUpdateRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		metadata, err := tunnel.UpdateManaged(ctx, cfg, id, tunnel.UpdateRequest{Name: request.Name, Description: request.Description, TenantIDs: request.TenantIDs, WorkspaceIDs: request.WorkspaceIDs, OrganizationIDs: request.OrganizationIDs})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := config.SaveTunnelMetadata(metadata); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, metadata)
	case http.MethodDelete:
		if !cfg.Admin.ManageAccess {
			http.Error(w, "tunnel admin key does not have verified Manage access", http.StatusForbidden)
			return
		}
		if strings.TrimSpace(cfg.ID) == id {
			http.Error(w, "cannot delete the locally selected tunnel; select another tunnel or clear runtime configuration first", http.StatusConflict)
			return
		}
		metadata, err := tunnel.DeleteManaged(ctx, cfg, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := config.RemoveTunnelMetadata(metadata.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, metadata)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleManagedTunnelUse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if api.Config == nil || api.Tunnel == nil {
		http.Error(w, "tunnel configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	var request managedTunnelUseRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(request.ID)
	if id == "" {
		http.Error(w, "managed tunnel id is required", http.StatusBadRequest)
		return
	}
	current := api.Config.Snapshot()
	if !tunnel.AdminEnabled(current.Tunnel) {
		http.Error(w, "tunnel admin management is disabled", http.StatusForbidden)
		return
	}
	if !tunnel.AdminConfigured(current.Tunnel) {
		http.Error(w, "configured tunnel admin key and scope are required", http.StatusBadRequest)
		return
	}
	if !current.Tunnel.Admin.ReadAccess && !current.Tunnel.Admin.ManageAccess {
		http.Error(w, "tunnel admin key does not have verified Read access", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	metadata, err := tunnel.GetManaged(ctx, current.Tunnel, id)
	cancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(request.RuntimeAPIKey)
	if key == "" {
		key = strings.TrimSpace(current.Tunnel.APIKey)
	}
	if key == "" && request.AutoGenerateRuntimeKey {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		generated, generateErr := tunnel.GenerateRuntimeKey(ctx, current.Tunnel, request.ProjectID)
		cancel()
		if generateErr != nil {
			http.Error(w, generateErr.Error(), http.StatusBadRequest)
			return
		}
		key = generated.Value
	}
	if key == "" {
		http.Error(w, "runtime API key is required to use this tunnel; provide one or enable automatic generation", http.StatusBadRequest)
		return
	}
	candidate := current
	candidate.Tunnel.ID = metadata.ID
	candidate.Tunnel.APIKey = key
	candidate.Tunnel.Enabled = true
	if len(metadata.OrganizationIDs) == 1 {
		candidate.Tunnel.OrganizationID = metadata.OrganizationIDs[0]
	}
	if err := config.Validate(candidate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := config.SaveTunnelMetadata(metadata); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = api.Config.Update(func(config.Config) (config.Config, error) {
		if err := api.Tunnel.ReconfigureSeeded(candidate.Tunnel, metadata, func() error { return api.persistConfig(candidate) }); err != nil {
			return current, err
		}
		return candidate, nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = api.Tunnel.SeedMetadata(metadata)
	writeJSON(w, managedTunnelUseResult{Metadata: metadata, Status: api.tunnelStatus(r.Context())})
}
