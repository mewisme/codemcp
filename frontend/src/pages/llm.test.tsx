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
    const modelInput = screen.getByLabelText("Model ID") as HTMLInputElement
    expect(modelInput).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Probe" }))
    await waitFor(() => expect(adminApi.probeLLMProvider).toHaveBeenCalledWith("openrouter"))

    await user.clear(modelInput)
    await user.type(modelInput, "vendor/model-a")
    await user.click(screen.getByRole("button", { name: "Set model" }))
    await waitFor(() => expect(adminApi.setLLMProviderModel).toHaveBeenCalledWith("openrouter", "vendor/model-a"))

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
    expect(vi.mocked(adminApi.llmStatus).mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(vi.mocked(adminApi.llmProviders).mock.calls.length).toBeGreaterThanOrEqual(2)

    for (const call of [...localSet.mock.calls, ...sessionSet.mock.calls]) {
      expect(call.join(" ")).not.toContain(secret)
    }
    expect(window.location.href).not.toContain(secret)
  })

  it("keeps an inactive custom provider fully manageable without implicitly selecting it", async () => {
    const user = userEvent.setup()
    const custom: LLMProvider = {
      ...provider,
      id: "custom-fixture",
      name: "Custom Fixture",
      base_url: "https://custom.example/v1",
      model: "custom/model-a",
      auth_mode: "none",
      discovery: "openai-models",
      core_kind: undefined,
      core: false,
      selected: false,
      credential: { provider_id: "custom-fixture", configured: false, preview: "" },
    }
    vi.mocked(adminApi.llmStatus).mockResolvedValue({
      active_provider: provider.id,
      active: provider,
      providers: [provider, custom],
    })
    vi.mocked(adminApi.llmProviders).mockResolvedValue([provider, custom])
    vi.mocked(adminApi.llmModels).mockResolvedValue({
      provider_id: custom.id,
      total_catalog: 1,
      matched: 1,
      offset: 0,
      limit: 25,
      returned: 1,
      has_more: false,
      models: [{ id: "custom/model-b", name: "Custom Model B" }],
      refreshed: false,
      sort: [{ field: "id", direction: "asc" }],
      query_capabilities: { filters: ["search"], sorts: ["id"], recommendation: false },
    })
    vi.mocked(adminApi.selectLLMProvider).mockResolvedValue({ ...custom, selected: true })
    vi.mocked(adminApi.probeLLMProvider).mockResolvedValue({ provider_id: custom.id, model: custom.model, readiness: "ready" })
    vi.mocked(adminApi.setLLMProviderModel).mockResolvedValue({ ...custom, model: "custom/model-b" })
    vi.spyOn(adminApi, "configureLLMProvider").mockResolvedValue(custom)
    vi.spyOn(adminApi, "removeLLMProvider").mockResolvedValue({ provider_id: custom.id, removed: true })

    render(<LLMPage />)
    expect(await screen.findByText("Custom Fixture")).toBeInTheDocument()
    const manageButtons = screen.getAllByRole("button", { name: "Manage provider" })
    await user.click(manageButtons[1])

    expect(await screen.findByRole("button", { name: "Use provider" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Probe" }))
    await waitFor(() => expect(adminApi.probeLLMProvider).toHaveBeenCalledWith(custom.id))
    expect(adminApi.selectLLMProvider).not.toHaveBeenCalled()

    const modelInput = screen.getByLabelText("Model ID") as HTMLInputElement
    await user.clear(modelInput)
    await user.type(modelInput, "custom/model-b")
    await user.click(screen.getByRole("button", { name: "Set model" }))
    await waitFor(() => expect(adminApi.setLLMProviderModel).toHaveBeenCalledWith(custom.id, "custom/model-b"))
    expect(adminApi.selectLLMProvider).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "Edit" }))
    expect(await screen.findByText("Edit LLM provider")).toBeInTheDocument()
    const nameInput = screen.getByLabelText("Name") as HTMLInputElement
    await user.clear(nameInput)
    await user.type(nameInput, "Custom Fixture Updated")
    await user.click(screen.getByRole("button", { name: "Save provider" }))
    await waitFor(() =>
      expect(adminApi.configureLLMProvider).toHaveBeenCalledWith(
        custom.id,
        expect.objectContaining({ name: "Custom Fixture Updated" })
      )
    )
    expect(adminApi.selectLLMProvider).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "Remove" }))
    expect(await screen.findByText("Remove LLM provider?")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Remove provider" }))
    await waitFor(() => expect(adminApi.removeLLMProvider).toHaveBeenCalledWith(custom.id))
    expect(adminApi.selectLLMProvider).not.toHaveBeenCalled()
  })
})
