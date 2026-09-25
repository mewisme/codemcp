package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/mcp"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	"go.mewis.me/codemcp/internal/tools"
)

func TestConfigReadHTTPNeverReturnsManagedSecretsOrUnsafeURLValues(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })

	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "configured-mcp-hash"
	cfg.Auth.AdminTokenHash = "configured-admin-hash"
	cfg.Permissions.MCPConfigRead = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	shortSecret := "short-http-secret"
	longSecret := strings.Repeat("long-http-secret-", 128)
	credentialURL := "https://http-user:http-secret@example.invalid/control"
	settings := application.NewSettingService()
	for _, item := range []struct {
		key   string
		value string
	}{
		{"tunnel.api_key", shortSecret},
		{"tunnel.admin.key", longSecret},
		{"tunnel.control_plane_base_url", credentialURL},
	} {
		if _, err := settings.Set(t.Context(), item.key, item.value); err != nil {
			t.Fatalf("set %s: %v", item.key, err)
		}
	}

	runtime := tools.NewRuntime()
	runtime.SetConfigReadProvider(application.NewMCPConfigReadService())
	handler, err := mcp.NewSDKHTTPHandler(runtime, "", false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "config-security-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	results := make([]*sdkmcp.CallToolResult, 0, 3)
	for _, key := range []string{"tunnel.api_key", "tunnel.admin.key"} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: mcpconfigwire.GetToolName, Arguments: map[string]any{"key": key}})
		if err != nil || result.IsError {
			t.Fatalf("config_get %s result=%#v err=%v", key, result, err)
		}
		results = append(results, result)
	}
	listed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: mcpconfigwire.ListToolName, Arguments: map[string]any{"prefix": "tunnel"}})
	if err != nil || listed.IsError {
		t.Fatalf("config_list result=%#v err=%v", listed, err)
	}
	results = append(results, listed)

	data, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{shortSecret, longSecret, credentialURL, "http-user:http-secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("HTTP config read leaked %q: %s", forbidden, text)
		}
	}
	if strings.Contains(text, "tunnel.control_plane_base_url") {
		t.Fatalf("unsafe URL field was exposed over HTTP: %s", text)
	}
}
