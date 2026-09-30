package admin

import (
	"context"
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
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
	cfg := api.Config.Snapshot()
	value := cfg.Tunnel
	view := tunnelConfigView{
		Config: value, RuntimeKeyConfigured: strings.TrimSpace(value.APIKey) != "", RuntimeKeyPreview: tunnel.SecretPreview(value.APIKey),
		AdminKeyPreview: tunnel.SecretPreview(value.Admin.Key), Admin: tunnel.AdminStateFromConfig(value),
	}
	view.Config.APIKey = ""
	view.Config.Admin.Key = ""
	writeJSON(w, view)
}

func (api API) handleTunnelRuntimeKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	api.dispatch(w, r, capability.TunnelConfigure, application.TunnelConfigureInput{ClearRuntimeKey: true})
}

func (api API) handleTunnel(w http.ResponseWriter, r *http.Request) {
	if api.Tunnel == nil {
		http.Error(w, "tunnel unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, tunnel.PublicStatus(api.tunnelStatus(r.Context())))
	case http.MethodPost:
		dashboard, err := application.SetTunnelEnabled(r.Context(), true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, tunnel.PublicStatus(dashboard.Status))
	case http.MethodDelete:
		dashboard, err := application.SetTunnelEnabled(r.Context(), false)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, tunnel.PublicStatus(dashboard.Status))
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
	input := application.TunnelRuntimeInput{
		Enabled:             &next.Enabled,
		ID:                  &next.ID,
		ControlPlaneBaseURL: &next.ControlPlaneBaseURL,
		OrganizationID:      &next.OrganizationID,
	}
	if strings.TrimSpace(next.APIKey) != "" {
		input.APIKey = &next.APIKey
	}
	dashboard, err := application.ConfigureTunnelRuntime(r.Context(), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, tunnel.PublicStatus(dashboard.Status))
}

type tunnelConfigView struct {
	tunnel.Config
	RuntimeKeyConfigured bool              `json:"runtime_key_configured"`
	RuntimeKeyPreview    string            `json:"runtime_key_preview,omitempty"`
	AdminKeyPreview      string            `json:"admin_key_preview,omitempty"`
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
	KeyPreview    string             `json:"key_preview,omitempty"`
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
		status, err := application.TunnelAdminKeyStatusContext(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, tunnelAdminStatusFromApplication(status, 0))
	case http.MethodPut:
		var request tunnelAdminKeyRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var scope *tunnel.AdminScope
		requested := tunnel.AdminScope{OrganizationID: request.OrganizationID, WorkspaceID: request.WorkspaceID, TenantID: request.TenantID}
		if strings.TrimSpace(requested.OrganizationID) != "" || strings.TrimSpace(requested.WorkspaceID) != "" || strings.TrimSpace(requested.TenantID) != "" {
			scope = &requested
		}
		_, err := application.SetTunnelAdminKey(r.Context(), application.TunnelAdminKeyInput{Key: request.AdminKey, Scope: scope})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status, err := application.TunnelAdminKeyStatusContext(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, tunnelAdminStatusFromApplication(status, 0))
	case http.MethodPost:
		count, _, err := application.VerifyTunnelAdminKey(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status, err := application.TunnelAdminKeyStatusContext(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, tunnelAdminStatusFromApplication(status, count))
	case http.MethodDelete:
		if err := application.RemoveTunnelAdminKey(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		status, err := application.TunnelAdminKeyStatusContext(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, tunnelAdminStatusFromApplication(status, 0))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func tunnelAdminStatusFromApplication(status application.TunnelAdminStatus, count int) tunnelAdminKeyStatus {
	return tunnelAdminKeyStatus{Enabled: status.Enabled, KeyConfigured: status.KeyConfigured, KeyPreview: status.KeyPreview, Configured: status.Configured, Verified: status.Verified, Scope: status.Scope, Access: status.Access, Tunnels: count}
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
	switch r.Method {
	case http.MethodGet:
		items, err := application.RefreshManagedTunnels(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, items)
	case http.MethodPost:
		var request managedTunnelCreateRequest
		if err := decodeJSONBody(w, r, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := application.CreateManagedTunnel(r.Context(), tunnel.CreateRequest{Name: request.Name, Description: request.Description, TenantIDs: request.TenantIDs, WorkspaceIDs: request.WorkspaceIDs, OrganizationIDs: request.OrganizationIDs}, application.ManagedTunnelOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Metadata)
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
	switch r.Method {
	case http.MethodGet:
		if !cfg.Admin.ReadAccess && !cfg.Admin.ManageAccess {
			http.Error(w, "tunnel admin key does not have verified Read access", http.StatusForbidden)
			return
		}
		result, err := application.GetManagedTunnel(r.Context(), id, application.ManagedTunnelOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Metadata)
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
		result, err := application.UpdateManagedTunnel(r.Context(), id, tunnel.UpdateRequest{Name: request.Name, Description: request.Description, TenantIDs: request.TenantIDs, WorkspaceIDs: request.WorkspaceIDs, OrganizationIDs: request.OrganizationIDs}, application.ManagedTunnelOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Metadata)
	case http.MethodDelete:
		if !cfg.Admin.ManageAccess {
			http.Error(w, "tunnel admin key does not have verified Manage access", http.StatusForbidden)
			return
		}
		if strings.TrimSpace(cfg.ID) == id {
			http.Error(w, "cannot delete the locally selected tunnel; select another tunnel or clear runtime configuration first", http.StatusConflict)
			return
		}
		result, err := application.DeleteManagedTunnel(r.Context(), id, false)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, result.Metadata)
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
	result, err := application.UseManagedTunnel(r.Context(), id, application.ManagedTunnelUseOptions{RuntimeAPIKey: request.RuntimeAPIKey, AutoGenerateRuntimeKey: request.AutoGenerateRuntimeKey, ProjectID: request.ProjectID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dashboard, err := application.TunnelStatus()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, managedTunnelUseResult{Metadata: result.Metadata, Status: tunnel.PublicStatus(dashboard.Status)})
}
