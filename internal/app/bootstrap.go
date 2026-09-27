package app

import (
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/mcp"
	"go.mewis.me/codemcp/internal/notification"
	"go.mewis.me/codemcp/internal/runtime/activity"
	"go.mewis.me/codemcp/internal/telegram"
	"go.mewis.me/codemcp/internal/telemetry"
	"go.mewis.me/codemcp/internal/tools"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func (a *App) Bootstrap() error {
	span := tracepkg.StartObserver(a.trace, "APP", "app.bootstrap", "Bootstrapping application runtime")
	didBootstrap := false
	a.bootstrap.Do(func() {
		didBootstrap = true
		if a.Config == nil {
			a.Config = config.NewRuntimeStore(config.Default())
		}
		if a.Tools == nil {
			cfg := a.Config.Snapshot()
			a.Tools = tools.NewRuntimeWithAccess(cfg.Integrations, cfg.Permissions.AllowDirs, func() (bool, int) {
				current := a.Config.Snapshot()
				return current.Admin.Enabled, current.Admin.Port
			})
		}
		configProvider := application.NewMCPConfigReadService()
		a.Tools.SetConfigReadProvider(configProvider)
		a.Tools.SetConfigSetApprovalProvider(configProvider)
		a.Tools.SetConfigSetApplyProvider(configProvider)
		a.Tools.SetInstructionAuthoringProvider(application.NewAgentInstructionAuthoringProvider(a.Tools.Workspaces, a.Tools.InstructionChanges))
		a.Tools.SetPromptProvider(application.NewAgentPromptProvider(a.Tools.Workspaces))
		if a.Activity == nil {
			a.Activity = activity.NewStream()
		}
		if a.Logger == nil {
			a.Logger = logger.New(logger.Info)
		}
		if a.Operations == nil {
			a.Operations = application.NewDispatcher()
		}
		a.Operations.SetObserver(application.ProductOperationObserver(a.ProductTelemetry))
		if err := application.BindStatusOperations(a.Operations, a.telegramStatusOverview); err != nil {
			a.bootstrapErr = err
			return
		}
		telemetry.AttachTools(a.Tools, a.Activity, a.Logger)
		telemetry.AttachApprovals(a.Tools.Approvals, a.Activity, a.Logger)
		telemetry.AttachBackground(a.Tools.Processes, a.Activity, a.Logger)
		if a.Notifications == nil {
			a.Notifications = notification.NewCoordinator(notification.CoordinatorOptions{})
			a.Notifications.Register(notification.NewDesktopProvider())
		}
		if a.Telegram != nil {
			a.Notifications.Register(notification.NewTelegramProvider(a.Telegram))
			if a.TelegramPairing == nil {
				a.TelegramPairing = telegram.NewPairingStore(config.RootPath())
			}
			a.Telegram.SetSetupHandler(a.handleTelegramPairingUpdate)
			telegramUI, err := telegram.NewInterface(telegram.InterfaceOptions{Runtime: a.Telegram, Dispatcher: a.Operations})
			if err != nil {
				a.bootstrapErr = err
				return
			}
			a.TelegramUI = telegramUI
			a.Telegram.SetHandler(telegramUI.Handle)
		}
		if a.ApprovalNotifications == nil && a.Tools.Approvals != nil {
			a.ApprovalNotifications = notification.NewApprovalBridge(a.Tools.Approvals.Events(), a.Notifications, notification.ApprovalBridgeOptions{
				Policy: func() notification.ApprovalPolicy {
					cfg := a.Config.Snapshot().Notifications.Approval
					return notification.ApprovalPolicy{
						Enabled: cfg.Enabled, Pending: cfg.Pending, Resolved: cfg.Resolved,
						Providers: map[string]bool{
							notification.ProviderDesktop:  cfg.DesktopEnabled,
							notification.ProviderTelegram: cfg.TelegramEnabled,
						},
					}
				},
			})
		}
		if a.CompletionNotifications == nil && a.Tools.CompletionHooks != nil {
			hook := notification.NewCompletionHook(a.Notifications, notification.CompletionHookOptions{
				Policy: func() notification.CompletionPolicy {
					cfg := a.Config.Snapshot().Notifications.Completion
					return notification.CompletionPolicy{
						Enabled: cfg.Enabled,
						Providers: map[string]bool{
							notification.ProviderDesktop:  cfg.DesktopEnabled,
							notification.ProviderTelegram: cfg.TelegramEnabled,
						},
					}
				},
			})
			if err := a.Tools.CompletionHooks.Register(hook); err != nil {
				if a.Logger != nil {
					a.Logger.Warning("NOTIFICATION", "notification.completion.register.failed", "Completion notification hook could not be registered", err)
				}
			} else {
				a.CompletionNotifications = hook
			}
		}
		if a.BackgroundNotifications == nil && a.Tools.Processes != nil {
			a.BackgroundNotifications = notification.NewBackgroundJobBridge(a.Tools.Processes, a.Notifications, notification.BackgroundJobBridgeOptions{
				Policy: func() notification.BackgroundJobPolicy {
					cfg := a.Config.Snapshot().Notifications.Completion
					return notification.BackgroundJobPolicy{
						Enabled: cfg.Enabled,
						Providers: map[string]bool{
							notification.ProviderDesktop:  cfg.DesktopEnabled,
							notification.ProviderTelegram: cfg.TelegramEnabled,
						},
					}
				},
			})
		}
		a.Upstream = a.Tools.Upstream
		doctorDeps := application.DoctorDependencies{
			Workspaces:           a.Tools.Workspaces,
			Checkpoints:          a.Tools.Checkpoints,
			Completions:          a.Tools.Completions,
			Approvals:            a.Tools.Approvals,
			BackgroundDeliveries: a.Tools.BackgroundDeliveries,
			Notifications:        a.Notifications,
			Upstream:             a.Upstream,
			OAuth:                a.OAuth,
			Tunnel:               a.Tunnel,
		}
		if a.Telegram != nil {
			doctorDeps.TelegramHealth = func() application.TelegramHealthSnapshot {
				health := a.Telegram.Health()
				return application.TelegramHealthSnapshot{
					Enabled: health.Enabled, TokenConfigured: health.TokenConfigured,
					AuthorizationConfigured: health.AuthorizationConfigured, Running: health.Running,
					PollingHealthy: health.PollingHealthy, Reconnecting: health.Reconnecting,
					ReconnectCount: health.ReconnectCount,
				}
			}
		}
		doctorService, err := application.NewDoctorService(doctorDeps)
		if err != nil {
			a.bootstrapErr = err
			return
		}
		if err := application.BindDoctorOperations(a.Operations, doctorService); err != nil {
			a.bootstrapErr = err
			return
		}
		a.syncMCPHTTP(a.Config.Snapshot().Server.Enabled)
		a.attachTunnelLifecycle()
	})
	if a.bootstrapErr != nil {
		span.FailMessage("Application runtime bootstrap failed", a.bootstrapErr)
		return a.bootstrapErr
	}
	span.EndMessage("Application runtime bootstrapped", tracepkg.Bool("performed", didBootstrap), tracepkg.Bool("mcp_http_enabled", a.MCP != nil), tracepkg.Bool("tunnel_configured", a.Tunnel != nil), tracepkg.Int("tool_count", len(a.Tools.List())))
	return nil
}

func (a *App) syncMCPHTTP(enabled bool) {
	if !enabled {
		if a.MCP != nil {
			a.MCP.CloseSubscriptions()
			a.MCP = nil
		}
		return
	}
	if a.MCP == nil {
		a.MCP = mcp.NewHTTPRuntimeWithTools(a.Tools)
	} else if a.MCP.Server == nil {
		a.MCP.Server = mcp.NewRuntimeWithTools(a.Tools)
	} else {
		a.MCP.Server.Tools = a.Tools
	}
	a.MCP.Activity = a.Activity
}
