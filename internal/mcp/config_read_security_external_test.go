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
	"go.mewis.me/codemcp/internal/approval"
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
	cfg.HTTP.MCP.Auth.TokenHash = "configured-mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "configured-admin-hash"
	if !cfg.Permissions.MCPConfigRead {
		t.Fatal("fresh config must enable bounded MCP config reads")
	}
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

func TestDefaultMCPConfigWriteEligibilityStillRequiresCanonicalApproval(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "configured-mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "configured-admin-hash"
	if !cfg.Permissions.MCPConfigWrite {
		t.Fatal("fresh config must enable MCP config write eligibility")
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	runtime := tools.NewRuntime()
	provider := application.NewMCPConfigReadService()
	runtime.SetConfigSetApprovalProvider(provider)
	runtime.SetConfigSetApplyProvider(provider)
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := mcp.NewSDKHTTPHandler(runtime, "", false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "config-write-approval-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: mcpconfigwire.SetToolName,
		Arguments: map[string]any{"workspace_id": workspace.ID, "changes": []any{
			map[string]any{"key": "http.mcp.port", "value": "41001"},
		}},
	})
	if err != nil || !result.IsError {
		t.Fatalf("pre-approval MCP write result=%#v err=%v", result, err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "approval_required") || strings.Contains(string(data), string(mcpconfigwire.ErrorAccessDenied)) {
		t.Fatalf("default-eligible MCP write did not require approval: %s", data)
	}
	if requests := runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
		t.Fatalf("approval challenge unexpectedly created a review request automatically: %#v", requests)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HTTP.MCP.Port != cfg.HTTP.MCP.Port {
		t.Fatalf("pre-approval MCP write mutated config: before=%d after=%d", cfg.HTTP.MCP.Port, loaded.HTTP.MCP.Port)
	}
}

func TestExplicitMCPConfigWriteOptOutRemainsImmediate(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "configured-mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "configured-admin-hash"
	cfg.Permissions.MCPConfigWrite = false
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	runtime := tools.NewRuntime()
	provider := application.NewMCPConfigReadService()
	runtime.SetConfigSetApprovalProvider(provider)
	runtime.SetConfigSetApplyProvider(provider)
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(tools.WithApprovalCorrelation(tools.WithCallSource(t.Context(), "http"), "opt-out", "opt-out-request"), mcpconfigwire.SetToolName, map[string]any{
		"workspace_id": workspace.ID,
		"changes":      []any{map[string]any{"key": "http.mcp.port", "value": "41001"}},
	})
	if err != nil || !result.IsError {
		t.Fatalf("opt-out result=%#v err=%v", result, err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), string(mcpconfigwire.ErrorAccessDenied)) || strings.Contains(string(data), "approval_required") {
		t.Fatalf("opt-out denial=%s", data)
	}
	if requests := runtime.Approvals.List(approval.Filter{}); len(requests) != 0 {
		t.Fatalf("opt-out created approval state: %#v", requests)
	}
}
