export const canonicalOperationHeader = "X-CM-Operation-ID"

export type BrowserOperationBinding = {
  method: string
  pattern: string
  operation: string
}

const bindings: BrowserOperationBinding[] = [
  { method: "GET", pattern: "/api/health", operation: "health.read" },
  { method: "GET", pattern: "/api/status", operation: "status.overview" },
  { method: "GET", pattern: "/api/doctor", operation: "doctor.read" },
  { method: "GET", pattern: "/api/about", operation: "version.about" },
  { method: "POST", pattern: "/api/runtime/up", operation: "runtime.up" },
  { method: "POST", pattern: "/api/runtime/down", operation: "runtime.down" },
  {
    method: "POST",
    pattern: "/api/runtime/restart",
    operation: "runtime.restart",
  },
  { method: "GET", pattern: "/api/logs", operation: "logs.read" },
  { method: "GET", pattern: "/api/logs/follow", operation: "logs.follow" },
  { method: "DELETE", pattern: "/api/logs", operation: "logs.clear" },
  { method: "GET", pattern: "/api/logs/info", operation: "logs.path" },
  { method: "POST", pattern: "/api/install", operation: "install.run" },
  { method: "GET", pattern: "/api/update", operation: "update.check" },
  { method: "POST", pattern: "/api/update", operation: "update.apply" },
  { method: "GET", pattern: "/api/telemetry", operation: "telemetry.status" },
  {
    method: "GET",
    pattern: "/api/telemetry/show",
    operation: "telemetry.show",
  },
  {
    method: "POST",
    pattern: "/api/telemetry/enable",
    operation: "telemetry.enable",
  },
  {
    method: "POST",
    pattern: "/api/telemetry/disable",
    operation: "telemetry.disable",
  },
  {
    method: "GET",
    pattern: "/api/network/interfaces",
    operation: "network.interfaces.list",
  },
  { method: "GET", pattern: "/api/config", operation: "config.snapshot.read" },
  { method: "PUT", pattern: "/api/config", operation: "config.patch" },
  { method: "GET", pattern: "/api/config/path", operation: "config.path" },
  { method: "GET", pattern: "/api/config/verify", operation: "config.verify" },
  { method: "GET", pattern: "/api/settings/export", operation: "config.export" },
  { method: "GET", pattern: "/api/settings", operation: "config.list" },
  { method: "GET", pattern: "/api/settings/{setting_key}", operation: "config.get" },
  { method: "PUT", pattern: "/api/settings/{setting_key}", operation: "config.set" },
  { method: "GET", pattern: "/api/auth", operation: "auth.status" },
  { method: "POST", pattern: "/api/auth/mcp/rotate", operation: "auth.mcp.rotate" },
  { method: "POST", pattern: "/api/auth/mcp/enable", operation: "auth.mcp.enable" },
  { method: "POST", pattern: "/api/auth/mcp/disable", operation: "auth.mcp.disable" },
  { method: "POST", pattern: "/api/auth/admin/rotate", operation: "auth.admin.rotate" },
  { method: "POST", pattern: "/api/auth/admin/enable", operation: "auth.admin.enable" },
  { method: "POST", pattern: "/api/auth/admin/disable", operation: "auth.admin.disable" },
  { method: "PUT", pattern: "/api/telegram/setup", operation: "telegram.setup" },
  { method: "GET", pattern: "/api/notifications", operation: "notification.status" },
  {
    method: "GET",
    pattern: "/api/integrations/rtk",
    operation: "integration.rtk.status",
  },
  {
    method: "POST",
    pattern: "/api/integrations/rtk/enable",
    operation: "integration.rtk.enable",
  },
  {
    method: "POST",
    pattern: "/api/integrations/rtk/disable",
    operation: "integration.rtk.disable",
  },
  {
    method: "POST",
    pattern: "/api/integrations/rtk/probe",
    operation: "integration.rtk.probe",
  },
  {
    method: "POST",
    pattern: "/api/integrations/rtk/install",
    operation: "integration.rtk.install",
  },
  {
    method: "GET",
    pattern: "/api/integrations/rtk/global",
    operation: "integration.rtk.install.global",
  },
  {
    method: "GET",
    pattern: "/api/integrations/codegraph",
    operation: "integration.codegraph.status",
  },
  {
    method: "POST",
    pattern: "/api/integrations/codegraph/probe",
    operation: "integration.codegraph.probe",
  },
  {
    method: "POST",
    pattern: "/api/integrations/codegraph/install",
    operation: "integration.codegraph.install",
  },
  {
    method: "GET",
    pattern: "/api/integrations/codegraph/global",
    operation: "integration.codegraph.install.global",
  },
  { method: "GET", pattern: "/api/integrations/cf", operation: "integration.cf.status" },
  { method: "POST", pattern: "/api/integrations/cf/probe", operation: "integration.cf.probe" },
  { method: "POST", pattern: "/api/integrations/cf/install", operation: "integration.cf.install" },
  { method: "POST", pattern: "/api/integrations/cf/update", operation: "integration.cf.update" },
  { method: "DELETE", pattern: "/api/integrations/cf", operation: "integration.cf.remove" },
  {
    method: "GET",
    pattern: "/api/integrations/typesafe",
    operation: "integration.typesafe.status",
  },
  {
    method: "GET",
    pattern: "/api/integrations/typesafe/doctor",
    operation: "integration.typesafe.doctor",
  },
  {
    method: "POST",
    pattern: "/api/integrations/typesafe/probe",
    operation: "integration.typesafe.probe",
  },
  {
    method: "POST",
    pattern: "/api/integrations/typesafe/enable",
    operation: "integration.typesafe.enable",
  },
  {
    method: "POST",
    pattern: "/api/integrations/typesafe/disable",
    operation: "integration.typesafe.disable",
  },
  { method: "GET", pattern: "/api/prompts", operation: "prompt.list" },
  { method: "POST", pattern: "/api/prompts", operation: "prompt.create" },
  { method: "GET", pattern: "/api/prompts/{name}", operation: "prompt.get" },
  { method: "PUT", pattern: "/api/prompts/{name}", operation: "prompt.update" },
  {
    method: "DELETE",
    pattern: "/api/prompts/{name}",
    operation: "prompt.delete",
  },
  { method: "GET", pattern: "/api/workspaces", operation: "workspace.list" },
  {
    method: "POST",
    pattern: "/api/workspaces",
    operation: "workspace.register",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}",
    operation: "workspace.show",
  },
  {
    method: "DELETE",
    pattern: "/api/workspaces/{workspace_id}",
    operation: "workspace.unregister",
  },
  {
    method: "POST",
    pattern: "/api/workspaces/{workspace_id}/relocate",
    operation: "workspace.relocate",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/access",
    operation: "workspace.access.list",
  },
  {
    method: "POST",
    pattern: "/api/workspaces/{workspace_id}/access",
    operation: "workspace.access.add",
  },
  {
    method: "DELETE",
    pattern: "/api/workspaces/{workspace_id}/access",
    operation: "workspace.access.remove",
  },
  {
    method: "POST",
    pattern: "/api/workspaces/{workspace_id}/purge",
    operation: "workspace.purge",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/context",
    operation: "project.context.read",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/containers",
    operation: "workspace.container.membership.list",
  },
  {
    method: "POST",
    pattern: "/api/workspaces/{workspace_id}/containers",
    operation: "workspace.container.add",
  },
  {
    method: "DELETE",
    pattern: "/api/workspaces/{workspace_id}/containers",
    operation: "workspace.container.remove",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/executions",
    operation: "execution.list",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/executions/stream",
    operation: "execution.feed",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/executions/{execution_id}",
    operation: "execution.view",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/executions/{execution_id}/stream",
    operation: "execution.stream",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/processes",
    operation: "process.list",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/processes/{process_id}",
    operation: "process.view",
  },
  {
    method: "DELETE",
    pattern: "/api/workspaces/{workspace_id}/processes/{process_id}",
    operation: "process.clear",
  },
  {
    method: "GET",
    pattern: "/api/workspaces/{workspace_id}/integrations/codegraph",
    operation: "integration.codegraph.workspace.status",
  },
  {
    method: "POST",
    pattern: "/api/workspaces/{workspace_id}/integrations/codegraph/init",
    operation: "integration.codegraph.workspace.init",
  },
  {
    method: "POST",
    pattern: "/api/workspaces/{workspace_id}/integrations/codegraph/sync",
    operation: "integration.codegraph.workspace.sync",
  },
  {
    method: "GET",
    pattern: "/api/workspace-containers",
    operation: "workspace.container.list",
  },
  {
    method: "POST",
    pattern: "/api/workspace-containers",
    operation: "workspace.container.create",
  },
  {
    method: "GET",
    pattern: "/api/workspace-containers/{container_id}",
    operation: "workspace.container.show",
  },
  {
    method: "PATCH",
    pattern: "/api/workspace-containers/{container_id}",
    operation: "workspace.container.rename",
  },
  {
    method: "DELETE",
    pattern: "/api/workspace-containers/{container_id}",
    operation: "workspace.container.delete",
  },
  {
    method: "GET",
    pattern: "/api/workspace-containers/{container_id}/workspaces",
    operation: "workspace.container.membership.list",
  },
  {
    method: "POST",
    pattern: "/api/workspace-containers/{container_id}/workspaces",
    operation: "workspace.container.add",
  },
  {
    method: "DELETE",
    pattern: "/api/workspace-containers/{container_id}/workspaces",
    operation: "workspace.container.remove",
  },
  { method: "GET", pattern: "/api/tools", operation: "tools.inventory.read" },
  { method: "GET", pattern: "/api/requests", operation: "request.list" },
  {
    method: "GET",
    pattern: "/api/requests/stream",
    operation: "request.stream",
  },
  {
    method: "GET",
    pattern: "/api/requests/{request_id}",
    operation: "request.view",
  },
  {
    method: "POST",
    pattern: "/api/requests/{request_id}/approve",
    operation: "request.approve",
  },
  {
    method: "POST",
    pattern: "/api/requests/{request_id}/deny",
    operation: "request.deny",
  },
  {
    method: "GET",
    pattern: "/api/requests/explain/status",
    operation: "request.explain.status",
  },
  {
    method: "GET",
    pattern: "/api/requests/{request_id}/explanation",
    operation: "request.explanation.view",
  },
  {
    method: "POST",
    pattern: "/api/requests/{request_id}/explain",
    operation: "request.explain",
  },
  { method: "GET", pattern: "/api/requests/grants", operation: "request.grant.list" },
  {
    method: "POST",
    pattern: "/api/requests/grants/{request_id}/revoke",
    operation: "request.grant.revoke",
  },
  { method: "GET", pattern: "/api/llm/status", operation: "llm.status" },
  {
    method: "GET",
    pattern: "/api/llm/providers",
    operation: "llm.provider.list",
  },
  {
    method: "POST",
    pattern: "/api/llm/providers",
    operation: "llm.provider.add",
  },
  {
    method: "GET",
    pattern: "/api/llm/providers/{provider_id}",
    operation: "llm.provider.get",
  },
  {
    method: "PUT",
    pattern: "/api/llm/providers/{provider_id}",
    operation: "llm.provider.configure",
  },
  {
    method: "DELETE",
    pattern: "/api/llm/providers/{provider_id}",
    operation: "llm.provider.remove",
  },
  {
    method: "POST",
    pattern: "/api/llm/providers/{provider_id}/select",
    operation: "llm.provider.select",
  },
  {
    method: "GET",
    pattern: "/api/llm/providers/{provider_id}/models",
    operation: "llm.provider.models",
  },
  {
    method: "POST",
    pattern: "/api/llm/providers/{provider_id}/probe",
    operation: "llm.provider.probe",
  },
  {
    method: "PUT",
    pattern: "/api/llm/providers/{provider_id}/credential",
    operation: "llm.provider.credential.set",
  },
  {
    method: "DELETE",
    pattern: "/api/llm/providers/{provider_id}/credential",
    operation: "llm.provider.credential.clear",
  },
  { method: "GET", pattern: "/api/completions", operation: "completion.list" },
  {
    method: "GET",
    pattern: "/api/completions/current",
    operation: "completion.current",
  },
  {
    method: "GET",
    pattern: "/api/completions/stream",
    operation: "completion.feed",
  },
  {
    method: "GET",
    pattern: "/api/completions/view/{completion_id}",
    operation: "completion.view",
  },
  {
    method: "GET",
    pattern: "/api/completions/doctor",
    operation: "completion.doctor",
  },
  {
    method: "GET",
    pattern: "/api/upstream",
    operation: "upstream.server.list",
  },
  {
    method: "POST",
    pattern: "/api/upstream",
    operation: "upstream.server.add",
  },
  {
    method: "GET",
    pattern: "/api/upstream/{server_id}",
    operation: "upstream.server.show",
  },
  {
    method: "PUT",
    pattern: "/api/upstream/{server_id}",
    operation: "upstream.server.configure",
  },
  {
    method: "DELETE",
    pattern: "/api/upstream/{server_id}",
    operation: "upstream.server.remove",
  },
  {
    method: "POST",
    pattern: "/api/upstream/{server_id}/enable",
    operation: "upstream.server.enable",
  },
  {
    method: "POST",
    pattern: "/api/upstream/{server_id}/disable",
    operation: "upstream.server.disable",
  },
  {
    method: "GET",
    pattern: "/api/upstream/{server_id}/status",
    operation: "upstream.server.status",
  },
  {
    method: "GET",
    pattern: "/api/upstream/{server_id}/tools",
    operation: "upstream.server.tools",
  },
  {
    method: "GET",
    pattern: "/api/upstream/{server_id}/auth/status",
    operation: "upstream.server.auth.status",
  },
  {
    method: "POST",
    pattern: "/api/upstream/{server_id}/auth/login",
    operation: "upstream.server.auth.login",
  },
  {
    method: "DELETE",
    pattern: "/api/upstream/{server_id}/auth/logout",
    operation: "upstream.server.auth.logout",
  },
  {
    method: "GET",
    pattern: "/api/tunnel/config",
    operation: "tunnel.config.read",
  },
  { method: "GET", pattern: "/api/tunnel", operation: "tunnel.status" },
  { method: "POST", pattern: "/api/tunnel/sync", operation: "tunnel.sync" },
  { method: "POST", pattern: "/api/tunnel", operation: "tunnel.enable" },
  { method: "DELETE", pattern: "/api/tunnel", operation: "tunnel.disable" },
  { method: "PUT", pattern: "/api/tunnel", operation: "tunnel.configure" },
  {
    method: "GET",
    pattern: "/api/tunnel/admin/key",
    operation: "tunnel.admin.key.status",
  },
  {
    method: "PUT",
    pattern: "/api/tunnel/admin/key",
    operation: "tunnel.admin.key.set",
  },
  {
    method: "POST",
    pattern: "/api/tunnel/admin/key",
    operation: "tunnel.admin.key.verify",
  },
  {
    method: "DELETE",
    pattern: "/api/tunnel/admin/key",
    operation: "tunnel.admin.key.remove",
  },
  { method: "GET", pattern: "/api/tunnel/managed", operation: "tunnel.list" },
  {
    method: "POST",
    pattern: "/api/tunnel/managed",
    operation: "tunnel.create",
  },
  {
    method: "POST",
    pattern: "/api/tunnel/managed/use",
    operation: "tunnel.use",
  },
  {
    method: "GET",
    pattern: "/api/tunnel/managed/{tunnel_id}",
    operation: "tunnel.get",
  },
  {
    method: "PUT",
    pattern: "/api/tunnel/managed/{tunnel_id}",
    operation: "tunnel.update",
  },
  {
    method: "DELETE",
    pattern: "/api/tunnel/managed/{tunnel_id}",
    operation: "tunnel.delete",
  },
  {
    method: "GET",
    pattern: "/api/activity/stream",
    operation: "activity.stream",
  },
  {
    method: "GET",
    pattern: "/api/activity/{call_id}",
    operation: "activity.view",
  },
]

export function browserOperationBindings() {
  return bindings.map((binding) => ({ ...binding }))
}

export function browserOperationFor(method: string, rawPath: string) {
  const path = rawPath.split("?", 1)[0]
  const normalizedMethod = method.trim().toUpperCase() || "GET"
  return bindings.find(
    (binding) =>
      binding.method === normalizedMethod && matches(binding.pattern, path)
  )?.operation
}

export function browserOperationHeaders(
  rawPath: string,
  method = "GET",
  initial?: HeadersInit
) {
  const headers = new Headers(initial)
  const operation = browserOperationFor(method, rawPath)
  if (operation) headers.set(canonicalOperationHeader, operation)
  return headers
}

function matches(pattern: string, path: string) {
  const expected = pattern.split("/").filter(Boolean)
  const actual = path.split("/").filter(Boolean)
  if (expected.length !== actual.length) return false
  return expected.every((part, index) =>
    part.startsWith("{") && part.endsWith("}")
      ? Boolean(actual[index])
      : part === actual[index]
  )
}
