import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { adminApi, type CFTunnelStatus } from "@/lib/api"
import { IntegrationsPage } from "@/pages/integrations"

describe("IntegrationsPage", () => {
  const cfUnavailable: CFTunnelStatus = {
    platform: "linux/amd64",
    source: "unavailable",
    verified: false,
    managed_supported: true,
    managed_installed: false,
    consumer: "Telegram Logs Mini App",
  }
  const cfManaged: CFTunnelStatus = {
    ...cfUnavailable,
    source: "managed",
    path: "/managed/cf-tunnel",
    version: "v0.0.1",
    verified: true,
    managed_installed: true,
  }

  beforeEach(() => {
    vi.spyOn(adminApi, "rtkStatus").mockResolvedValue({
      enabled: true,
      state: "ready",
    })
    vi.spyOn(adminApi, "codeGraphStatus").mockResolvedValue({
      enabled: true,
      state: "ready",
    })
    vi.spyOn(adminApi, "cfTunnel").mockResolvedValue(cfUnavailable)
    vi.spyOn(adminApi, "typeSafeStatus").mockResolvedValue({
      enabled: false,
      api_key_configured: true,
      state: "disabled",
      model: "test-model",
      timeout_ms: 5000,
    })
    vi.spyOn(adminApi, "rtkAction").mockResolvedValue({})
    vi.spyOn(adminApi, "rtkGlobal").mockResolvedValue({
      status: { enabled: true, state: "managed" },
      available: false,
      managed_recommended: true,
    })
    vi.spyOn(adminApi, "codeGraphAction").mockResolvedValue({})
    vi.spyOn(adminApi, "codeGraphGlobal").mockResolvedValue({
      status: { enabled: true, state: "managed" },
      available: false,
      managed_recommended: true,
    })
    vi.spyOn(adminApi, "probeCFTunnel").mockResolvedValue({ status: cfManaged, version: "v0.0.1" })
    vi.spyOn(adminApi, "installCFTunnel").mockResolvedValue({ status: cfManaged, path: "/managed/cf-tunnel", version: "v0.0.1", installed: true, already_installed: false })
    vi.spyOn(adminApi, "updateCFTunnel").mockResolvedValue({ status: cfManaged, path: "/managed/cf-tunnel", version: "v0.0.1", installed: false, already_installed: true })
    vi.spyOn(adminApi, "removeCFTunnel").mockResolvedValue({ status: cfUnavailable, removed: true })
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

  it("checks user-installed global executables and recommends managed assets without installing globally", async () => {
    const user = userEvent.setup()
    render(<IntegrationsPage />)
    const globalButtons = await screen.findAllByRole("button", { name: "Check global" })
    await user.click(globalButtons[0])
    await waitFor(() => expect(adminApi.rtkGlobal).toHaveBeenCalledOnce())
    expect(await screen.findByRole("status")).toHaveTextContent("RTK is not installed globally")
    expect(screen.getByRole("status")).toHaveTextContent("cm integration rtk install")
    expect(adminApi.rtkAction).not.toHaveBeenCalledWith("install/global")
  })

  it("owns Cloudflare Quick Tunnel lifecycle on the Integrations page", async () => {
    const user = userEvent.setup()
    render(<IntegrationsPage />)
    expect(await screen.findByText("Cloudflare Quick Tunnel")).toBeInTheDocument()
    expect(screen.getByText(/OpenAI Secure MCP Tunnel remains the persistent MCP tunnel authority/)).toBeInTheDocument()
    const installButtons = screen.getAllByRole("button", { name: "Install managed" })
    await user.click(installButtons[installButtons.length - 1])
    await waitFor(() => expect(adminApi.installCFTunnel).toHaveBeenCalledOnce())
    expect(await screen.findByText("/managed/cf-tunnel")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Remove managed" }))
    expect(screen.getByText("Remove managed cf-tunnel?")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Remove" }))
    await waitFor(() => expect(adminApi.removeCFTunnel).toHaveBeenCalledOnce())
  })
})
