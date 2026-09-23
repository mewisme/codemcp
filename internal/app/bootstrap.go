package app

import (
	"go.mewis.me/codemcp/internal/activity"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/mcp"
	"go.mewis.me/codemcp/internal/telemetry"
	"go.mewis.me/codemcp/internal/tools"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func (a *App) Bootstrap() error {
	span := tracepkg.StartObserver(a.trace, "APP", "app.bootstrap", "Bootstrapping application runtime")
	didBootstrap := false
	a.bootstrap.Do(func() {
		didBootstrap = true
		if a.Config == nil {
			a.Config = config.NewRuntimeStore(config.Default())
		}
		if a.Tools == nil {
			cfg := a.Config.Snapshot()
			a.Tools = tools.NewRuntimeWithAccess(cfg.Features, cfg.Permissions.AllowDirs, func() (bool, int) {
				current := a.Config.Snapshot()
				return current.Admin.Enabled, current.Admin.Port
			})
		}
		if a.Activity == nil {
			a.Activity = activity.NewStream()
		}
		if a.Logger == nil {
			a.Logger = logger.New(logger.Info)
		}
		telemetry.AttachTools(a.Tools, a.Activity, a.Logger)
		telemetry.AttachApprovals(a.Tools.Approvals, a.Activity, a.Logger)
		a.Upstream = a.Tools.Upstream
		a.syncMCPHTTP(a.Config.Snapshot().Server.Enabled)
		a.attachTunnelLifecycle()
	})
	span.EndMessage("Application runtime bootstrapped", tracepkg.Bool("performed", didBootstrap), tracepkg.Bool("mcp_http_enabled", a.MCP != nil), tracepkg.Bool("tunnel_configured", a.Tunnel != nil), tracepkg.Int("tool_count", len(a.Tools.List())))
	return nil
}

func (a *App) syncMCPHTTP(enabled bool) {
	if !enabled {
		if a.MCP != nil {
			a.MCP.CloseSubscriptions()
			a.MCP = nil
		}
		return
	}
	if a.MCP == nil {
		a.MCP = mcp.NewHTTPRuntimeWithTools(a.Tools)
	} else if a.MCP.Server == nil {
		a.MCP.Server = mcp.NewRuntimeWithTools(a.Tools)
	} else {
		a.MCP.Server.Tools = a.Tools
	}
	a.MCP.Activity = a.Activity
}
