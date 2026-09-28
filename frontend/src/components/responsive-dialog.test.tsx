import { render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import { JsonViewer } from "@/components/json-viewer"
import { ResponsiveDialog } from "@/components/responsive-dialog"

const desktopWidth = window.innerWidth

describe("ResponsiveDialog mobile scrolling", () => {
  afterEach(() => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: desktopWidth })
  })

  it("gives a mobile drawer one native vertical scroll owner and leaves unbounded raw JSON horizontal-only", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 })

    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="server.ready" description="Safe runtime projection">
        <JsonViewer maxHeight={null} nativeUnbounded value={{ fields: Array.from({ length: 100 }, (_, index) => ({ index, value: "x".repeat(80) })) }} />
      </ResponsiveDialog>
    )

    await waitFor(() => expect(document.querySelector('[data-slot="drawer-content"]')).not.toBeNull())

    const drawer = document.querySelector<HTMLElement>('[data-slot="drawer-content"]')
    expect(drawer?.style.height).toContain("--tg-viewport-height")

    const scrollOwner = document.querySelector<HTMLElement>("[data-vaul-no-drag]")
    expect(scrollOwner).not.toBeNull()
    expect(scrollOwner?.className).toContain("overflow-y-auto")
    expect(scrollOwner?.className).toContain("overscroll-contain")
    expect(scrollOwner?.querySelector('[data-slot="scroll-area"]')).toBeNull()

    const code = screen.getByRole("code")
    expect(code.parentElement?.parentElement?.className).toContain("overflow-x-auto")
  })
})
