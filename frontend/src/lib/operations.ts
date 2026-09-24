export const canonicalOperationHeader = "X-CM-Operation-ID"

type Binding = {
  method: string
  pattern: string
  operation: string
}

const bindings: Binding[] = [
  { method: "GET", pattern: "/api/health", operation: "health.read" },
  { method: "GET", pattern: "/api/network/interfaces", operation: "network.interfaces.list" },
  { method: "GET", pattern: "/api/config", operation: "config.snapshot.read" },
  { method: "PUT", pattern: "/api/config", operation: "config.patch" },
  { method: "GET", pattern: "/api/instructions/global", operation: "instructions.settings.read" },
  { method: "PUT", pattern: "/api/instructions/global", operation: "instructions.settings.write" },
  { method: "GET", pattern: "/api/workspaces", operation: "workspace.list" },
  { method: "POST", pattern: "/api/workspaces", operation: "workspace.register" },
  { method: "GET", pattern: "/api/workspaces/{workspace_id}", operation: "workspace.show" },
  { method: "DELETE", pattern: "/api/workspaces/{workspace_id}", operation: "workspace.unregister" },
  { method: "POST", pattern: "/api/workspaces/{workspace_id}/purge", operation: "workspace.purge" },
  { method: "GET", pattern: "/api/workspaces/{workspace_id}/context", operation: "project.context.read" },
  { method: "GET", pattern: "/api/workspaces/{workspace_id}/containers", operation: "workspace.container.membership.list" },
  { method: "POST", pattern: "/api/workspaces/{workspace_id}/containers", operation: "workspace.container.add" },
  { method: "DELETE", pattern: "/api/workspaces/{workspace_id}/containers", operation: "workspace.container.remove" },
  { method: "GET", pattern: "/api/workspaces/{workspace_id}/executions", operation: "execution.list" },
  { method: "GET", pattern: "/api/workspaces/{workspace_id}/executions/stream", operation: "execution.feed" },
  { method: "GET", pattern: "/api/workspaces/{workspace_id}/executions/{execution_id}", operation: "execution.view" },
  { method: "GET", pattern: "/api/workspaces/{workspace_id}/executions/{execution_id}/stream", operation: "execution.stream" },
  { method: "GET", pattern: "/api/workspace-containers", operation: "workspace.container.list" },
  { method: "POST", pattern: "/api/workspace-containers", operation: "workspace.container.create" },
  { method: "GET", pattern: "/api/workspace-containers/{container_id}", operation: "workspace.container.show" },
  { method: "PATCH", pattern: "/api/workspace-containers/{container_id}", operation: "workspace.container.rename" },
  { method: "DELETE", pattern: "/api/workspace-containers/{container_id}", operation: "workspace.container.delete" },
  { method: "GET", pattern: "/api/workspace-containers/{container_id}/workspaces", operation: "workspace.container.membership.list" },
  { method: "POST", pattern: "/api/workspace-containers/{container_id}/workspaces", operation: "workspace.container.add" },
  { method: "DELETE", pattern: "/api/workspace-containers/{container_id}/workspaces", operation: "workspace.container.remove" },
  { method: "GET", pattern: "/api/tools", operation: "tools.inventory.read" },
  { method: "GET", pattern: "/api/requests", operation: "request.list" },
  { method: "GET", pattern: "/api/requests/stream", operation: "request.stream" },
  { method: "GET", pattern: "/api/requests/{request_id}", operation: "request.view" },
  { method: "POST", pattern: "/api/requests/{request_id}/approve", operation: "request.approve" },
  { method: "POST", pattern: "/api/requests/{request_id}/deny", operation: "request.deny" },
  { method: "GET", pattern: "/api/upstream", operation: "upstream.server.list" },
  { method: "POST", pattern: "/api/upstream", operation: "upstream.server.add" },
  { method: "GET", pattern: "/api/upstream/{server_id}", operation: "upstream.server.show" },
  { method: "PUT", pattern: "/api/upstream/{server_id}", operation: "upstream.server.configure" },
  { method: "DELETE", pattern: "/api/upstream/{server_id}", operation: "upstream.server.remove" },
  { method: "GET", pattern: "/api/upstream/{server_id}/status", operation: "upstream.server.status" },
  { method: "GET", pattern: "/api/upstream/{server_id}/tools", operation: "upstream.server.tools" },
  { method: "GET", pattern: "/api/upstream/{server_id}/auth/status", operation: "upstream.server.auth.status" },
  { method: "POST", pattern: "/api/upstream/{server_id}/auth/login", operation: "upstream.server.auth.login" },
  { method: "DELETE", pattern: "/api/upstream/{server_id}/auth/logout", operation: "upstream.server.auth.logout" },
  { method: "GET", pattern: "/api/tunnel/config", operation: "tunnel.config.read" },
  { method: "GET", pattern: "/api/tunnel", operation: "tunnel.status" },
  { method: "POST", pattern: "/api/tunnel", operation: "tunnel.enable" },
  { method: "DELETE", pattern: "/api/tunnel", operation: "tunnel.disable" },
  { method: "PUT", pattern: "/api/tunnel", operation: "tunnel.configure" },
  { method: "GET", pattern: "/api/tunnel/admin/key", operation: "tunnel.admin.key.status" },
  { method: "PUT", pattern: "/api/tunnel/admin/key", operation: "tunnel.admin.key.set" },
  { method: "POST", pattern: "/api/tunnel/admin/key", operation: "tunnel.admin.key.verify" },
  { method: "DELETE", pattern: "/api/tunnel/admin/key", operation: "tunnel.admin.key.remove" },
  { method: "GET", pattern: "/api/tunnel/managed", operation: "tunnel.list" },
  { method: "POST", pattern: "/api/tunnel/managed", operation: "tunnel.create" },
  { method: "POST", pattern: "/api/tunnel/managed/use", operation: "tunnel.use" },
  { method: "GET", pattern: "/api/tunnel/managed/{tunnel_id}", operation: "tunnel.get" },
  { method: "PUT", pattern: "/api/tunnel/managed/{tunnel_id}", operation: "tunnel.update" },
  { method: "DELETE", pattern: "/api/tunnel/managed/{tunnel_id}", operation: "tunnel.delete" },
  { method: "GET", pattern: "/api/activity/stream", operation: "activity.stream" },
  { method: "GET", pattern: "/api/activity/{call_id}", operation: "activity.view" },
]

export function browserOperationFor(method: string, rawPath: string) {
  const path = rawPath.split("?", 1)[0]
  const normalizedMethod = method.trim().toUpperCase() || "GET"
  return bindings.find((binding) =>
    binding.method === normalizedMethod && matches(binding.pattern, path)
  )?.operation
}

export function browserOperationHeaders(
  rawPath: string,
  method = "GET",
  initial?: HeadersInit,
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
