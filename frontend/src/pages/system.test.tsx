import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { adminApi } from "@/lib/api"
import { SystemPage } from "@/pages/system"

describe("SystemPage", () => {
  beforeEach(() => {
    vi.spyOn(adminApi, "status").mockResolvedValue({
      runtime_running: true,
      mcp_http_enabled: true,
      admin_enabled: true,
      tunnel_enabled: true,
      telegram_enabled: true,
      telegram_running: true,
      telegram_healthy: true,
    })
    vi.spyOn(adminApi, "telemetry").mockResolvedValue({
      persisted_enabled: true,
      effective_enabled: true,
      source: "config",
      environment_override: false,
      endpoint_available: true,
      identity_present: true,
    })
    vi.spyOn(adminApi, "doctor").mockResolvedValue({ healthy: true })
    vi.spyOn(adminApi, "about").mockResolvedValue({ version: "test" })
    vi.spyOn(adminApi, "runtimeAction").mockResolvedValue({})
    vi.spyOn(adminApi, "setTelemetry").mockResolvedValue({
      persisted_enabled: false,
      effective_enabled: false,
      source: "config",
      environment_override: false,
      endpoint_available: true,
      identity_present: true,
    })
  })
  afterEach(() => vi.restoreAllMocks())

  it("uses canonical runtime and Telegram read models and actions", async () => {
    const user = userEvent.setup()
    render(<SystemPage />)
    expect(await screen.findByText("Healthy")).toBeInTheDocument()
    expect(screen.getByText("Running")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Restart" }))
    await waitFor(() =>
      expect(adminApi.runtimeAction).toHaveBeenCalledWith("restart")
    )
    await user.click(screen.getByRole("button", { name: "Disable" }))
    await waitFor(() =>
      expect(adminApi.setTelemetry).toHaveBeenCalledWith(false)
    )
  })
})
