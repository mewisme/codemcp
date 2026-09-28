import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { MiniApp } from "@/mini-app/app"

class MockWebSocket {
  static instances: MockWebSocket[] = []
  url: string
  closed = 0
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null
  constructor(url: string | URL) {
    this.url = String(url)
    MockWebSocket.instances.push(this)
  }
  close() { this.closed++ }
  emit(value: unknown) {
    this.onmessage?.({ data: JSON.stringify(value) } as MessageEvent<string>)
  }
}

describe("Telegram Logs Mini App", () => {
  beforeEach(() => {
    MockWebSocket.instances = []
    vi.stubGlobal("WebSocket", MockWebSocket)
    window.localStorage.clear()
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 1024 })
  })

  afterEach(() => {
    delete window.Telegram
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it("authenticates, opens the canonical realtime Runtime feed, and renders three audited tabs", async () => {
    const ready = vi.fn()
    const expand = vi.fn()
    const backShow = vi.fn()
    const backHide = vi.fn()
    const backOnClick = vi.fn()
    const backOffClick = vi.fn()
    window.Telegram = {
      WebApp: {
        initData: "signed-init-data",
        colorScheme: "dark",
        viewportStableHeight: 720,
        contentSafeAreaInset: { top: 8, bottom: 12, left: 4, right: 4 },
        ready,
        expand,
        BackButton: { show: backShow, hide: backHide, onClick: backOnClick, offClick: backOffClick },
      },
    }
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(input instanceof Request ? input.url : String(input), "https://mini.example")
      if (url.pathname === "/api/auth") {
        expect(init?.method).toBe("POST")
        expect(String(init?.body)).toContain("signed-init-data")
        return new Response(null, { status: 204 })
      }
      throw new Error("Unhandled Mini App request: " + url.pathname + url.search)
    })
    vi.stubGlobal("fetch", fetchMock)

    render(<TooltipProvider><MiniApp /></TooltipProvider>)

    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(1))
    expect(MockWebSocket.instances[0].url).toContain("/api/stream?feed=runtime")
    act(() => {
      MockWebSocket.instances[0].emit({
        type: "snapshot",
        feed: "runtime",
        latest_sequence: 9,
        payload: {
          latest_sequence: 9,
          session: "run_123456789",
          total: 1,
          truncated: false,
          events: [{ sequence: 9, timestamp: "2026-09-28T01:30:00Z", level: "info", component: "telegram", event: "runtime.ready", message: "Telegram runtime ready" }],
        },
      })
    })

    expect(await screen.findByText("Telegram runtime ready")).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Runtime" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Command Execute" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Tool Call/MCP" })).toBeInTheDocument()
    expect(screen.getByText("Live")).toBeInTheDocument()
    expect(document.documentElement).toHaveClass("dark")
    expect(document.documentElement.style.getPropertyValue("--tg-viewport-stable-height")).toBe("720px")
    expect(document.documentElement.style.getPropertyValue("--tg-content-safe-area-inset-bottom")).toBe("12px")
    expect(ready).toHaveBeenCalledOnce()
    expect(expand).toHaveBeenCalledOnce()
    expect(fetchMock).toHaveBeenCalledTimes(1)

    await userEvent.click(screen.getByText("Telegram runtime ready"))
    expect((await screen.findAllByText("runtime.ready")).length).toBeGreaterThan(1)
    expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Request" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Response" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Metadata" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Raw" })).toBeInTheDocument()
    expect(backShow).toHaveBeenCalled()
  })

  it("switches feeds by disposing the old socket and consuming execution snapshots", async () => {
    window.Telegram = { WebApp: { initData: "signed-init-data", ready: vi.fn(), expand: vi.fn() } }
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })))

    render(<TooltipProvider><MiniApp /></TooltipProvider>)
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(1))
    act(() => {
      MockWebSocket.instances[0].emit({ type: "snapshot", feed: "runtime", latest_sequence: 1, payload: { events: [], total: 0, truncated: false, latest_sequence: 1 } })
    })

    await userEvent.click(screen.getByRole("tab", { name: "Command Execute" }))
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(2))
    expect(MockWebSocket.instances[0].closed).toBeGreaterThan(0)
    expect(MockWebSocket.instances[1].url).toContain("feed=executions")
    act(() => {
      MockWebSocket.instances[1].emit({
        type: "snapshot",
        feed: "executions",
        latest_sequence: 4,
        payload: {
          latest_sequence: 4,
          events: [{ sequence: 4, type: "started", execution_id: "exec_1", workspace_id: "ws_1", timestamp: "2026-09-28T01:31:00Z" }],
          executions: [{ id: "exec_1", workspace_id: "ws_1", tool: "run_command", command: "go test ./...", cwd: "/workspace", started_at: "2026-09-28T01:31:00Z", status: "running" }],
        },
      })
      MockWebSocket.instances[1].emit({
        type: "event",
        feed: "executions",
        sequence: 5,
        latest_sequence: 5,
        payload: { sequence: 5, type: "output", execution_id: "exec_1", workspace_id: "ws_1", stream: "stdout", data: "stdout-first\n", timestamp: "2026-09-28T01:31:01Z" },
      })
      MockWebSocket.instances[1].emit({
        type: "event",
        feed: "executions",
        sequence: 6,
        latest_sequence: 6,
        payload: { sequence: 6, type: "output", execution_id: "exec_1", workspace_id: "ws_1", stream: "stderr", data: "stderr-second\n", timestamp: "2026-09-28T01:31:02Z" },
      })
    })
    expect(await screen.findByText("go test ./...")).toBeInTheDocument()
    expect(screen.getByText("running")).toBeInTheDocument()

    await userEvent.click(screen.getByText("go test ./..."))
    await userEvent.click(screen.getByRole("tab", { name: "Response" }))
    expect(screen.getByText(/stdout-first/)).toBeInTheDocument()
    expect(screen.getByText(/stderr-second/)).toBeInTheDocument()
  })

  it("suspends the realtime socket while Telegram marks the Mini App inactive and reconnects on activation", async () => {
    const listeners = new Map<string, (...args: unknown[]) => void>()
    window.Telegram = {
      WebApp: {
        initData: "signed-init-data",
        isActive: true,
        ready: vi.fn(),
        expand: vi.fn(),
        onEvent: vi.fn((event: string, callback: (...args: unknown[]) => void) => listeners.set(event, callback)),
        offEvent: vi.fn(),
      },
    }
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })))

    render(<TooltipProvider><MiniApp /></TooltipProvider>)
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(1))
    act(() => {
      MockWebSocket.instances[0].emit({
        type: "snapshot",
        feed: "runtime",
        latest_sequence: 1,
        payload: { events: [], total: 0, truncated: false, latest_sequence: 1 },
      })
    })
    expect(await screen.findByText("Live")).toBeInTheDocument()

    act(() => listeners.get("deactivated")?.())
    expect(await screen.findByText("Inactive")).toBeInTheDocument()
    expect(MockWebSocket.instances[0].closed).toBeGreaterThan(0)

    act(() => listeners.get("activated")?.())
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(2))
    expect(screen.getByText("Reconnecting")).toBeInTheDocument()
  })

  it("releases Telegram bottom-button space while a mobile detail drawer is open", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 })
    const mainHide = vi.fn()
    const secondaryHide = vi.fn()
    window.Telegram = {
      WebApp: {
        initData: "signed-init-data",
        ready: vi.fn(),
        expand: vi.fn(),
        BackButton: { show: vi.fn(), hide: vi.fn(), onClick: vi.fn(), offClick: vi.fn() },
        MainButton: { setText: vi.fn(), show: vi.fn(), hide: mainHide, enable: vi.fn(), disable: vi.fn(), hideProgress: vi.fn(), onClick: vi.fn(), offClick: vi.fn() },
        SecondaryButton: { setText: vi.fn(), show: vi.fn(), hide: secondaryHide, enable: vi.fn(), disable: vi.fn(), hideProgress: vi.fn(), onClick: vi.fn(), offClick: vi.fn() },
      },
    }
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })))

    render(<TooltipProvider><MiniApp /></TooltipProvider>)
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(1))
    act(() => {
      MockWebSocket.instances[0].emit({
        type: "snapshot",
        feed: "runtime",
        latest_sequence: 7,
        payload: {
          events: [{ sequence: 7, timestamp: "2026-09-28T01:30:00Z", level: "info", component: "server", event: "server.ready", message: "Server ready" }],
          total: 1,
          truncated: false,
          latest_sequence: 7,
        },
      })
    })

    await userEvent.click(await screen.findByText("Server ready"))

    await waitFor(() => expect(document.querySelector("[data-vaul-no-drag]")).not.toBeNull())
    expect(mainHide).toHaveBeenCalled()
    expect(secondaryHide).toHaveBeenCalled()
  })

  it("does not fall back to Admin authentication outside Telegram", async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)

    render(<TooltipProvider><MiniApp /></TooltipProvider>)

    expect(await screen.findByText("Logs unavailable")).toBeInTheDocument()
    expect(screen.getByText("Telegram Mini App context is unavailable")).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(MockWebSocket.instances).toHaveLength(0)
  })

  it("renders a bounded fallback when WebSocket is unavailable", async () => {
    vi.stubGlobal("WebSocket", undefined)
    window.Telegram = { WebApp: { initData: "signed-init-data", ready: vi.fn(), expand: vi.fn() } }
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })))

    render(<TooltipProvider><MiniApp /></TooltipProvider>)

    expect(await screen.findByText("Logs unavailable")).toBeInTheDocument()
    expect(screen.getByText("Realtime streaming is unavailable in this Telegram client")).toBeInTheDocument()
  })
})
