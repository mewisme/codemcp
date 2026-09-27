package app

import (
	"context"

	"go.mewis.me/codemcp/internal/application"
)

func (a *App) telegramStatusOverview(context.Context) (application.StatusOverview, error) {
	if a == nil || a.Config == nil {
		return application.StatusOverview{}, nil
	}
	cfg := a.Config.Snapshot()
	health := a.Telegram.Health()
	return application.StatusOverview{
		RuntimeRunning:  a.running,
		MCPHTTPEnabled:  cfg.Server.Enabled,
		AdminEnabled:    cfg.Admin.Enabled,
		TunnelEnabled:   cfg.Tunnel.Enabled,
		TelegramEnabled: cfg.Telegram.Enabled,
		TelegramRunning: health.Running,
		TelegramHealthy: health.Running && health.PollingHealthy && health.AuthorizationConfigured,
	}, nil
}
