import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import { adminToken, type ApprovalRequest } from "@/lib/api"
import { RequestsPage } from "@/pages/requests"

describe("RequestsPage", () => {
  beforeEach(() => adminToken.set("test-admin-token"))
  afterEach(() => { adminToken.clear(); vi.unstubAllGlobals() })

  it("shows approval history, filters it, and resolves a pending request", async () => {
    const user = userEvent.setup()
    const pending = request("req_pending", "pending", "cm update")
    const consumed = request("req_consumed", "consumed", "cm install")
    let items = [pending, consumed]
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input)
      if (path === "/api/requests?status=&workspace_id=ws_test") return json(items)
      if (path === "/api/requests/stream?workspace_id=ws_test") return approvalStream()
      if (path === "/api/requests/req_pending") return json(items.find((item) => item.id === pending.id))
      if (path === "/api/requests/req_pending/approve" && init?.method === "POST") {
        const approved = { ...pending, status: "approved", resolved_at: new Date().toISOString(), resolved_by: "admin" }
        items = [approved, consumed]
        return json(approved)
      }
      throw new Error(`Unhandled test request: ${path}`)
    }))

    renderPage()
    expect(await screen.findByText("Allow cm update")).toBeInTheDocument()
    expect(screen.getByText("Allow cm install")).toBeInTheDocument()
    expect(screen.getByText("1 pending")).toBeInTheDocument()

    const search = screen.getByPlaceholderText("Search request, tool, source...")
    await user.type(search, "consumed")
    expect(screen.queryByText("Allow cm update")).not.toBeInTheDocument()
    expect(screen.getByText("Allow cm install")).toBeInTheDocument()
    await user.clear(search)

    await user.click(screen.getByText("Allow cm update"))
    expect(await screen.findByText(/Control approval request · req_pending/)).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "Details" }))
    expect(screen.getByRole("code").textContent).toContain('"command": "cm update"')
    await user.click(screen.getByRole("button", { name: /Approve/ }))
    await waitFor(() => expect(screen.getAllByText("approved").length).toBeGreaterThan(0))
    await waitFor(() => expect(screen.getByText("0 pending")).toBeInTheDocument())
  })
})

function renderPage() { return render(<ThemeProvider><TooltipProvider><RequestsPage workspaceID="ws_test" /></TooltipProvider></ThemeProvider>) }

function request(id: string, status: string, command: string): ApprovalRequest {
  const now = Date.now()
  return { id, status, workspace_id: "ws_test", source: "tunnel", target_tool: "run_command", arguments: { workspace_id: "ws_test", command }, guard_code: "control_plane_mutation", guard_reason: "control-plane mutation denied", title: `Allow ${command}`, created_at: new Date(now).toISOString(), expires_at: new Date(now + 60_000).toISOString() }
}

function requestPath(input: RequestInfo | URL) { const raw = input instanceof Request ? input.url : String(input); const url = new URL(raw, "http://localhost"); return `${url.pathname}${url.search}` }
function json(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } }) }
function approvalStream() { const encoder = new TextEncoder(); const body = new ReadableStream({ start(controller) { controller.enqueue(encoder.encode('event: ready\ndata: {"latest_sequence":0}\n\n')); controller.close() } }); return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } }) }
