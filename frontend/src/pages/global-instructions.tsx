import { useEffect, useMemo, useState } from "react"
import { Plus, Save, Trash2, Undo2 } from "lucide-react"
import { PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { ScrollableTabsList, Tabs, TabsContent, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { adminApi, type GlobalInstructionRule, type GlobalInstructions, type InstructionSource, type InstructionSourcePolicy } from "@/lib/api"

const resourceLabels: Record<string, string> = { context: "Context", rules: "Rules", skills: "Skills" }

export function GlobalInstructionsPage() {
  const [settings, setSettings] = useState<GlobalInstructions | null>(null)
  const [saved, setSaved] = useState<GlobalInstructions | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")

  useEffect(() => { let active = true; void adminApi.globalInstructions().then((value) => { if (active) { const next = normalizeGlobalInstructions(value); setSettings(next); setSaved(next); setError("") } }).catch((value) => { if (active) setError(errorText(value)) }); return () => { active = false } }, [])
  const dirty = useMemo(() => Boolean(settings && saved && editableJSON(settings) !== editableJSON(saved)), [settings, saved])
  const providers = useMemo(() => groupSources(settings?.detected_sources ?? []), [settings?.detected_sources])

  async function save() {
    if (!settings) return
    setBusy(true)
    try {
      const next = normalizeGlobalInstructions(await adminApi.saveGlobalInstructions({ context: settings.context, rules: settings.rules, source_policy: settings.source_policy }))
      setSettings(next); setSaved(next); setMessage("Saved. New project_context calls use these instructions immediately."); setError("")
    } catch (value) { setError(errorText(value)); setMessage("") } finally { setBusy(false) }
  }
  function updatePolicy(provider: string, update: Partial<InstructionSourcePolicy>) {
    if (!settings) return
    setSettings({ ...settings, source_policy: { ...settings.source_policy, [provider]: { ...(settings.source_policy[provider] ?? {}), ...update } } })
  }
  function updateRule(index: number, update: Partial<GlobalInstructionRule>) {
    if (!settings) return
    setSettings({ ...settings, rules: settings.rules.map((rule, current) => current === index ? { ...rule, ...update } : rule) })
  }
  function addRule() {
    if (!settings) return
    setSettings({ ...settings, rules: [...settings.rules, { id: newRuleID(), name: "New rule", enabled: true, content: "" }] })
  }

  if (!settings || !saved) return <div className="space-y-4"><PageError message={error} /><PageLoading rows={5} /></div>
  return <div className="space-y-6"><PageHeader title="Global Instructions" description="Manage global context, rules, and detected user-level instruction sources." actions={<Badge variant="secondary">{settings.detected_sources.length} detected source{settings.detected_sources.length === 1 ? "" : "s"}</Badge>} /><PageError message={error} />{message ? <div className="rounded-lg border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">{message}</div> : null}<Tabs defaultValue="context"><ScrollableTabsList className="justify-start"><TabsTrigger value="context">Global Context</TabsTrigger><TabsTrigger value="rules">Global Rules</TabsTrigger><TabsTrigger value="sources">Sources</TabsTrigger></ScrollableTabsList><TabsContent className="mt-6" value="context"><Card><CardHeader><CardTitle>Global context</CardTitle><CardDescription>Injected into every project_context independently from project and user memory.</CardDescription></CardHeader><CardContent><Textarea className="min-h-72 font-mono" placeholder="Instructions shared by every workspace..." value={settings.context} onChange={(event) => setSettings({ ...settings, context: event.target.value })} /></CardContent></Card></TabsContent><TabsContent className="mt-6 space-y-4" value="rules"><div className="flex justify-end"><Button size="sm" onClick={addRule}><Plus />Add rule</Button></div>{settings.rules.length === 0 ? <Card><CardContent className="py-8 text-center text-sm text-muted-foreground">No managed global rules.</CardContent></Card> : settings.rules.map((rule, index) => <Card key={rule.id}><CardHeader className="border-b"><div className="flex min-w-0 items-center gap-3"><Switch checked={rule.enabled} onCheckedChange={(enabled) => updateRule(index, { enabled })} /><Input className="min-w-0 flex-1" aria-label={`Rule ${index + 1} name`} value={rule.name ?? ""} onChange={(event) => updateRule(index, { name: event.target.value })} /><Button aria-label={`Delete ${rule.name || rule.id}`} size="icon-sm" variant="ghost" onClick={() => setSettings({ ...settings, rules: settings.rules.filter((_, current) => current !== index) })}><Trash2 /></Button></div></CardHeader><CardContent><Textarea className="min-h-40 font-mono" aria-label={`Rule ${index + 1} content`} placeholder="Always-on rule..." value={rule.content} onChange={(event) => updateRule(index, { content: event.target.value })} /></CardContent></Card>)}</TabsContent><TabsContent className="mt-6 space-y-4" value="sources">{providers.length === 0 ? <Card><CardHeader><CardTitle>No user-level sources detected</CardTitle><CardDescription>Providers and resource kinds only appear here when their context, rules, or skills exist on this machine.</CardDescription></CardHeader></Card> : providers.map(([provider, sources]) => { const policy = settings.source_policy[provider] ?? {}; const master = policy.enabled ?? true; return <Card key={provider}><CardHeader className="border-b"><div className="flex items-center justify-between gap-4"><div><CardTitle>{providerLabel(provider)}</CardTitle><CardDescription>Only detected resources are shown.</CardDescription></div><div className="flex items-center gap-2"><span className="text-xs text-muted-foreground">Enabled</span><Switch aria-label={`${providerLabel(provider)} enabled`} checked={master} onCheckedChange={(enabled) => updatePolicy(provider, { enabled })} /></div></div></CardHeader><CardContent className="divide-y">{sources.map((source) => { const key = source.kind as "context" | "rules" | "skills"; const checked = resourceEnabled(policy, key); return <div className="flex items-start gap-4 py-4 first:pt-0 last:pb-0" key={`${provider}-${source.kind}`}><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><div className="font-medium">{resourceLabels[source.kind] ?? source.kind}</div><Badge variant="outline">{source.count}</Badge></div><div className="mt-1 space-y-1">{source.paths.map((path) => <div className="break-all font-mono text-xs text-muted-foreground" key={path}>{path}</div>)}</div></div><Switch aria-label={`${providerLabel(provider)} ${resourceLabels[source.kind] ?? source.kind}`} checked={master && checked} disabled={!master} onCheckedChange={(enabled) => updatePolicy(provider, { [key]: enabled })} /></div> })}</CardContent></Card> })}</TabsContent></Tabs>{dirty ? <div className="sticky bottom-4 z-10 mx-auto flex max-w-2xl items-center justify-between gap-3 rounded-xl border bg-background/95 p-3 shadow-lg backdrop-blur"><div className="min-w-0"><div className="text-sm font-medium">Unsaved changes</div><div className="truncate text-xs text-muted-foreground">Save to affect future project_context calls.</div></div><div className="flex shrink-0 gap-2"><Button disabled={busy} variant="outline" onClick={() => { setSettings(saved); setMessage(""); setError("") }}><Undo2 />Reset</Button><Button disabled={busy} onClick={() => void save()}><Save />{busy ? "Saving..." : "Save"}</Button></div></div> : null}</div>
}

function groupSources(values: InstructionSource[]) { const groups = new Map<string, InstructionSource[]>(); for (const source of values) groups.set(source.provider, [...(groups.get(source.provider) ?? []), source]); return [...groups.entries()] }
function resourceEnabled(policy: InstructionSourcePolicy, kind: "context" | "rules" | "skills") { return policy[kind] ?? true }
function providerLabel(provider: string) { return provider === "agents" ? "Agents" : provider === "claude" ? "Claude" : provider === "claudes" ? "Claudes" : provider === "cursor" ? "Cursor" : provider === "codex" ? "Codex" : provider }
function editableJSON(value: GlobalInstructions) { return JSON.stringify({ context: value.context, rules: value.rules, source_policy: value.source_policy }) }
function normalizeGlobalInstructions(value: GlobalInstructions): GlobalInstructions {
  const sources = Array.isArray(value?.detected_sources) ? value.detected_sources.filter((source) => source && typeof source === "object").map((source) => ({ ...source, paths: Array.isArray(source.paths) ? source.paths : [] })) : []
  return { ...value, context: typeof value?.context === "string" ? value.context : "", rules: Array.isArray(value?.rules) ? value.rules : [], source_policy: value?.source_policy && typeof value.source_policy === "object" && !Array.isArray(value.source_policy) ? value.source_policy : {}, detected_sources: sources }
}
function newRuleID() { return `rule_${Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) => value.toString(16).padStart(2, "0")).join("")}` }
function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }
