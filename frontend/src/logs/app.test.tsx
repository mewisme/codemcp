import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { LogsMiniApp } from "@/logs/app"

describe("Telegram Logs Mini App", () => {
  afterEach(() => {
    delete window.Telegram
    vi.unstubAllGlobals()
  })

  it("authenticates with Telegram init data and renders the read-only snapshot UI", async () => {
    const ready = vi.fn()
    const expand = vi.fn()
    window.Telegram = { WebApp: { initData: "signed-init-data", colorScheme: "dark", ready, expand } }
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(input instanceof Request ? input.url : String(input), "https://mini.example")
      if (url.pathname === "/logs/auth") {
        expect(init?.method).toBe("POST")
        expect(String(init?.body)).toContain("signed-init-data")
        return new Response(null, { status: 204 })
      }
      if (url.pathname === "/logs/api/snapshot") {
        return json({
          session: "run_123456789",
          total: 1,
          events: [{ sequence: 9, timestamp: "2026-09-28T01:30:00Z", level: "info", component: "telegram", name: "runtime.ready", message: "Telegram runtime ready" }],
        })
      }
      throw new Error(`Unhandled Mini App request: ${url.pathname}${url.search}`)
    })
    vi.stubGlobal("fetch", fetchMock)

    render(<TooltipProvider><LogsMiniApp /></TooltipProvider>)

    expect(await screen.findByText("CodeMCP Logs")).toBeInTheDocument()
    expect(screen.getByText("Telegram runtime ready")).toBeInTheDocument()
    expect(screen.getByText("Live snapshot")).toBeInTheDocument()
    expect(document.documentElement).toHaveClass("dark")
    expect(ready).toHaveBeenCalledOnce()
    expect(expand).toHaveBeenCalledOnce()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))

    await userEvent.click(screen.getByText("Telegram runtime ready"))
    expect(await screen.findByText("runtime.ready")).toBeInTheDocument()
  })

  it("does not fall back to Admin authentication outside Telegram", async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)

    render(<TooltipProvider><LogsMiniApp /></TooltipProvider>)

    expect(await screen.findByText("Logs unavailable")).toBeInTheDocument()
    expect(screen.getByText("Telegram Mini App context is unavailable")).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

function json(value: unknown) {
  return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } })
}
