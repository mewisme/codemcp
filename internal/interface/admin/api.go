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

type publicHTTPAuth struct {
	Enabled         bool `json:"enabled"`
	TokenConfigured bool `json:"token_configured"`
}

type publicMCPHTTPAuth struct {
	publicHTTPAuth
	LegacyBearer bool `json:"legacy_bearer"`
}

type publicMCPHTTPConfig struct {
	Enabled bool              `json:"enabled"`
	Port    int               `json:"port"`
	Auth    publicMCPHTTPAuth `json:"auth"`
}

type publicAdminHTTPConfig struct {
	Enabled bool           `json:"enabled"`
	Port    int            `json:"port"`
	Auth    publicHTTPAuth `json:"auth"`
}

type publicHTTPConfig struct {
	Exposure config.ExposureConfig     `json:"exposure"`
	Security config.HTTPSecurityConfig `json:"security"`
	MCP      publicMCPHTTPConfig       `json:"mcp"`
	Admin    publicAdminHTTPConfig     `json:"admin"`
}

type publicConfig struct {
	HTTP         publicHTTPConfig          `json:"http"`
	Permissions  config.PermissionsConfig  `json:"permissions"`
	Shell        config.ShellConfig        `json:"shell"`
	Integrations config.IntegrationsConfig `json:"integrations"`
}

type configPatch struct {
	HTTP         *httpPatch                `json:"http,omitempty"`
	Permissions  *config.PermissionsConfig `json:"permissions,omitempty"`
	Shell        *config.ShellConfig       `json:"shell,omitempty"`
	Integrations *integrationPatch         `json:"integrations,omitempty"`
}

type httpPatch struct {
	Exposure *config.ExposureConfig `json:"exposure,omitempty"`
	Security *httpSecurityPatch     `json:"security,omitempty"`
	MCP      *httpMCPPatch          `json:"mcp,omitempty"`
	Admin    *httpAdminPatch        `json:"admin,omitempty"`
}

type httpSecurityPatch struct {
	AllowInsecure                *bool `json:"allow_insecure,omitempty"`
	AllowUnauthenticatedLoopback *bool `json:"allow_unauthenticated_loopback,omitempty"`
}

type httpMCPPatch struct {
	Enabled *bool             `json:"enabled,omitempty"`
	Port    *int              `json:"port,omitempty"`
	Auth    *httpMCPAuthPatch `json:"auth,omitempty"`
}

type httpMCPAuthPatch struct {
	Enabled      *bool `json:"enabled,omitempty"`
	LegacyBearer *bool `json:"legacy_bearer,omitempty"`
}

type httpAdminPatch struct {
	Enabled *bool          `json:"enabled,omitempty"`
	Port    *int           `json:"port,omitempty"`
	Auth    *httpAuthPatch `json:"auth,omitempty"`
}

type httpAuthPatch struct {
	Enabled *bool `json:"enabled,omitempty"`
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
		authEnabled := api.Config != nil && api.Config.Snapshot().HTTP.Admin.Auth.Enabled
		writeJSON(w, map[string]bool{"ok": true, "auth_enabled": authEnabled})
	}))
	mux.HandleFunc("/api/status", method(http.MethodGet, api.handleStatus))
	mux.HandleFunc("/api/doctor", method(http.MethodGet, api.handleDoctor))
	mux.HandleFunc("/api/about", method(http.MethodGet, api.handleAbout))
	mux.HandleFunc("/api/runtime/up", api.handleRuntime)
	mux.HandleFunc("/api/runtime/down", api.handleRuntime)
	mux.HandleFunc("/api/runtime/restart", api.handleRuntime)
	mux.HandleFunc("/api/logs", api.handleLogs)
	mux.HandleFunc("/api/logs/follow", api.handleLogsFollow)
	mux.HandleFunc("/api/logs/info", method(http.MethodGet, api.handleLogsInfo))
	mux.HandleFunc("/api/install", api.handleInstall)
	mux.HandleFunc("/api/update", api.handleUpdate)
	mux.HandleFunc("/api/telemetry", api.handleTelemetry)
	mux.HandleFunc("/api/telemetry/", api.handleTelemetry)
	mux.HandleFunc("/api/network/interfaces", api.handleNetworkInterfaces)

	mux.HandleFunc("/api/config", api.handleConfig)
	mux.HandleFunc("/api/config/path", method(http.MethodGet, api.handleConfigPath))
	mux.HandleFunc("/api/config/verify", method(http.MethodGet, api.handleConfigVerify))
	mux.HandleFunc("/api/settings", api.handleSettings)
	mux.HandleFunc("/api/settings/", api.handleSettings)
	mux.HandleFunc("/api/auth", api.handleAuth)
	mux.HandleFunc("/api/auth/", api.handleAuth)
	mux.HandleFunc("/api/telegram/setup", api.handleTelegramSetup)
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
	mux.HandleFunc("/api/llm/status", api.handleLLMStatus)
	mux.HandleFunc("/api/llm/providers", api.handleLLMProviders)
	mux.HandleFunc("/api/llm/providers/", api.handleLLMProvider)
	mux.HandleFunc("/api/completions", api.handleCompletions)
	mux.HandleFunc("/api/completions/doctor", api.handleCompletionDoctor)
	mux.HandleFunc("/api/completions/", api.handleCompletion)
	mux.HandleFunc("/api/notifications", api.handleNotifications)
	mux.HandleFunc("/api/background/diagnostics", api.handleBackgroundDiagnostics)
	mux.HandleFunc("/api/upstream", api.handleUpstreams)
	mux.HandleFunc("/api/upstream/", api.handleUpstream)
	mux.HandleFunc("/api/tunnel/config", api.handleTunnelConfig)
	mux.HandleFunc("/api/tunnel/sync", api.handleTunnelSync)
	mux.HandleFunc("/api/tunnel/runtime/key", api.handleTunnelRuntimeKey)
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
		if patch.HTTP != nil {
			if patch.HTTP.MCP != nil && patch.HTTP.MCP.Enabled != nil {
				changes = append(changes, application.SettingChange{Key: "http.mcp.enabled", Value: strconv.FormatBool(*patch.HTTP.MCP.Enabled)})
			}
			if patch.HTTP.MCP != nil && patch.HTTP.MCP.Port != nil {
				changes = append(changes, application.SettingChange{Key: "http.mcp.port", Value: strconv.Itoa(*patch.HTTP.MCP.Port)})
			}
			if patch.HTTP.MCP != nil && patch.HTTP.MCP.Auth != nil && patch.HTTP.MCP.Auth.Enabled != nil {
				changes = append(changes, application.SettingChange{Key: "http.mcp.auth.enabled", Value: strconv.FormatBool(*patch.HTTP.MCP.Auth.Enabled)})
			}
			if patch.HTTP.MCP != nil && patch.HTTP.MCP.Auth != nil && patch.HTTP.MCP.Auth.LegacyBearer != nil {
				changes = append(changes, application.SettingChange{Key: "http.mcp.auth.legacy_bearer", Value: strconv.FormatBool(*patch.HTTP.MCP.Auth.LegacyBearer)})
			}
			if patch.HTTP.Exposure != nil {
				changes = append(changes, application.SettingChange{Key: "http.exposure.mode", Value: string(patch.HTTP.Exposure.Mode)})
				changes = append(changes, application.SettingChange{Key: "http.exposure.interfaces", Value: strings.Join(patch.HTTP.Exposure.Interfaces, ",")})
			}
			if patch.HTTP.Security != nil && patch.HTTP.Security.AllowInsecure != nil {
				changes = append(changes, application.SettingChange{Key: "http.security.allow_insecure", Value: strconv.FormatBool(*patch.HTTP.Security.AllowInsecure)})
			}
			if patch.HTTP.Security != nil && patch.HTTP.Security.AllowUnauthenticatedLoopback != nil {
				changes = append(changes, application.SettingChange{Key: "http.security.allow_unauthenticated_loopback", Value: strconv.FormatBool(*patch.HTTP.Security.AllowUnauthenticatedLoopback)})
			}
			if patch.HTTP.Admin != nil && patch.HTTP.Admin.Enabled != nil {
				changes = append(changes, application.SettingChange{Key: "http.admin.enabled", Value: strconv.FormatBool(*patch.HTTP.Admin.Enabled)})
			}
			if patch.HTTP.Admin != nil && patch.HTTP.Admin.Port != nil {
				changes = append(changes, application.SettingChange{Key: "http.admin.port", Value: strconv.Itoa(*patch.HTTP.Admin.Port)})
			}
			if patch.HTTP.Admin != nil && patch.HTTP.Admin.Auth != nil && patch.HTTP.Admin.Auth.Enabled != nil {
				changes = append(changes, application.SettingChange{Key: "http.admin.auth.enabled", Value: strconv.FormatBool(*patch.HTTP.Admin.Auth.Enabled)})
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
		HTTP: publicHTTPConfig{
			Exposure: cfg.HTTP.Exposure,
			Security: cfg.HTTP.Security,
			MCP: publicMCPHTTPConfig{Enabled: cfg.HTTP.MCP.Enabled, Port: cfg.HTTP.MCP.Port, Auth: publicMCPHTTPAuth{
				publicHTTPAuth: publicHTTPAuth{Enabled: cfg.HTTP.MCP.Auth.Enabled, TokenConfigured: cfg.HTTP.MCP.Auth.TokenHash != ""},
				LegacyBearer:   cfg.HTTP.MCP.Auth.LegacyBearer,
			}},
			Admin: publicAdminHTTPConfig{Enabled: cfg.HTTP.Admin.Enabled, Port: cfg.HTTP.Admin.Port, Auth: publicHTTPAuth{
				Enabled: cfg.HTTP.Admin.Auth.Enabled, TokenConfigured: cfg.HTTP.Admin.Auth.TokenHash != "",
			}},
		},
		Permissions: cfg.Permissions, Shell: cfg.Shell, Integrations: cfg.Integrations,
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
