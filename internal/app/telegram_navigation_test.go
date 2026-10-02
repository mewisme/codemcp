package app

import (
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
)

func TestBootstrapBindsTelegramNavigationToCanonicalStatusOperation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	cfg := config.Default()
	cfg.HTTP.MCP.Enabled = true
	cfg.HTTP.Admin.Enabled = true
	cfg.Tunnel.Enabled = true

	value, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if value.Notifications != nil {
			value.Notifications.Stop()
		}
	})
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
	if status.TunnelConfigured || status.TunnelRunning || status.TunnelReady {
		t.Fatalf("default tunnel intent was reported as effective: %#v", status)
	}
	if !status.TelegramEnabled || status.TelegramConfigured || status.TelegramRunning || status.TelegramHealthy {
		t.Fatalf("default Telegram intent did not remain unconfigured: %#v", status)
	}
	if !status.TelegramTopicsEnabled || status.TelegramTopicsSupported || status.TelegramTopicsEffective {
		t.Fatalf("default Telegram topics state=%#v", status)
	}
	if !status.LogsMiniAppEnabled || status.LogsMiniAppEffective {
		t.Fatalf("default Logs Mini App state=%#v", status)
	}
	if !status.TypeSafeEnabled || status.TypeSafeConfigured || status.TypeSafeAvailable {
		t.Fatalf("default TypeSafe state=%#v", status)
	}
	if !status.SemanticApprovalEnabled || status.SemanticApprovalEffective {
		t.Fatalf("default semantic approval state=%#v", status)
	}
}
