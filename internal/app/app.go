package app

import (
	"context"
	"net/http"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/explain"
	"go.mewis.me/codemcp/internal/interface/admin"
	"go.mewis.me/codemcp/internal/interface/web"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/mcp"
	"go.mewis.me/codemcp/internal/notification"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/runtime/activity"
	"go.mewis.me/codemcp/internal/telegram"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	"go.mewis.me/codemcp/internal/tools"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

type App struct {
	Config                    *config.RuntimeStore
	MCP                       *mcp.HTTPRuntime
	Upstream                  *upstream.Manager
	Tools                     *tools.Runtime
	Activity                  *activity.Stream
	Tunnel                    *tunnel.Client
	Logger                    *logger.Logger
	OAuth                     *mcpoauth.Store
	OAuthFlows                *mcpoauth.FlowManager
	Notifications             *notification.Coordinator
	ApprovalNotifications     *notification.ApprovalBridge
	Explain                   *explain.Service
	ApprovalExplain           *application.ApprovalExplainService
	CompletionNotifications   *notification.CompletionHook
	BackgroundNotifications   *notification.BackgroundJobBridge
	ProductTelemetry          productTelemetryRuntime
	ProductLifecycleTelemetry *productLifecycleTelemetry
	Operations                *application.Dispatcher
	CFTunnel                  *application.CFTunnelService
	Telegram                  *telegram.Runtime
	TelegramPairing           *telegram.PairingStore
	TelegramUI                *telegram.Interface
	typeSafeMu                sync.Mutex
	typeSafeFingerprint       string
	typeSafeHTTPClient        *http.Client
	typeSafeBaseURL           string
	runtimeCtx                context.Context
	trace                     tracepkg.Observer
	running                   bool
	bootstrap                 sync.Once
	bootstrapErr              error
}

type productTelemetryRuntime interface {
	Record(context.Context, producttelemetry.EventName, producttelemetry.Usage) bool
	SetEnabled(bool)
	Close(context.Context)
}

func New(cfg config.Config) (*App, error) {
	return NewWithLoggerContext(context.Background(), cfg, nil)
}

func NewWithLogger(cfg config.Config, appLogger *logger.Logger) (*App, error) {
	return NewWithLoggerContext(context.Background(), cfg, appLogger)
}

// NewWithLoggerContext constructs the runtime application. Shell-policy and Bootstrap
// failures are returned; tools.NewRuntimeWithAccess may still panic on registry/workspace
// bootstrap hard failures (crypto/rand-backed auth helpers similarly panic).
func NewWithLoggerContext(ctx context.Context, cfg config.Config, appLogger *logger.Logger) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	observer := tracepkg.ObserverFromContext(ctx)
	span := tracepkg.Start(ctx, "APP", "app.construct", "Constructing server runtime application", tracepkg.Bool("mcp_http_enabled", cfg.HTTP.MCP.Enabled), tracepkg.Bool("admin_enabled", cfg.HTTP.Admin.Enabled), tracepkg.Bool("tunnel_enabled", cfg.Tunnel.Enabled))
	stream := activity.NewStream()
	configStore := config.NewRuntimeStore(cfg)
	effectiveTelemetry := config.ResolveTelemetryEnabled(cfg, true)
	productRecorder, err := producttelemetry.NewRecorder(producttelemetry.RecorderOptions{
		Enabled:  effectiveTelemetry.Enabled,
		Endpoint: producttelemetry.Endpoint,
	})
	if err != nil {
		productRecorder = nil
	}
	toolSpan := tracepkg.Start(ctx, "APP", "app.tools.bootstrap", "Bootstrapping tool runtime")
	toolRuntime := tools.NewRuntimeWithAccess(cfg.Integrations, cfg.Permissions.AllowDirs, func() (bool, int) {
		current := configStore.Snapshot()
		return current.HTTP.Admin.Enabled, current.HTTP.Admin.Port
	})
	configProvider := application.NewMCPConfigReadService()
	toolRuntime.SetConfigReadProvider(configProvider)
	toolRuntime.SetConfigSetApprovalProvider(configProvider)
	toolRuntime.SetConfigSetApplyProvider(configProvider)
	toolRuntime.SetInstructionAuthoringProvider(application.NewAgentInstructionAuthoringProvider(toolRuntime.Workspaces, toolRuntime.InstructionChanges))
	toolRuntime.SetPromptProvider(application.NewAgentPromptProvider(toolRuntime.Workspaces))
	toolRuntime.SetCallObserver(productToolObserver(productRecorder))
	toolSpan.EndMessage("Tool runtime bootstrapped", tracepkg.Int("tool_count", len(toolRuntime.List())))
	if toolRuntime.Upstream != nil {
		toolRuntime.Upstream.SetTraceObserver(observer)
	}
	if toolRuntime.Semantic != nil {
		toolRuntime.Semantic.SetTraceObserver(observer)
	}
	workspaceSpan := tracepkg.Start(ctx, "APP", "app.workspaces.load", "Loading workspace registry")
	if workspaces, err := toolRuntime.Workspaces.List(); err != nil {
		workspaceSpan.FailMessage("Workspace registry load failed", err)
	} else {
		workspaceSpan.EndMessage("Workspace registry loaded", tracepkg.Int("workspace_count", len(workspaces)))
	}
	upstreamSpan := tracepkg.Start(ctx, "APP", "app.upstream.bootstrap", "Bootstrapping Upstream manager")
	upstreamCount := 0
	if toolRuntime.Upstream != nil {
		upstreamCount = len(toolRuntime.Upstream.List())
	}
	upstreamSpan.EndMessage("Upstream manager bootstrapped", tracepkg.Int("server_count", upstreamCount))
	toolRuntime.SetShellPath(cfg.Shell.Path)
	toolRuntime.SetSemanticApprovalPolicy(semanticApprovalPolicy(cfg.Approval.Semantic))
	var mcpRuntime *mcp.HTTPRuntime
	if cfg.HTTP.MCP.Enabled {
		mcpRuntime = mcp.NewHTTPRuntimeWithTools(toolRuntime)
		mcpRuntime.Activity = stream
	}
	oauthStore := mcpoauth.NewStore(mcpoauth.Path()).SetTraceObserver(observer)
	if appLogger == nil {
		appLogger = logger.New(logger.Info)
	}
	tunnelClient := tunnel.NewConfiguredWithLogger(cfg.Tunnel, toolRuntime, appLogger)
	seedSpan := tracepkg.Start(ctx, "APP", "app.tunnel.metadata-seed", "Seeding tunnel metadata cache", tracepkg.String("tunnel_id", cfg.Tunnel.ID))
	if metadata, err := config.LoadTunnelMetadata(cfg.Tunnel.ID); err == nil {
		if seedErr := tunnelClient.SeedMetadata(metadata); seedErr != nil {
			seedSpan.FailMessage("Tunnel metadata seed failed", seedErr)
		} else {
			seedSpan.EndMessage("Tunnel metadata cache seeded", tracepkg.Bool("seeded", true), tracepkg.String("tunnel_id", metadata.ID))
		}
	} else {
		seedSpan.EndMessage("Tunnel metadata cache unavailable", tracepkg.Bool("seeded", false), tracepkg.Bool("configured", cfg.Tunnel.ID != ""))
	}
	cfTunnel := application.NewCFTunnelService()
	app := &App{
		Config: configStore, MCP: mcpRuntime, Upstream: toolRuntime.Upstream, Tools: toolRuntime, Activity: stream,
		Tunnel: tunnelClient, Logger: appLogger,
		OAuth: oauthStore, OAuthFlows: mcpoauth.NewFlowManager(oauthStore), ProductTelemetry: productRecorder, trace: observer,
		Operations: application.NewDispatcher(), CFTunnel: cfTunnel,
		Telegram: telegram.NewRuntime(telegram.Options{Root: config.RootPath(), CFTunnelResolver: cfTunnel.ResolvePath}), TelegramPairing: telegram.NewPairingStore(config.RootPath()),
	}
	typeSafeCandidate, err := app.prepareTypeSafe(cfg)
	if err != nil {
		span.FailMessage("TypeSafe semantic runtime preparation failed", err)
		return nil, err
	}
	if err := app.commitTypeSafe(typeSafeCandidate); err != nil {
		span.FailMessage("TypeSafe semantic runtime configuration failed", err)
		return nil, err
	}
	app.ProductLifecycleTelemetry = newProductLifecycleTelemetry(productRecorder, toolRuntime.Approvals, toolRuntime.Processes)
	bootstrapStarted := time.Now()
	if err := app.Bootstrap(); err != nil {
		span.FailMessage("Server runtime application bootstrap failed", err, tracepkg.Int64("bootstrap_ms", time.Since(bootstrapStarted).Milliseconds()))
		return nil, err
	}
	span.EndMessage("Server runtime application constructed", tracepkg.Int("tool_count", len(app.Tools.List())), tracepkg.Int("upstream_count", upstreamCount), tracepkg.Int64("bootstrap_ms", time.Since(bootstrapStarted).Milliseconds()))
	return app, nil
}

func (a *App) MCPHandler() http.Handler {
	if a == nil || a.MCP == nil {
		return http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	mcpHandler := auth.DynamicHashedMiddleware(func() (bool, string) {
		cfg := a.Config.Snapshot()
		return cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.MCP.Auth.TokenHash
	}, a.MCP.Handler())
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}

func (a *App) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	cfg := a.Config.Snapshot()
	if !cfg.HTTP.Admin.Enabled {
		return http.NotFoundHandler()
	}
	adminAPI := admin.API{
		Upstream: a.Upstream, Tools: a.Tools, Tunnel: a.Tunnel, Config: a.Config, OAuth: a.OAuth, OAuthFlows: a.OAuthFlows, Operations: a.Operations,
		Approvals: a.Tools.Approvals, Executions: a.Tools.Executions, Notifications: a.Notifications, Activity: a.Activity,
	}
	adminAuth := func() (bool, string) {
		cfg := a.Config.Snapshot()
		return cfg.HTTP.Admin.Auth.Enabled, cfg.HTTP.Admin.Auth.TokenHash
	}
	adminHandler := auth.DynamicHashedMiddleware(adminAuth, admin.New(adminAPI))
	mux.Handle("/oauth/callback/", adminAPI.OAuthCallbackHandler())
	mux.Handle("/admin/", adminHandler)
	mux.Handle("/api/", adminHandler)
	mux.Handle("/api/activity/stream", auth.DynamicHashedMiddleware(adminAuth, activity.Handler(a.Activity)))
	mux.Handle("/api/activity/", auth.DynamicHashedMiddleware(adminAuth, activity.CallHandler(a.Activity)))
	mux.Handle("/", web.Handler())
	return web.SecurityHeaders(mux)
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", a.MCPHandler())
	mux.Handle("/mcp/", a.MCPHandler())
	mux.Handle("/health", a.MCPHandler())
	mux.Handle("/", a.AdminHandler())
	return mux
}
