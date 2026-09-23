import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { TooltipProvider } from "@/components/ui/tooltip"
import { ActivityCallPage, ActivityPage } from "@/pages/activity"

let controller: ReadableStreamDefaultController<Uint8Array> | undefined

describe("activity page", () => {
  afterEach(() => { controller = undefined; vi.unstubAllGlobals() })

  it("pauses live events, resumes them, and routes tool calls to their call id", async () => {
    const encoder = new TextEncoder()
    const callID = "019a1111-2222-7333-8444-555555555555"
    const event = { sequence: 1, call_id: callID, kind: "tool_call", tool: "run_command", workspace_id: "ws_test", received_by_instance_id: "inst_receiver", executed_by_instance_id: "inst_executor", status: "success", duration_ms: 12, message: "Command completed", raw: { call_id: callID, method: "tools/call", source: "tunnel", tool: "run_command", arguments: { workspace_id: "ws_test", command: "go test ./..." }, params: { name: "run_command", arguments: { workspace_id: "ws_test", command: "go test ./..." }, _meta: { request_id: "req_test" } }, request: { jsonrpc: "2.0", id: "call_1", method: "tools/call" }, result_type: "complete", status: "ok" }, timestamp: "2026-08-31T12:00:00Z" }
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestPath(input)
      if (path === "/api/activity/stream?history=100") return new Response(new ReadableStream<Uint8Array>({ start(value) { controller = value; value.enqueue(encoder.encode('event: ready\ndata: {"latest_sequence":0}\n\n')) } }), { status: 200, headers: { "Content-Type": "text/event-stream" } })
      if (path === `/api/activity/${callID}`) return json(event)
      throw new Error(`Unhandled test request: ${path}`)
    }))
    const user = userEvent.setup()
    const view = renderActivityRouter()
    expect(await screen.findByText("Live")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Pause" }))
    await act(async () => { controller?.enqueue(encoder.encode(`event: activity\ndata: ${JSON.stringify(event)}\n\n`)) })
    expect(await screen.findByRole("button", { name: "Resume (1)" })).toBeInTheDocument()
    expect(screen.queryByText("run_command")).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Resume (1)" }))
    await user.click(await screen.findByText("run_command"))
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(`/activity/${callID}`))
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(await screen.findByRole("tab", { name: "Overview" })).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "Metadata" }))
    expect(screen.getByText(callID)).toBeInTheDocument()
    expect(screen.getByText("inst_receiver")).toBeInTheDocument()
    expect(screen.getByText("inst_executor")).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "Raw" }))
    expect(screen.queryByRole("button", { name: "Copy JSON" })).not.toBeInTheDocument()
    expect(screen.queryByText("activity.json")).not.toBeInTheDocument()
    expect(screen.getAllByText(/go test \.\/\.\.\./).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/req_test/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/call_1/).length).toBeGreaterThan(0)
    await act(async () => { controller?.close() })
    view.unmount()
  })

  it("reconnects the activity stream without reloading the page", async () => {
    const encoder = new TextEncoder()
    const controllers: ReadableStreamDefaultController<Uint8Array>[] = []
    const fetchMock = vi.fn(async () => new Response(new ReadableStream<Uint8Array>({ start(value) { controllers.push(value); value.enqueue(encoder.encode('event: ready\ndata: {"latest_sequence":0}\n\n')) } }), { status: 200, headers: { "Content-Type": "text/event-stream" } }))
    vi.stubGlobal("fetch", fetchMock)
    const user = userEvent.setup()
    const view = renderActivityRouter()

    expect(await screen.findByText("Live")).toBeInTheDocument()
    await act(async () => { controllers[0].close() })
    expect(await screen.findByText("Disconnected")).toBeInTheDocument()
    expect(screen.getByText("Activity stream closed; use Refresh to reconnect.")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Refresh" }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(await screen.findByText("Live")).toBeInTheDocument()
    expect(screen.queryByText("Activity stream closed; use Refresh to reconnect.")).not.toBeInTheDocument()

    await act(async () => { controllers[1].close() })
    view.unmount()
  })
})

function renderActivityRouter() {
  return render(<MemoryRouter initialEntries={["/activity"]}><TooltipProvider><LocationProbe /><Routes><Route path="/activity" element={<ActivityPage />} /><Route path="/activity/:callID" element={<ActivityCallPage />} /></Routes></TooltipProvider></MemoryRouter>)
}

function LocationProbe() { const location = useLocation(); return <span className="sr-only" data-testid="location">{location.pathname}</span> }
function requestPath(input: RequestInfo | URL) { const raw = input instanceof Request ? input.url : String(input); const url = new URL(raw, "http://localhost"); return `${url.pathname}${url.search}` }
function json(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } }) }
