import type { MCPServer } from "@/lib/api"

export type MCPServerJSONItem = { key: string; server?: MCPServer; errors: string[] }
export type MCPServerJSONAnalysis = { kind: "single" | "collection"; items: MCPServerJSONItem[]; error?: string }

export function analyzeMCPServerJSON(text: string): MCPServerJSONAnalysis {
  const trimmed = text.trim()
  if (!trimmed) return { kind: "single", items: [], error: "Enter a server JSON object or an mcpServers map." }
  let root: unknown
  try { root = JSON.parse(trimmed) } catch (error) { return { kind: "single", items: [], error: error instanceof Error ? error.message : "Invalid JSON." } }
  if (Array.isArray(root)) return { kind: "collection", items: markDuplicateIDs(root.map((value, index) => normalizeItem(value, String(index + 1)))) }
  if (!isRecord(root)) return { kind: "single", items: [], error: "Top-level JSON must be an object or array." }
  if ("mcpServers" in root) {
    if (!isRecord(root.mcpServers)) return { kind: "collection", items: [], error: "mcpServers must be an object keyed by server ID." }
    const items = Object.entries(root.mcpServers).map(([id, value]) => normalizeItem(value, id, id))
    return { kind: "collection", items: markDuplicateIDs(items) }
  }
  return { kind: "single", items: [normalizeItem(root, "server")] }
}

export function formatMCPServerJSON(text: string) {
  return JSON.stringify(JSON.parse(text), null, 2)
}

export function serverJSONExample() {
  return JSON.stringify({ mcpServers: { local: { command: "node", args: ["./server.js"], env: { NODE_ENV: "production" } }, docs: { type: "http", url: "https://example.com/mcp", headers: { Authorization: "Bearer ..." } } } }, null, 2)
}

function normalizeItem(value: unknown, key: string, fallbackID = ""): MCPServerJSONItem {
  if (!isRecord(value)) return { key, errors: ["Server entry must be an object."] }
  const errors: string[] = []
  const id = stringValue(value.id, "id", errors) || fallbackID.trim()
  const command = stringValue(value.command, "command", errors)
  const url = stringValue(value.url, "url", errors)
  const rawTransport = stringValue(value.transport ?? value.type, "transport", errors).toLowerCase()
  const transport = normalizeTransport(rawTransport, command, url, errors)
  if (!id) errors.push("id is required.")
  if (transport === "stdio" && !command) errors.push("stdio server requires command.")
  if (transport === "http" && !url) errors.push("http server requires url.")
  const enabled = booleanValue(value.enabled, "enabled", errors, typeof value.disabled === "boolean" ? !value.disabled : true)
  const expose = stringValue(value.expose, "expose", errors) || "all"
  if (!["all", "allowlist", "meta_only", "none"].includes(expose)) errors.push("expose must be all, allowlist, meta_only, or none.")
  const auth = authValue(value.auth, transport, errors)
  const server: MCPServer = {
    id,
    name: stringValue(value.name, "name", errors) || id,
    transport,
    enabled,
    expose,
    tool_prefix: stringValue(value.tool_prefix, "tool_prefix", errors),
    idle_timeout_sec: numberValue(value.idle_timeout_sec, "idle_timeout_sec", errors, 600),
    args: stringArray(value.args, "args", errors),
    env: stringMap(value.env, "env", errors),
    headers: stringMap(value.headers, "headers", errors),
    tools: stringArray(value.tools, "tools", errors),
    disabled_tools: stringArray(value.disabled_tools ?? value.disabledTools, "disabled_tools", errors),
    auth,
  }
  if (transport === "stdio") {
    server.command = command
    server.cwd = stringValue(value.cwd, "cwd", errors)
    server.auth = { type: "none" }
  } else {
    server.url = url
    server.bearer_token_env_var = stringValue(value.bearer_token_env_var, "bearer_token_env_var", errors)
  }
  return { key: id || key, server: errors.length === 0 ? cleanServer(server) : undefined, errors }
}

function normalizeTransport(raw: string, command: string, url: string, errors: string[]): MCPServer["transport"] {
  if (!raw) {
    if (command) return "stdio"
    if (url) return "http"
    errors.push("transport could not be inferred; provide command or url.")
    return "http"
  }
  if (raw === "stdio") return "stdio"
  if (raw === "http" || raw === "streamable-http" || raw === "streamable_http") return "http"
  errors.push(`unsupported transport: ${raw}.`)
  return raw
}

function authValue(value: unknown, transport: string, errors: string[]) {
  if (value === undefined || value === null) return { type: transport === "http" ? "auto" : "none" }
  if (typeof value === "string") return { type: value }
  if (!isRecord(value)) { errors.push("auth must be an object or string."); return { type: transport === "http" ? "auto" : "none" } }
  const type = stringValue(value.type, "auth.type", errors) || (transport === "http" ? "auto" : "none")
  if (!["auto", "oauth", "none"].includes(type)) errors.push("auth.type must be auto, oauth, or none.")
  return { type, scope: stringValue(value.scope, "auth.scope", errors) || undefined }
}

function stringValue(value: unknown, key: string, errors: string[]) {
  if (value === undefined || value === null) return ""
  if (typeof value !== "string") { errors.push(`${key} must be a string.`); return "" }
  return value.trim()
}

function booleanValue(value: unknown, key: string, errors: string[], fallback: boolean) {
  if (value === undefined || value === null) return fallback
  if (typeof value !== "boolean") { errors.push(`${key} must be a boolean.`); return fallback }
  return value
}

function numberValue(value: unknown, key: string, errors: string[], fallback: number) {
  if (value === undefined || value === null) return fallback
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) { errors.push(`${key} must be a non-negative number.`); return fallback }
  return value
}

function stringArray(value: unknown, key: string, errors: string[]) {
  if (value === undefined || value === null) return []
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string")) { errors.push(`${key} must be an array of strings.`); return [] }
  return value.map((item) => item.trim()).filter(Boolean)
}

function stringMap(value: unknown, key: string, errors: string[]) {
  if (value === undefined || value === null) return {}
  if (!isRecord(value)) { errors.push(`${key} must be an object of string values.`); return {} }
  const result: Record<string, string> = {}
  for (const [name, item] of Object.entries(value)) {
    if (typeof item !== "string") { errors.push(`${key}.${name} must be a string.`); continue }
    result[name] = item
  }
  return result
}

function markDuplicateIDs(items: MCPServerJSONItem[]) {
  const counts = new Map<string, number>()
  for (const item of items) if (item.server?.id) counts.set(item.server.id, (counts.get(item.server.id) || 0) + 1)
  return items.map((item) => item.server && (counts.get(item.server.id) || 0) > 1 ? { ...item, server: undefined, errors: [...item.errors, `duplicate server id: ${item.server.id}.`] } : item)
}

function cleanServer(server: MCPServer): MCPServer {
  const result = { ...server }
  if (!result.tool_prefix) delete result.tool_prefix
  if (!result.cwd) delete result.cwd
  if (!result.bearer_token_env_var) delete result.bearer_token_env_var
  if (result.args?.length === 0) delete result.args
  if (result.tools?.length === 0) delete result.tools
  if (result.disabled_tools?.length === 0) delete result.disabled_tools
  if (result.env && Object.keys(result.env).length === 0) delete result.env
  if (result.headers && Object.keys(result.headers).length === 0) delete result.headers
  return result
}

function isRecord(value: unknown): value is Record<string, unknown> { return typeof value === "object" && value !== null && !Array.isArray(value) }
