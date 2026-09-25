import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import { adminToken, type CompletionRecord } from "@/lib/api"
import { CompletionsPage } from "@/pages/completions"

describe("CompletionsPage", () => {
  beforeEach(() => adminToken.set("test-admin-token"))
  afterEach(() => {
    adminToken.clear()
    vi.unstubAllGlobals()
  })

  it("shows read-only durable history, consumes the live feed, and opens detail", async () => {
    const user = userEvent.setup()
    const first = completion("completion_first", 1, "partial", "Partial result")
    const second = completion("completion_second", 2, "completed", "Finished work")
    const calls: Array<{ path: string; method: string }> = []

    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input)
      const method = init?.method ?? "GET"
      calls.push({ path, method })
      if (path === "/api/completions?limit=100") return json([first])
      if (path === "/api/completions/stream?limit=100") return completionStream([first], second)
      if (path === "/api/completions/view/completion_second") return json(second)
      throw new Error("Unhandled test request: " + path)
    }))

    renderPage()
    expect(await screen.findByText("Partial result")).toBeInTheDocument()
    expect(await screen.findByText("Finished work")).toBeInTheDocument()
    expect(screen.getByText("Live")).toBeInTheDocument()

    await user.click(screen.getByText("Finished work"))
    expect(await screen.findByText(/Agent completion · completion_second/)).toBeInTheDocument()
    expect(screen.getAllByText("Final summary").length).toBeGreaterThan(0)

    await waitFor(() => expect(calls.some((call) => call.path === "/api/completions/view/completion_second")).toBe(true))
    expect(calls.every((call) => call.method === "GET")).toBe(true)
  })
})

function renderPage() {
  return render(<ThemeProvider><TooltipProvider><CompletionsPage /></TooltipProvider></ThemeProvider>)
}

function completion(id: string, sequence: number, status: string, title: string): CompletionRecord {
  return {
    id,
    sequence,
    agent_id: "agent_test",
    workspace_id: "ws_test",
    status,
    title,
    summary: status === "completed" ? "Final summary" : "Partial summary",
    source: "mcp",
    created_at: new Date(1_700_000_000_000 + sequence * 1000).toISOString(),
  }
}

function requestPath(input: RequestInfo | URL) {
  const raw = input instanceof Request ? input.url : String(input)
  const url = new URL(raw, "http://localhost")
  return `${url.pathname}${url.search}`
}

function json(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}

function completionStream(records: CompletionRecord[], eventRecord: CompletionRecord) {
  const encoder = new TextEncoder()
  const snapshot = JSON.stringify({ latest_sequence: records.at(-1)?.sequence ?? 0, records })
  const event = JSON.stringify({
    id: "completion-event:" + eventRecord.id,
    sequence: eventRecord.sequence,
    name: "completion.accepted",
    record: eventRecord,
    timestamp: eventRecord.created_at,
  })
  const body = new ReadableStream({
    start(controller) {
      controller.enqueue(encoder.encode(`event: ready\ndata: ${snapshot}\n\n`))
      controller.enqueue(encoder.encode(`id: ${eventRecord.sequence}\nevent: completion.accepted\ndata: ${event}\n\n`))
    },
  })
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  })
}
