package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/capability"
)

type StatusOverview struct {
	RuntimeRunning  bool `json:"runtime_running"`
	MCPHTTPEnabled  bool `json:"mcp_http_enabled"`
	AdminEnabled    bool `json:"admin_enabled"`
	TunnelEnabled   bool `json:"tunnel_enabled"`
	TelegramEnabled bool `json:"telegram_enabled"`
	TelegramRunning bool `json:"telegram_running"`
	TelegramHealthy bool `json:"telegram_healthy"`
}

type StatusOverviewProvider func(context.Context) (StatusOverview, error)

func BindStatusOperations(dispatcher *Dispatcher, provider StatusOverviewProvider) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if provider == nil {
		return errors.New("status overview provider is nil")
	}
	return dispatcher.Register(capability.StatusOverview, func(ctx context.Context, _ any) (any, error) {
		return provider(ctx)
	})
}
