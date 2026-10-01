import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { adminApi } from "@/lib/api"
import { LogsPage } from "@/pages/logs"

describe("LogsPage observability semantics", () => {
  afterEach(() => vi.restoreAllMocks())

  it("clears only the browser view unless persisted journal deletion is explicitly confirmed", async () => {
    const user = userEvent.setup()
    const event = {
      sequence: 7,
      timestamp: "2026-10-01T08:00:00Z",
      level: "info",
      component: "SERVER",
      name: "runtime.ready",
      message: "Server ready",
    }
    vi.spyOn(adminApi, "followLogs").mockResolvedValue({
      events: [event],
      total: 1,
    })
    vi.spyOn(adminApi, "logsInfo").mockResolvedValue({
      path: "/tmp/runtime.jsonl",
      files: 1,
      bytes: 128,
    })
    const clear = vi
      .spyOn(adminApi, "clearLogs")
      .mockResolvedValue({ cleared: true })

    render(
      <TooltipProvider>
        <LogsPage />
      </TooltipProvider>
    )

    expect(await screen.findByText("runtime.ready")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Clear view" }))
    expect(screen.queryByText("runtime.ready")).not.toBeInTheDocument()
    expect(clear).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "Refresh" }))
    expect(await screen.findByText("runtime.ready")).toBeInTheDocument()
    expect(clear).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "Delete journal" }))
    expect(screen.getByText("Delete runtime log journal?")).toBeInTheDocument()
    expect(clear).not.toHaveBeenCalled()

    await user.click(
      screen.getByRole("button", { name: "Delete logs permanently" })
    )
    await waitFor(() => expect(clear).toHaveBeenCalledOnce())
  })
})
