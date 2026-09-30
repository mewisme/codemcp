import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { adminApi, type LLMProvider } from "@/lib/api"
import { LLMPage } from "@/pages/llm"

const provider: LLMProvider = {
  id: "openrouter",
  name: "OpenRouter",
  protocol: "openai",
  base_url: "https://openrouter.ai/api/v1",
  model: "openrouter/free",
  auth_mode: "bearer",
  discovery: "openai-models",
  core_kind: "openrouter",
  core: true,
  selected: true,
  configured: true,
  readiness: "ready",
  credential: { provider_id: "openrouter", configured: true, preview: "sk-…abcd" },
}

describe("LLMPage", () => {
  beforeEach(() => {
    vi.spyOn(adminApi, "llmStatus").mockResolvedValue({ active_provider: provider.id, active: provider, providers: [provider] })
    vi.spyOn(adminApi, "llmProviders").mockResolvedValue([provider])
    vi.spyOn(adminApi, "llmModels").mockResolvedValue({
      provider_id: provider.id,
      total_catalog: 1,
      matched: 1,
      offset: 0,
      limit: 25,
      returned: 1,
      has_more: false,
      models: [{ id: "openrouter/free", name: "Free Router", free: true, free_known: true }],
      refreshed: false,
      sort: [{ field: "id", direction: "asc" }],
      query_capabilities: {
        filters: ["search", "id", "author", "free"],
        sorts: ["id", "name"],
        ranks: ["usage", "trending"],
        rank_windows: ["day", "week", "month"],
        recommendation: true,
      },
    })
    vi.spyOn(adminApi, "setLLMCredential").mockResolvedValue({ provider_id: provider.id, configured: true, preview: "sk-…wxyz" })
    vi.spyOn(adminApi, "clearLLMCredential").mockResolvedValue({ provider_id: provider.id, configured: false, preview: "" })
    vi.spyOn(adminApi, "selectLLMProvider").mockResolvedValue(provider)
    vi.spyOn(adminApi, "probeLLMProvider").mockResolvedValue({ provider_id: provider.id, model: provider.model, readiness: "ready" })
    vi.spyOn(adminApi, "setLLMProviderModel").mockResolvedValue(provider)
  })

  afterEach(() => vi.restoreAllMocks())

  it("uses only Admin API state, clears protected credentials, and refetches canonical reads after mutation", async () => {
    const user = userEvent.setup()
    const localSet = vi.spyOn(Storage.prototype, "setItem")
    const sessionSet = vi.spyOn(sessionStorage, "setItem")
    const secret = "sk-browser-ui-secret-never-persist"

    render(<LLMPage />)
    expect(await screen.findByText("OpenRouter")).toBeInTheDocument()
    expect(screen.getAllByText("https://openrouter.ai/api/v1").length).toBeGreaterThan(0)
    expect(screen.getAllByText("sk-…abcd").length).toBeGreaterThan(0)

    await user.click(screen.getByRole("button", { name: "Manage provider" }))
    await waitFor(() => expect(adminApi.llmModels).toHaveBeenCalledWith("openrouter", { limit: 25 }))
    expect(await screen.findByText("Free Router")).toBeInTheDocument()
    expect(screen.getByLabelText("Search")).toBeInTheDocument()
    expect(screen.getByLabelText("Price")).toBeInTheDocument()
    expect(screen.getByLabelText("Author")).toBeInTheDocument()
    expect(screen.getByLabelText("Sort")).toBeInTheDocument()
    expect(screen.getByLabelText("Rank")).toBeInTheDocument()
    expect(screen.getByLabelText("Recommend for")).toBeInTheDocument()
    expect(screen.getByLabelText("Model ID")).toBeInTheDocument()

    await user.type(screen.getByLabelText("Search"), "vendor")
    await user.click(screen.getByRole("button", { name: "Apply model query" }))
    await waitFor(() => expect(adminApi.llmModels).toHaveBeenLastCalledWith("openrouter", expect.objectContaining({
      search: "vendor",
      sort: ["id:asc"],
      offset: 0,
      limit: 25,
      refresh: false,
    })))

    const keyInput = screen.getByLabelText("New API key") as HTMLInputElement
    expect(keyInput.type).toBe("password")
    expect(keyInput.autocomplete).toBe("new-password")
    await user.type(keyInput, secret)
    await user.click(screen.getByRole("button", { name: "Set key" }))

    await waitFor(() => expect(adminApi.setLLMCredential).toHaveBeenCalledWith("openrouter", secret))
    expect(keyInput.value).toBe("")
    expect(adminApi.llmStatus).toHaveBeenCalledTimes(2)
    expect(adminApi.llmProviders).toHaveBeenCalledTimes(2)

    for (const call of [...localSet.mock.calls, ...sessionSet.mock.calls]) {
      expect(call.join(" ")).not.toContain(secret)
    }
    expect(window.location.href).not.toContain(secret)
  })
})
