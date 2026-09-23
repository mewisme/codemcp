import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import { adminToken } from "@/lib/api"
import { ToolsPage } from "@/pages/tools"

describe("ToolsPage", () => {
  afterEach(() => { adminToken.clear(); vi.unstubAllGlobals() })

  it("contains long JSON schemas with horizontal and vertical scroll areas without replacing the tab list", async () => {
    const user = userEvent.setup()
    const longValue = "x".repeat(5000)
    adminToken.set("test-admin-token")
    vi.stubGlobal("fetch", vi.fn(async () => json([{ name: "long_schema", title: "Long schema", description: "Schema overflow regression", inputSchema: { type: "object", properties: { payload: { type: "string", description: longValue } } }, outputSchema: {}, annotations: {} }])))
    render(<ThemeProvider><TooltipProvider><ToolsPage /></TooltipProvider></ThemeProvider>)
    await user.click(await screen.findByText("long_schema"))
    expect(screen.getByRole("tab", { name: "Input schema" })).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "Input schema" }))
    expect(screen.getByRole("code").textContent).toContain(longValue)
    expect(document.querySelectorAll('[data-slot="scroll-area"][data-scrollbars="both"]').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Annotations" })).toBeInTheDocument()
  })
})

function json(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } }) }
