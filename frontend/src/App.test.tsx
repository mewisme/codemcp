import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { RouterProvider } from "react-router-dom"
import { ThemeProvider } from "@/components/theme-provider"
import { adminDocumentTitle, navItems } from "@/lib/admin-navigation"
import { adminToken } from "@/lib/api"
import { createAdminRouter } from "@/router"

const config = {
  server: {
    port: 37421,
    expose: { mode: "none", interfaces: [] },
    allow_insecure_http: false,
  },
  admin: { enabled: true, port: 37422 },
  auth: {
    mcp_enabled: true,
    admin_enabled: true,
    mcp_token_configured: true,
    admin_token_configured: true,
  },
  permissions: { allow_dirs: [] },
  shell: { path: [] },
  features: { ponytail: { active: true, mode: "full" }, caveman: { active: true, mode: "full" } },
}
const tunnel = {
  provider: "openai",
  enabled: false,
  running: false,
  ready: false,
}

describe("admin app runtime smoke", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/overview")
    adminToken.set("test-admin-token")
    vi.stubGlobal("fetch", vi.fn(mockFetch))
    document.title = ""
  })

  afterEach(() => {
    adminToken.clear()
    vi.unstubAllGlobals()
  })

  it("mounts the authenticated shell and every main page with the correct document title", async () => {
    const user = userEvent.setup()
    renderAdminApp()

    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Overview"))
    )
    expect(
      await screen.findByText("Active runtime listeners and endpoints.")
    ).toBeInTheDocument()

    const pageSmokeText: Record<string, string> = {
      workspaces: "Register workspace",
      instructions:
        "Manage global context, rules, and detected user-level instruction sources.",
      tools:
        "Inspect every tool exposed by the local runtime and enabled upstream servers, including schemas and behavioral hints.",
      servers: "Add MCP server",
      tunnel: "OpenAI Secure MCP Tunnel",
      activity:
        "Live MCP requests, tool calls, and runtime lifecycle events. Tool calls open as addressable child routes.",
      settings: "Runtime",
    }
    for (const item of navItems.slice(1)) {
      await user.click(screen.getByRole("link", { name: item.title }))
      await waitFor(() =>
        expect(document.title).toBe(adminDocumentTitle(item.title))
      )
      expect(window.location.pathname).toBe(item.path)
      expect(
        (await screen.findAllByText(pageSmokeText[item.id])).length
      ).toBeGreaterThan(0)
    }
  }, 15_000)

  it("loads a deep-linked page and follows popstate navigation", async () => {
    window.history.replaceState({}, "", "/tunnel")
    renderAdminApp()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Tunnel"))
    )
    expect(
      (await screen.findAllByText("OpenAI Secure MCP Tunnel")).length
    ).toBeGreaterThan(0)
    window.history.pushState({}, "", "/tools")
    window.dispatchEvent(new PopStateEvent("popstate"))
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Tools"))
    )
    expect(
      await screen.findByText(
        "Inspect every tool exposed by the local runtime and enabled upstream servers, including schemas and behavioral hints."
      )
    ).toBeInTheDocument()
  })

  it("navigates from the workspace list to the workspace-id child route", async () => {
    const user = userEvent.setup()
    const workspace = { id: "ws_test", path: "/projects/test", allow_dirs: [] }
    window.history.replaceState({}, "", "/workspaces")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === "/api/workspaces") return json([workspace])
        if (path === "/api/workspace-containers") return json([])
        if (path === "/api/workspaces/ws_test") return json(workspace)
        if (
          path ===
          "/api/workspaces/ws_test/context?include_git=true&include_memory=true&include_skills=true"
        )
          return json(projectContextFixture())
        return mockFetch(input)
      })
    )
    renderAdminApp()
    await user.click(await screen.findByText("/projects/test"))
    await waitFor(() =>
      expect(window.location.pathname).toBe("/workspaces/ws_test")
    )
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Workspace"))
    )
    expect((await screen.findAllByText("ws_test")).length).toBeGreaterThan(0)
    await user.click(screen.getByRole("tab", { name: "Context" }))
    await waitFor(() =>
      expect(window.location.pathname).toBe("/workspaces/ws_test/context")
    )
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Project Context"))
    )
    expect(
      await screen.findByText("EFFECTIVE PROJECT CONTEXT")
    ).toBeInTheDocument()
  })

  it("deep-links workspace-scoped approval requests", async () => {
    const workspace = { id: "ws_test", path: "/projects/test", allow_dirs: [] }
    window.history.replaceState({}, "", "/workspaces/ws_test/requests")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === "/api/workspaces/ws_test") return json(workspace)
        if (path === "/api/requests?status=&workspace_id=ws_test")
          return json([])
        if (path === "/api/requests/stream?workspace_id=ws_test")
          return approvalStream()
        return mockFetch(input)
      })
    )
    renderAdminApp()
    await waitFor(() =>
      expect(window.location.pathname).toBe("/workspaces/ws_test/requests")
    )
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Workspace Requests"))
    )
    expect(
      await screen.findByText(
        "Review control approvals and resolved request history for ws_test."
      )
    ).toBeInTheDocument()
  })

  it("deep-links project context and renders the exact effective instructions", async () => {
    const workspace = { id: "ws_test", path: "/projects/test", allow_dirs: [] }
    window.history.replaceState({}, "", "/workspaces/ws_test/context")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === "/api/workspaces/ws_test") return json(workspace)
        if (
          path ===
          "/api/workspaces/ws_test/context?include_git=true&include_memory=true&include_skills=true"
        )
          return json(projectContextFixture())
        return mockFetch(input)
      })
    )
    renderAdminApp()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Project Context"))
    )
    expect(
      await screen.findByText("EFFECTIVE PROJECT CONTEXT")
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe("/workspaces/ws_test/context")
  })

  it("deep-links workspace activity and loads workspace-scoped executions", async () => {
    const workspace = { id: "ws_test", path: "/projects/test", allow_dirs: [] }
    window.history.replaceState({}, "", "/workspaces/ws_test/activity")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === "/api/workspaces/ws_test") return json(workspace)
        if (path === "/api/workspaces/ws_test/executions?limit=50")
          return json([])
        return mockFetch(input)
      })
    )
    renderAdminApp()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Workspace Activity"))
    )
    expect(await screen.findByText("No command executions")).toBeInTheDocument()
    expect(window.location.pathname).toBe("/workspaces/ws_test/activity")
  })

  it("deep-links a workspace command execution as an activity child route", async () => {
    const workspace = { id: "ws_test", path: "/projects/test", allow_dirs: [] }
    const execution = {
      id: "exec_1",
      workspace_id: "ws_test",
      tool: "run_command",
      command: "go test ./...",
      cwd: "/projects/test",
      source: "mcp",
      started_at: "2026-09-05T00:00:00Z",
      finished_at: "2026-09-05T00:00:01Z",
      status: "success",
      exit_code: 0,
    }
    const snapshot = {
      execution,
      stdout: "ok\n",
      stderr: "",
      latest_sequence: 4,
    }
    window.history.replaceState({}, "", "/workspaces/ws_test/activity/exec_1")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === "/api/workspaces/ws_test") return json(workspace)
        if (path === "/api/workspaces/ws_test/executions/exec_1")
          return json(snapshot)
        return mockFetch(input)
      })
    )
    renderAdminApp()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Command Execution"))
    )
    expect(await screen.findByText("go test ./...")).toBeInTheDocument()
    const output = screen.getByRole("code")
    expect(output.textContent).toContain("ok\n")
    expect(
      (output.closest('[data-slot="scroll-area"]') as HTMLElement | null)?.style
        .maxHeight
    ).toBe("")
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(window.location.pathname).toBe("/workspaces/ws_test/activity/exec_1")
  })

  it("deep-links an activity tool call by call id", async () => {
    const callID = "019a1111-2222-7333-8444-555555555555"
    const event = {
      sequence: 7,
      call_id: callID,
      kind: "tool_call",
      method: "tools/call",
      source: "tunnel",
      tool: "run_command",
      workspace_id: "ws_test",
      status: "ok",
      duration_ms: 42,
      timestamp: "2026-09-05T00:00:00Z",
      raw: {
        call_id: callID,
        arguments: { workspace_id: "ws_test", command: "go test ./..." },
      },
    }
    window.history.replaceState({}, "", `/activity/${callID}`)
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestPath(input)
        if (path === `/api/activity/${callID}`) return json(event)
        return mockFetch(input)
      })
    )
    renderAdminApp()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Tool Call"))
    )
    expect(await screen.findByText(new RegExp(callID))).toBeInTheDocument()
    expect(screen.getAllByText("run_command").length).toBeGreaterThan(0)
    await userEvent.setup().click(screen.getByRole("tab", { name: "Raw" }))
    const raw = screen.getByRole("code")
    expect(
      (raw.closest('[data-slot="scroll-area"]') as HTMLElement | null)?.style
        .maxHeight
    ).toBe("")
    expect(window.location.pathname).toBe(`/activity/${callID}`)
  })

  it("only renders detected instruction sources and persists their toggles", async () => {
    const user = userEvent.setup()
    const detected = [
      {
        provider: "claude",
        kind: "context",
        paths: ["/home/test/.claude/CLAUDE.md"],
        count: 1,
        enabled: true,
        loaded: false,
      },
    ]
    let savedPolicy: Record<string, unknown> | undefined
    window.history.replaceState({}, "", "/instructions")
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = requestPath(input)
        if (path === "/api/instructions/global" && init?.method === "PUT") {
          const body = JSON.parse(String(init.body)) as {
            source_policy: Record<string, unknown>
          }
          savedPolicy = body.source_policy
          return json({
            version: 1,
            context: null,
            rules: null,
            source_policy: null,
            detected_sources: null,
          })
        }
        if (path === "/api/instructions/global")
          return json({
            version: 1,
            context: "",
            rules: [],
            source_policy: {},
            detected_sources: detected,
          })
        return mockFetch(input)
      })
    )
    renderAdminApp()
    await user.click(await screen.findByRole("tab", { name: "Sources" }))
    expect(screen.getByText("Claude")).toBeInTheDocument()
    expect(screen.getByText("/home/test/.claude/CLAUDE.md")).toBeInTheDocument()
    expect(screen.queryByText("Cursor")).not.toBeInTheDocument()
    await user.click(screen.getByRole("switch", { name: "Claude Context" }))
    await user.click(screen.getByRole("button", { name: /Save/ }))
    await waitFor(() =>
      expect(savedPolicy).toEqual({ claude: { context: false } })
    )
    expect(
      await screen.findByText(
        "Saved. New project_context calls use these instructions immediately."
      )
    ).toBeInTheDocument()
    expect(screen.getByText("0 detected sources")).toBeInTheDocument()
  })

  it("normalizes root and unknown paths to overview", async () => {
    window.history.replaceState({}, "", "/missing")
    renderAdminApp()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Overview"))
    )
    expect(window.location.pathname).toBe("/overview")
  })

  it("uses a login title when admin authentication rejects the current token", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (requestPath(input) === "/api/health")
          return new Response("unauthorized", { status: 401 })
        return mockFetch(input)
      })
    )
    renderAdminApp()
    expect(
      await screen.findByText("Enter the admin token generated by the CLI.")
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Login"))
    )
  })

  it("uses a connecting title while the initial health check is pending", async () => {
    let resolveHealth: ((response: Response) => void) | undefined
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        if (requestPath(input) === "/api/health")
          return new Promise<Response>((resolve) => {
            resolveHealth = resolve
          })
        return mockFetch(input)
      })
    )
    renderAdminApp()
    expect(
      await screen.findByText("Connecting to admin API...")
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Connecting"))
    )
    resolveHealth?.(json({ ok: true, auth_enabled: true }))
    await waitFor(() =>
      expect(document.title).toBe(adminDocumentTitle("Overview"))
    )
  })
})

function renderAdminApp() {
  const router = createAdminRouter()
  return render(
    <ThemeProvider>
      <RouterProvider router={router} />
    </ThemeProvider>
  )
}

async function mockFetch(input: RequestInfo | URL): Promise<Response> {
  const path = requestPath(input)
  if (path === "/api/health") return json({ ok: true, auth_enabled: true })
  if (path === "/api/workspaces") return json([])
  if (path === "/api/workspace-containers") return json([])
  if (path === "/api/instructions/global")
    return json({
      version: 1,
      context: "",
      rules: [],
      source_policy: {},
      detected_sources: [],
    })
  if (path === "/api/tools") return json([])
  if (path === "/api/upstream") return json([])
  if (path === "/api/tunnel") return json(tunnel)
  if (path === "/api/tunnel/config")
    return json({
      enabled: false,
      runtime_key_configured: false,
      admin_key_configured: false,
    })
  if (path === "/api/tunnel/admin/key")
    return json({ configured: false, scope: {} })
  if (path === "/api/config") return json(config)
  if (path === "/api/network/interfaces") return json([])
  if (path === "/api/requests?status=pending") return json([])
  if (path === "/api/requests?status=") return json([])
  if (path === "/api/requests/stream") return approvalStream()
  if (path === "/api/activity/stream?history=100") return activityStream()
  throw new Error(`Unhandled test request: ${path}`)
}

function projectContextFixture() {
  return {
    root: "/projects/test",
    workspace_id: "ws_test",
    summary: {
      memory_files: [],
      memory_bytes: 0,
      instruction_bytes: 25,
      git: { is_repo: true, branch: "main", commits: 1 },
      rules: 0,
      skills: 0,
    },
    instruction_context: {
      root: "/projects/test",
      workspace_id: "ws_test",
      instructions_text: "EFFECTIVE PROJECT CONTEXT",
      instruction_bytes: 25,
      global_rules: [],
      rules: [],
      skills: [],
      sources: [],
      project_memory: {
        sections: [],
        imports: [],
        total_bytes: 0,
        budget_bytes: 100000,
        budget_truncated: false,
      },
      auto_memory: { loaded: false, bytes: 0 },
      git: { is_repo: true, branch: "main" },
      environment: {},
    },
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

function activityStream() {
  const encoder = new TextEncoder()
  const body = new ReadableStream({
    start(controller) {
      controller.enqueue(
        encoder.encode('event: ready\ndata: {"latest_sequence":0}\n\n')
      )
      controller.close()
    },
  })
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  })
}

function approvalStream() {
  const encoder = new TextEncoder()
  const body = new ReadableStream({
    start(controller) {
      controller.enqueue(
        encoder.encode('event: ready\ndata: {"latest_sequence":0}\n\n')
      )
      controller.close()
    },
  })
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  })
}
