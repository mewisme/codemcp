import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"

import { RequestExplanation } from "@/components/request-explanation"
import { adminApi } from "@/lib/api"

describe("RequestExplanation", () => {
  afterEach(() => vi.restoreAllMocks())

  it("requires an explicit action in manual mode", async () => {
    const user = userEvent.setup()
    vi.spyOn(adminApi, "approvalExplainStatus").mockResolvedValue({ mode: "manual", available: true, active_provider: "openrouter", model: "openrouter/free", configured: true, readiness: "ready" })
    vi.spyOn(adminApi, "approvalExplanation").mockResolvedValue({ request_id: "req_manual", state: "none" })
    vi.spyOn(adminApi, "explainApproval").mockResolvedValue({ request_id: "req_manual", state: "pending", attempt: 1 })

    render(<RequestExplanation requestID="req_manual" />)
    const explain = await screen.findByRole("button", { name: "Explain command" })
    expect(adminApi.explainApproval).not.toHaveBeenCalled()
    await user.click(explain)
    await waitFor(() => expect(adminApi.explainApproval).toHaveBeenCalledWith("req_manual", false))
  })

  it("renders auto-mode provenance and generated content separately from request details", async () => {
    vi.spyOn(adminApi, "approvalExplainStatus").mockResolvedValue({ mode: "auto", available: true, active_provider: "openrouter", model: "openrouter/free", configured: true, readiness: "ready" })
    vi.spyOn(adminApi, "approvalExplanation").mockResolvedValue({
      request_id: "req_auto",
      state: "ready",
      attempt: 1,
      explanation: {
        summary: "Pushes the feature branch to the configured remote.",
        steps: ["Runs git push."],
        effects: ["May update a remote branch."],
        risk_notes: ["Remote write."],
        unknowns: ["Remote policy is unknown."],
        provider_id: "openrouter",
        model: "openrouter/free",
        generated_at: "2026-09-30T00:00:00Z",
      },
    })

    render(<RequestExplanation requestID="req_auto" />)
    expect(await screen.findByText("Pushes the feature branch to the configured remote.")).toBeInTheDocument()
    expect(screen.getByText("openrouter/free")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Explain command" })).not.toBeInTheDocument()
  })

  it("keeps failure local and exposes an explicit retry", async () => {
    const user = userEvent.setup()
    vi.spyOn(adminApi, "approvalExplainStatus").mockResolvedValue({ mode: "auto", available: true, active_provider: "openrouter", model: "openrouter/free", configured: true, readiness: "ready" })
    vi.spyOn(adminApi, "approvalExplanation").mockResolvedValue({ request_id: "req_failed", state: "failed", attempt: 1, failure: "provider unavailable" })
    vi.spyOn(adminApi, "explainApproval").mockResolvedValue({ request_id: "req_failed", state: "pending", attempt: 2 })

    render(<RequestExplanation requestID="req_failed" />)
    expect(await screen.findByText("provider unavailable")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Retry explanation" }))
    await waitFor(() => expect(adminApi.explainApproval).toHaveBeenCalledWith("req_failed", true))
  })
})
