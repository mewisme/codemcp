import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import type { PublicConfig } from "@/lib/api"
import { SettingsPage } from "@/pages/settings"

const config: PublicConfig = {
  server: {
    enabled: true,
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
  integrations: {
    ponytail: { active: true, mode: "full" },
    caveman: { active: true, mode: "full" },
    rtk: { enabled: true, path: "" },
    codegraph: { enabled: false, path: "" },
    typesafe: { enabled: false, model: "test-model", timeout_ms: 5000 },
  },
}

const authStatus = {
  mcp_enabled: true,
  mcp_configured: true,
  mcp_legacy_bearer: false,
  admin_enabled: true,
  admin_configured: true,
  unauthenticated_loopback: false,
  cleartext_http: false,
}

describe("settings authentication", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("rotates a credential through the canonical auth operation and bounds the reveal lifecycle", async () => {
    const user = userEvent.setup()
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(input instanceof Request ? input.url : String(input), "http://localhost")
      if (url.pathname === "/api/config") return json(config)
      if (url.pathname === "/api/network/interfaces") return json([])
      if (url.pathname === "/api/tunnel/config") return json({ enabled: false })
      if (url.pathname === "/api/auth" && (!init?.method || init.method === "GET")) return json(authStatus)
      if (url.pathname === "/api/settings") return json([])
      if (url.pathname === "/api/notifications") return json({})
      if (url.pathname === "/api/auth/mcp/rotate" && init?.method === "POST") {
        return json({ token: "mcp_test_one_time_secret", status: authStatus })
      }
      throw new Error(`Unhandled request: ${init?.method ?? "GET"} ${url.pathname}${url.search}`)
    })
    vi.stubGlobal("fetch", fetchMock)

    render(
      <ThemeProvider>
        <TooltipProvider>
          <SettingsPage />
        </TooltipProvider>
      </ThemeProvider>
    )

    await user.click(await screen.findByRole("tab", { name: "Authentication" }))
    expect(screen.getByText("MCP authentication")).toBeInTheDocument()
    const rotateButtons = screen.getAllByRole("button", { name: "Rotate" })
    await user.click(rotateButtons[0])

    expect(await screen.findByText("One-time credential")).toBeInTheDocument()
    expect(screen.getByText("mcp_test_one_time_secret")).toBeInTheDocument()
    expect(window.localStorage.getItem("mcp_test_one_time_secret")).toBeNull()
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/auth/mcp/rotate"),
        expect.objectContaining({ method: "POST" })
      )
    )
    await user.click(screen.getByRole("button", { name: "Hide" }))
    expect(screen.queryByText("mcp_test_one_time_secret")).not.toBeInTheDocument()
  })
})

function json(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}
