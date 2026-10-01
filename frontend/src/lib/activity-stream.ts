import { adminRequestHeaders, type ActivityEvent } from "@/lib/api"

export type ActivityStreamHandlers = {
  onReady?: () => void
  onEvent?: (event: ActivityEvent) => void
  onGap?: (from: number, to: number) => void
}

export async function streamActivity(signal: AbortSignal, handlers: ActivityStreamHandlers = {}, history = 100) {
  const path = `/api/activity/stream?history=${history}`
  const response = await fetch(path, { headers: adminRequestHeaders(path), signal })
  if (!response.ok || !response.body) {
    const message = await response.text().catch(() => "")
    throw new Error(message.trim() || `Activity stream ${response.status}`)
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  let lastSequence = 0
  while (true) {
    const { value, done } = await reader.read()
    if (done) {
      if (signal.aborted) return
      throw new Error("Activity stream ended; reconnecting to resync recent events.")
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
      if (eventType === "ready" || eventType === "heartbeat") handlers.onReady?.()
      else if (eventType === "overflow") throw new Error("Activity stream subscriber overflowed; reconnecting to resync recent events.")
      else if (eventType === "activity" && data) {
        try {
          const event = JSON.parse(data) as ActivityEvent
          const sequence = event.sequence ?? 0
          if (lastSequence > 0 && sequence > lastSequence + 1) handlers.onGap?.(lastSequence, sequence)
          if (sequence > 0) lastSequence = sequence
          handlers.onEvent?.(event)
        } catch (value) {
          if (!(value instanceof SyntaxError)) throw value
        }
      }
      boundary = buffer.indexOf("\n\n")
    }
  }
}
