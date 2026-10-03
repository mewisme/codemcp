package app

import (
	"context"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/telegram"
)

func (a *App) telegramStatusOverview(ctx context.Context) (application.StatusOverview, error) {
	if a == nil || a.Config == nil {
		return application.StatusOverview{}, nil
	}
	cfg := a.Config.Snapshot()
	health := a.Telegram.Health()
	tunnelSnapshot := a.Tunnel.Snapshot()
	semanticHealth := a.Tools.Semantic.Health()
	typeSafeService := application.NewTypeSafeService()
	typeSafeService.LoadConfig = func() (config.Config, error) { return cfg, nil }
	typeSafeStatus, _ := typeSafeService.Status(ctx)
	return application.StatusOverview{
		RuntimeRunning:               a.running,
		MCPHTTPEnabled:               cfg.HTTP.MCP.Enabled,
		AdminEnabled:                 cfg.HTTP.Admin.Enabled,
		TunnelEnabled:                cfg.Tunnel.Enabled,
		TunnelConfigured:             tunnelSnapshot.Configured,
		TunnelRunning:                tunnelSnapshot.Status.Running,
		TunnelReady:                  tunnelSnapshot.Status.Ready,
		TelegramEnabled:              cfg.Telegram.Enabled,
		TelegramConfigured:           health.TokenConfigured && health.AuthorizationConfigured,
		TelegramRunning:              health.Running,
		TelegramHealthy:              health.Running && health.PollingHealthy && health.AuthorizationConfigured,
		TelegramTopicsEnabled:        cfg.Telegram.TopicsEnabled,
		TelegramTopicsSupported:      health.TopicsSupported,
		TelegramTopicsEffective:      health.TopicsEffective,
		TelegramTopicsStoreHealthy:   health.TopicStoreHealthy,
		TelegramTopicsRepairing:      health.TopicReconcilePending,
		TelegramTopicsError:          health.TopicLastError,
		LogsMiniAppEnabled:           cfg.Telegram.LogsMiniApp.Enabled,
		LogsMiniAppAvailable:         health.LogsMiniApp.DependencyAvailable,
		LogsMiniAppEffective:         health.LogsMiniApp.State == telegram.MiniAppReady,
		TypeSafeEnabled:              cfg.Integrations.TypeSafe.Enabled,
		TypeSafeConfigured:           typeSafeStatus.APIKeyConfigured,
		TypeSafeAvailable:            semanticHealth.Available && semanticHealth.Provider == typeSafeSemanticProvider,
		SemanticApprovalEnabled:      cfg.Approval.Semantic.Enabled,
		SemanticApprovalEffective:    cfg.Approval.Semantic.Enabled && semanticHealth.Available,
		ApprovalNotificationsReady:   a.approvalNotificationsReady.Load(),
		CompletionNotificationsReady: a.completionNotificationsReady.Load(),
	}, nil
}

func (a *App) StatusOverview(ctx context.Context) (application.StatusOverview, error) {
	return a.telegramStatusOverview(ctx)
}
