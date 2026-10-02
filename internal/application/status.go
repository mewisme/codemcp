package application

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/capability"
)

type StatusOverview struct {
	RuntimeRunning             bool   `json:"runtime_running"`
	MCPHTTPEnabled             bool   `json:"mcp_http_enabled"`
	AdminEnabled               bool   `json:"admin_enabled"`
	TunnelEnabled              bool   `json:"tunnel_enabled"`
	TunnelConfigured           bool   `json:"tunnel_configured"`
	TunnelRunning              bool   `json:"tunnel_running"`
	TunnelReady                bool   `json:"tunnel_ready"`
	TelegramEnabled            bool   `json:"telegram_enabled"`
	TelegramConfigured         bool   `json:"telegram_configured"`
	TelegramRunning            bool   `json:"telegram_running"`
	TelegramHealthy            bool   `json:"telegram_healthy"`
	TelegramTopicsEnabled      bool   `json:"telegram_topics_enabled"`
	TelegramTopicsSupported    bool   `json:"telegram_topics_supported"`
	TelegramTopicsEffective    bool   `json:"telegram_topics_effective"`
	TelegramTopicsStoreHealthy bool   `json:"telegram_topics_store_healthy"`
	TelegramTopicsRepairing    bool   `json:"telegram_topics_repairing"`
	TelegramTopicsError        string `json:"telegram_topics_error,omitempty"`
	LogsMiniAppEnabled         bool   `json:"logs_mini_app_enabled"`
	LogsMiniAppAvailable       bool   `json:"logs_mini_app_available"`
	LogsMiniAppEffective       bool   `json:"logs_mini_app_effective"`
	TypeSafeEnabled            bool   `json:"typesafe_enabled"`
	TypeSafeConfigured         bool   `json:"typesafe_configured"`
	TypeSafeAvailable          bool   `json:"typesafe_available"`
	SemanticApprovalEnabled    bool   `json:"semantic_approval_enabled"`
	SemanticApprovalEffective  bool   `json:"semantic_approval_effective"`
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
