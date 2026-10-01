package app

import (
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/telegram"
)

func TestBootstrapBindsTelegramNavigationToCanonicalStatusOperation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	cfg := config.Default()
	cfg.HTTP.MCP.Enabled = true
	cfg.HTTP.Admin.Enabled = true
	cfg.Tunnel.Enabled = true

	runtime := telegram.NewRuntime(telegram.Options{Root: root})
	value := &App{
		Config:   config.NewRuntimeStore(cfg),
		Telegram: runtime,
	}
	if err := value.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if value.Operations == nil || value.TelegramUI == nil {
		t.Fatalf("operations=%v telegram_ui=%v", value.Operations != nil, value.TelegramUI != nil)
	}
	result, err := value.Operations.Dispatch(
		application.WithOperationInterface(t.Context(), application.OperationInterfaceTelegram),
		application.DispatchRequest{Operation: capability.StatusOverview},
	)
	if err != nil {
		t.Fatal(err)
	}
	status, ok := result.Value.(application.StatusOverview)
	if !ok {
		t.Fatalf("status value=%T", result.Value)
	}
	if result.Metadata.ID != capability.StatusOverview || !status.MCPHTTPEnabled || !status.AdminEnabled || !status.TunnelEnabled {
		t.Fatalf("result=%#v status=%#v", result, status)
	}
}
