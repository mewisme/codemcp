package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/tools"
)

func TestConfigGetKeepsManagedSecretsOutOfResultsAndActivity(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })

	cfg := config.Default()
	cfg.Tunnel.Enabled = false
	cfg.HTTP.MCP.Auth.TokenHash = "configured-mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "configured-admin-hash"
	cfg.Permissions.MCPConfigRead = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("activity-secret-", 64)
	if _, err := application.NewSettingService().Set(t.Context(), "tunnel.api_key", secret); err != nil {
		t.Fatal(err)
	}

	runtime := tools.NewRuntime()
	runtime.SetConfigReadProvider(application.NewMCPConfigReadService())
	observed := make([]tools.CallObservation, 0, 2)
	runtime.SetCallObserver(func(value tools.CallObservation) {
		observed = append(observed, value)
	})
	result, err := runtime.Call(t.Context(), mcpconfigwire.GetToolName, map[string]any{"key": "tunnel.api_key"})
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	projected, ok := result.StructuredContent.(mcpconfigwire.GetResult)
	if !ok || projected.Setting.Value != nil || !projected.Setting.Secret ||
		projected.Setting.Configured == nil || !*projected.Setting.Configured {
		t.Fatalf("structured projection=%#v", result.StructuredContent)
	}
	if len(observed) != 2 {
		t.Fatalf("observations=%d want=2", len(observed))
	}
	data, err := json.Marshal(struct {
		Result       tools.Result
		Observations []tools.CallObservation
	}{result, observed})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) {
		t.Fatalf("managed secret leaked into result/activity: %s", text)
	}
	if result.Meta != nil {
		t.Fatalf("config_get emitted unexpected metadata: %#v", result.Meta)
	}
}
