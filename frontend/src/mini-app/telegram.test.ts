import { afterEach, describe, expect, it, vi } from "vitest"

import {
  bindBackButton,
  bindMainButton,
  bindSecondaryButton,
  bindSettingsButton,
  hideTelegramKeyboard,
  initializeTelegramMiniApp,
  loadMiniAppPreferences,
  saveMiniAppPreferences,
  telegramHaptic,
  toggleTelegramFullscreen,
  type TelegramWebApp,
} from "@/mini-app/telegram"

describe("Telegram Mini App adapter", () => {
  afterEach(() => {
    delete window.Telegram
    window.localStorage.clear()
    document.documentElement.className = ""
    document.documentElement.removeAttribute("style")
    vi.restoreAllMocks()
  })

  it("reacts to Telegram theme, viewport, and safe-area changes", () => {
    const listeners = new Map<string, (...args: unknown[]) => void>()
    const onEvent = vi.fn((event: string, callback: (...args: unknown[]) => void) => listeners.set(event, callback))
    const offEvent = vi.fn((event: string) => listeners.delete(event))
    const ready = vi.fn()
    const expand = vi.fn()
    const webApp: TelegramWebApp = {
      colorScheme: "dark",
      themeParams: { bg_color: "#101010", text_color: "#eeeeee" },
      viewportStableHeight: 640,
      contentSafeAreaInset: { top: 10, bottom: 18 },
      onEvent,
      offEvent,
      ready,
      expand,
    }
    window.Telegram = { WebApp: webApp }

    const cleanup = initializeTelegramMiniApp()
    expect(document.documentElement).toHaveClass("dark")
    expect(document.documentElement.style.getPropertyValue("--tg-theme-bg-color")).toBe("#101010")
    expect(document.documentElement.style.getPropertyValue("--background")).toBe("#101010")
    expect(document.documentElement.style.getPropertyValue("--foreground")).toBe("#eeeeee")
    expect(document.documentElement.style.getPropertyValue("--tg-viewport-stable-height")).toBe("640px")
    expect(document.documentElement.style.getPropertyValue("--tg-content-safe-area-inset-bottom")).toBe("18px")
    expect(ready).toHaveBeenCalledOnce()
    expect(expand).toHaveBeenCalledOnce()

    webApp.colorScheme = "light"
    webApp.viewportStableHeight = 700
    webApp.contentSafeAreaInset = { top: 12, bottom: 24 }
    listeners.get("viewportChanged")?.()
    expect(document.documentElement).toHaveClass("light")
    expect(document.documentElement.style.getPropertyValue("--tg-viewport-stable-height")).toBe("700px")
    expect(document.documentElement.style.getPropertyValue("--tg-content-safe-area-inset-bottom")).toBe("24px")

    cleanup()
    expect(offEvent).toHaveBeenCalled()
  })

  it("loads bounded presentation preferences from DeviceStorage and falls back to defaults", async () => {
    const stored = JSON.stringify({ density: "compact", autoFollow: false, defaultFeed: "tools" })
    const getItem = vi.fn((_key: string, callback: (error: string | null, value?: string | null) => void) => callback(null, stored))
    window.Telegram = { WebApp: { DeviceStorage: { getItem } } }

    await expect(loadMiniAppPreferences()).resolves.toEqual({
      density: "compact",
      autoFollow: false,
      defaultFeed: "tools",
    })

    getItem.mockImplementation((_key: string, callback: (error: string | null, value?: string | null) => void) => callback("unavailable"))
    await expect(loadMiniAppPreferences()).resolves.toEqual({
      density: "comfortable",
      autoFollow: true,
      defaultFeed: "runtime",
    })
  })

  it("persists only the small presentation preference payload", async () => {
    const setItem = vi.fn((_key: string, _value: string, callback?: (error: string | null, stored?: boolean) => void) => callback?.(null, true))
    window.Telegram = { WebApp: { DeviceStorage: { setItem } } }

    await saveMiniAppPreferences({ density: "compact", autoFollow: true, defaultFeed: "runtime" })

    expect(setItem).toHaveBeenCalledOnce()
    const [, raw] = setItem.mock.calls[0]
    expect(String(raw)).not.toContain("token")
    expect(String(raw).length).toBeLessThan(512)
  })

  it("treats rejected optional Telegram chrome capabilities as deterministic no-ops", () => {
    const reject = () => { throw new Error("unsupported") }
    window.Telegram = {
      WebApp: {
        onEvent: reject,
        offEvent: reject,
        ready: reject,
        expand: reject,
        hideKeyboard: reject,
        requestFullscreen: reject,
        BackButton: { show: reject, hide: reject, onClick: reject, offClick: reject },
        MainButton: { setText: reject, show: reject, hide: reject, enable: reject, onClick: reject, offClick: reject },
        SecondaryButton: { setText: reject, show: reject, hide: reject, enable: reject, onClick: reject, offClick: reject },
        SettingsButton: { show: reject, hide: reject, onClick: reject, offClick: reject },
        HapticFeedback: { impactOccurred: reject },
      },
    }

    expect(() => {
      const cleanups = [
        initializeTelegramMiniApp(),
        bindBackButton(true, vi.fn()),
        bindMainButton("Pause", true, vi.fn()),
        bindSecondaryButton("Clear view", true, vi.fn()),
        bindSettingsButton(vi.fn()),
      ]
      hideTelegramKeyboard()
      telegramHaptic()
      toggleTelegramFullscreen()
      for (const cleanup of cleanups) cleanup()
    }).not.toThrow()
  })
})
