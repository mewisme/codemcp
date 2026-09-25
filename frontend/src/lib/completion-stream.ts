import { adminRequestHeaders, type CompletionEvent, type CompletionSnapshot } from "@/lib/api"

export type CompletionStreamHandlers = {
  onReady?: (snapshot: CompletionSnapshot) => void
  onEvent?: (event: CompletionEvent) => void
}

export async function streamCompletions(
  signal: AbortSignal,
  handlers: CompletionStreamHandlers = {},
  workspaceID = "",
  limit = 100,
) {
  const query = new URLSearchParams({ limit: String(limit) })
  if (workspaceID) query.set("workspace_id", workspaceID)
  const path = `/api/completions/stream?${query}`
  const response = await fetch(path, { headers: adminRequestHeaders(path), signal })
  if (!response.ok || !response.body) {
    const message = await response.text().catch(() => "")
    throw new Error(message.trim() || `Completion stream ${response.status}`)
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  while (true) {
    const { value, done } = await reader.read()
    if (done) {
      if (signal.aborted) return
      throw new Error("Completion stream ended; reconnecting to resync history.")
    }
    buffer += decoder.decode(value, { stream: true })
    let boundary = buffer.indexOf("\n\n")
    while (boundary >= 0) {
      const packet = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      let eventType = "message"
      let data = ""
      for (const line of packet.split("\n")) {
        if (line.startsWith("event: ")) eventType = line.slice(7).trim()
        if (line.startsWith("data: ")) data += line.slice(6)
      }
      if (eventType === "overflow") {
        throw new Error("Completion stream overflowed; reconnecting to resync history.")
      }
      if (eventType === "ready" && data) {
        try {
          const snapshot = JSON.parse(data) as CompletionSnapshot
          handlers.onReady?.({
            latest_sequence: snapshot.latest_sequence ?? 0,
            records: snapshot.records ?? [],
          })
        } catch (value) {
          if (!(value instanceof SyntaxError)) throw value
        }
      } else if (eventType.startsWith("completion.") && data) {
        try {
          handlers.onEvent?.(JSON.parse(data) as CompletionEvent)
        } catch (value) {
          if (!(value instanceof SyntaxError)) throw value
        }
      }
      boundary = buffer.indexOf("\n\n")
    }
  }
}
