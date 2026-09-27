import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { adminApi } from "@/lib/api"
import { IntegrationsPage } from "@/pages/integrations"

describe("IntegrationsPage", () => {
  beforeEach(() => {
    vi.spyOn(adminApi, "rtkStatus").mockResolvedValue({
      enabled: true,
      state: "ready",
    })
    vi.spyOn(adminApi, "codeGraphStatus").mockResolvedValue({
      enabled: true,
      state: "ready",
    })
    vi.spyOn(adminApi, "typeSafeStatus").mockResolvedValue({
      enabled: false,
      api_key_configured: true,
      state: "disabled",
      model: "test-model",
      timeout_ms: 5000,
    })
    vi.spyOn(adminApi, "rtkAction").mockResolvedValue({})
    vi.spyOn(adminApi, "codeGraphAction").mockResolvedValue({})
    vi.spyOn(adminApi, "typeSafeAction").mockResolvedValue({
      enabled: true,
      api_key_configured: true,
      state: "ready",
      model: "test-model",
      timeout_ms: 5000,
    })
  })
  afterEach(() => vi.restoreAllMocks())

  it("drives TypeSafe lifecycle through the dedicated application operation", async () => {
    const user = userEvent.setup()
    render(<IntegrationsPage />)
    expect(await screen.findByText(/Model test-model/)).toBeInTheDocument()
    const enable = screen.getAllByRole("button", { name: "Enable" }).at(-1)
    expect(enable).toBeDefined()
    if (!enable) return
    await user.click(enable)
    await waitFor(() =>
      expect(adminApi.typeSafeAction).toHaveBeenCalledWith("enable")
    )
  })
})
