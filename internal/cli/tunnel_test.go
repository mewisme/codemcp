package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestRenderTunnelLifecycleUsesSharedProgress(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = previous }()

	var output bytes.Buffer
	log := logger.NewWithOptions(logger.Options{Level: logger.Debug, Mode: logger.ModeVerbose, Writer: &output})
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{Unicode: true})
	session.Begin("Run OpenAI tunnel")
	renderTunnelLifecycle(session, log, tunnel.LifecycleEvent{State: tunnel.LifecycleReconnecting, ID: "tunnel_test", Attempt: 3, RetryIn: 4 * time.Second})
	renderTunnelLifecycle(session, log, tunnel.LifecycleEvent{State: tunnel.LifecycleReady, ID: "tunnel_test"})
	session.Close()
	text := output.String()
	for _, expected := range []string{"Run OpenAI tunnel", "Tunnel connected", "tunnel id — tunnel_test"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q missing %q", text, expected)
		}
	}
	if strings.Contains(text, "⠋") || strings.Contains(text, "Reconnecting tunnel\n") {
		t.Fatalf("legacy spinner escaped shared lifecycle: %q", text)
	}
}

func TestConfigureManagedTunnelRequiresSeparateRuntimeKey(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Admin.Key = "admin-only"
	cfg.Tunnel.Admin.WorkspaceID = "ws_admin"
	metadata := tunnel.Metadata{ID: "tunnel_test", OrganizationIDs: []string{"org_test"}}
	if err := configureManagedTunnel(&cfg, metadata, "", false); err == nil {
		t.Fatal("admin key was accepted as a runtime key")
	}
	if err := configureManagedTunnel(&cfg, metadata, "runtime-key", true); err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.APIKey != "runtime-key" || cfg.Tunnel.Admin.Key != "admin-only" || cfg.Tunnel.ID != "tunnel_test" || !cfg.Tunnel.Enabled {
		t.Fatalf("tunnel config = %#v", cfg.Tunnel)
	}
}

func TestTunnelCommandAdminHierarchy(t *testing.T) {
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"tunnel"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range [][]string{
		{"key", "set"}, {"key", "remove"},
		{"admin", "key", "set"}, {"admin", "key", "status"}, {"admin", "key", "verify"}, {"admin", "key", "remove"},
		{"admin", "organization", "set"}, {"admin", "workspace", "set"}, {"admin", "tenant", "set"},
		{"admin", "enable"}, {"admin", "disable"}, {"admin", "verify"},
		{"list"}, {"get"}, {"use"}, {"create"}, {"update"}, {"delete"}, {"sync"},
	} {
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

func TestTunnelStatusResultJSONIsIndependentFromDiagnosticLogFormat(t *testing.T) {
	root := &cobra.Command{Use: "cm"}
	addLoggingFlags(root)
	cmd := tunnelStatusCommand()
	root.AddCommand(cmd)
	var output bytes.Buffer
	cmd.SetOut(&output)

	if cmd.Flags().Lookup("json") == nil {
		t.Fatal("tunnel status does not expose canonical --json result flag")
	}
	if err := root.PersistentFlags().Set("log-format", "json"); err != nil {
		t.Fatal(err)
	}
	if mode := commandResultModeFor(cmd); mode != resultModePlain {
		t.Fatalf("--log-format=json changed result mode to %v", mode)
	}
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if mode := commandResultModeFor(cmd); mode != resultModeJSON {
		t.Fatalf("--json result mode = %v", mode)
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

	cfg := tunnel.Config{Enabled: true, ID: "tunnel_test", APIKey: "runtime-key", Admin: tunnel.AdminConfig{Key: "admin-key", WorkspaceID: "ws_admin"}}
	status := tunnel.Status{
		Provider: tunnel.ProviderOpenAI, Enabled: true, Running: true, Ready: true, ID: "tunnel_test",
		Admin:    tunnel.AdminState{Enabled: true, KeyConfigured: true, Configured: true, WorkspaceID: "ws_admin"},
		Metadata: &tunnel.Metadata{ID: "tunnel_test", Name: "MCP WSL", Description: "WSL tunnel"},
	}
	var output bytes.Buffer
	renderTunnelStatusText(presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true}), cfg, status, true, false)
	text := output.String()
	for _, expected := range []string{"┌  OpenAI Secure MCP Tunnel", "✓  OpenAI Secure MCP Tunnel is connected", "│  enabled — true", "│  configured — true", "│  id — tunnel_test", "│  name — MCP WSL", "│  admin — configured · workspace:ws_admin", "└  Status complete"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q missing %q", text, expected)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") || strings.Contains(text, `"provider":`) {
		t.Fatalf("default tunnel status rendered JSON: %q", text)
	}
}

func TestTunnelReadRenderersUseRailHierarchyAndRedaction(t *testing.T) {
	metadata := tunnel.Metadata{
		ID: "tunnel_one", Name: "One", Description: "First", Creator: "user",
		WorkspaceIDs: []string{"ws_admin"}, OrganizationIDs: []string{"org_demo"},
	}
	var output bytes.Buffer
	renderManagedTunnelList(presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true}), []tunnel.Metadata{metadata})
	listText := output.String()
	for _, expected := range []string{"┌  Managed OpenAI tunnels", "│  ◆ tunnel_one", "│  │  name — One", "│  │  workspaces — ws_admin", "└  Done"} {
		if !strings.Contains(listText, expected) {
			t.Fatalf("managed tunnel list missing %q: %q", expected, listText)
		}
	}
	if strings.Contains(listText, "scope=") {
		t.Fatalf("managed tunnel list retained dense summary: %q", listText)
	}

	output.Reset()
	renderTunnelAdminKeyStatus(presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true}), application.TunnelAdminStatus{
		Configured: true,
		Scope:      tunnel.AdminScope{WorkspaceID: "ws_admin"},
		Access:     tunnel.AdminAccess{Read: true, Manage: true},
	})
	adminText := output.String()
	for _, expected := range []string{"┌  OpenAI tunnel admin key", "✓  Admin key configured", "│  key — <redacted>", "│  scope — workspace:ws_admin", "│  access — full management"} {
		if !strings.Contains(adminText, expected) {
			t.Fatalf("admin status missing %q: %q", expected, adminText)
		}
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
