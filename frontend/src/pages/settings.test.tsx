import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import type { PublicConfig } from "@/lib/api"
import { SettingsPage } from "@/pages/settings"

const config: PublicConfig = {
  http: {
    exposure: { mode: "none", interfaces: [] },
    security: { allow_insecure: false, allow_unauthenticated_loopback: false },
    mcp: {
      enabled: true,
      port: 37421,
      auth: { enabled: true, legacy_bearer: true, token_configured: true },
    },
    admin: {
      enabled: true,
      port: 37422,
      auth: { enabled: true, token_configured: true },
    },
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
    const fetchMock = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(
          input instanceof Request ? input.url : String(input),
          "http://localhost"
        )
        if (url.pathname === "/api/config") return json(config)
        if (url.pathname === "/api/network/interfaces") return json([])
        if (url.pathname === "/api/tunnel/config")
          return json({ enabled: false })
        if (
          url.pathname === "/api/auth" &&
          (!init?.method || init.method === "GET")
        )
          return json(authStatus)
        if (url.pathname === "/api/settings") return json([])
        if (url.pathname === "/api/notifications") return json({})
        if (
          url.pathname === "/api/auth/mcp/rotate" &&
          init?.method === "POST"
        ) {
          return json({ token: "mcp_test_one_time_secret", status: authStatus })
        }
        throw new Error(
          `Unhandled request: ${init?.method ?? "GET"} ${url.pathname}${url.search}`
        )
      }
    )
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
    expect(
      screen.queryByText("mcp_test_one_time_secret")
    ).not.toBeInTheDocument()
  })

  it("renders canonical setting choices and numeric bounds from FieldSpec metadata", async () => {
    const user = userEvent.setup()
    const exposure = {
      Spec: {
        Key: "http.exposure.mode",
        Label: "Exposure",
        Description: "controls network exposure",
        Kind: "enum",
        Editable: true,
        Sensitive: false,
        Writable: true,
        Secret: false,
        Clearable: false,
        Options: ["none", "all"],
        Values: [
          { value: "none", description: "Loopback only" },
          { value: "all", description: "All eligible interfaces" },
        ],
      },
      Value: "none",
      RuntimeReloaded: false,
    }
    const port = {
      Spec: {
        Key: "http.mcp.port",
        Label: "MCP HTTP port",
        Description: "sets the TCP port",
        Kind: "int",
        Editable: true,
        Sensitive: false,
        Writable: true,
        Secret: false,
        Clearable: false,
        Input: {
          min_int: 1,
          max_int: 65535,
          has_min_int: true,
          has_max_int: true,
        },
      },
      Value: "37421",
      RuntimeReloaded: false,
    }
    const fetchMock = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(
          input instanceof Request ? input.url : String(input),
          "http://localhost"
        )
        if (url.pathname === "/api/config") return json(config)
        if (url.pathname === "/api/network/interfaces") return json([])
        if (url.pathname === "/api/tunnel/config")
          return json({ enabled: false })
        if (url.pathname === "/api/auth") return json(authStatus)
        if (url.pathname === "/api/settings") return json([exposure, port])
        if (url.pathname === "/api/settings/http.exposure.mode")
          return json(exposure)
        if (url.pathname === "/api/settings/http.mcp.port") return json(port)
        if (url.pathname === "/api/notifications") return json({})
        throw new Error(
          `Unhandled request: ${init?.method ?? "GET"} ${url.pathname}${url.search}`
        )
      }
    )
    vi.stubGlobal("fetch", fetchMock)

    render(
      <ThemeProvider>
        <TooltipProvider>
          <SettingsPage />
        </TooltipProvider>
      </ThemeProvider>
    )

    await user.click(await screen.findByRole("tab", { name: "Advanced" }))
    await user.click(await screen.findByRole("button", { name: /Exposure/ }))
    const select = await screen.findByRole("combobox")
    expect(select).toHaveTextContent("none")

    await user.click(screen.getByRole("button", { name: /MCP HTTP port/ }))
    const input = await screen.findByRole("spinbutton")
    expect(input).toHaveAttribute("min", "1")
    expect(input).toHaveAttribute("max", "65535")
  })

  it("keeps Settings global and preserves dirty edits when a save fails", async () => {
    const user = userEvent.setup()
    const fetchMock = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(
          input instanceof Request ? input.url : String(input),
          "http://localhost"
        )
        if (url.pathname === "/api/config" && init?.method === "PUT") {
          return new Response("save failed", { status: 500 })
        }
        if (url.pathname === "/api/config") return json(config)
        if (url.pathname === "/api/network/interfaces") return json([])
        if (url.pathname === "/api/tunnel/config")
          return json({ enabled: false })
        if (url.pathname === "/api/auth") return json(authStatus)
        if (url.pathname === "/api/settings") return json([])
        if (url.pathname === "/api/notifications") return json({})
        throw new Error(
          `Unhandled request: ${init?.method ?? "GET"} ${url.pathname}${url.search}`
        )
      }
    )
    vi.stubGlobal("fetch", fetchMock)

    render(
      <ThemeProvider>
        <TooltipProvider>
          <SettingsPage />
        </TooltipProvider>
      </ThemeProvider>
    )

    expect(await screen.findByText("Saved")).toBeInTheDocument()
    expect(
      screen.queryByRole("tab", { name: "Integrations" })
    ).not.toBeInTheDocument()
    expect(screen.queryByText("Telegram setup")).not.toBeInTheDocument()

    const port = screen.getAllByRole("spinbutton")[0]
    await user.clear(port)
    await user.type(port, "38421")
    expect(screen.getAllByText("Unsaved changes").length).toBeGreaterThan(0)

    await user.click(screen.getByRole("button", { name: "Save" }))
    expect(await screen.findByText("Save failed")).toBeInTheDocument()
    expect(port).toHaveValue(38421)
  })
})

function json(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}
