package tui

import (
	"context"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/docs/tuiguide"
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/interface/tui/action"
	tuipage "go.mewis.me/codemcp/internal/interface/tui/page"
)

type navigateMsg struct {
	route   Route
	sibling bool
}

func defaultActionRegistry() *action.Registry {
	actions := []action.Action{
		navigationAction("app.go.workspaces", "Workspaces", Route{Kind: RouteWorkspaces}, []string{"workspace", "workspaces", "ws", "container", "containers"}, capability.WorkspaceList, capability.WorkspaceShow, capability.WorkspaceAccessList, capability.WorkspaceContainerList, capability.WorkspaceContainerShow),
		navigationAction("app.go.upstreams", "Upstreams", Route{Kind: RouteMCP}, []string{"mcp", "server", "upstream"}, capability.UpstreamServerList, capability.UpstreamServerShow, capability.UpstreamAuthStatus),
		navigationAction("app.go.tunnel", "Tunnel", Route{Kind: RouteTunnel}, []string{"tunnel", "secure"}, capability.TunnelStatus, capability.TunnelAdminKeyStatus),
		navigationAction("app.go.tunnels", "Managed Tunnels", Route{Kind: RouteTunnels}, []string{"tunnel", "tunnels", "managed", "openai"}, capability.TunnelList, capability.TunnelGet),
		navigationAction("app.go.requests", "Requests", Route{Kind: RouteRequests}, []string{"request", "approval"}, capability.RequestView),
		navigationAction("app.go.logs", "Logs", Route{Kind: RouteLogs}, []string{"logs", "events", "journal"}),
		navigationAction("app.go.logs-exec", "Command Execution", Route{Kind: RouteLogsExec}, []string{"logs", "command", "execution", "exec", "output"}),
		navigationAction("app.go.logs-tools", "Tool Calls", Route{Kind: RouteLogsTools}, []string{"logs", "tools", "calls", "tool calls"}),
		navigationAction("app.go.config", "Config", Route{Kind: RouteConfig}, []string{"config", "settings", "cfg"}, capability.ConfigPath, capability.ConfigGet),
		navigationAction("app.go.instruction", "Instruction", Route{Kind: RouteInstruction}, []string{"instruction", "instructions", "global", "context", "rules", "sources"}),
		navigationAction("app.go.runtime", "Runtime", Route{Kind: RouteRuntime}, []string{"runtime", "status", "service"}, capability.AuthStatus),
		navigationAction("app.go.about", "About", Route{Kind: RouteAbout}, []string{"about", "version", "build", "uptime"}, capability.VersionAbout),
		navigationAction("app.go.guide", "Guide", Route{Kind: RouteGuide}, []string{"guide", "help", "docs", "documentation"}),
	}
	actions = append(actions, guideActions()...)
	actions = append(actions, instructionNavigationActions()...)
	actions = append(actions, workspaceActions()...)
	actions = append(actions, mcpActions()...)
	actions = append(actions, tunnelActions()...)
	actions = append(actions, requestActions()...)
	actions = append(actions, logsActions()...)
	actions = append(actions, systemActions()...)
	actions = append(actions, configActions()...)
	registry, err := action.NewRegistry(actions...)
	if err != nil {
		panic(err)
	}
	return registry
}

func guideActions() []action.Action {
	topics := tuiguide.Topics()
	actions := make([]action.Action, 0, len(topics))
	for _, topic := range topics {
		topic := topic
		actions = append(actions, action.Action{
			ID: "guide." + strings.ReplaceAll(topic.ID, "/", "."), Title: topic.Title, Category: "Guide", Description: topic.Description,
			Keywords: topic.Keywords, Scope: action.ScopeGlobal,
			Run: func(context.Context, action.Context) tea.Cmd {
				return func() tea.Msg { return navigateMsg{route: Route{Kind: RouteGuide, ResourceID: topic.ID}} }
			},
		})
	}
	return actions
}

func systemActions() []action.Action {
	return []action.Action{
		systemAction("system.refresh", "Refresh system status", "Refresh runtime, service, auth, installation, update, and build state", []string{"system", "runtime", "refresh", "status"}, []string{"status"}, tuipage.SystemRefresh, false),
		systemAction("runtime.up.user", "Start user service", "Install or update and start the per-user managed runtime", []string{"runtime", "service", "up", "user", "start"}, []string{"up"}, tuipage.RuntimeUpUser, false),
		systemAction("runtime.up.system", "Start system service", "Install or update and start the machine-level managed runtime", []string{"runtime", "service", "up", "system", "start"}, []string{"up", "--system"}, tuipage.RuntimeUpSystem, true),
		systemAction("runtime.down.user", "Stop user service", "Stop and remove the per-user managed runtime while preserving config and logs", []string{"runtime", "service", "down", "user", "stop"}, []string{"down"}, tuipage.RuntimeDownUser, false),
		systemAction("runtime.down.system", "Stop system service", "Stop and remove the machine-level managed runtime while preserving config and logs", []string{"runtime", "service", "down", "system", "stop"}, []string{"down", "--system"}, tuipage.RuntimeDownSystem, true),
		systemAction("runtime.restart.user", "Restart user service", "Restart the per-user managed runtime", []string{"runtime", "service", "restart", "user"}, []string{"restart"}, tuipage.RuntimeRestartUser, false),
		systemAction("runtime.restart.system", "Restart system service", "Restart the machine-level managed runtime", []string{"runtime", "service", "restart", "system"}, []string{"restart", "--system"}, tuipage.RuntimeRestartSystem, true),
		systemAction("runtime.foreground", "Run foreground runtime", "Show the foreground serve command to run after leaving the TUI", []string{"runtime", "foreground", "serve", "terminal"}, []string{"serve"}, tuipage.RuntimeForeground, false),
		systemAction("mcp.stdio.foreground", "Run MCP stdio server", "Show the MCP stdio command to run after leaving the TUI", []string{"mcp", "stdio", "cursor", "terminal", "transport"}, []string{"mcp", "stdio"}, tuipage.MCPStdioForeground, false),
		systemAction("mcp.http.foreground", "Run standalone MCP HTTP server", "Show the Streamable HTTP/SSE command to run after leaving the TUI", []string{"mcp", "http", "sse", "oauth", "transport", "terminal"}, []string{"mcp", "http"}, tuipage.MCPHTTPForeground, false),
		systemAction("transport.mcp-http.enable", "Enable MCP HTTP server", "Enable the local MCP HTTP transport and reload the running runtime", []string{"mcp", "http", "server", "transport", "enable", "listener"}, []string{"config", "set"}, tuipage.MCPHTTPEnable, false),
		systemAction("transport.mcp-http.disable", "Disable MCP HTTP server", "Disable the local MCP HTTP transport and close its listener; the Secure MCP Tunnel must remain enabled", []string{"mcp", "http", "server", "transport", "disable", "listener", "port"}, []string{"config", "set"}, tuipage.MCPHTTPDisable, false),
		systemAction("config.initialize.external", "Initialize configuration", "Show the initialization command that creates configuration and one-time authentication tokens", []string{"config", "init", "initialize", "token"}, []string{"init"}, tuipage.ConfigInitialize, false),
		systemAction("config.uninitialize.external", "Uninitialize configuration", "Show the destructive command that removes local configuration and state", []string{"config", "uninit", "uninitialize", "remove", "state"}, []string{"uninit"}, tuipage.ConfigUninitialize, false),
		systemAction("auth.mcp.enable", "Enable MCP authentication", "Enable MCP token authentication", []string{"auth", "mcp", "enable"}, []string{"auth", "mcp", "enable"}, tuipage.AuthMCPEnable, false),
		systemAction("auth.mcp.disable", "Disable MCP authentication", "Disable MCP token authentication", []string{"auth", "mcp", "disable"}, []string{"auth", "mcp", "disable"}, tuipage.AuthMCPDisable, false),
		systemAction("auth.mcp.rotate", "Rotate MCP token", "Rotate the MCP token and reveal the replacement once", []string{"auth", "mcp", "token", "rotate", "create"}, []string{"auth", "mcp", "create"}, tuipage.AuthMCPRotate, false),
		systemAction("auth.admin.enable", "Enable admin authentication", "Enable admin token authentication", []string{"auth", "admin", "enable"}, []string{"auth", "admin", "enable"}, tuipage.AuthAdminEnable, false),
		systemAction("auth.admin.disable", "Disable admin authentication", "Disable admin token authentication", []string{"auth", "admin", "disable"}, []string{"auth", "admin", "disable"}, tuipage.AuthAdminDisable, false),
		systemAction("auth.admin.rotate", "Rotate admin token", "Rotate the admin token and reveal the replacement once", []string{"auth", "admin", "token", "rotate", "create"}, []string{"auth", "admin", "create"}, tuipage.AuthAdminRotate, false),
		editorNavigationAction("install.run", "Install managed binary", "System", "Install this binary into the versioned managed layout", []string{"install", "managed", "binary"}, []string{"install"}, func(ctx action.Context) bool { return ctx.Route == string(RouteRuntime) }, func(action.Context) Route { return Route{Kind: RouteRuntime, Action: "install"} }),
		systemAction("update.check", "Check for upgrades", "Check the latest available verified release", []string{"upgrade", "update", "check", "latest", "release"}, []string{"upgrade", "check"}, tuipage.UpdateCheck, false),
		editorNavigationAction("update.apply", "Apply upgrade", "System", "Download, verify, install, and activate an upgrade", []string{"upgrade", "update", "apply", "install", "release"}, []string{"upgrade"}, func(ctx action.Context) bool { return ctx.Route == string(RouteRuntime) }, func(action.Context) Route { return Route{Kind: RouteRuntime, Action: "update"} }),
	}
}

func systemAction(id, title, description string, keywords, commandPath []string, command tuipage.SystemCommand, systemOnly bool) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "System", Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool {
			return ctx.Route == string(RouteRuntime) && (!systemOnly || runtime.GOOS != "windows")
		},
		Run: func(context.Context, action.Context) tea.Cmd {
			return func() tea.Msg { return tuipage.SystemCommandMsg{Command: command} }
		},
	}
}

func logsActions() []action.Action {
	return []action.Action{
		logsAction("logs.refresh", "Refresh logs", "Reload journal history and reconnect the live stream", []string{"logs", "refresh", "history", "reconnect"}, []string{"logs"}, tuipage.LogsRefresh),
		editorNavigationAction("logs.filter", "Filter logs", "Logs", "Configure structured runtime log filters", []string{"logs", "filter", "grep", "session", "level"}, []string{"logs"}, func(ctx action.Context) bool { return ctx.Route == string(RouteLogs) }, func(action.Context) Route { return Route{Kind: RouteLogs, Action: "filter"} }),
		logsAction("logs.toggle", "Pause or resume logs", "Toggle live tail following without dropping buffered events", []string{"logs", "pause", "resume", "follow"}, []string{"logs", "follow"}, tuipage.LogsToggle),
		logsAction("logs.info", "Show logs info", "Show the runtime journal path, file count, and size", []string{"logs", "path", "info", "journal"}, []string{"logs", "path"}, tuipage.LogsInfo),
		logsAction("logs.clear", "Clear logs", "Clear current and rotated runtime logs after confirmation", []string{"logs", "clear", "delete"}, []string{"logs", "clear"}, tuipage.LogsClear),
	}
}

func logsAction(id, title, description string, keywords, commandPath []string, command tuipage.LogsCommand) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Logs", Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool { return ctx.Route == string(RouteLogs) },
		Run: func(context.Context, action.Context) tea.Cmd {
			return func() tea.Msg { return tuipage.LogsCommandMsg{Command: command} }
		},
	}
}

func configActions() []action.Action {
	return []action.Action{
		configAction("config.refresh", "Refresh config", "Reload persisted configuration and runtime state", []string{"config", "refresh", "reload", "view"}, []string{"config", "list"}, tuipage.ConfigRefresh),
		configAction("config.edit", "Edit config field", "Edit the selected typed configuration field", []string{"config", "edit", "set", "field"}, []string{"config", "set"}, tuipage.ConfigEdit),
		configAction("config.verify", "Verify config", "Verify structured config/state format consistency and configuration validity", []string{"config", "verify", "validate"}, []string{"config", "verify"}, tuipage.ConfigVerify),
		configAction("config.migrate", "Migrate config secrets", "Migrate legacy plaintext credentials into the secret store", []string{"config", "migrate", "secrets"}, []string{"config", "migrate"}, tuipage.ConfigMigrate),
		configAction("config.migrate.secrets", "Migrate secret files", "Migrate legacy secret files to encrypted JSON envelopes", []string{"config", "migrate", "secrets", "envelope"}, []string{"config", "migrate", "secrets"}, tuipage.ConfigMigrateSecrets),
		editorNavigationAction("config.export", "Export config envelope", "Config", "Export portable non-secret configuration and state as JSON", []string{"config", "export", "envelope", "backup"}, []string{"config", "export"}, func(ctx action.Context) bool { return ctx.Route == string(RouteConfig) }, func(action.Context) Route { return Route{Kind: RouteConfig, Section: "storage", Action: "export"} }),
		editorNavigationAction("config.import", "Import config envelope", "Config", "Import a portable JSON configuration envelope while preserving target secrets", []string{"config", "import", "envelope", "restore"}, []string{"config", "import"}, func(ctx action.Context) bool { return ctx.Route == string(RouteConfig) }, func(action.Context) Route { return Route{Kind: RouteConfig, Section: "storage", Action: "import"} }),
	}
}

func configAction(id, title, description string, keywords, commandPath []string, command tuipage.ConfigCommand) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Config", Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool { return ctx.Route == string(RouteConfig) },
		Run: func(_ context.Context, ctx action.Context) tea.Cmd {
			return func() tea.Msg { return tuipage.ConfigCommandMsg{Command: command, ResourceID: ctx.ResourceID} }
		},
	}
}

func requestActions() []action.Action {
	return []action.Action{
		requestAction("request.refresh", "Refresh requests", "Refresh approval requests from the running runtime", []string{"request", "approval", "refresh", "list"}, []string{"request", "list"}, tuipage.RequestRefresh, false),
		editorNavigationAction("request.create.test", "Create test request", "Requests", "Create a synthetic approval request for testing the Requests TUI and approval flow", []string{"request", "approval", "create", "test", "dummy", "synthetic"}, []string{"request", "create-test"}, func(ctx action.Context) bool { return ctx.Route == string(RouteRequests) }, func(action.Context) Route { return Route{Kind: RouteRequests, Action: "create-test"} }),
		requestAction("request.show.pending", "Show pending requests", "Show only pending approval requests", []string{"request", "pending", "filter"}, []string{"request", "list"}, tuipage.RequestShowPending, false),
		requestAction("request.show.history", "Show request history", "Show resolved and expired approval requests", []string{"request", "history", "resolved", "filter"}, []string{"request", "list"}, tuipage.RequestShowHistory, false),
		requestAction("request.show.all", "Show all requests", "Show pending and historical approval requests", []string{"request", "all", "filter"}, []string{"request", "list"}, tuipage.RequestShowAll, false),
		requestAction("request.approve", "Approve request", "Approve the selected pending control request", []string{"request", "approve", "allow", "accept"}, []string{"request", "approve"}, tuipage.RequestApprove, false),
		requestAction("request.deny", "Deny request", "Deny the selected pending control request", []string{"request", "deny", "reject"}, []string{"request", "deny"}, tuipage.RequestDeny, false),
		requestAction("request.grant.list", "List runtime grants", "List active similar-command runtime session grants", []string{"request", "grant", "list", "similar"}, []string{"request", "grant", "list"}, tuipage.RequestGrantList, false),
		requestAction("request.grant.revoke", "Revoke runtime grant", "Revoke the selected similar-command runtime session grant", []string{"request", "grant", "revoke", "similar"}, []string{"request", "grant", "revoke"}, tuipage.RequestGrantRevoke, true),
	}
}

func requestAction(id, title, description string, keywords, commandPath []string, command tuipage.RequestCommand, needsResource bool) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Requests", Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool {
			if ctx.Route != string(RouteRequests) {
				return false
			}
			return !needsResource || ctx.ResourceID != ""
		},
		Run: func(_ context.Context, ctx action.Context) tea.Cmd {
			return func() tea.Msg { return tuipage.RequestCommandMsg{Command: command, ResourceID: ctx.ResourceID} }
		},
	}
}

func tunnelActions() []action.Action {
	return []action.Action{
		editorNavigationAction("tunnel.configure", "Configure runtime tunnel", "Tunnel", "Configure the local OpenAI Secure MCP Tunnel", []string{"tunnel", "configure", "runtime"}, []string{"tunnel", "configure"}, func(ctx action.Context) bool { return ctx.Route == string(RouteTunnel) }, func(action.Context) Route { return Route{Kind: RouteTunnel, Action: "edit"} }),
		tunnelAction("tunnel.enable", "Enable runtime tunnel", "Enable the local OpenAI Secure MCP Tunnel", []string{"tunnel", "enable", "runtime"}, []string{"tunnel", "enable"}, tuipage.TunnelEnable, RouteTunnel, false),
		tunnelAction("tunnel.disable", "Disable runtime tunnel", "Disable the local OpenAI Secure MCP Tunnel", []string{"tunnel", "disable", "runtime"}, []string{"tunnel", "disable"}, tuipage.TunnelDisable, RouteTunnel, false),
		tunnelAction("tunnel.foreground", "Run foreground tunnel", "Show the foreground tunnel command to run after leaving the TUI", []string{"tunnel", "foreground", "run", "terminal"}, []string{"tunnel", "run"}, tuipage.TunnelForeground, RouteTunnel, false),
		tunnelAction("tunnel.sync", "Sync tunnel metadata", "Fetch and persist metadata for the configured runtime tunnel", []string{"tunnel", "sync", "metadata"}, []string{"tunnel", "sync"}, tuipage.TunnelSync, RouteTunnel, false),
		editorNavigationAction("tunnel.admin.key.set", "Set admin key", "Tunnel", "Verify and store an OpenAI tunnel admin key", []string{"tunnel", "admin", "key", "set"}, []string{"tunnel", "admin", "key", "set"}, func(ctx action.Context) bool { return ctx.Route == string(RouteTunnel) }, func(action.Context) Route { return Route{Kind: RouteTunnel, Section: "admin-key", Action: "edit"} }),
		tunnelAction("tunnel.admin.key.verify", "Verify admin key", "Re-verify Tunnels Manage access for the stored admin key", []string{"tunnel", "admin", "key", "verify"}, []string{"tunnel", "admin", "key", "verify"}, tuipage.TunnelAdminKeyVerify, RouteTunnel, false),
		tunnelAction("tunnel.admin.key.remove", "Remove admin key", "Remove the stored tunnel admin key and verification scope", []string{"tunnel", "admin", "key", "remove"}, []string{"tunnel", "admin", "key", "remove"}, tuipage.TunnelAdminKeyRemove, RouteTunnel, false),
		tunnelAction("tunnel.managed.refresh", "Refresh managed tunnels", "Refresh managed tunnels from the OpenAI control plane", []string{"tunnel", "managed", "refresh", "list"}, []string{"tunnel", "list"}, tuipage.TunnelManagedRefresh, RouteTunnels, false),
		editorNavigationAction("tunnel.managed.create", "Create managed tunnel", "Tunnel", "Create a tunnel through the OpenAI Tunnel Management API", []string{"tunnel", "managed", "create"}, []string{"tunnel", "create"}, func(ctx action.Context) bool {
			return ctx.Route == string(RouteTunnels) && tunnelAdminManageAvailable()
		}, func(action.Context) Route { return Route{Kind: RouteTunnels, Action: "create"} }),
		editorNavigationAction("tunnel.managed.update", "Update managed tunnel", "Tunnel", "Update the current managed tunnel", []string{"tunnel", "managed", "update", "edit"}, []string{"tunnel", "update"}, func(ctx action.Context) bool {
			return ctx.Route == string(RouteTunnels) && ctx.ResourceID != "" && tunnelAdminManageAvailable()
		}, func(ctx action.Context) Route {
			return Route{Kind: RouteTunnels, ResourceID: ctx.ResourceID, Action: "edit"}
		}),
		editorNavigationAction("tunnel.managed.configure", "Use managed tunnel", "Tunnel", "Configure cm to use the current managed tunnel", []string{"tunnel", "managed", "use", "select", "switch", "runtime"}, []string{"tunnel", "use"}, func(ctx action.Context) bool {
			return ctx.Route == string(RouteTunnels) && ctx.ResourceID != "" && tunnelAdminReadAvailable()
		}, func(ctx action.Context) Route {
			return Route{Kind: RouteTunnels, ResourceID: ctx.ResourceID, Action: "configure"}
		}),
		tunnelAction("tunnel.managed.delete", "Delete managed tunnel", "Permanently delete the current managed tunnel", []string{"tunnel", "managed", "delete", "remove"}, []string{"tunnel", "delete"}, tuipage.TunnelManagedDelete, RouteTunnels, true),
	}
}

func tunnelAction(id, title, description string, keywords, commandPath []string, command tuipage.TunnelCommand, route RouteKind, needsResource bool) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Tunnel", Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool {
			if ctx.Route != string(route) || needsResource && ctx.ResourceID == "" {
				return false
			}
			switch command {
			case tuipage.TunnelManagedRefresh, tuipage.TunnelManagedCreate, tuipage.TunnelManagedUpdate, tuipage.TunnelManagedDelete:
				return tunnelAdminManageAvailable()
			case tuipage.TunnelManagedConfigure:
				return tunnelAdminReadAvailable()
			}
			return true
		},
		Run: func(_ context.Context, ctx action.Context) tea.Cmd {
			return func() tea.Msg { return tuipage.TunnelCommandMsg{Command: command, ResourceID: ctx.ResourceID} }
		},
	}
}

func tunnelAdminReadAvailable() bool {
	status, err := application.TunnelAdminKeyStatus()
	return err == nil && (status.Access.Read || status.Access.Manage)
}
func tunnelAdminManageAvailable() bool {
	status, err := application.TunnelAdminKeyStatus()
	return err == nil && status.Access.Manage
}

func mcpActions() []action.Action {
	return []action.Action{
		editorNavigationAction("upstream.server.add", "Add server", "Upstream", "Add an Upstream server", []string{"upstream", "server", "add", "mcp"}, []string{"upstream", "server", "add"}, nil, func(action.Context) Route { return Route{Kind: RouteMCP, Action: "create"} }),
		editorNavigationAction("upstream.server.configure", "Configure server", "Upstream", "Configure the current Upstream server", []string{"upstream", "server", "configure", "set", "mcp"}, []string{"upstream", "server", "configure"}, func(ctx action.Context) bool { return ctx.Route == string(RouteMCP) && ctx.ResourceID != "" }, func(ctx action.Context) Route {
			return Route{Kind: RouteMCP, ResourceID: ctx.ResourceID, Action: "edit"}
		}),
		mcpAction("upstream.server.remove", "Remove server", "Remove the current Upstream server", []string{"upstream", "server", "remove", "delete", "mcp"}, []string{"upstream", "server", "remove"}, tuipage.UpstreamServerRemove, true),
		mcpAction("upstream.server.enable", "Enable server", "Enable the current Upstream server", []string{"upstream", "server", "enable", "mcp"}, []string{"upstream", "server", "enable"}, tuipage.UpstreamServerEnable, true),
		mcpAction("upstream.server.disable", "Disable server", "Disable the current Upstream server", []string{"upstream", "server", "disable", "mcp"}, []string{"upstream", "server", "disable"}, tuipage.UpstreamServerDisable, true),
		mcpAction("upstream.server.status", "Refresh health", "Refresh Upstream health and connection status", []string{"upstream", "server", "status", "health", "refresh", "mcp"}, []string{"upstream", "server", "status"}, tuipage.UpstreamServerHealth, false),
		mcpAction("upstream.server.tools", "View tools", "Load tools exposed by the current Upstream server", []string{"upstream", "server", "tools", "refresh", "mcp"}, []string{"upstream", "server", "tools"}, tuipage.UpstreamServerTools, true),
		editorNavigationAction("upstream.server.auth.login", "OAuth login", "Upstream", "Authorize the current HTTP Upstream with OAuth", []string{"upstream", "server", "auth", "login", "oauth", "mcp"}, []string{"upstream", "server", "auth", "login"}, func(ctx action.Context) bool { return ctx.Route == string(RouteMCP) && ctx.ResourceID != "" }, func(ctx action.Context) Route {
			return Route{Kind: RouteMCP, ResourceID: ctx.ResourceID, Section: "oauth", Action: "login"}
		}),
		mcpAction("upstream.server.auth.logout", "OAuth logout", "Remove stored OAuth authorization for the current Upstream server", []string{"upstream", "server", "auth", "logout", "oauth", "mcp"}, []string{"upstream", "server", "auth", "logout"}, tuipage.UpstreamAuthLogout, true),
	}
}

func mcpAction(id, title, description string, keywords, commandPath []string, command tuipage.UpstreamCommand, needsResource bool) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Upstream", Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool {
			return !needsResource || ctx.Route == string(RouteMCP) && ctx.ResourceID != ""
		},
		Run: func(_ context.Context, ctx action.Context) tea.Cmd {
			return func() tea.Msg { return tuipage.UpstreamCommandMsg{Command: command, ResourceID: ctx.ResourceID} }
		},
	}
}

func workspaceActions() []action.Action {
	return []action.Action{
		workspaceContextNavigationAction("workspace.context.configure", "Configure Workspace Project Context", "Configure build and preview parameters for the current workspace", "context"),
		workspaceContextNavigationAction("workspace.context.preview", "Preview Workspace Project Context", "Open the last successful Project Context build for the current workspace", "context-preview"),
		editorNavigationAction("workspace.register", "Register", "Workspace", "Register a workspace root", []string{"workspace", "register"}, []string{"workspace", "register"}, nil, func(action.Context) Route { return Route{Kind: RouteWorkspaces, Action: "register"} }),
		editorNavigationAction("workspace.relocate", "Relocate", "Workspace", "Rebind the current workspace after its project directory was renamed or moved", []string{"workspace", "relocate", "move", "rename", "root"}, []string{"workspace", "relocate"}, func(ctx action.Context) bool { return ctx.Route == string(RouteWorkspaces) && ctx.ResourceID != "" }, func(ctx action.Context) Route {
			return Route{Kind: RouteWorkspaces, ResourceID: ctx.ResourceID, Action: "relocate"}
		}),
		workspaceAction("workspace.unregister", "Unregister", "Unregister the current workspace without deleting project files", []string{"workspace", "unregister"}, []string{"workspace", "unregister"}, tuipage.WorkspaceUnregister, true, false),
		workspaceAction("workspace.purge", "Purge local state", "Delete local .cm state for the current workspace after confirmation", []string{"workspace", "purge", "delete", "state"}, []string{"workspace", "purge"}, tuipage.WorkspacePurge, true, false),
		editorNavigationAction("workspace.access.add", "Add access directory", "Workspace", "Grant the current workspace access to an additional directory", []string{"workspace", "access", "add"}, []string{"workspace", "access", "add"}, func(ctx action.Context) bool { return ctx.Route == string(RouteWorkspaces) && ctx.ResourceID != "" }, func(ctx action.Context) Route {
			return Route{Kind: RouteWorkspaces, ResourceID: ctx.ResourceID, Section: "access", Action: "add"}
		}),
		editorNavigationAction("workspace.access.remove", "Remove access directory", "Workspace", "Revoke an additional directory from the current workspace", []string{"workspace", "access", "remove"}, []string{"workspace", "access", "remove"}, func(ctx action.Context) bool { return ctx.Route == string(RouteWorkspaces) && ctx.ResourceID != "" }, func(ctx action.Context) Route {
			return Route{Kind: RouteWorkspaces, ResourceID: ctx.ResourceID, Section: "access", Action: "remove"}
		}),
		editorNavigationAction("workspace.container.create", "Create container", "Workspace", "Create a workspace container", []string{"workspace", "container", "create"}, []string{"workspace", "container", "create"}, nil, func(action.Context) Route { return Route{Kind: RouteContainers, Action: "create"} }),
		editorNavigationAction("workspace.container.rename", "Rename container", "Workspace", "Rename the current workspace container", []string{"workspace", "container", "rename"}, []string{"workspace", "container", "rename"}, func(ctx action.Context) bool { return ctx.Route == string(RouteContainers) && ctx.ResourceID != "" }, func(ctx action.Context) Route {
			return Route{Kind: RouteContainers, ResourceID: ctx.ResourceID, Action: "edit"}
		}),
		workspaceAction("workspace.container.delete", "Delete container", "Delete the current container without unregistering workspaces", []string{"workspace", "container", "delete"}, []string{"workspace", "container", "delete"}, tuipage.WorkspaceContainerDelete, true, true),
		workspaceAction("workspace.container.add", "Add container members", "Edit workspace membership for the current container", []string{"workspace", "container", "add", "members"}, []string{"workspace", "container", "add"}, tuipage.WorkspaceContainerMembers, true, true),
		workspaceAction("workspace.container.remove", "Remove container members", "Edit workspace membership for the current container", []string{"workspace", "container", "remove", "members"}, []string{"workspace", "container", "remove"}, tuipage.WorkspaceContainerMembers, true, true),
	}
}

func instructionNavigationActions() []action.Action {
	return []action.Action{
		instructionNavigationAction("instruction.open.context", "Open Global Context", "Open managed global instruction context", "context", []string{"instruction", "global", "context"}),
		instructionNavigationAction("instruction.open.rules", "Open Global Rules", "Open managed global instruction rules", "rules", []string{"instruction", "global", "rules"}),
		instructionNavigationAction("instruction.open.sources", "Open Instruction Sources", "Open detected user-level instruction sources and source policy", "sources", []string{"instruction", "sources", "policy", "agents", "claude"}),
	}
}

func instructionNavigationAction(id, title, description, section string, keywords []string) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Instruction", Description: description, Keywords: keywords, Scope: action.ScopeGlobal,
		Run: func(context.Context, action.Context) tea.Cmd {
			return func() tea.Msg {
				return navigateMsg{route: Route{Kind: RouteInstruction, Section: section}, sibling: true}
			}
		},
	}
}

func workspaceContextNavigationAction(id, title, description, section string) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Workspace", Description: description, Keywords: []string{"workspace", "project", "context", section}, Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool { return ctx.Route == string(RouteWorkspaces) && ctx.ResourceID != "" },
		Run: func(_ context.Context, ctx action.Context) tea.Cmd {
			return func() tea.Msg {
				return navigateMsg{route: Route{Kind: RouteWorkspaces, ResourceID: ctx.ResourceID, Section: section}}
			}
		},
	}
}

func workspaceAction(id, title, description string, keywords, commandPath []string, command tuipage.WorkspaceCommand, needsResource, container bool) action.Action {
	return action.Action{
		ID: id, Title: title, Category: "Workspace", Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal,
		Available: func(ctx action.Context) bool {
			if !needsResource {
				return true
			}
			want := string(RouteWorkspaces)
			if container {
				want = string(RouteContainers)
			}
			return ctx.Route == want && ctx.ResourceID != ""
		},
		Run: func(_ context.Context, ctx action.Context) tea.Cmd {
			return func() tea.Msg { return tuipage.WorkspaceCommandMsg{Command: command, ResourceID: ctx.ResourceID} }
		},
	}
}

func navigationAction(id, title string, route Route, keywords []string, capabilities ...capability.ID) action.Action {
	return action.Action{
		ID: id, Title: "Go to " + title, Category: "App", Description: "Open the " + title + " page", Keywords: keywords,
		CommandPath: []string{"tui", string(route.Kind)}, Capabilities: capabilities, Scope: action.ScopeGlobal,
		Run: func(context.Context, action.Context) tea.Cmd {
			return func() tea.Msg { return navigateMsg{route: route, sibling: true} }
		},
	}
}

func editorNavigationAction(id, title, category, description string, keywords, commandPath []string, available func(action.Context) bool, route func(action.Context) Route) action.Action {
	return action.Action{
		ID: id, Title: title, Category: category, Description: description, Keywords: keywords, CommandPath: commandPath, Operation: operationForCommandPath(commandPath), Capabilities: capabilitiesForCommandPath(commandPath), Scope: action.ScopeGlobal, Available: available,
		Run: func(_ context.Context, ctx action.Context) tea.Cmd {
			return func() tea.Msg { return navigateMsg{route: route(ctx)} }
		},
	}
}

func operationForCommandPath(commandPath []string) capability.ID {
	id, _ := capability.ForPath(strings.Join(commandPath, " "))
	return id
}

func capabilitiesForCommandPath(commandPath []string) []capability.ID {
	id := operationForCommandPath(commandPath)
	if id == "" {
		return nil
	}
	return []capability.ID{id}
}

func actionContext(route Route) action.Context {
	return action.Context{Route: string(route.Kind), Mode: route.Mode, ResourceID: route.ResourceID, Section: route.Section, Action: route.Action}
}
