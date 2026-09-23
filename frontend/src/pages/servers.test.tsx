import { fireEvent, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { TooltipProvider } from "@/components/ui/tooltip"
import { ServersPage } from "@/pages/servers"

const server = { id: "local", name: "Local server", transport: "http", enabled: true, url: "http://127.0.0.1:3000/mcp", auth: { type: "none" }, expose: "all" }

describe("servers page", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("keeps the page list-first and opens health/tools in server detail", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(input instanceof Request ? input.url : String(input), "http://localhost")
      if (url.pathname === "/api/upstream") return json([server])
      if (url.pathname === "/api/upstream/local/status") return json({ ...server, auth: "none", health: "connected", connected: true, tool_count: 1, proxied_tools: ["local__read_file"] })
      if (url.pathname === "/api/upstream/local/tools") return json({ server_id: "local", tools: [{ name: "read_file", description: "Read one file" }], proxied_tools: ["local__read_file"] })
      throw new Error(`Unhandled request: ${url.pathname}${url.search}`)
    }))
    const user = userEvent.setup()
    render(<TooltipProvider><ServersPage /></TooltipProvider>)
    expect(await screen.findByText("Local server")).toBeInTheDocument()
    expect(screen.queryByRole("tab", { name: "Overview" })).not.toBeInTheDocument()
    await user.click(screen.getByText("Local server"))
    expect(await screen.findByRole("tab", { name: "Overview" })).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "Tools" }))
    expect(await screen.findByText("read_file")).toBeInTheDocument()
    expect(screen.getByText("Proxied")).toBeInTheDocument()
  })

  it("imports multiple servers from an mcpServers JSON config", async () => {
    const configured: typeof server[] = []
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(new URL(String(input), "http://localhost"), init)
      const url = new URL(request.url)
      if (url.pathname === "/api/upstream" && request.method === "GET") return json(configured)
      if (url.pathname === "/api/upstream" && request.method === "POST") {
        const value = await request.json() as typeof server
        configured.push(value)
        return json(value)
      }
      if (url.pathname.endsWith("/status")) return json({ id: url.pathname.split("/")[3], enabled: true, transport: "stdio", auth: "none", health: "unknown", connected: false, tool_count: 0, expose: "all", proxied_tools: [] })
      if (url.pathname.endsWith("/auth/status")) return json({ server_id: url.pathname.split("/")[3], configured: false, has_refresh_token: false, expired: false })
      throw new Error(`Unhandled request: ${request.method} ${url.pathname}${url.search}`)
    }))
    const user = userEvent.setup()
    render(<TooltipProvider><ServersPage /></TooltipProvider>)
    const addButtons = await screen.findAllByRole("button", { name: "Add MCP server" })
    await user.click(addButtons[0])
    await user.click(screen.getByRole("tab", { name: "JSON import" }))
    fireEvent.change(screen.getByLabelText("MCP server JSON"), { target: { value: JSON.stringify({ mcpServers: { local_tools: { command: "node", args: ["./server.js"] }, docs: { type: "http", url: "https://example.com/mcp" } } }) } })
    expect(screen.getByText("Detected 2 servers")).toBeInTheDocument()
    expect(screen.getByText("2 valid")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Import 2 servers" }))
    expect((await screen.findAllByText("local_tools")).length).toBeGreaterThan(0)
    expect(screen.getAllByText("docs").length).toBeGreaterThan(0)
    expect(configured.map((item) => item.id)).toEqual(["local_tools", "docs"])
  })

  it("edits an existing server through raw JSON without changing its ID", async () => {
    let current = { ...server }
    let updated: typeof server | undefined
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(new URL(String(input), "http://localhost"), init)
      const url = new URL(request.url)
      if (url.pathname === "/api/upstream" && request.method === "GET") return json([current])
      if (url.pathname === "/api/upstream/local" && request.method === "PUT") {
        updated = await request.json() as typeof server
        current = { ...current, ...updated }
        return json(current)
      }
      if (url.pathname === "/api/upstream/local/status") return json({ ...current, auth: "none", health: "connected", connected: true, tool_count: 0, proxied_tools: [] })
      if (url.pathname === "/api/upstream/local/tools") return json({ server_id: "local", tools: [], proxied_tools: [] })
      throw new Error(`Unhandled request: ${request.method} ${url.pathname}${url.search}`)
    }))
    const user = userEvent.setup()
    render(<TooltipProvider><ServersPage /></TooltipProvider>)
    await user.click(await screen.findByText("Local server"))
    await user.click(await screen.findByRole("button", { name: "Edit" }))
    await user.click(screen.getByRole("tab", { name: "JSON" }))
    const editor = screen.getByLabelText("MCP server JSON")
    const value = JSON.parse((editor as HTMLTextAreaElement).value)
    value.name = "Edited server"
    fireEvent.change(editor, { target: { value: JSON.stringify(value) } })
    await user.click(screen.getByRole("button", { name: "Save JSON" }))
    expect(updated).toMatchObject({ id: "local", name: "Edited server" })
    expect((await screen.findAllByText("Edited server")).length).toBeGreaterThan(0)
  })
})

function json(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } }) }
