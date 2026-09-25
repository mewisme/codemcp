import { adminRequestHeaders, type ApprovalEvent, type ApprovalRequest } from "@/lib/api"

export type ApprovalStreamHandlers = {
  onReady?: (snapshot: ApprovalStreamSnapshot) => void
  onEvent?: (event: ApprovalEvent) => void
}

export type ApprovalStreamSnapshot = {
  requests: ApprovalRequest[]
  latest_sequence: number
}

export async function streamApprovals(signal: AbortSignal, handlers: ApprovalStreamHandlers = {}, workspaceID = "") {
  const query = workspaceID ? `?workspace_id=${encodeURIComponent(workspaceID)}` : ""
  const path = `/api/requests/stream${query}`
  const response = await fetch(path, { headers: adminRequestHeaders(path), signal })
  if (!response.ok || !response.body) {
    const message = await response.text().catch(() => "")
    throw new Error(message.trim() || `Approval stream ${response.status}`)
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  while (true) {
    const { value, done } = await reader.read()
    if (done) {
      if (signal.aborted) return
      throw new Error("Approval stream ended; reconnecting to resync requests.")
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
      if (eventType === "overflow") throw new Error("Approval stream overflowed; reconnecting to resync requests.")
      if (eventType === "ready" && data) {
        try {
          const snapshot = JSON.parse(data) as ApprovalStreamSnapshot
          handlers.onReady?.({ requests: snapshot.requests ?? [], latest_sequence: snapshot.latest_sequence ?? 0 })
        } catch (value) {
          if (!(value instanceof SyntaxError)) throw value
        }
      } else if (eventType.startsWith("approval.") && data) {
        try { handlers.onEvent?.(JSON.parse(data) as ApprovalEvent) } catch (value) { if (!(value instanceof SyntaxError)) throw value }
      }
      boundary = buffer.indexOf("\n\n")
    }
  }
}
