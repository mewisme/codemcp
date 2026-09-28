import type { ActivityEvent, ExecutionFeedEvent, ExecutionInfo, LogEvent } from "@/lib/api"

export type MiniAppFeed = "runtime" | "executions" | "tools"
export type StreamState = "connecting" | "live" | "reconnecting" | "suspended" | "disconnected"

export type ToolRecord = {
  call_id: string
  first: ActivityEvent
  latest: ActivityEvent
}

export type RuntimeSnapshot = {
  events: LogEvent[]
  session?: string
  total: number
  truncated: boolean
  latest_sequence: number
}

export type ExecutionSnapshot = {
  events: ExecutionFeedEvent[]
  executions: ExecutionInfo[]
  latest_sequence: number
}

export type ToolSnapshot = {
  events: ActivityEvent[]
  records: ToolRecord[]
  latest_sequence: number
}

export type MiniAppStreamFrame = {
  type: "snapshot" | "event" | "heartbeat" | "resync" | "error"
  feed: MiniAppFeed
  sequence?: number
  latest_sequence?: number
  reason?: string
  payload?: unknown
}

export function streamURL(feed: MiniAppFeed) {
  const url = new URL("/api/stream", window.location.href)
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:"
  url.searchParams.set("feed", feed)
  return url.toString()
}

export function parseStreamFrame(raw: string): MiniAppStreamFrame | null {
  try {
    const value = JSON.parse(raw) as MiniAppStreamFrame
    if (!value || !["snapshot", "event", "heartbeat", "resync", "error"].includes(value.type)) return null
    if (!["runtime", "executions", "tools"].includes(value.feed)) return null
    return value
  } catch {
    return null
  }
}

export function boundedAppend<T>(items: T[], item: T, limit = 1000) {
  const next = [...items, item]
  return next.length > limit ? next.slice(next.length - limit) : next
}

export function upsertExecution(items: ExecutionInfo[], execution: ExecutionInfo) {
  const index = items.findIndex((item) => item.id === execution.id)
  if (index < 0) return boundedAppend(items, execution, 1000)
  const next = items.slice()
  next[index] = { ...next[index], ...execution }
  return next
}

export function applyExecutionEvent(items: ExecutionInfo[], event: ExecutionFeedEvent) {
  if (event.execution) return upsertExecution(items, event.execution)
  const index = items.findIndex((item) => item.id === event.execution_id)
  if (index < 0) return items
  const next = items.slice()
  next[index] = {
    ...next[index],
    status: event.status || next[index].status,
    exit_code: event.exit_code ?? next[index].exit_code,
    timed_out: event.timed_out ?? next[index].timed_out,
    finished_at: event.type === "completed" ? event.timestamp : next[index].finished_at,
  }
  return next
}

export function applyToolEvent(records: ToolRecord[], event: ActivityEvent) {
  const callID = event.call_id
  if (!callID) return records
  const index = records.findIndex((item) => item.call_id === callID)
  if (index < 0) return boundedAppend(records, { call_id: callID, first: event, latest: event }, 1000)
  const next = records.slice()
  next[index] = { ...next[index], latest: event }
  return next
}
