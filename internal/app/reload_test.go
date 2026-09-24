package app

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/tools"
)

func TestReloadConfigUpdatesLiveRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Auth.MCPEnabled = true
	next.Auth.MCPTokenHash = "hash"
	next.Integrations.Ponytail.Active = false
	next.Permissions.AllowDirs = []string{t.TempDir()}
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	got := app.Config.Snapshot()
	if !got.Auth.MCPEnabled || got.Integrations.Ponytail.Active || len(got.Permissions.AllowDirs) != 1 {
		t.Fatalf("runtime config = %#v", got)
	}
	if _, ok := app.Tools.Registry.Schema("ponytail_turn"); !ok {
		t.Fatal("inactive feature controller tool disappeared")
	}
}

func TestReloadConfigUpdatesShellPath(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Shell.Path = []string{t.TempDir()}
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if got := app.Config.Snapshot().Shell.Path; len(got) != 1 || got[0] != next.Shell.Path[0] {
		t.Fatalf("stored shell path = %#v", got)
	}
	if got := app.Tools.Workspaces.ShellPath(); len(got) != 1 || got[0] != next.Shell.Path[0] {
		t.Fatalf("runtime shell path = %#v", got)
	}
}

func TestReloadConfigSyncsTunnelAdminKeyWithoutRuntimeReconfigure(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Tunnel.AdminKey = "admin-key"
	next.Tunnel.AdminWorkspaceID = "ws_admin"
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if got := app.Tunnel.Config(); got.AdminKey != "admin-key" || got.AdminWorkspaceID != "ws_admin" {
		t.Fatalf("tunnel config = %#v", got)
	}
	if !app.Tunnel.Status().AdminKeyConfigured {
		t.Fatalf("tunnel status = %#v", app.Tunnel.Status())
	}
}

func TestReloadConfigFailedApplyRestoresCommittedConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	previous := app.Config.Snapshot()
	if err := app.Tools.Registry.ReplaceOwned("integration:ponytail", nil); err != nil {
		t.Fatal(err)
	}
	if err := app.Tools.Registry.Register("ponytail_turn", tools.Schema{Name: "ponytail_turn"}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.TextResult("blocked"), nil
	}); err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Auth.MCPEnabled = true
	next.Auth.MCPTokenHash = "hash"
	next.Integrations.Ponytail.Active = !previous.Integrations.Ponytail.Active
	next.Permissions.AllowDirs = []string{t.TempDir()}
	next.Shell.Path = []string{t.TempDir()}
	if err := app.ReloadConfig(next); err == nil {
		t.Fatal("expected apply failure")
	}
	got := app.Config.Snapshot()
	if got.Auth.MCPEnabled != previous.Auth.MCPEnabled || got.Auth.MCPTokenHash != previous.Auth.MCPTokenHash {
		t.Fatalf("committed auth not restored: %#v", got.Auth)
	}
	if got.Integrations.Ponytail.Active != previous.Integrations.Ponytail.Active {
		t.Fatalf("committed integrations not restored: %#v", got.Integrations)
	}
	if len(got.Permissions.AllowDirs) != len(previous.Permissions.AllowDirs) {
		t.Fatalf("committed permissions not restored: %#v", got.Permissions)
	}
	if len(got.Shell.Path) != len(previous.Shell.Path) {
		t.Fatalf("committed shell path not restored: %#v", got.Shell.Path)
	}
	if runtimePath := app.Tools.Workspaces.ShellPath(); len(runtimePath) != len(previous.Shell.Path) {
		t.Fatalf("runtime shell path = %#v, want %#v", runtimePath, previous.Shell.Path)
	}
	if app.Tools.Integrations().Ponytail.Active != previous.Integrations.Ponytail.Active {
		t.Fatalf("runtime integrations = %#v", app.Tools.Integrations())
	}
}

func TestReloadConfigCommitsBeforeRuntimeApply(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	reloadTestAfterCommit = func() {
		close(entered)
		<-release
	}
	defer func() { reloadTestAfterCommit = nil }()
	next := cfg
	next.Shell.Path = []string{t.TempDir()}
	errCh := make(chan error, 1)
	go func() { errCh <- app.ReloadConfig(next) }()
	<-entered
	if got := app.Config.Snapshot().Shell.Path; len(got) != 1 || got[0] != next.Shell.Path[0] {
		t.Fatalf("store after commit = %#v", got)
	}
	if got := app.Tools.Workspaces.ShellPath(); len(got) != 0 {
		t.Fatalf("runtime changed before apply: %#v", got)
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if got := app.Tools.Workspaces.ShellPath(); len(got) != 1 || got[0] != next.Shell.Path[0] {
		t.Fatalf("runtime after apply = %#v", got)
	}
}
