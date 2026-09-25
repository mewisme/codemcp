import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { toast } from "sonner"
import { RequestApprovalHost } from "@/components/request-approval-host"
import { ThemeProvider } from "@/components/theme-provider"
import { adminToken, type ApprovalRequest } from "@/lib/api"

vi.mock("sonner", () => ({ toast: { warning: vi.fn() } }))

describe("RequestApprovalHost", () => {
  beforeEach(() => adminToken.set("test-admin-token"))
  afterEach(() => {
    adminToken.clear()
    vi.mocked(toast.warning).mockReset()
    vi.unstubAllGlobals()
  })

  it("shows queued requests with exact arguments and resolves them in order", async () => {
    const user = userEvent.setup()
    let pending = [
      request("req_first", "cm update"),
      request("req_second", "cm install"),
    ]
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = requestPath(input)
        if (path === "/api/requests/stream") return approvalStream(pending)
        if (
          path === "/api/requests/req_first/approve" &&
          init?.method === "POST"
        ) {
          const resolved = { ...pending[0], status: "approved" }
          pending = pending.slice(1)
          return json(resolved)
        }
        if (
          path === "/api/requests/req_second/deny" &&
          init?.method === "POST"
        ) {
          const resolved = { ...pending[0], status: "denied" }
          pending = []
          return json(resolved)
        }
        throw new Error(`Unhandled test request: ${path}`)
      })
    )

    renderHost()
    expect(await screen.findByText("Allow cm update")).toBeInTheDocument()
    expect(
      screen.getByText("Control approval request · 1 of 2")
    ).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "Details" }))
    expect(screen.getByRole("code").textContent).toContain(
      '"command": "cm update"'
    )
    await user.click(screen.getByRole("button", { name: /Approve/ }))
    expect(await screen.findByText("Allow cm install")).toBeInTheDocument()
    expect(
      screen.getByText("Control approval request · 1 of 1")
    ).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Deny" }))
    await waitFor(() =>
      expect(screen.queryByText("Allow cm install")).not.toBeInTheDocument()
    )
  })

  it("loads only the new request detail when the approval SSE reports a pending request", async () => {
    let listCalls = 0
    const pending = request("req_stream", "cm update --version v2")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === "/api/requests?status=pending") {
          listCalls++
          return json([])
        }
        if (path === "/api/requests/stream")
          return approvalStream([], {
            name: "approval.pending",
            request_id: pending.id,
          })
        if (path === "/api/requests/req_stream") return json(pending)
        throw new Error(`Unhandled test request: ${path}`)
      })
    )

    renderHost()
    expect(
      await screen.findByText("Allow cm update --version v2")
    ).toBeInTheDocument()
    expect(listCalls).toBe(0)
    expect(toast.warning).toHaveBeenCalledWith("Control approval requested", expect.objectContaining({ description: expect.stringContaining("ws_test"), action: expect.objectContaining({ label: "Review" }) }))
  })

  it("drops a stale dialog immediately when another surface resolves it", async () => {
    const pending = request("req_remote", "cm update --remote")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === "/api/requests/stream")
          return approvalStream([pending], { name: "approval.approved", request_id: pending.id }, 500)
        throw new Error(`Unhandled test request: ${path}`)
      })
    )

    renderHost()
    expect(await screen.findByText("Allow cm update --remote")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Approve/ })).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByText("Allow cm update --remote")).not.toBeInTheDocument()
    )
  })

  it("drops a stale dialog when resolving it reports a conflict", async () => {
    const user = userEvent.setup()
    let pending = [request("req_stale", "cm update")]
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = requestPath(input)
        if (path === "/api/requests?status=pending") return json(pending)
        if (path === "/api/requests/stream") return approvalStream(pending)
        if (
          path === "/api/requests/req_stale/approve" &&
          init?.method === "POST"
        ) {
          pending = []
          return new Response("approval request is already resolved", {
            status: 409,
          })
        }
        throw new Error(`Unhandled test request: ${path}`)
      })
    )

    renderHost()
    expect(await screen.findByText("Allow cm update")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /Approve/ }))
    await waitFor(() =>
      expect(screen.queryByText("Allow cm update")).not.toBeInTheDocument()
    )
  })
})

function renderHost() {
  return render(
    <ThemeProvider>
      <RequestApprovalHost />
    </ThemeProvider>
  )
}

function request(id: string, command: string): ApprovalRequest {
  const now = Date.now()
  return {
    id,
    status: "pending",
    workspace_id: "ws_test",
    session_hash: "hash-session",
    source: "tunnel",
    target_tool: "run_command",
    arguments: { workspace_id: "ws_test", command },
    guard_code: "control_plane_mutation",
    guard_reason: "control-plane mutation denied",
    title: `Allow ${command}`,
    created_at: new Date(now).toISOString(),
    expires_at: new Date(now + 60_000).toISOString(),
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

function approvalStream(
  snapshot: ApprovalRequest[] = [],
  event?: { name: string; request_id: string },
  eventDelayMS = 0
) {
  const encoder = new TextEncoder()
  const body = new ReadableStream({
    start(controller) {
      controller.enqueue(
        encoder.encode(
          "event: ready\ndata: " + JSON.stringify({ latest_sequence: 0, requests: snapshot }) + "\n\n"
        )
      )
      const publishEvent = () => {
        if (event)
          controller.enqueue(
            encoder.encode(
              `event: ${event.name}\ndata: ${JSON.stringify({ ...event, subject: "request", workspace_id: "ws_test", target_tool: "run_command", status: "pending", created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 60_000).toISOString(), timestamp: new Date().toISOString() })}\n\n`
            )
          )
        controller.close()
      }
      if (event && eventDelayMS > 0) window.setTimeout(publishEvent, eventDelayMS)
      else publishEvent()
    },
  })
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  })
}
