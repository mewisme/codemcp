package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/config"
	mcpnetwork "go.mewis.me/codemcp/internal/network"
	"go.mewis.me/codemcp/internal/notification"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/runtime/activity"
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
	Activity      *activity.Stream
	OAuth         *mcpoauth.Store
	OAuthFlows    *mcpoauth.FlowManager
	TypeSafe      *application.TypeSafeService
	Operations    *application.Dispatcher
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
	TypeSafe  *typeSafePatch              `json:"typesafe,omitempty"`
}

type integrationStatePatch struct {
	Active *bool   `json:"active,omitempty"`
	Mode   *string `json:"mode,omitempty"`
}

type integrationExecutablePatch struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Path    *string `json:"path,omitempty"`
}

type typeSafePatch struct {
	Enabled   *bool   `json:"enabled,omitempty"`
	Model     *string `json:"model,omitempty"`
	TimeoutMS *int    `json:"timeout_ms,omitempty"`
}

func New(api API) http.Handler {
	api = api.withOAuth()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", method(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		authEnabled := api.Config != nil && api.Config.Snapshot().Auth.AdminEnabled
		writeJSON(w, map[string]bool{"ok": true, "auth_enabled": authEnabled})
	}))
	mux.HandleFunc("/api/status", method(http.MethodGet, api.handleStatus))
	mux.HandleFunc("/api/doctor", method(http.MethodGet, api.handleDoctor))
	mux.HandleFunc("/api/about", method(http.MethodGet, api.handleAbout))
	mux.HandleFunc("/api/runtime/up", api.handleRuntime)
	mux.HandleFunc("/api/runtime/down", api.handleRuntime)
	mux.HandleFunc("/api/runtime/restart", api.handleRuntime)
	mux.HandleFunc("/api/logs", api.handleLogs)
	mux.HandleFunc("/api/logs/info", method(http.MethodGet, api.handleLogsInfo))
	mux.HandleFunc("/api/install", api.handleInstall)
	mux.HandleFunc("/api/update", api.handleUpdate)
	mux.HandleFunc("/api/telemetry", api.handleTelemetry)
	mux.HandleFunc("/api/telemetry/", api.handleTelemetry)
	mux.HandleFunc("/api/network/interfaces", api.handleNetworkInterfaces)

	mux.HandleFunc("/api/config", api.handleConfig)
	mux.HandleFunc("/api/config/path", method(http.MethodGet, api.handleConfigPath))
	mux.HandleFunc("/api/config/verify", method(http.MethodGet, api.handleConfigVerify))
	mux.HandleFunc("/api/integrations/typesafe", api.handleTypeSafeStatus)
	mux.HandleFunc("/api/integrations/typesafe/doctor", api.handleTypeSafeDoctor)
	mux.HandleFunc("/api/integrations/typesafe/probe", api.handleTypeSafeProbe)
	mux.HandleFunc("/api/integrations/typesafe/enable", api.handleTypeSafeToggle)
	mux.HandleFunc("/api/integrations/typesafe/disable", api.handleTypeSafeToggle)
	mux.HandleFunc("/api/integrations/rtk", api.handleRTK)
	mux.HandleFunc("/api/integrations/rtk/", api.handleRTK)
	mux.HandleFunc("/api/integrations/codegraph", api.handleCodeGraph)
	mux.HandleFunc("/api/integrations/codegraph/", api.handleCodeGraph)
	mux.HandleFunc("/api/integrations/cf", api.handleCFIntegration)
	mux.HandleFunc("/api/integrations/cf/", api.handleCFIntegration)
	mux.HandleFunc("/api/instructions/global", api.handleGlobalInstructions)
	mux.HandleFunc("/api/prompts", api.handlePrompts)
	mux.HandleFunc("/api/prompts/{name}", api.handlePrompt)
	mux.HandleFunc("/api/workspaces", api.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", api.handleWorkspace)
	mux.HandleFunc("/api/workspace-containers", api.handleWorkspaceContainers)
	mux.HandleFunc("/api/workspace-containers/", api.handleWorkspaceContainer)
	mux.HandleFunc("/api/tools", api.handleTools)
	mux.HandleFunc("/api/requests", api.handleRequests)
	mux.HandleFunc("/api/requests/", api.handleRequest)
	mux.HandleFunc("/api/completions", api.handleCompletions)
	mux.HandleFunc("/api/completions/", api.handleCompletion)
	mux.HandleFunc("/api/notifications", api.handleNotifications)
	mux.HandleFunc("/api/background/diagnostics", api.handleBackgroundDiagnostics)
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

type backgroundDiagnosticsResponse struct {
	Processes              shellruntime.ProcessDiagnostics   `json:"processes"`
	Executions             shellruntime.ExecutionDiagnostics `json:"executions"`
	Deliveries             backgrounddelivery.Diagnostics    `json:"deliveries"`
	ActivityLatestSequence uint64                            `json:"activity_latest_sequence"`
}

func (api API) handleBackgroundDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if api.Tools == nil {
		http.Error(w, "tool runtime unavailable", http.StatusServiceUnavailable)
		return
	}
	response := backgroundDiagnosticsResponse{}
	if api.Tools.Processes != nil {
		response.Processes = api.Tools.Processes.Diagnostics()
	}
	if api.Tools.Executions != nil {
		response.Executions = api.Tools.Executions.Diagnostics()
	}
	if api.Tools.BackgroundDeliveries != nil {
		response.Deliveries = api.Tools.BackgroundDeliveries.Diagnostics()
	}
	if api.Activity != nil {
		response.ActivityLatestSequence = api.Activity.LatestSequence()
	}
	writeJSON(w, response)
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
		cfg := api.Config.Snapshot().Notifications
		if cfg.Approval.Enabled {
			enabled[notification.ProviderDesktop] = cfg.Approval.DesktopEnabled
			enabled[notification.ProviderTelegram] = cfg.Approval.TelegramEnabled
		}
		if cfg.Completion.Enabled {
			enabled[notification.ProviderDesktop] = enabled[notification.ProviderDesktop] || cfg.Completion.DesktopEnabled
			enabled[notification.ProviderTelegram] = enabled[notification.ProviderTelegram] || cfg.Completion.TelegramEnabled
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
		changes := make([]application.SettingChange, 0, 16)
		if patch.Server != nil {
			if patch.Server.Enabled != nil {
				changes = append(changes, application.SettingChange{Key: "server.enabled", Value: strconv.FormatBool(*patch.Server.Enabled)})
			}
			if patch.Server.Port != nil {
				changes = append(changes, application.SettingChange{Key: "server.port", Value: strconv.Itoa(*patch.Server.Port)})
			}
			if patch.Server.AllowInsecureHTTP != nil {
				changes = append(changes, application.SettingChange{Key: "server.allow_insecure_http", Value: strconv.FormatBool(*patch.Server.AllowInsecureHTTP)})
			}
			if patch.Server.AllowUnauthenticatedLoopback != nil {
				changes = append(changes, application.SettingChange{Key: "server.allow_unauthenticated_loopback", Value: strconv.FormatBool(*patch.Server.AllowUnauthenticatedLoopback)})
			}
			if patch.Server.Expose != nil {
				changes = append(changes, application.SettingChange{Key: "server.expose.mode", Value: string(patch.Server.Expose.Mode)})
				changes = append(changes, application.SettingChange{Key: "server.expose.interfaces", Value: strings.Join(patch.Server.Expose.Interfaces, ",")})
			}
		}
		if patch.Admin != nil {
			changes = append(changes,
				application.SettingChange{Key: "admin.enabled", Value: strconv.FormatBool(patch.Admin.Enabled)},
				application.SettingChange{Key: "admin.port", Value: strconv.Itoa(patch.Admin.Port)},
			)
		}
		if patch.Auth != nil {
			if patch.Auth.MCPEnabled != nil {
				changes = append(changes, application.SettingChange{Key: "auth.mcp_enabled", Value: strconv.FormatBool(*patch.Auth.MCPEnabled)})
			}
			if patch.Auth.AdminEnabled != nil {
				changes = append(changes, application.SettingChange{Key: "auth.admin_enabled", Value: strconv.FormatBool(*patch.Auth.AdminEnabled)})
			}
		}
		if patch.Permissions != nil {
			changes = append(changes, application.SettingChange{Key: "permissions.allow_dirs", Value: strings.Join(patch.Permissions.AllowDirs, ",")})
		}
		if patch.Shell != nil {
			if patch.Shell.Path != nil {
				changes = append(changes, application.SettingChange{Key: "shell.path", Value: strings.Join(patch.Shell.Path, ",")})
			}
		}
		if patch.Integrations != nil {
			if patch.Integrations.Ponytail != nil && patch.Integrations.Ponytail.Active != nil {
				changes = append(changes, application.SettingChange{Key: "integrations.ponytail.active", Value: strconv.FormatBool(*patch.Integrations.Ponytail.Active)})
			}
			if patch.Integrations.Ponytail != nil && patch.Integrations.Ponytail.Mode != nil {
				changes = append(changes, application.SettingChange{Key: "integrations.ponytail.mode", Value: *patch.Integrations.Ponytail.Mode})
			}
			if patch.Integrations.Caveman != nil && patch.Integrations.Caveman.Active != nil {
				changes = append(changes, application.SettingChange{Key: "integrations.caveman.active", Value: strconv.FormatBool(*patch.Integrations.Caveman.Active)})
			}
			if patch.Integrations.Caveman != nil && patch.Integrations.Caveman.Mode != nil {
				changes = append(changes, application.SettingChange{Key: "integrations.caveman.mode", Value: *patch.Integrations.Caveman.Mode})
			}
			if patch.Integrations.RTK != nil {
				if patch.Integrations.RTK.Enabled != nil {
					changes = append(changes, application.SettingChange{Key: "integrations.rtk.enabled", Value: strconv.FormatBool(*patch.Integrations.RTK.Enabled)})
				}
				if patch.Integrations.RTK.Path != nil {
					changes = append(changes, application.SettingChange{Key: "integrations.rtk.path", Value: *patch.Integrations.RTK.Path})
				}
			}
			if patch.Integrations.CodeGraph != nil {
				if patch.Integrations.CodeGraph.Enabled != nil {
					changes = append(changes, application.SettingChange{Key: "integrations.codegraph.enabled", Value: strconv.FormatBool(*patch.Integrations.CodeGraph.Enabled)})
				}
				if patch.Integrations.CodeGraph.Path != nil {
					changes = append(changes, application.SettingChange{Key: "integrations.codegraph.path", Value: *patch.Integrations.CodeGraph.Path})
				}
			}
			if patch.Integrations.TypeSafe != nil {
				if patch.Integrations.TypeSafe.Enabled != nil {
					changes = append(changes, application.SettingChange{Key: "integrations.typesafe.enabled", Value: strconv.FormatBool(*patch.Integrations.TypeSafe.Enabled)})
				}
				if patch.Integrations.TypeSafe.Model != nil {
					changes = append(changes, application.SettingChange{Key: "integrations.typesafe.model", Value: *patch.Integrations.TypeSafe.Model})
				}
				if patch.Integrations.TypeSafe.TimeoutMS != nil {
					changes = append(changes, application.SettingChange{Key: "integrations.typesafe.timeout_ms", Value: strconv.Itoa(*patch.Integrations.TypeSafe.TimeoutMS)})
				}
			}
		}
		applied, err := application.NewSettingService().Apply(r.Context(), changes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, publicConfigView(applied.Config))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) typeSafeService() *application.TypeSafeService {
	base := api.TypeSafe
	if base == nil {
		base = application.NewTypeSafeService()
	}
	service := *base
	if api.Config != nil {
		service.LoadConfig = func() (config.Config, error) { return api.Config.Snapshot(), nil }
	}
	return &service
}

func (api API) handleTypeSafeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	status, err := api.typeSafeService().Status(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, status)
}

func (api API) handleTypeSafeDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	probe := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("probe")), "true")
	result, err := api.typeSafeService().Doctor(r.Context(), probe)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, result)
}

func (api API) handleTypeSafeProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := api.typeSafeService().Probe(r.Context())
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, result)
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

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
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
