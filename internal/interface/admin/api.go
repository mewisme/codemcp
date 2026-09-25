package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/config"
	mcpnetwork "go.mewis.me/codemcp/internal/network"
	"go.mewis.me/codemcp/internal/notification"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

const maxRequestBodyBytes int64 = 1 << 20

type API struct {
	Approvals     *approval.Manager
	Executions    *shellruntime.ExecutionHub
	Upstream      *upstream.Manager
	Tools         *tools.Runtime
	Workspaces    *workspace.Manager
	Tunnel        *tunnel.Client
	Config        *config.RuntimeStore
	Notifications *notification.Coordinator
	OAuth         *mcpoauth.Store
	OAuthFlows    *mcpoauth.FlowManager
	ReloadConfig  func(config.Config) error
	saveConfig    func(config.Config) error
}

type authSettings struct {
	MCPEnabled           bool `json:"mcp_enabled"`
	AdminEnabled         bool `json:"admin_enabled"`
	MCPTokenConfigured   bool `json:"mcp_token_configured"`
	AdminTokenConfigured bool `json:"admin_token_configured"`
}

type authPatch struct {
	MCPEnabled   *bool `json:"mcp_enabled,omitempty"`
	AdminEnabled *bool `json:"admin_enabled,omitempty"`
}

type publicConfig struct {
	Server       config.ServerConfig       `json:"server"`
	Admin        config.AdminConfig        `json:"admin"`
	Auth         authSettings              `json:"auth"`
	Permissions  config.PermissionsConfig  `json:"permissions"`
	Shell        config.ShellConfig        `json:"shell"`
	Integrations config.IntegrationsConfig `json:"integrations"`
}

type configPatch struct {
	Server       *serverPatch              `json:"server,omitempty"`
	Admin        *config.AdminConfig       `json:"admin,omitempty"`
	Auth         *authPatch                `json:"auth,omitempty"`
	Permissions  *config.PermissionsConfig `json:"permissions,omitempty"`
	Shell        *config.ShellConfig       `json:"shell,omitempty"`
	Integrations *integrationPatch         `json:"integrations,omitempty"`
}

type serverPatch struct {
	Enabled                      *bool                  `json:"enabled,omitempty"`
	Port                         *int                   `json:"port,omitempty"`
	Expose                       *config.ExposureConfig `json:"expose,omitempty"`
	AllowInsecureHTTP            *bool                  `json:"allow_insecure_http,omitempty"`
	AllowUnauthenticatedLoopback *bool                  `json:"allow_unauthenticated_loopback,omitempty"`
}

type integrationPatch struct {
	Ponytail  *integrationStatePatch      `json:"ponytail,omitempty"`
	Caveman   *integrationStatePatch      `json:"caveman,omitempty"`
	RTK       *integrationExecutablePatch `json:"rtk,omitempty"`
	CodeGraph *integrationExecutablePatch `json:"codegraph,omitempty"`
}

type integrationStatePatch struct {
	Active *bool   `json:"active,omitempty"`
	Mode   *string `json:"mode,omitempty"`
}

type integrationExecutablePatch struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Path    *string `json:"path,omitempty"`
}

func New(api API) http.Handler {
	api = api.withOAuth()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", method(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		authEnabled := api.Config != nil && api.Config.Snapshot().Auth.AdminEnabled
		writeJSON(w, map[string]bool{"ok": true, "auth_enabled": authEnabled})
	}))
	mux.HandleFunc("/api/network/interfaces", api.handleNetworkInterfaces)

	mux.HandleFunc("/api/config", api.handleConfig)
	mux.HandleFunc("/api/instructions/global", api.handleGlobalInstructions)
	mux.HandleFunc("/api/workspaces", api.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", api.handleWorkspace)
	mux.HandleFunc("/api/workspace-containers", api.handleWorkspaceContainers)
	mux.HandleFunc("/api/workspace-containers/", api.handleWorkspaceContainer)
	mux.HandleFunc("/api/tools", api.handleTools)
	mux.HandleFunc("/api/requests", api.handleRequests)
	mux.HandleFunc("/api/requests/", api.handleRequest)
	mux.HandleFunc("/api/notifications", api.handleNotifications)
	mux.HandleFunc("/api/upstream", api.handleUpstreams)
	mux.HandleFunc("/api/upstream/", api.handleUpstream)
	mux.HandleFunc("/api/tunnel/config", api.handleTunnelConfig)
	mux.HandleFunc("/api/tunnel/admin/key", api.handleTunnelAdminKey)
	mux.HandleFunc("/api/tunnel/managed", api.handleManagedTunnels)
	mux.HandleFunc("/api/tunnel/managed/use", api.handleManagedTunnelUse)
	mux.HandleFunc("/api/tunnel/managed/", api.handleManagedTunnel)
	mux.HandleFunc("/api/tunnel", api.handleTunnel)
	return withCanonicalOperation(mux)
}

func (api API) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	enabled := map[string]bool{
		notification.ProviderDesktop:  false,
		notification.ProviderTelegram: false,
	}
	if api.Config != nil {
		cfg := api.Config.Snapshot().Notifications.Approval
		if cfg.Enabled {
			enabled[notification.ProviderDesktop] = cfg.DesktopEnabled
			enabled[notification.ProviderTelegram] = cfg.TelegramEnabled
		}
	}
	status := notification.StatusSnapshot{}
	if api.Notifications != nil {
		status = api.Notifications.Status(enabled)
	}
	writeJSON(w, status)
}

func (api API) handleNetworkInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	interfaces, err := mcpnetwork.Discover()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, interfaces)
}

func (api API) handleConfig(w http.ResponseWriter, r *http.Request) {
	if api.Config == nil {
		http.Error(w, "config unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, publicConfigView(api.Config.Snapshot()))
	case http.MethodPut:
		var patch configPatch
		if err := decodeJSONBody(w, r, &patch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status := http.StatusInternalServerError
		previous := api.Config.Snapshot()
		next := previous
		var err error
		if patch.Server != nil {
			if patch.Server.Enabled != nil {
				next.Server.Enabled = *patch.Server.Enabled
			}
			if patch.Server.Port != nil {
				next.Server.Port = *patch.Server.Port
			}
			if patch.Server.Expose != nil {
				next.Server.Expose = *patch.Server.Expose
			}
			if patch.Server.AllowInsecureHTTP != nil {
				next.Server.AllowInsecureHTTP = *patch.Server.AllowInsecureHTTP
			}
			if patch.Server.AllowUnauthenticatedLoopback != nil {
				next.Server.AllowUnauthenticatedLoopback = *patch.Server.AllowUnauthenticatedLoopback
			}
		}
		if patch.Admin != nil {
			next.Admin = *patch.Admin
		}
		if patch.Auth != nil {
			if patch.Auth.MCPEnabled != nil {
				next.Auth.MCPEnabled = *patch.Auth.MCPEnabled
			}
			if patch.Auth.AdminEnabled != nil {
				next.Auth.AdminEnabled = *patch.Auth.AdminEnabled
			}
		}

		if patch.Permissions != nil {
			var allowDirs []string
			allowDirs, err = config.NormalizeAllowDirs(patch.Permissions.AllowDirs)
			if err == nil {
				next.Permissions.AllowDirs = allowDirs
			}
		}
		if err == nil && patch.Shell != nil {
			if patch.Shell.Path != nil {
				next.Shell.Path, err = config.NormalizeShellPath(patch.Shell.Path)
			}
		}
		if err == nil && patch.Integrations != nil {
			if patch.Integrations.Ponytail != nil && patch.Integrations.Ponytail.Active != nil {
				next.Integrations.Ponytail.Active = *patch.Integrations.Ponytail.Active
			}
			if patch.Integrations.Ponytail != nil && patch.Integrations.Ponytail.Mode != nil {
				next.Integrations.Ponytail.Mode = strings.ToLower(strings.TrimSpace(*patch.Integrations.Ponytail.Mode))
			}
			if patch.Integrations.Caveman != nil && patch.Integrations.Caveman.Active != nil {
				next.Integrations.Caveman.Active = *patch.Integrations.Caveman.Active
			}
			if patch.Integrations.Caveman != nil && patch.Integrations.Caveman.Mode != nil {
				next.Integrations.Caveman.Mode = strings.ToLower(strings.TrimSpace(*patch.Integrations.Caveman.Mode))
			}
			if patch.Integrations.RTK != nil {
				if patch.Integrations.RTK.Enabled != nil {
					next.Integrations.RTK.Enabled = *patch.Integrations.RTK.Enabled
				}
				if patch.Integrations.RTK.Path != nil {
					next.Integrations.RTK.Path = strings.TrimSpace(*patch.Integrations.RTK.Path)
				}
			}
			if patch.Integrations.CodeGraph != nil {
				if patch.Integrations.CodeGraph.Enabled != nil {
					next.Integrations.CodeGraph.Enabled = *patch.Integrations.CodeGraph.Enabled
				}
				if patch.Integrations.CodeGraph.Path != nil {
					next.Integrations.CodeGraph.Path = strings.TrimSpace(*patch.Integrations.CodeGraph.Path)
				}
			}
		}
		if err == nil {
			err = config.Validate(next)
		}
		if err != nil {
			status = http.StatusBadRequest
		}
		if err == nil {
			err = api.commitConfig(next, previous)
		}
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, publicConfigView(next))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if api.Tools == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, api.Tools.List())
}

func (api API) workspaceManager() *workspace.Manager {
	if api.Workspaces != nil {
		return api.Workspaces
	}
	if api.Tools != nil {
		return api.Tools.Workspaces
	}
	return nil
}

func (api API) upstreamManager() *upstream.Manager {
	if api.Tools != nil && api.Tools.Upstream != nil {
		return api.Tools.Upstream
	}
	return api.Upstream
}

func publicConfigView(cfg config.Config) publicConfig {
	return publicConfig{
		Server: cfg.Server, Admin: cfg.Admin, Permissions: cfg.Permissions, Shell: cfg.Shell, Integrations: cfg.Integrations,
		Auth: authSettings{
			MCPEnabled: cfg.Auth.MCPEnabled, AdminEnabled: cfg.Auth.AdminEnabled,
			MCPTokenConfigured: cfg.Auth.MCPTokenHash != "", AdminTokenConfigured: cfg.Auth.AdminTokenHash != "",
		},
	}
}

func (api API) commitConfig(next, previous config.Config) error {
	if api.ReloadConfig == nil {
		_, err := api.Config.Update(func(config.Config) (config.Config, error) {
			return next, api.persistConfigWithIntegrations(next, previous)
		})
		return err
	}
	if err := api.persistConfig(next); err != nil {
		return err
	}
	if err := api.ReloadConfig(next); err != nil {
		return errors.Join(err, api.persistConfig(previous))
	}
	return nil
}

func (api API) persistConfigWithIntegrations(next, previous config.Config) error {
	if err := api.persistConfig(next); err != nil {
		return err
	}
	if next.Integrations != previous.Integrations && api.Tools != nil {
		if err := api.Tools.SyncIntegrations(next.Integrations); err != nil {
			return errors.Join(err, api.persistConfig(previous))
		}
	}
	if api.Tools != nil {
		api.Tools.SetGlobalAllowDirs(next.Permissions.AllowDirs)
		api.Tools.SetShellPath(next.Shell.Path)
	}
	return nil
}

func (api API) persistConfig(value config.Config) error {
	if api.saveConfig != nil {
		return api.saveConfig(value)
	}
	return config.Save(value)
}

func method(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

func Handler() http.Handler { return New(API{}) }
