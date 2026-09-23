import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { TunnelPage } from "@/pages/tunnel"
import { adminApi, type PublicConfig, type TunnelStatus } from "@/lib/api"

const publicConfig = {
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
  features: {
    ponytail: { active: true, mode: "full" },
    caveman: { active: true, mode: "full" },
  },
} satisfies PublicConfig

const tunnelStatus: TunnelStatus = {
  provider: "openai",
  enabled: true,
  running: true,
  ready: true,
  restarting: false,
  id: "tunnel_one",
  control_plane_base_url: "https://api.openai.com",
  organization_id: "org_runtime",
  started_at: "2026-09-05T00:00:00Z",
  admin_key_configured: true,
  admin_scope: { workspace_id: "ws_admin" },
  metadata: {
    id: "tunnel_one",
    name: "Primary tunnel",
    description: "Production ChatGPT bridge",
    creator: "user_test",
    organization_ids: ["org_one"],
    workspace_ids: ["ws_admin"],
    tenant_ids: ["tenant_one"],
    request_id: "req_meta",
    fetched_at: "2026-09-05T00:01:00Z",
  },
}

const managedTunnels = [
  tunnelStatus.metadata!,
  {
    id: "tunnel_two",
    name: "Secondary tunnel",
    description: "Secondary ChatGPT bridge",
    workspace_ids: ["ws_admin"],
    fetched_at: "2026-09-05T00:02:00Z",
  },
]

describe("TunnelPage", () => {
  beforeEach(() => {
    vi.spyOn(adminApi, "tunnelConfig").mockResolvedValue({
      enabled: true,
      id: "tunnel_one",
      runtime_key_configured: true,
      admin_key_configured: true,
    })
    vi.spyOn(adminApi, "tunnel").mockResolvedValue(tunnelStatus)
    vi.spyOn(adminApi, "tunnelAdminKey").mockResolvedValue({
      configured: true,
      scope: { workspace_id: "ws_admin" },
      access: { read: true, manage: true },
      tunnels: 2,
    })
    vi.spyOn(adminApi, "config").mockResolvedValue(publicConfig)
    vi.spyOn(adminApi, "managedTunnels").mockResolvedValue(managedTunnels)
    vi.spyOn(adminApi, "managedTunnel").mockImplementation(async (id) => managedTunnels.find((item) => item.id === id) ?? managedTunnels[0])
    vi.spyOn(adminApi, "createManagedTunnel").mockResolvedValue(managedTunnels[1])
    vi.spyOn(adminApi, "updateManagedTunnel").mockResolvedValue(managedTunnels[1])
    vi.spyOn(adminApi, "deleteManagedTunnel").mockResolvedValue(managedTunnels[1])
    vi.spyOn(adminApi, "useManagedTunnel").mockResolvedValue({
      metadata: managedTunnels[1],
      status: {
        ...tunnelStatus,
        id: "tunnel_two",
        metadata: managedTunnels[1],
      },
    })
    vi.spyOn(adminApi, "removeTunnelAdminKey").mockResolvedValue({
      configured: false,
      scope: {},
      access: { read: false, manage: false },
    })
    vi.spyOn(adminApi, "startTunnel").mockResolvedValue(tunnelStatus)
    vi.spyOn(adminApi, "stopTunnel").mockResolvedValue({
      ...tunnelStatus,
      running: false,
      ready: false,
    })
  })

  afterEach(() => vi.restoreAllMocks())

  it("separates runtime, administration, and metadata and confirms admin-key removal", async () => {
    const user = userEvent.setup()
    render(<TunnelPage />)

    expect(await screen.findByText("Primary tunnel")).toBeInTheDocument()
    expect(screen.getByText("Runtime connectivity")).toBeInTheDocument()
    expect(screen.getByText("Ready")).toBeInTheDocument()

    await user.click(screen.getByRole("tab", { name: "Administration" }))
    expect(screen.getByText("Tunnel administration")).toBeInTheDocument()
    expect(screen.getByText("2 accessible tunnels")).toBeInTheDocument()
    expect(screen.getByText("Secret file store")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Remove" }))
    expect(screen.getByText("Remove tunnel admin key?")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Remove admin key" }))
    await waitFor(() =>
      expect(adminApi.removeTunnelAdminKey).toHaveBeenCalledOnce()
    )
    expect(await screen.findByText("Admin key removed.")).toBeInTheDocument()

    await user.click(screen.getByRole("tab", { name: "Metadata" }))
    expect(screen.getByText("Connection identity")).toBeInTheDocument()
    expect(screen.getByText("Tunnel scope")).toBeInTheDocument()
    expect(screen.getByText("tenant_one")).toBeInTheDocument()
    expect(screen.getByText("req_meta")).toBeInTheDocument()
  })

  it("locks the tunnel when MCP HTTP is disabled", async () => {
    vi.mocked(adminApi.config).mockResolvedValue({
      ...publicConfig,
      server: { ...publicConfig.server, enabled: false },
    })
    render(<TunnelPage />)
    expect(
      await screen.findByText(/Secure MCP Tunnel is the required MCP transport/)
    ).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Stop tunnel" })).toBeDisabled()
    expect(screen.getByRole("switch", { name: "Enable tunnel" })).toBeDisabled()
  })

  it("loads managed tunnels from admin access and switches the runtime", async () => {
    const user = userEvent.setup()
    render(<TunnelPage />)

    await user.click(await screen.findByRole("tab", { name: "Administration" }))
    expect(await screen.findByText("Secondary tunnel")).toBeInTheDocument()
    expect(screen.getByText("Full management")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Create tunnel" })).toBeInTheDocument()
    expect(screen.getAllByRole("button", { name: "Edit" }).length).toBeGreaterThan(0)
    expect(adminApi.managedTunnels).toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "Use tunnel" }))
    await waitFor(() =>
      expect(adminApi.useManagedTunnel).toHaveBeenCalledWith({
        id: "tunnel_two",
      })
    )
    expect(
      await screen.findByText("Using managed tunnel Secondary tunnel.")
    ).toBeInTheDocument()
  })

  it("offers automatic or manual runtime credentials when using a tunnel without a runtime key", async () => {
    Object.defineProperty(Element.prototype, "scrollIntoView", { configurable: true, value: vi.fn() })
    vi.mocked(adminApi.tunnelConfig).mockResolvedValue({ enabled: false, admin_key_configured: true })
    const user = userEvent.setup()
    render(<TunnelPage />)

    await user.click(await screen.findByRole("tab", { name: "Administration" }))
    const useButtons = await screen.findAllByRole("button", { name: "Use tunnel" })
    await user.click(useButtons[1])
    expect(screen.getByText("Use managed tunnel?")).toBeInTheDocument()
    expect(screen.getByText("Auto generate runtime key")).toBeInTheDocument()
    expect(screen.getByPlaceholderText("proj_...")).toBeInTheDocument()
    expect(screen.queryByText("Enable tunnel")).not.toBeInTheDocument()

    const credentialSelect = screen.getAllByRole("combobox").find((element) => element.textContent?.includes("Auto generate runtime key"))
    expect(credentialSelect).toBeDefined()
    if (!credentialSelect) return
    credentialSelect.focus()
    await user.keyboard("{Enter}{ArrowDown}{Enter}")
    expect(await screen.findByText("Enter runtime key manually")).toBeInTheDocument()
    expect(screen.queryByPlaceholderText("proj_...")).not.toBeInTheDocument()
    const runtimeKey = screen.getByText("Runtime API key").parentElement?.querySelector("input")
    expect(runtimeKey).not.toBeNull()
    if (!runtimeKey) return
    await user.type(runtimeKey, "sk-runtime-manual")
    await user.click(screen.getByRole("button", { name: "Use tunnel" }))
    await waitFor(() => expect(adminApi.useManagedTunnel).toHaveBeenCalledWith({ id: "tunnel_two", runtime_api_key: "sk-runtime-manual" }))
  })

  it("limits a read-only admin key to lookup and use actions", async () => {
    vi.mocked(adminApi.tunnelAdminKey).mockResolvedValue({ configured: true, scope: { workspace_id: "ws_admin" }, access: { read: true, manage: false } })
    const user = userEvent.setup()
    render(<TunnelPage />)
    await user.click(await screen.findByRole("tab", { name: "Administration" }))
    expect(await screen.findByText("Read only")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Create tunnel" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Lookup" })).toBeDisabled()
    expect(adminApi.managedTunnels).not.toHaveBeenCalled()
    expect(adminApi.managedTunnel).toHaveBeenCalledWith("tunnel_one")
  })
})
