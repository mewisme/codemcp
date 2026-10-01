import { useCallback, useEffect, useMemo, useState } from "react"
import { BrainCircuit, KeyRound, Plus, RefreshCw, Trash2, Zap } from "lucide-react"

import { PageHeader } from "@/components/page-header"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
  adminApi,
  type LLMModelCatalog,
  type LLMProvider,
  type LLMProviderConfig,
  type LLMStatus,
} from "@/lib/api"

const emptyConfig: LLMProviderConfig = {
  name: "",
  protocol: "openai",
  base_url: "",
  model: "",
  auth_mode: "none",
  discovery: "none",
}

export function LLMPage() {
  const [status, setStatus] = useState<LLMStatus | null>(null)
  const [providers, setProviders] = useState<LLMProvider[]>([])
  const [selectedID, setSelectedID] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [editor, setEditor] = useState<{ mode: "add" | "edit"; id: string; config: LLMProviderConfig } | null>(null)
  const [removeTarget, setRemoveTarget] = useState<LLMProvider | null>(null)

  const load = useCallback(async () => {
    try {
      const [nextStatus, nextProviders] = await Promise.all([
        adminApi.llmStatus(),
        adminApi.llmProviders(),
      ])
      setStatus(nextStatus)
      setProviders(nextProviders)
      setSelectedID((current) => current && nextProviders.some((value) => value.id === current) ? current : "")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    void Promise.all([adminApi.llmStatus(), adminApi.llmProviders()])
      .then(([nextStatus, nextProviders]) => {
        if (cancelled) return
        setStatus(nextStatus)
        setProviders(nextProviders)
        setSelectedID((current) => current && nextProviders.some((value) => value.id === current) ? current : "")
      })
      .catch((value) => { if (!cancelled) setError(errorText(value)) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  const selected = useMemo(
    () => providers.find((provider) => provider.id === selectedID) ?? null,
    [providers, selectedID]
  )

  async function mutate(action: () => Promise<unknown>) {
    setBusy(true)
    setError("")
    try {
      await action()
      await load()
      return true
    } catch (value) {
      setError(errorText(value))
      return false
    } finally {
      setBusy(false)
    }
  }

  async function saveProvider(id: string, config: LLMProviderConfig, mode: "add" | "edit") {
    const ok = await mutate(() => mode === "add" ? adminApi.addLLMProvider(id, config) : adminApi.configureLLMProvider(id, config))
    if (ok) {
      setEditor(null)
      setSelectedID(id)
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="LLM providers"
        description="Manage canonical inference providers, credentials, models, and readiness."
        actions={<>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => { setLoading(true); void load() }}>
            <RefreshCw className={loading ? "animate-spin" : ""} />Refresh
          </Button>
          <Button size="sm" disabled={busy} onClick={() => setEditor({ mode: "add", id: "", config: { ...emptyConfig } })}>
            <Plus />Add provider
          </Button>
        </>}
      />
      <PageError message={error} />
      {status ? <ActiveProviderSummary status={status} /> : null}
      {loading ? <PageLoading rows={5} /> : providers.length === 0 ? (
        <PageEmpty icon={BrainCircuit} title="No LLM providers" description="Add a compatible provider to begin." action={<Button onClick={() => setEditor({ mode: "add", id: "", config: { ...emptyConfig } })}><Plus />Add provider</Button>} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-2 2xl:grid-cols-3">
          {providers.map((provider) => <ProviderCard key={provider.id} provider={provider} onOpen={() => setSelectedID(provider.id)} />)}
        </div>
      )}

      {selected ? (
        <ProviderDetail
          key={`${selected.id}:${selected.model || ""}`}
          provider={selected}
          busy={busy}
          open
          onOpenChange={(open) => { if (!open) setSelectedID("") }}
          onMutate={mutate}
          onEdit={() => setEditor({ mode: "edit", id: selected.id, config: configFromProvider(selected) })}
          onRemove={() => setRemoveTarget(selected)}
        />
      ) : null}

      {editor ? (
        <ProviderEditor
          value={editor}
          busy={busy}
          onOpenChange={(open) => { if (!open) setEditor(null) }}
          onSave={(id, config) => void saveProvider(id, config, editor.mode)}
        />
      ) : null}

      <AlertDialog open={Boolean(removeTarget)} onOpenChange={(open) => { if (!open) setRemoveTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove LLM provider?</AlertDialogTitle>
            <AlertDialogDescription>
              This removes {removeTarget?.name || removeTarget?.id} and its stored credential. Core providers cannot be removed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={busy || !removeTarget || removeTarget.core}
              onClick={() => {
                if (!removeTarget) return
                const id = removeTarget.id
                void mutate(() => adminApi.removeLLMProvider(id)).then((ok) => { if (ok) { setRemoveTarget(null); setSelectedID("") } })
              }}
            >
              <Trash2 />{busy ? "Removing..." : "Remove provider"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ActiveProviderSummary({ status }: { status: LLMStatus }) {
  const active = status.active
  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-base">Active provider</CardTitle>
          <ReadinessBadge readiness={active?.readiness || "unknown"} />
        </div>
        <CardDescription>{status.active_provider || "No active provider"}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <Field label="Model" value={active?.model || "Not configured"} />
        <Field label="Protocol" value={active?.protocol || "—"} />
        <Field label="Endpoint" value={active?.base_url || "—"} mono />
        <Field label="Credential" value={active?.credential?.configured ? active.credential.preview || "Configured" : "Not configured"} />
        {active?.reason ? <div className="sm:col-span-2 lg:col-span-4 text-sm text-muted-foreground">{active.reason}</div> : null}
      </CardContent>
    </Card>
  )
}

function ProviderCard({ provider, onOpen }: { provider: LLMProvider; onOpen: () => void }) {
  return (
    <Card className="min-w-0">
      <CardHeader className="pb-3">
        <div className="flex min-w-0 items-start justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="truncate text-base">{provider.name || provider.id}</CardTitle>
            <CardDescription className="truncate font-mono text-xs">{provider.id}</CardDescription>
          </div>
          <div className="flex flex-wrap justify-end gap-1.5">
            {provider.selected ? <Badge>Active</Badge> : null}
            {provider.core ? <Badge variant="secondary">Core</Badge> : null}
            <ReadinessBadge readiness={provider.readiness} />
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-2 text-sm sm:grid-cols-2">
          <Field label="Protocol" value={provider.protocol} />
          <Field label="Model" value={provider.model || "Not configured"} />
          <Field label="Endpoint" value={provider.base_url} mono className="sm:col-span-2" />
          <Field label="API key" value={provider.credential.configured ? provider.credential.preview || "Configured" : "Not configured"} />
          <Field label="Discovery" value={provider.discovery} />
        </div>
        {provider.reason ? <p className="text-sm text-muted-foreground">{provider.reason}</p> : null}
        <Button className="w-full" variant="outline" onClick={onOpen}>Manage provider</Button>
      </CardContent>
    </Card>
  )
}

function ProviderDetail({ provider, busy, open, onOpenChange, onMutate, onEdit, onRemove }: {
  provider: LLMProvider
  busy: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
  onMutate: (action: () => Promise<unknown>) => Promise<boolean>
  onEdit: () => void
  onRemove: () => void
}) {
  const [catalog, setCatalog] = useState<LLMModelCatalog | null>(null)
  const [modelsBusy, setModelsBusy] = useState(true)
  const [modelsError, setModelsError] = useState("")
  const [model, setModel] = useState(provider.model || "")
  const [apiKey, setAPIKey] = useState("")
  const [modelSearch, setModelSearch] = useState("")
  const [modelPrice, setModelPrice] = useState("all")
  const [modelAuthor, setModelAuthor] = useState("")
  const [modelMinContext, setModelMinContext] = useState("")
  const [modelMaxContext, setModelMaxContext] = useState("")
  const [modelMinPromptPrice, setModelMinPromptPrice] = useState("")
  const [modelMaxPromptPrice, setModelMaxPromptPrice] = useState("")
  const [modelMinCompletionPrice, setModelMinCompletionPrice] = useState("")
  const [modelMaxCompletionPrice, setModelMaxCompletionPrice] = useState("")
  const [modelCapability, setModelCapability] = useState("")
  const [modelParameter, setModelParameter] = useState("")
  const [modelInput, setModelInput] = useState("")
  const [modelOutput, setModelOutput] = useState("")
  const [modelFamily, setModelFamily] = useState("")
  const [modelFormat, setModelFormat] = useState("")
  const [modelQuantization, setModelQuantization] = useState("")
  const [modelMinParameters, setModelMinParameters] = useState("")
  const [modelMaxParameters, setModelMaxParameters] = useState("")
  const [modelMinSize, setModelMinSize] = useState("")
  const [modelMaxSize, setModelMaxSize] = useState("")
  const [modelCreatedAfter, setModelCreatedAfter] = useState("")
  const [modelCreatedBefore, setModelCreatedBefore] = useState("")
  const [modelModifiedAfter, setModelModifiedAfter] = useState("")
  const [modelModifiedBefore, setModelModifiedBefore] = useState("")
  const [modelSort, setModelSort] = useState("id:asc")
  const [modelRank, setModelRank] = useState("")
  const [modelRankWindow, setModelRankWindow] = useState("week")
  const [modelTask, setModelTask] = useState("")
  const [modelOffset, setModelOffset] = useState(0)
  const modelLimit = 25

  async function loadModels(refresh = false, offset = modelOffset) {
    setModelsBusy(true)
    setModelsError("")
    try {
      const capabilities = catalog?.query_capabilities
      const optionalNumber = (value: string) => value.trim() === "" ? undefined : Number(value)
      const result = await adminApi.llmModels(provider.id, {
        search: modelSearch.trim() || undefined,
        free: modelPrice === "free" ? true : undefined,
        paid: modelPrice === "paid" ? true : undefined,
        author: capabilities?.filters.includes("author") && modelAuthor.trim() ? [modelAuthor.trim()] : undefined,
        min_context: capabilities?.filters.includes("context") ? optionalNumber(modelMinContext) : undefined,
        max_context: capabilities?.filters.includes("context") ? optionalNumber(modelMaxContext) : undefined,
        min_prompt_price: capabilities?.filters.includes("prompt-price") && modelMinPromptPrice.trim() ? modelMinPromptPrice.trim() : undefined,
        max_prompt_price: capabilities?.filters.includes("prompt-price") && modelMaxPromptPrice.trim() ? modelMaxPromptPrice.trim() : undefined,
        min_completion_price: capabilities?.filters.includes("completion-price") && modelMinCompletionPrice.trim() ? modelMinCompletionPrice.trim() : undefined,
        max_completion_price: capabilities?.filters.includes("completion-price") && modelMaxCompletionPrice.trim() ? modelMaxCompletionPrice.trim() : undefined,
        capability: capabilities?.filters.includes("capability") && modelCapability.trim() ? [modelCapability.trim()] : undefined,
        parameter: capabilities?.filters.includes("parameter") && modelParameter.trim() ? [modelParameter.trim()] : undefined,
        input: capabilities?.filters.includes("input") && modelInput.trim() ? [modelInput.trim()] : undefined,
        output: capabilities?.filters.includes("output") && modelOutput.trim() ? [modelOutput.trim()] : undefined,
        family: capabilities?.filters.includes("family") && modelFamily.trim() ? [modelFamily.trim()] : undefined,
        format: capabilities?.filters.includes("format") && modelFormat.trim() ? [modelFormat.trim()] : undefined,
        quantization: capabilities?.filters.includes("quantization") && modelQuantization.trim() ? [modelQuantization.trim()] : undefined,
        min_parameters: capabilities?.filters.includes("parameter-size") ? optionalNumber(modelMinParameters) : undefined,
        max_parameters: capabilities?.filters.includes("parameter-size") ? optionalNumber(modelMaxParameters) : undefined,
        min_size: capabilities?.filters.includes("size") ? optionalNumber(modelMinSize) : undefined,
        max_size: capabilities?.filters.includes("size") ? optionalNumber(modelMaxSize) : undefined,
        created_after: capabilities?.filters.includes("created") && modelCreatedAfter.trim() ? modelCreatedAfter.trim() : undefined,
        created_before: capabilities?.filters.includes("created") && modelCreatedBefore.trim() ? modelCreatedBefore.trim() : undefined,
        modified_after: capabilities?.filters.includes("modified") && modelModifiedAfter.trim() ? modelModifiedAfter.trim() : undefined,
        modified_before: capabilities?.filters.includes("modified") && modelModifiedBefore.trim() ? modelModifiedBefore.trim() : undefined,
        sort: !modelRank && !modelTask && modelSort ? [modelSort] : undefined,
        rank: modelRank || undefined,
        window: modelRank === "usage" ? modelRankWindow : modelRank === "trending" ? "week" : undefined,
        recommend_for: !modelRank && modelTask.trim() ? modelTask.trim() : undefined,
        offset,
        limit: modelLimit,
        refresh,
        check_access: provider.id === "ollama",
      })
      setCatalog(result)
      setModelOffset(result.offset)
    } catch (value) {
      setCatalog(null)
      setModelsError(errorText(value))
    } finally {
      setModelsBusy(false)
    }
  }

  useEffect(() => {
    let cancelled = false
    adminApi.llmModels(provider.id, { limit: modelLimit, check_access: provider.id === "ollama" }).then((result) => {
      if (!cancelled) { setCatalog(result); setModelOffset(result.offset); setModelsError("") }
    }).catch((value) => { if (!cancelled) { setCatalog(null); setModelsError(errorText(value)) } }).finally(() => { if (!cancelled) setModelsBusy(false) })
    return () => { cancelled = true }
  }, [provider.id])

  const models = catalog?.models || []
  const modelCapabilities = catalog?.query_capabilities

  async function setCredential() {
    if (!apiKey) return
    const value = apiKey
    setAPIKey("")
    await onMutate(() => adminApi.setLLMCredential(provider.id, value))
  }

  const footer = <div className="flex w-full flex-wrap justify-between gap-2">
    <div className="flex gap-2">
      {!provider.core ? <Button variant="outline" disabled={busy} onClick={onEdit}>Edit</Button> : null}
      {!provider.core ? <Button variant="destructive" disabled={busy || provider.selected} onClick={onRemove}><Trash2 />Remove</Button> : null}
    </div>
    <Button variant="outline" onClick={() => onOpenChange(false)}>Close</Button>
  </div>

  return (
    <ResponsiveDialog open={open} onOpenChange={onOpenChange} title={provider.name || provider.id} description={provider.id} wide footer={footer}>
      <div className="space-y-6">
        <div className="flex flex-wrap gap-2">
          {provider.selected ? <Badge>Active</Badge> : null}
          {provider.core ? <Badge variant="secondary">Core</Badge> : null}
          <ReadinessBadge readiness={provider.readiness} />
          <Badge variant="outline">{provider.protocol}</Badge>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Endpoint" value={provider.base_url} mono className="sm:col-span-2" />
          <Field label="Auth" value={provider.auth_mode} />
          <Field label="Discovery" value={provider.discovery} />
          <Field label="Configured" value={provider.configured ? "Yes" : "No"} />
          <Field label="Credential" value={provider.credential.configured ? provider.credential.preview || "Configured" : "Not configured"} />
        </div>
        {provider.reason ? <PageError title="Provider degraded" message={provider.reason} /> : null}
        <div className="flex flex-wrap gap-2">
          {!provider.selected ? <Button disabled={busy} onClick={() => void onMutate(() => adminApi.selectLLMProvider(provider.id))}>Use provider</Button> : null}
          <Button disabled={busy} variant="outline" onClick={() => void onMutate(() => adminApi.probeLLMProvider(provider.id))}><Zap />Probe</Button>
        </div>

        <Separator />
        <section className="space-y-3" aria-labelledby="llm-model-heading">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div><h3 id="llm-model-heading" className="font-medium">Model</h3><p className="text-sm text-muted-foreground">Choose a discovered model or enter an exact provider model ID.</p></div>
            <Button size="sm" variant="outline" disabled={modelsBusy} onClick={() => void loadModels(true)}><RefreshCw className={modelsBusy ? "animate-spin" : ""} />Refresh models</Button>
          </div>
          {modelsError ? <PageError title="Model catalog unavailable" message={modelsError} /> : null}
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            <FormField label="Search" htmlFor={`llm-model-search-${provider.id}`}><Input id={`llm-model-search-${provider.id}`} value={modelSearch} onChange={(event) => setModelSearch(event.target.value)} placeholder="ID, name, author" /></FormField>
            {modelCapabilities?.filters.includes("free") ? <FormField label="Price" htmlFor={`llm-model-price-${provider.id}`}><Select value={modelPrice} onValueChange={setModelPrice}><SelectTrigger id={`llm-model-price-${provider.id}`} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">All prices</SelectItem><SelectItem value="free">Free</SelectItem><SelectItem value="paid">Paid</SelectItem></SelectContent></Select></FormField> : null}
            {modelCapabilities?.filters.includes("author") ? <FormField label="Author" htmlFor={`llm-model-author-${provider.id}`}><Input id={`llm-model-author-${provider.id}`} value={modelAuthor} onChange={(event) => setModelAuthor(event.target.value)} /></FormField> : null}
            {modelCapabilities?.filters.includes("context") ? <><FormField label="Min context" htmlFor={`llm-model-min-context-${provider.id}`}><Input id={`llm-model-min-context-${provider.id}`} type="number" min="0" value={modelMinContext} onChange={(event) => setModelMinContext(event.target.value)} /></FormField><FormField label="Max context" htmlFor={`llm-model-max-context-${provider.id}`}><Input id={`llm-model-max-context-${provider.id}`} type="number" min="0" value={modelMaxContext} onChange={(event) => setModelMaxContext(event.target.value)} /></FormField></> : null}
            {modelCapabilities?.filters.includes("prompt-price") ? <><FormField label="Min prompt price" htmlFor={`llm-model-min-prompt-price-${provider.id}`}><Input id={`llm-model-min-prompt-price-${provider.id}`} value={modelMinPromptPrice} onChange={(event) => setModelMinPromptPrice(event.target.value)} /></FormField><FormField label="Max prompt price" htmlFor={`llm-model-max-prompt-price-${provider.id}`}><Input id={`llm-model-max-prompt-price-${provider.id}`} value={modelMaxPromptPrice} onChange={(event) => setModelMaxPromptPrice(event.target.value)} /></FormField></> : null}
            {modelCapabilities?.filters.includes("completion-price") ? <><FormField label="Min completion price" htmlFor={`llm-model-min-completion-price-${provider.id}`}><Input id={`llm-model-min-completion-price-${provider.id}`} value={modelMinCompletionPrice} onChange={(event) => setModelMinCompletionPrice(event.target.value)} /></FormField><FormField label="Max completion price" htmlFor={`llm-model-max-completion-price-${provider.id}`}><Input id={`llm-model-max-completion-price-${provider.id}`} value={modelMaxCompletionPrice} onChange={(event) => setModelMaxCompletionPrice(event.target.value)} /></FormField></> : null}
            {modelCapabilities?.filters.includes("capability") ? <FormField label="Capability" htmlFor={`llm-model-capability-${provider.id}`}><Input id={`llm-model-capability-${provider.id}`} value={modelCapability} onChange={(event) => setModelCapability(event.target.value)} placeholder="tools" /></FormField> : null}
            {modelCapabilities?.filters.includes("parameter") ? <FormField label="Parameter" htmlFor={`llm-model-parameter-${provider.id}`}><Input id={`llm-model-parameter-${provider.id}`} value={modelParameter} onChange={(event) => setModelParameter(event.target.value)} placeholder="structured_outputs" /></FormField> : null}
            {modelCapabilities?.filters.includes("input") ? <FormField label="Input modality" htmlFor={`llm-model-input-${provider.id}`}><Input id={`llm-model-input-${provider.id}`} value={modelInput} onChange={(event) => setModelInput(event.target.value)} placeholder="image" /></FormField> : null}
            {modelCapabilities?.filters.includes("output") ? <FormField label="Output modality" htmlFor={`llm-model-output-${provider.id}`}><Input id={`llm-model-output-${provider.id}`} value={modelOutput} onChange={(event) => setModelOutput(event.target.value)} placeholder="text" /></FormField> : null}
            {modelCapabilities?.filters.includes("family") ? <FormField label="Family" htmlFor={`llm-model-family-${provider.id}`}><Input id={`llm-model-family-${provider.id}`} value={modelFamily} onChange={(event) => setModelFamily(event.target.value)} /></FormField> : null}
            {modelCapabilities?.filters.includes("format") ? <FormField label="Format" htmlFor={`llm-model-format-${provider.id}`}><Input id={`llm-model-format-${provider.id}`} value={modelFormat} onChange={(event) => setModelFormat(event.target.value)} placeholder="gguf" /></FormField> : null}
            {modelCapabilities?.filters.includes("quantization") ? <FormField label="Quantization" htmlFor={`llm-model-quantization-${provider.id}`}><Input id={`llm-model-quantization-${provider.id}`} value={modelQuantization} onChange={(event) => setModelQuantization(event.target.value)} placeholder="Q4_K_M" /></FormField> : null}
            {modelCapabilities?.filters.includes("parameter-size") ? <><FormField label="Min parameters" htmlFor={`llm-model-min-parameters-${provider.id}`}><Input id={`llm-model-min-parameters-${provider.id}`} type="number" min="0" value={modelMinParameters} onChange={(event) => setModelMinParameters(event.target.value)} /></FormField><FormField label="Max parameters" htmlFor={`llm-model-max-parameters-${provider.id}`}><Input id={`llm-model-max-parameters-${provider.id}`} type="number" min="0" value={modelMaxParameters} onChange={(event) => setModelMaxParameters(event.target.value)} /></FormField></> : null}
            {modelCapabilities?.filters.includes("size") ? <><FormField label="Min size (bytes)" htmlFor={`llm-model-min-size-${provider.id}`}><Input id={`llm-model-min-size-${provider.id}`} type="number" min="0" value={modelMinSize} onChange={(event) => setModelMinSize(event.target.value)} /></FormField><FormField label="Max size (bytes)" htmlFor={`llm-model-max-size-${provider.id}`}><Input id={`llm-model-max-size-${provider.id}`} type="number" min="0" value={modelMaxSize} onChange={(event) => setModelMaxSize(event.target.value)} /></FormField></> : null}
            {modelCapabilities?.filters.includes("created") ? <><FormField label="Created after" htmlFor={`llm-model-created-after-${provider.id}`}><Input id={`llm-model-created-after-${provider.id}`} value={modelCreatedAfter} onChange={(event) => setModelCreatedAfter(event.target.value)} placeholder="2026-09-01T00:00:00Z" /></FormField><FormField label="Created before" htmlFor={`llm-model-created-before-${provider.id}`}><Input id={`llm-model-created-before-${provider.id}`} value={modelCreatedBefore} onChange={(event) => setModelCreatedBefore(event.target.value)} placeholder="2026-10-01T00:00:00Z" /></FormField></> : null}
            {modelCapabilities?.filters.includes("modified") ? <><FormField label="Modified after" htmlFor={`llm-model-modified-after-${provider.id}`}><Input id={`llm-model-modified-after-${provider.id}`} value={modelModifiedAfter} onChange={(event) => setModelModifiedAfter(event.target.value)} placeholder="2026-09-01T00:00:00Z" /></FormField><FormField label="Modified before" htmlFor={`llm-model-modified-before-${provider.id}`}><Input id={`llm-model-modified-before-${provider.id}`} value={modelModifiedBefore} onChange={(event) => setModelModifiedBefore(event.target.value)} placeholder="2026-10-01T00:00:00Z" /></FormField></> : null}
            <FormField label="Sort" htmlFor={`llm-model-sort-${provider.id}`}><Select value={modelSort} disabled={Boolean(modelRank || modelTask.trim())} onValueChange={setModelSort}><SelectTrigger id={`llm-model-sort-${provider.id}`} className="w-full"><SelectValue /></SelectTrigger><SelectContent>{(modelCapabilities?.sorts || ["id", "name"]).flatMap((field) => [<SelectItem key={`${field}-asc`} value={`${field}:asc`}>{field} ↑</SelectItem>, <SelectItem key={`${field}-desc`} value={`${field}:desc`}>{field} ↓</SelectItem>])}</SelectContent></Select></FormField>
            {modelCapabilities?.ranks?.length ? <FormField label="Rank" htmlFor={`llm-model-rank-${provider.id}`}><Select value={modelRank || "none"} onValueChange={(value) => { setModelRank(value === "none" ? "" : value); if (value !== "none") setModelTask("") }}><SelectTrigger id={`llm-model-rank-${provider.id}`} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">No rank</SelectItem>{modelCapabilities.ranks.map((rank) => <SelectItem key={rank} value={rank}>{rank}</SelectItem>)}</SelectContent></Select></FormField> : null}
            {modelRank === "usage" && modelCapabilities?.rank_windows?.length ? <FormField label="Rank window" htmlFor={`llm-model-rank-window-${provider.id}`}><Select value={modelRankWindow} onValueChange={setModelRankWindow}><SelectTrigger id={`llm-model-rank-window-${provider.id}`} className="w-full"><SelectValue /></SelectTrigger><SelectContent>{modelCapabilities.rank_windows.map((window) => <SelectItem key={window} value={window}>{window}</SelectItem>)}</SelectContent></Select></FormField> : null}
            {modelCapabilities?.recommendation ? <FormField label="Recommend for" htmlFor={`llm-model-task-${provider.id}`}><Input id={`llm-model-task-${provider.id}`} value={modelTask} disabled={Boolean(modelRank)} onChange={(event) => setModelTask(event.target.value)} placeholder="Code Generation" /></FormField> : null}
            <div className="flex items-end"><Button variant="outline" disabled={modelsBusy} onClick={() => { setModelOffset(0); void loadModels(false, 0) }}>Apply model query</Button></div>
          </div>
          {catalog ? <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
            <span>
              {catalog.matched} matched / {catalog.total_catalog} catalog · {catalog.returned ? `${catalog.offset + 1}-${catalog.offset + catalog.returned}` : "0"}
              {catalog.model_access ? ` · ${catalog.access_available || 0} available · ${catalog.access_unavailable || 0} unavailable · ${catalog.access_unknown || 0} unknown` : ""}
            </span>
            <span>{catalog.rank_basis || catalog.recommendation_basis || "Catalog order"}</span>
          </div> : null}
          {catalog?.access_error ? <PageError title="Model access check incomplete" message={catalog.access_error} /> : null}
          <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
            <div>
              <Label htmlFor={`llm-model-${provider.id}`}>Model ID</Label>
              <Input id={`llm-model-${provider.id}`} list={`llm-models-${provider.id}`} value={model} onChange={(event) => setModel(event.target.value)} />
              <datalist id={`llm-models-${provider.id}`}>
                {provider.id === "ollama" ? <option value="auto">Auto — automatically choose and switch models</option> : null}
                {models
                  .filter((item) => catalog?.model_access?.[item.id]?.state !== "unavailable")
                  .map((item) => {
                    const access = catalog?.model_access?.[item.id]?.state
                    const label = item.name || item.id
                    return <option key={item.id} value={item.id}>{access ? `${label} — ${access}` : label}</option>
                  })}
              </datalist>
            </div>
            <Button className="self-end" disabled={busy || !model.trim() || model.trim() === (provider.model || "")} onClick={() => void onMutate(() => adminApi.setLLMProviderModel(provider.id, model.trim()))}>Set model</Button>
          </div>
          {catalog ? <div className="flex justify-end gap-2"><Button size="sm" variant="outline" disabled={modelsBusy || catalog.offset <= 0} onClick={() => void loadModels(false, Math.max(0, catalog.offset - modelLimit))}>Previous</Button><Button size="sm" variant="outline" disabled={modelsBusy || !catalog.has_more} onClick={() => void loadModels(false, catalog.offset + catalog.returned)}>Next</Button></div> : null}
          {!modelsBusy && !modelsError && models.length === 0 ? <p className="text-sm text-muted-foreground">No discovered models. Exact model IDs can still be entered manually.</p> : null}
        </section>

        <Separator />
        <section className="space-y-3" aria-labelledby="llm-key-heading">
          <div><h3 id="llm-key-heading" className="font-medium">API key</h3><p className="text-sm text-muted-foreground">The raw value is submitted directly to the Admin API and is not stored in browser storage.</p></div>
          <div className="grid gap-2 sm:grid-cols-[1fr_auto_auto]">
            <div>
              <Label htmlFor={`llm-key-${provider.id}`}>New API key</Label>
              <Input id={`llm-key-${provider.id}`} type="password" autoComplete="new-password" value={apiKey} onChange={(event) => setAPIKey(event.target.value)} placeholder={provider.credential.configured ? provider.credential.preview || "Configured" : "Enter API key"} />
            </div>
            <Button className="self-end" disabled={busy || !apiKey} onClick={() => void setCredential()}><KeyRound />Set key</Button>
            <Button className="self-end" variant="outline" disabled={busy || !provider.credential.configured} onClick={() => { setAPIKey(""); void onMutate(() => adminApi.clearLLMCredential(provider.id)) }}>Clear key</Button>
          </div>
        </section>
      </div>
    </ResponsiveDialog>
  )
}

function ProviderEditor({ value, busy, onOpenChange, onSave }: {
  value: { mode: "add" | "edit"; id: string; config: LLMProviderConfig }
  busy: boolean
  onOpenChange: (open: boolean) => void
  onSave: (id: string, config: LLMProviderConfig) => void
}) {
  const [id, setID] = useState(value.id)
  const [config, setConfig] = useState<LLMProviderConfig>({ ...value.config })
  const valid = id.trim() && config.base_url.trim() && config.protocol
  return (
    <ResponsiveDialog
      open
      onOpenChange={onOpenChange}
      title={value.mode === "add" ? "Add LLM provider" : "Edit LLM provider"}
      description="Custom providers use OpenAI-compatible or Anthropic-compatible protocols."
      footer={<><Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button><Button disabled={busy || !valid} onClick={() => onSave(id.trim(), { ...config, name: config.name.trim(), base_url: config.base_url.trim(), model: config.model.trim() })}>{busy ? "Saving..." : "Save provider"}</Button></>}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <FormField label="Provider ID" htmlFor="llm-provider-id" className="sm:col-span-2"><Input id="llm-provider-id" value={id} disabled={value.mode === "edit"} onChange={(event) => setID(event.target.value)} placeholder="my-provider" /></FormField>
        <FormField label="Name" htmlFor="llm-provider-name"><Input id="llm-provider-name" value={config.name} onChange={(event) => setConfig({ ...config, name: event.target.value })} placeholder="My provider" /></FormField>
        <FormField label="Protocol" htmlFor="llm-provider-protocol"><Select value={config.protocol} onValueChange={(protocol) => setConfig({ ...config, protocol })}><SelectTrigger id="llm-provider-protocol" className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="openai">OpenAI compatible</SelectItem><SelectItem value="anthropic">Anthropic compatible</SelectItem></SelectContent></Select></FormField>
        <FormField label="Base URL" htmlFor="llm-provider-url" className="sm:col-span-2"><Input id="llm-provider-url" value={config.base_url} onChange={(event) => setConfig({ ...config, base_url: event.target.value })} placeholder="https://api.example.com/v1" /></FormField>
        <FormField label="Model" htmlFor="llm-provider-model"><Input id="llm-provider-model" value={config.model} onChange={(event) => setConfig({ ...config, model: event.target.value })} /></FormField>
        <FormField label="Authentication" htmlFor="llm-provider-auth"><Select value={config.auth_mode} onValueChange={(auth_mode) => setConfig({ ...config, auth_mode })}><SelectTrigger id="llm-provider-auth" className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">None</SelectItem><SelectItem value="bearer">Bearer</SelectItem><SelectItem value="x-api-key">x-api-key</SelectItem></SelectContent></Select></FormField>
        <FormField label="Model discovery" htmlFor="llm-provider-discovery" className="sm:col-span-2"><Select value={config.discovery} onValueChange={(discovery) => setConfig({ ...config, discovery })}><SelectTrigger id="llm-provider-discovery" className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">None</SelectItem><SelectItem value="openai-models">OpenAI /models</SelectItem><SelectItem value="ollama-tags">Ollama /api/tags</SelectItem></SelectContent></Select></FormField>
      </div>
    </ResponsiveDialog>
  )
}

function FormField({ label, htmlFor, children, className = "" }: { label: string; htmlFor: string; children: React.ReactNode; className?: string }) {
  return <div className={`space-y-1.5 ${className}`}><Label htmlFor={htmlFor}>{label}</Label>{children}</div>
}

function Field({ label, value, mono = false, className = "" }: { label: string; value: string; mono?: boolean; className?: string }) {
  return <div className={`min-w-0 ${className}`}><div className="text-xs text-muted-foreground">{label}</div><div className={`${mono ? "font-mono text-xs" : "text-sm"} break-all`}>{value || "—"}</div></div>
}

function ReadinessBadge({ readiness }: { readiness: string }) {
  const variant = readiness === "ready" ? "default" : readiness === "degraded" || readiness === "unavailable" ? "destructive" : "outline"
  return <Badge variant={variant}>{readiness || "unknown"}</Badge>
}

function configFromProvider(provider: LLMProvider): LLMProviderConfig {
  return {
    name: provider.name,
    protocol: provider.protocol,
    base_url: provider.base_url,
    model: provider.model || "",
    auth_mode: provider.auth_mode,
    discovery: provider.discovery,
  }
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
