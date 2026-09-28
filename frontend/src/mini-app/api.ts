import type { LogsSnapshot } from "@/lib/api"

export type { LogEvent, LogsSnapshot } from "@/lib/api"

export type LogsQuery = {
  tail?: number
  level?: string
  components?: string
  workspace?: string
  session?: string
  event?: string
  grep?: string
}

export class LogsMiniAppError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = "LogsMiniAppError"
    this.status = status
  }
}

async function request(path: string, init?: RequestInit) {
  const response = await fetch(path, { ...init, credentials: "same-origin" })
  const text = response.status === 204 ? "" : await response.text()
  if (!response.ok) {
    throw new LogsMiniAppError(text.trim() || `Logs Mini App API ${response.status}`, response.status)
  }
  if (!text) return undefined
  try {
    return JSON.parse(text) as unknown
  } catch {
    throw new Error(`Invalid JSON response from ${path}`)
  }
}

export async function authenticateTelegramSession(initData: string) {
  await request("/mini-app/auth", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ init_data: initData }),
  })
}

export async function loadLogsSnapshot(options: LogsQuery = {}) {
  const query = new URLSearchParams()
  if (options.tail !== undefined) query.set("tail", String(options.tail))
  if (options.level) query.set("level", options.level)
  if (options.components) query.set("components", options.components)
  if (options.workspace) query.set("workspace", options.workspace)
  if (options.session) query.set("session", options.session)
  if (options.event) query.set("event", options.event)
  if (options.grep) query.set("grep", options.grep)
  const suffix = query.size ? `?${query.toString()}` : ""
  return (await request(`/mini-app/api/logs/snapshot${suffix}`)) as LogsSnapshot
}

export function snapshotEvents(snapshot: LogsSnapshot | null) {
  return snapshot?.events ?? snapshot?.Events ?? []
}
