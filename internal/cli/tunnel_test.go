package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestLogTunnelLifecycleReconnect(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()

	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Info, Mode: logger.ModeVerbose, Writer: &output})
	logTunnelLifecycle(log, tunnel.LifecycleEvent{State: tunnel.LifecycleReconnecting, ID: "tunnel_test", Attempt: 3, RetryIn: 4 * time.Second})
	text := output.String()
	for _, expected := range []string{"⠋ Reconnecting tunnel", "tunnel_id: tunnel_test", "attempt: 3", "retry_in: 4s"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q missing %q", text, expected)
		}
	}
}

func TestConfigureManagedTunnelRequiresSeparateRuntimeKey(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.AdminKey = "admin-only"
	cfg.Tunnel.AdminWorkspaceID = "ws_admin"
	metadata := tunnel.Metadata{ID: "tunnel_test", OrganizationIDs: []string{"org_test"}}
	if err := configureManagedTunnel(&cfg, metadata, "", false); err == nil {
		t.Fatal("admin key was accepted as a runtime key")
	}
	if err := configureManagedTunnel(&cfg, metadata, "runtime-key", true); err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.APIKey != "runtime-key" || cfg.Tunnel.AdminKey != "admin-only" || cfg.Tunnel.ID != "tunnel_test" || !cfg.Tunnel.Enabled {
		t.Fatalf("tunnel config = %#v", cfg.Tunnel)
	}
}

func TestTunnelCommandAdminHierarchy(t *testing.T) {
	cmd := tunnelCommand()
	for _, path := range [][]string{{"admin", "key", "set"}, {"admin", "key", "status"}, {"admin", "key", "verify"}, {"admin", "key", "remove"}, {"list"}, {"get"}, {"use"}, {"create"}, {"update"}, {"delete"}, {"sync"}} {
		resolved, _, err := cmd.Find(path)
		if err != nil || resolved.Name() != path[len(path)-1] {
			t.Fatalf("tunnel path %v resolved to %v: %v", path, resolved, err)
		}
	}
	for _, alias := range []string{"select", "switch"} {
		resolved, _, err := cmd.Find([]string{alias})
		if err != nil || resolved.Name() != "use" {
			t.Fatalf("tunnel alias %q resolved to %v: %v", alias, resolved, err)
		}
	}
}

func TestFetchTunnelStatusUsesPersistedMetadata(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled = false, false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveTunnelMetadata(tunnel.Metadata{ID: "tunnel_test", Name: "Persisted tunnel"}); err != nil {
		t.Fatal(err)
	}
	status := fetchTunnelStatus(context.Background(), tunnel.Config{Enabled: true, ID: "tunnel_test", APIKey: "runtime-key"})
	if status.Metadata == nil || status.Metadata.Name != "Persisted tunnel" {
		t.Fatalf("status metadata = %#v", status.Metadata)
	}
}

func TestRenderTunnelStatusTextIsCLIFirst(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()

	cfg := tunnel.Config{Enabled: true, ID: "tunnel_test", APIKey: "runtime-key", AdminKey: "admin-key", AdminWorkspaceID: "ws_admin"}
	status := tunnel.Status{
		Provider: tunnel.ProviderOpenAI, Enabled: true, Running: true, Ready: true, ID: "tunnel_test", AdminKeyConfigured: true,
		AdminScope: &tunnel.AdminScope{WorkspaceID: "ws_admin"}, Metadata: &tunnel.Metadata{ID: "tunnel_test", Name: "MCP WSL", Description: "WSL tunnel"},
	}
	var output bytes.Buffer
	renderTunnelStatusText(&output, cfg, status, true, false)
	text := output.String()
	for _, expected := range []string{"✓ OpenAI Secure MCP Tunnel is connected", "Tunnel", "status      connected", "enabled     true", "configured  true", "id          tunnel_test", "name        MCP WSL", "admin       configured · workspace:ws_admin"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q missing %q", text, expected)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") || strings.Contains(text, `"provider":`) {
		t.Fatalf("default tunnel status rendered JSON: %q", text)
	}
}

func TestTunnelCLIState(t *testing.T) {
	configured := tunnel.Config{Enabled: true, ID: "tunnel_test", APIKey: "runtime-key"}
	for _, test := range []struct {
		name           string
		cfg            tunnel.Config
		status         tunnel.Status
		runtimeRunning bool
		want           string
	}{
		{name: "disabled", cfg: tunnel.Config{}, status: tunnel.Status{}, want: "disabled"},
		{name: "not configured", cfg: tunnel.Config{Enabled: true}, status: tunnel.Status{Enabled: true}, want: "not configured"},
		{name: "offline", cfg: configured, status: tunnel.Status{Enabled: true}, want: "offline"},
		{name: "starting", cfg: configured, status: tunnel.Status{Enabled: true}, runtimeRunning: true, want: "starting"},
		{name: "connecting", cfg: configured, status: tunnel.Status{Enabled: true, Running: true}, runtimeRunning: true, want: "connecting"},
		{name: "reconnecting", cfg: configured, status: tunnel.Status{Enabled: true, Restarting: true, LastError: "retrying"}, runtimeRunning: true, want: "reconnecting"},
		{name: "connected", cfg: configured, status: tunnel.Status{Enabled: true, Running: true, Ready: true}, runtimeRunning: true, want: "connected"},
		{name: "failed", cfg: configured, status: tunnel.Status{Enabled: true, LastError: "failed"}, runtimeRunning: true, want: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := tunnelCLIState(test.cfg, test.status, test.runtimeRunning); got != test.want {
				t.Fatalf("state = %q, want %q", got, test.want)
			}
		})
	}
}
