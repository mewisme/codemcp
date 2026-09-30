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
  type LLMModel,
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
    setError("")
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

  useEffect(() => { void load() }, [load])

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
  const [models, setModels] = useState<LLMModel[]>([])
  const [modelsBusy, setModelsBusy] = useState(false)
  const [modelsError, setModelsError] = useState("")
  const [model, setModel] = useState(provider.model || "")
  const [apiKey, setAPIKey] = useState("")

  useEffect(() => { setModel(provider.model || ""); setAPIKey("") }, [provider.id, provider.model])

  const loadModels = useCallback(async (refresh = false) => {
    setModelsBusy(true)
    setModelsError("")
    try {
      const result = await adminApi.llmModels(provider.id, refresh)
      setModels(result.models || [])
    } catch (value) {
      setModels([])
      setModelsError(errorText(value))
    } finally {
      setModelsBusy(false)
    }
  }, [provider.id])

  useEffect(() => { void loadModels(false) }, [loadModels])

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
          <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
            <div>
              <Label htmlFor={`llm-model-${provider.id}`}>Model ID</Label>
              <Input id={`llm-model-${provider.id}`} list={`llm-models-${provider.id}`} value={model} onChange={(event) => setModel(event.target.value)} />
              <datalist id={`llm-models-${provider.id}`}>{models.map((item) => <option key={item.id} value={item.id}>{item.name || item.id}</option>)}</datalist>
            </div>
            <Button className="self-end" disabled={busy || !model.trim() || model.trim() === (provider.model || "")} onClick={() => void onMutate(() => adminApi.setLLMProviderModel(provider.id, model.trim()))}>Set model</Button>
          </div>
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
