import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import { WorkspaceExecutionDetail, WorkspaceExecutions } from "@/components/workspace-executions"
import { adminToken, type ExecutionInfo, type ExecutionSnapshot } from "@/lib/api"

describe("WorkspaceExecutions", () => {
  afterEach(() => { adminToken.clear(); vi.unstubAllGlobals() })

  it("routes to execution detail, hydrates snapshot, and streams stdout, stderr, and completion", async () => {
    const user = userEvent.setup()
    const execution: ExecutionInfo = { id: "exec_1", workspace_id: "ws_test", tool: "run_command", command: "pnpm test", cwd: "/projects/test", source: "mcp", started_at: new Date().toISOString(), status: "running" }
    const snapshot: ExecutionSnapshot = { execution, stdout: "start\n", stderr: "", latest_sequence: 1 }
    adminToken.set("test-admin-token")
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestPath(input)
      if (path === "/api/workspaces/ws_test/executions?limit=50") return json([execution])
      if (path === "/api/workspaces/ws_test/executions/exec_1") return json(snapshot)
      if (path === "/api/workspaces/ws_test/executions/exec_1/stream") return executionStream(snapshot)
      throw new Error(`Unhandled test request: ${path}`)
    }))

    render(<MemoryRouter initialEntries={["/workspaces/ws_test/activity"]}><ThemeProvider><TooltipProvider><LocationProbe /><Routes><Route path="/workspaces/:workspaceID/activity" element={<WorkspaceExecutions workspaceID="ws_test" />} /><Route path="/workspaces/:workspaceID/activity/:executionID" element={<WorkspaceExecutionDetail workspaceID="ws_test" executionID="exec_1" />} /></Routes></TooltipProvider></ThemeProvider></MemoryRouter>)
    await user.click(await screen.findByText("pnpm test"))
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/workspaces/ws_test/activity/exec_1"))
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole("code").textContent).toContain("start\nnext\n"))
    expect(await screen.findByText("success")).toBeInTheDocument()
    expect(screen.getByText("exit 0")).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "stderr" }))
    expect(screen.getByRole("code").textContent).toContain("warn\n")
  })

  it("streams all workspace commands into one combined view separated by execution id", async () => {
    const user = userEvent.setup()
    const scrollIntoView = vi.fn()
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView })
    adminToken.set("test-admin-token")
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestPath(input)
      if (path === "/api/workspaces/ws_test/executions?limit=50") return json([])
      if (path === "/api/workspaces/ws_test/executions/stream") return executionFeedStream()
      throw new Error(`Unhandled test request: ${path}`)
    }))

    render(<MemoryRouter initialEntries={["/workspaces/ws_test/activity"]}><ThemeProvider><TooltipProvider><WorkspaceExecutions workspaceID="ws_test" /></TooltipProvider></ThemeProvider></MemoryRouter>)
    await user.click(screen.getByRole("tab", { name: "Combined stream" }))
    await waitFor(() => expect(screen.getByRole("code").textContent).toContain("exec_id=exec_2"))
    const log = screen.getByRole("code").textContent ?? ""
    expect(log).toContain("exec_id=exec_1")
    expect(log).toContain("$ first")
    expect(log).toContain("one\n")
    expect(log).toContain("[success, exit 0]")
    expect(log).toContain("exec_id=exec_2")
    expect(log).toContain("$ second")
    expect(log).toContain("two\n")
    expect(scrollIntoView).toHaveBeenCalled()
  })
})

function LocationProbe() { const location = useLocation(); return <span className="sr-only" data-testid="location">{location.pathname}</span> }

function executionStream(snapshot: ExecutionSnapshot) {
  const encoder = new TextEncoder()
  const packets = [
    `event: ready\ndata: ${JSON.stringify(snapshot)}\n\n`,
    `id: 2\nevent: output\ndata: ${JSON.stringify({ sequence: 2, type: "output", execution_id: "exec_1", stream: "stdout", data: "next\n", timestamp: new Date().toISOString() })}\n\n`,
    `id: 3\nevent: output\ndata: ${JSON.stringify({ sequence: 3, type: "output", execution_id: "exec_1", stream: "stderr", data: "warn\n", timestamp: new Date().toISOString() })}\n\n`,
    `id: 4\nevent: completed\ndata: ${JSON.stringify({ sequence: 4, type: "completed", execution_id: "exec_1", status: "success", exit_code: 0, timestamp: new Date().toISOString() })}\n\n`,
  ]
  const body = new ReadableStream({ start(controller) { for (const packet of packets) controller.enqueue(encoder.encode(packet)); controller.close() } })
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } })
}

function executionFeedStream() {
  const encoder = new TextEncoder()
  const first = { id: "exec_1", workspace_id: "ws_test", tool: "run_command", command: "first", cwd: "/projects/test", source: "mcp", started_at: new Date().toISOString(), status: "success", exit_code: 0 }
  const second = { id: "exec_2", workspace_id: "ws_test", tool: "run_command", command: "second", cwd: "/projects/test", source: "mcp", started_at: new Date().toISOString(), status: "running" }
  const snapshot = { events: [
    { sequence: 1, type: "started", execution_id: "exec_1", workspace_id: "ws_test", execution: { ...first, status: "running", exit_code: undefined }, status: "running", timestamp: first.started_at },
    { sequence: 2, type: "output", execution_id: "exec_1", workspace_id: "ws_test", execution: { ...first, status: "running", exit_code: undefined }, stream: "stdout", data: "one\n", timestamp: first.started_at },
    { sequence: 3, type: "completed", execution_id: "exec_1", workspace_id: "ws_test", execution: first, status: "success", exit_code: 0, timestamp: first.started_at },
  ], latest_sequence: 3 }
  const packets = [
    `event: ready\ndata: ${JSON.stringify({ latest_sequence: snapshot.latest_sequence, replay_count: snapshot.events.length })}\n\n`,
    ...snapshot.events.map((event) => `id: ${event.sequence}\nevent: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`),
    `id: 4\nevent: started\ndata: ${JSON.stringify({ sequence: 4, type: "started", execution_id: "exec_2", workspace_id: "ws_test", execution: second, status: "running", timestamp: second.started_at })}\n\n`,
    `id: 5\nevent: output\ndata: ${JSON.stringify({ sequence: 5, type: "output", execution_id: "exec_2", workspace_id: "ws_test", execution: second, stream: "stderr", data: "two\n", timestamp: second.started_at })}\n\n`,
  ]
  const body = new ReadableStream({ start(controller) { for (const packet of packets) controller.enqueue(encoder.encode(packet)) } })
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } })
}

function requestPath(input: RequestInfo | URL) { const raw = input instanceof Request ? input.url : String(input); const url = new URL(raw, "http://localhost"); return `${url.pathname}${url.search}` }
function json(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } }) }
