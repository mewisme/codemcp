import { useEffect, useRef, useState, type ReactNode } from "react"
import { Activity, Cloud, KeyRound, Network, Power, RefreshCw, ShieldCheck } from "lucide-react"
import { CopyButton } from "@/components/copy-button"
import { DetailRow } from "@/components/detail-row"
import { PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ScrollableTabsList, Tabs, TabsContent, TabsTrigger } from "@/components/ui/tabs"
import { streamActivity } from "@/lib/activity-stream"
import { adminApi, type ManagedTunnelCreateRequest, type ManagedTunnelUpdateRequest, type ManagedTunnelUseRequest, type TunnelAdminAccess, type TunnelAdminKeyRequest, type TunnelAdminKeyStatus, type TunnelAdminScope, type TunnelConfig, type TunnelMetadata, type TunnelStatus } from "@/lib/api"

const emptyConfig: TunnelConfig = { enabled: false }
const reconnectDelay = 1000
type AdminScopeKind = "organization" | "workspace" | "tenant"

export function TunnelPage() {
  const [config, setConfig] = useState<TunnelConfig>(emptyConfig)
  const [mcpHTTPEnabled, setMCPHTTPEnabled] = useState(true)
  const [status, setStatus] = useState<TunnelStatus | null>(null)
  const [adminConfigured, setAdminConfigured] = useState(false)
  const [adminTunnels, setAdminTunnels] = useState<number | undefined>()
  const [adminAccess, setAdminAccess] = useState<TunnelAdminAccess>({ read: false, manage: false })
  const [adminCurrentScope, setAdminCurrentScope] = useState<TunnelAdminScope>({})
  const [adminKey, setAdminKey] = useState("")
  const [adminScope, setAdminScope] = useState<AdminScopeKind>("workspace")
  const [adminScopeID, setAdminScopeID] = useState("")
  const [managedTunnels, setManagedTunnels] = useState<TunnelMetadata[]>([])
  const [managedLoading, setManagedLoading] = useState(false)
  const [managedUseBusyID, setManagedUseBusyID] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [adminBusy, setAdminBusy] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [removeAdminOpen, setRemoveAdminOpen] = useState(false)
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")
  const retryTimer = useRef<number | null>(null)

  function syncAdmin(value: TunnelAdminKeyStatus) {
    setAdminConfigured(value.configured); setAdminTunnels(value.tunnels); setAdminAccess(value.access ?? { read: false, manage: false }); setAdminCurrentScope(value.scope)
    const [kind, id] = adminScopeValue(value.scope)
    if (kind) setAdminScope(kind)
    setAdminScopeID(id)
  }

  async function loadManagedTunnels(access = adminAccess, currentID = config.id) {
    if (!adminConfigured || (!access.read && !access.manage)) { setManagedTunnels([]); return }
    setManagedLoading(true)
    try {
      if (access.manage) setManagedTunnels(await adminApi.managedTunnels())
      else if (currentID) setManagedTunnels([await adminApi.managedTunnel(currentID)])
      else setManagedTunnels([])
      setError("")
    } catch (value) { setError(errorText(value)) } finally { setManagedLoading(false) }
  }

  async function lookupManagedTunnel(id: string) {
    setManagedLoading(true)
    try { const item = await adminApi.managedTunnel(id); setManagedTunnels([item]); setError("") } catch (value) { setError(errorText(value)) } finally { setManagedLoading(false) }
  }
  async function createManagedTunnel(request: ManagedTunnelCreateRequest) {
    setManagedLoading(true)
    try { await adminApi.createManagedTunnel(request); setManagedTunnels(await adminApi.managedTunnels()); setMessage("Managed tunnel created."); setError("") } catch (value) { setError(errorText(value)); throw value } finally { setManagedLoading(false) }
  }
  async function updateManagedTunnel(id: string, request: ManagedTunnelUpdateRequest) {
    setManagedLoading(true)
    try { await adminApi.updateManagedTunnel(id, request); setManagedTunnels(await adminApi.managedTunnels()); setMessage("Managed tunnel updated."); setError("") } catch (value) { setError(errorText(value)); throw value } finally { setManagedLoading(false) }
  }
  async function deleteManagedTunnel(id: string) {
    setManagedLoading(true)
    try { await adminApi.deleteManagedTunnel(id); setManagedTunnels(await adminApi.managedTunnels()); setMessage("Managed tunnel deleted."); setError("") } catch (value) { setError(errorText(value)); throw value } finally { setManagedLoading(false) }
  }

  async function switchManagedTunnel(request: ManagedTunnelUseRequest) {
    setManagedUseBusyID(request.id)
    try {
      const result = await adminApi.useManagedTunnel(request)
      setStatus(result.status); setConfig(await adminApi.tunnelConfig()); setMessage(`Using managed tunnel ${result.metadata.name || result.metadata.id}.`); setError("")
    } catch (value) { setError(errorText(value)); setMessage("") } finally { setManagedUseBusyID("") }
  }

  useEffect(() => {
    let active = true
    void Promise.all([adminApi.tunnelConfig(), adminApi.tunnel(), adminApi.tunnelAdminKey(), adminApi.config()]).then(([nextConfig, nextStatus, nextAdmin, runtimeConfig]) => {
      if (!active) return
      setConfig(nextConfig); setStatus(nextStatus); setMCPHTTPEnabled(runtimeConfig.http.mcp.enabled); syncAdmin(nextAdmin); setError(""); setLoading(false)
      if (nextAdmin.configured && (nextAdmin.access?.read || nextAdmin.access?.manage)) { setManagedLoading(true); const request = nextAdmin.access.manage ? adminApi.managedTunnels() : nextConfig.id ? adminApi.managedTunnel(nextConfig.id).then((item) => [item]) : Promise.resolve([]); void request.then((items) => { if (active) setManagedTunnels(items) }).catch((value) => { if (active) setError(errorText(value)) }).finally(() => { if (active) setManagedLoading(false) }) }
    }).catch((value) => { if (active) { setError(errorText(value)); setLoading(false) } })
    return () => { active = false }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    async function connect() {
      try {
        await streamActivity(controller.signal, { onEvent: (event) => { if (!event.kind.startsWith("tunnel.")) return; void Promise.all([adminApi.tunnelConfig(), adminApi.tunnel()]).then(([nextConfig, nextStatus]) => { if (!stopped) { setConfig(nextConfig); setStatus(nextStatus); setError("") } }).catch((value) => { if (!stopped) setError(errorText(value)) }) } }, 20)
      } catch {
        if (controller.signal.aborted || stopped) return
        retryTimer.current = window.setTimeout(() => void connect(), reconnectDelay)
      }
    }
    void connect()
    return () => { stopped = true; controller.abort(); if (retryTimer.current !== null) window.clearTimeout(retryTimer.current) }
  }, [])

  async function refresh() {
    setRefreshing(true)
    try {
      const [nextConfig, nextStatus, nextAdmin, runtimeConfig] = await Promise.all([adminApi.tunnelConfig(), adminApi.tunnel(), adminApi.tunnelAdminKey(), adminApi.config()])
      setConfig(nextConfig); setStatus(nextStatus); setMCPHTTPEnabled(runtimeConfig.http.mcp.enabled); syncAdmin(nextAdmin); setError("")
    } catch (value) { setError(errorText(value)) } finally { setRefreshing(false) }
  }
  async function syncTunnel() {
    setRefreshing(true)
    try {
      setStatus(await adminApi.syncTunnel())
      setConfig(await adminApi.tunnelConfig())
      setMessage("Tunnel metadata synchronized.")
      setError("")
    } catch (value) {
      setError(errorText(value))
      setMessage("")
    } finally {
      setRefreshing(false)
    }
  }
  async function saveRuntime() {
    setBusy(true)
    try {
      setStatus(await adminApi.configureTunnel(runtimeConfig(config)))
      setConfig(await adminApi.tunnelConfig())
      setMessage("Runtime tunnel configuration saved."); setError("")
    } catch (value) { setError(errorText(value)); setMessage("") } finally { setBusy(false) }
  }
  async function clearRuntimeKey() {
    setBusy(true)
    try {
      await adminApi.clearTunnelRuntimeKey()
      setConfig(await adminApi.tunnelConfig())
      setStatus(await adminApi.tunnel())
      setMessage("Runtime key cleared."); setError("")
    } catch (value) { setError(errorText(value)); setMessage("") } finally { setBusy(false) }
  }
  async function toggle() {
    const active = status?.running || status?.restarting
    setBusy(true)
    try { setStatus(active ? await adminApi.stopTunnel() : await adminApi.startTunnel()); setMessage(active ? "Tunnel stopped." : "Tunnel start requested."); setError("") } catch (value) { setError(errorText(value)); setMessage("") } finally { setBusy(false) }
  }
  async function saveAdmin() {
    if (!adminScopeID.trim()) { setError("Admin scope ID is required."); return }
    setAdminBusy(true)
    try {
      const next = await adminApi.configureTunnelAdminKey(adminRequest(adminKey, adminScope, adminScopeID))
      setAdminKey(""); syncAdmin(next); setStatus(await adminApi.tunnel()); setConfig(await adminApi.tunnelConfig()); setMessage(adminResultMessage("Admin key verified and saved", next)); setError("")
      if (next.configured && (next.access?.read || next.access?.manage)) { setManagedLoading(true); try { setManagedTunnels(next.access.manage ? await adminApi.managedTunnels() : config.id ? [await adminApi.managedTunnel(config.id)] : []) } finally { setManagedLoading(false) } }
    } catch (value) { setError(errorText(value)); setMessage("") } finally { setAdminBusy(false) }
  }
  async function verifyAdmin() {
    setAdminBusy(true)
    try { const next = await adminApi.verifyTunnelAdminKey(); syncAdmin(next); if (next.access?.read || next.access?.manage) await loadManagedTunnels(next.access, config.id); setMessage(adminResultMessage("Admin key verified", next)); setError("") } catch (value) { setError(errorText(value)); setMessage("") } finally { setAdminBusy(false) }
  }
  async function removeAdmin() {
    setAdminBusy(true)
    try {
      const next = await adminApi.removeTunnelAdminKey()
      setAdminKey(""); syncAdmin(next); setManagedTunnels([]); setStatus(await adminApi.tunnel()); setConfig(await adminApi.tunnelConfig()); setRemoveAdminOpen(false); setMessage("Admin key removed."); setError("")
    } catch (value) { setError(errorText(value)); setMessage("") } finally { setAdminBusy(false) }
  }

  const active = status?.running || status?.restarting
  const state = status?.restarting ? "Reconnecting" : status?.running ? status.ready ? "Ready" : "Connecting" : "Stopped"
  const variant = status?.ready ? "default" : active ? "secondary" : "outline"
  return <div className="space-y-6">
    <PageHeader title="Tunnel" description="Operate the OpenAI Secure MCP Tunnel, runtime credential, and management access from one control surface." actions={<><Button disabled={refreshing} size="sm" variant="outline" onClick={() => void syncTunnel()}><Cloud className={refreshing ? "animate-pulse" : ""} />Sync</Button><Button aria-label="Refresh tunnel status" disabled={refreshing} size="sm" variant="outline" onClick={() => void refresh()}><RefreshCw className={refreshing ? "animate-spin" : ""} />Refresh</Button></>} />
    <PageError message={error} />
    {message ? <Alert><Network /><AlertDescription>{message}</AlertDescription></Alert> : null}
    {!mcpHTTPEnabled ? <Alert><Network /><AlertDescription>MCP HTTP is disabled. Secure MCP Tunnel is the required MCP transport and cannot be disabled, stopped, or deleted.</AlertDescription></Alert> : null}
    {loading ? <PageLoading rows={5} /> : <>
      <TunnelHero active={active} busy={busy} config={config} state={state} status={status} variant={variant} adminConfigured={adminConfigured} mcpHTTPEnabled={mcpHTTPEnabled} onToggle={() => void toggle()} />
      <Tabs defaultValue="runtime" className="gap-4">
        <ScrollableTabsList variant="line" className="justify-start border-b"><TabsTrigger value="runtime"><Power />Runtime</TabsTrigger><TabsTrigger value="admin"><ShieldCheck />Administration</TabsTrigger><TabsTrigger value="metadata"><Activity />Metadata</TabsTrigger></ScrollableTabsList>
        <TabsContent value="runtime"><RuntimePanel busy={busy} config={config} mcpHTTPEnabled={mcpHTTPEnabled} setConfig={setConfig} onSave={() => void saveRuntime()} onClearKey={() => void clearRuntimeKey()} /></TabsContent>
        <TabsContent value="admin"><div className="space-y-4"><AdminPanel busy={adminBusy} configured={adminConfigured} currentScope={adminCurrentScope} tunnels={adminTunnels} keyValue={adminKey} scope={adminScope} scopeID={adminScopeID} setKey={setAdminKey} setScope={(value) => { setAdminScope(value); setAdminScopeID("") }} setScopeID={setAdminScopeID} onSave={() => void saveAdmin()} onVerify={() => void verifyAdmin()} onRemove={() => setRemoveAdminOpen(true)} /><ManagedTunnelsPanel configured={adminConfigured} access={adminAccess} currentID={config.id} runtimeKeyConfigured={Boolean(config.runtime_key_configured)} items={managedTunnels} loading={managedLoading} busyID={managedUseBusyID} onRefresh={() => void loadManagedTunnels()} onLookup={lookupManagedTunnel} onCreate={createManagedTunnel} onUpdate={updateManagedTunnel} onDelete={deleteManagedTunnel} onUse={(request) => void switchManagedTunnel(request)} /></div></TabsContent>
        <TabsContent value="metadata"><MetadataPanel config={config} status={status} /></TabsContent>
      </Tabs>
    </>}
    <AlertDialog open={removeAdminOpen} onOpenChange={(open) => { if (!adminBusy) setRemoveAdminOpen(open) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Remove tunnel admin key?</AlertDialogTitle><AlertDialogDescription>The stored Tunnels Manage credential and its verification scope will be removed. Runtime tunnel credentials are not changed.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel disabled={adminBusy}>Cancel</AlertDialogCancel><AlertDialogAction disabled={adminBusy} variant="destructive" onClick={() => void removeAdmin()}>{adminBusy ? "Removing..." : "Remove admin key"}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
  </div>
}

function TunnelHero({ active, busy, config, state, status, variant, adminConfigured, mcpHTTPEnabled, onToggle }: { active: boolean | undefined; busy: boolean; config: TunnelConfig; state: string; status: TunnelStatus | null; variant: "default" | "secondary" | "outline"; adminConfigured: boolean; mcpHTTPEnabled: boolean; onToggle: () => void }) {
  const title = status?.metadata?.name || "OpenAI Secure MCP Tunnel"
  const description = status?.metadata?.description || "Secure connection between this runtime and ChatGPT through the OpenAI control plane."
  return <Card className="overflow-hidden"><CardHeader className="border-b"><div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between"><div className="flex min-w-0 gap-3"><div className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-muted/40"><Cloud className="size-5 text-muted-foreground" /></div><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><CardTitle>{title}</CardTitle><Badge variant={variant}>{status?.restarting || (status?.running && !status.ready) ? <Spinner className="size-3" /> : null}{state}</Badge></div><CardDescription className="mt-1 max-w-3xl">{description}</CardDescription></div></div><Button disabled={busy || !config.enabled || Boolean(active && !mcpHTTPEnabled)} variant={active ? "outline" : "default"} onClick={onToggle}><Power />{busy ? "Working..." : active ? "Stop tunnel" : "Start tunnel"}</Button></div></CardHeader><CardContent className="p-0"><div className="grid sm:grid-cols-2 xl:grid-cols-4"><HeroMetric label="Tunnel ID" value={<CopyValue value={status?.metadata?.id ?? status?.id ?? config.id ?? "-"} />} description={status?.provider || "openai"} /><HeroMetric label="Runtime key" value={config.runtime_key_configured ? config.runtime_key_preview || "runtime_********legacy" : "Not configured"} description={config.enabled ? "Tunnel enabled" : "Tunnel disabled"} /><HeroMetric label="MCP HTTP" value={mcpHTTPEnabled ? "Enabled" : "Disabled"} description="Alternate MCP transport" /><HeroMetric label="Admin access" value={adminConfigured ? config.admin_key_preview || "admin_********legacy" : "Not configured"} description={status?.admin_key_configured ? formatAdminScope(status.admin_scope) : "Tunnels Manage"} /></div>{status?.last_error || status?.metadata_error ? <div className="border-t p-4"><div className="break-words rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{status.last_error || `Tunnel metadata unavailable: ${status.metadata_error}`}</div></div> : null}</CardContent></Card>
}

function HeroMetric({ label, value, description }: { label: string; value: ReactNode; description: string }) { return <div className="min-w-0 border-b p-4 last:border-b-0 sm:[&:nth-child(odd)]:border-r xl:border-b-0 xl:border-r xl:last:border-r-0"><div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</div><div className="mt-2 min-w-0 text-sm font-medium">{value}</div><div className="mt-1 truncate text-xs text-muted-foreground">{description}</div></div> }

function RuntimePanel({ busy, config, mcpHTTPEnabled, setConfig, onSave, onClearKey }: { busy: boolean; config: TunnelConfig; mcpHTTPEnabled: boolean; setConfig: (value: TunnelConfig) => void; onSave: () => void; onClearKey: () => void }) {
  return <Card><CardHeader><div className="flex flex-wrap items-start justify-between gap-3"><div><CardTitle>Runtime connectivity</CardTitle><CardDescription className="mt-1">Read + Use credential and connection settings used by the running cm tunnel client.</CardDescription></div><Badge variant={config.runtime_key_configured ? "secondary" : "outline"}><KeyRound />{config.runtime_key_configured ? "Runtime key configured" : "Runtime key missing"}</Badge></div></CardHeader><CardContent className="space-y-6"><Field orientation="horizontal" className="rounded-lg border p-4"><div className="min-w-0 flex-1"><FieldLabel htmlFor="tunnel-enabled">Enable tunnel</FieldLabel><FieldDescription>{mcpHTTPEnabled ? "Allow the managed runtime to connect to this OpenAI tunnel. At least one MCP transport must remain enabled." : "Required while MCP HTTP is disabled."}</FieldDescription></div><Switch id="tunnel-enabled" checked={config.enabled} disabled={!mcpHTTPEnabled} onCheckedChange={(enabled) => setConfig({ ...config, enabled })} /></Field><FieldGroup><div className="grid gap-5 md:grid-cols-2"><ConfigField label="Tunnel ID" description="Assigned OpenAI tunnel identifier."><Input placeholder="tunnel_..." value={config.id ?? ""} onChange={(event) => setConfig({ ...config, id: event.target.value })} /></ConfigField><ConfigField label="Runtime key" description={config.runtime_key_configured ? "Leave blank to keep the stored Read + Use key." : "OpenAI runtime key with Read + Use permissions."}><Input autoComplete="off" placeholder={config.runtime_key_configured ? "Leave blank to keep current key" : "Runtime API key"} type="password" value={config.api_key ?? ""} onChange={(event) => setConfig({ ...config, api_key: event.target.value })} /></ConfigField><ConfigField label="Control plane base URL" description="Leave empty to use the OpenAI default endpoint."><Input placeholder="https://api.openai.com" value={config.control_plane_base_url ?? ""} onChange={(event) => setConfig({ ...config, control_plane_base_url: event.target.value })} /></ConfigField><ConfigField label="Organization ID" description="Optional runtime organization scope."><Input placeholder="org_..." value={config.organization_id ?? ""} onChange={(event) => setConfig({ ...config, organization_id: event.target.value })} /></ConfigField></div></FieldGroup></CardContent><CardFooter className="justify-end gap-2 border-t">{config.runtime_key_configured ? <Button disabled={busy} variant="outline" onClick={onClearKey}>Clear runtime key</Button> : null}<Button disabled={busy || (!mcpHTTPEnabled && !config.enabled)} onClick={onSave}>{busy ? "Saving..." : "Save runtime configuration"}</Button></CardFooter></Card>
}

function AdminPanel({ busy, configured, currentScope, tunnels, keyValue, scope, scopeID, setKey, setScope, setScopeID, onSave, onVerify, onRemove }: { busy: boolean; configured: boolean; currentScope: TunnelAdminScope; tunnels?: number; keyValue: string; scope: AdminScopeKind; scopeID: string; setKey: (value: string) => void; setScope: (value: AdminScopeKind) => void; setScopeID: (value: string) => void; onSave: () => void; onVerify: () => void; onRemove: () => void }) {
  return <Card><CardHeader><div className="flex flex-wrap items-start justify-between gap-3"><div><CardTitle>Tunnel administration</CardTitle><CardDescription className="mt-1">Tunnels Manage credential for listing and managing tunnel metadata. This key is never reused by the runtime connection.</CardDescription></div><div className="flex flex-wrap gap-2"><Badge variant={configured ? "secondary" : "outline"}>{configured ? "Management configured" : "Management not configured"}</Badge>{tunnels !== undefined ? <Badge variant="outline">{tunnels} accessible tunnel{tunnels === 1 ? "" : "s"}</Badge> : null}</div></div></CardHeader><CardContent className="space-y-6">{configured ? <div className="grid gap-3 rounded-lg border bg-muted/20 p-4 md:grid-cols-2"><SummaryLine label="Current scope" value={formatAdminScope(currentScope)} /><SummaryLine label="Credential storage" value="Secret file store" /></div> : <Alert><ShieldCheck /><AlertDescription>Add an admin API key with Tunnels Manage permission and verify it against exactly one organization, workspace, or tenant scope.</AlertDescription></Alert>}<FieldGroup><ConfigField label="Admin key" description={configured ? "Leave blank to keep the stored admin key while changing or re-verifying scope." : "The key is stored only after verification succeeds."}><Input autoComplete="off" placeholder={configured ? "Leave blank to keep current key" : "Admin API key"} type="password" value={keyValue} onChange={(event) => setKey(event.target.value)} /></ConfigField><div className="grid gap-5 md:grid-cols-2"><ConfigField label="Scope type" description="Management verification uses exactly one scope."><Select value={scope} onValueChange={(value) => setScope(value as AdminScopeKind)}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="organization">Organization</SelectItem><SelectItem value="workspace">Workspace</SelectItem><SelectItem value="tenant">Tenant</SelectItem></SelectContent></Select></ConfigField><ConfigField label={`${scopeLabel(scope)} ID`} description={`OpenAI ${scope} used to verify Tunnels Manage access.`}><Input placeholder={scopePlaceholder(scope)} value={scopeID} onChange={(event) => setScopeID(event.target.value)} /></ConfigField></div></FieldGroup></CardContent><CardFooter className="flex-col items-stretch gap-3 border-t sm:flex-row sm:items-center sm:justify-between"><div className="text-xs text-muted-foreground">The secret file store keeps credentials outside tunnel.&lt;ext&gt;; configuration stores only scope and configured-state metadata.</div><ButtonGroup className="self-end sm:self-auto">{configured ? <><Button disabled={busy} variant="outline" onClick={onVerify}>Verify</Button><Button disabled={busy} variant="outline" onClick={onRemove}>Remove</Button></> : null}<Button disabled={busy || !scopeID.trim()} onClick={onSave}>{busy ? "Verifying..." : "Save & verify"}</Button></ButtonGroup></CardFooter></Card>
}

function ManagedTunnelsPanel({ configured, access, currentID, runtimeKeyConfigured, items, loading, busyID, onRefresh, onLookup, onCreate, onUpdate, onDelete, onUse }: { configured: boolean; access: TunnelAdminAccess; currentID?: string; runtimeKeyConfigured: boolean; items: TunnelMetadata[]; loading: boolean; busyID: string; onRefresh: () => void; onLookup: (id: string) => Promise<void>; onCreate: (request: ManagedTunnelCreateRequest) => Promise<void>; onUpdate: (id: string, request: ManagedTunnelUpdateRequest) => Promise<void>; onDelete: (id: string) => Promise<void>; onUse: (request: ManagedTunnelUseRequest) => void }) {
  const [lookupID, setLookupID] = useState("")
  const [creating, setCreating] = useState(false)
  const [createName, setCreateName] = useState(""); const [createDescription, setCreateDescription] = useState(""); const [createOrganizations, setCreateOrganizations] = useState(""); const [createWorkspaces, setCreateWorkspaces] = useState(""); const [createTenants, setCreateTenants] = useState("")
  const [editID, setEditID] = useState(""); const [editName, setEditName] = useState(""); const [editDescription, setEditDescription] = useState(""); const [editOrganizations, setEditOrganizations] = useState(""); const [editWorkspaces, setEditWorkspaces] = useState(""); const [editTenants, setEditTenants] = useState("")
  const [deleteID, setDeleteID] = useState(""); const [mutationBusy, setMutationBusy] = useState(false)
  const [useID, setUseID] = useState(""); const [runtimeMode, setRuntimeMode] = useState<"auto" | "manual">("auto"); const [runtimeAPIKey, setRuntimeAPIKey] = useState(""); const [projectID, setProjectID] = useState("")
  const accessLabel = access.manage ? "Full management" : access.read ? "Read only" : "No verified access"
  async function createTunnel() { setMutationBusy(true); try { await onCreate({ name: createName.trim(), description: createDescription.trim(), organization_ids: splitIDs(createOrganizations), workspace_ids: splitIDs(createWorkspaces), tenant_ids: splitIDs(createTenants) }); setCreating(false); setCreateName(""); setCreateDescription(""); setCreateOrganizations(""); setCreateWorkspaces(""); setCreateTenants("") } finally { setMutationBusy(false) } }
  function beginEdit(item: TunnelMetadata) { setEditID(item.id); setEditName(item.name); setEditDescription(item.description); setEditOrganizations((item.organization_ids ?? []).join(", ")); setEditWorkspaces((item.workspace_ids ?? []).join(", ")); setEditTenants((item.tenant_ids ?? []).join(", ")) }
  async function updateTunnel() { setMutationBusy(true); try { await onUpdate(editID, { name: editName.trim(), description: editDescription.trim(), organization_ids: splitIDs(editOrganizations), workspace_ids: splitIDs(editWorkspaces), tenant_ids: splitIDs(editTenants) }); setEditID("") } finally { setMutationBusy(false) } }
  async function deleteTunnel() { setMutationBusy(true); try { await onDelete(deleteID); setDeleteID("") } finally { setMutationBusy(false) } }
  function selectTunnel(id: string) {
    if (runtimeKeyConfigured) { onUse({ id }); return }
    setUseID(id); setRuntimeMode("auto"); setRuntimeAPIKey(""); setProjectID("")
  }
  function confirmUse() {
    if (!useID) return
    if (runtimeMode === "manual" && !runtimeAPIKey.trim()) return
    onUse(runtimeMode === "auto" ? { id: useID, auto_generate_runtime_key: true, project_id: projectID.trim() || undefined } : { id: useID, runtime_api_key: runtimeAPIKey.trim() })
    setUseID("")
  }
  return <Card><CardHeader><div className="flex flex-wrap items-start justify-between gap-3"><div><div className="flex flex-wrap items-center gap-2"><CardTitle>Managed tunnels</CardTitle><Badge variant={access.manage ? "secondary" : "outline"}>{accessLabel}</Badge></div><CardDescription className="mt-1">Capabilities are verified from the stored admin key; unavailable management actions are removed automatically.</CardDescription></div><div className="flex gap-2">{access.manage ? <Button disabled={loading || mutationBusy} size="sm" onClick={() => setCreating((value) => !value)}>{creating ? "Cancel create" : "Create tunnel"}</Button> : null}<Button disabled={!configured || loading || (!access.read && !access.manage)} size="sm" variant="outline" onClick={onRefresh}><RefreshCw className={loading ? "animate-spin" : ""} />Refresh</Button></div></div></CardHeader><CardContent className="space-y-4 p-4 pt-0">{!configured ? <div className="text-sm text-muted-foreground">Configure and verify an admin key to access managed tunnels.</div> : !access.read && !access.manage ? <Alert><ShieldCheck /><AlertDescription>The key is configured but no tunnel capability has been verified. Re-verify it after changing OpenAI permissions.</AlertDescription></Alert> : <>{access.read && !access.manage ? <div className="flex gap-2"><Input placeholder="tunnel_..." value={lookupID} onChange={(event) => setLookupID(event.target.value)} /><Button disabled={loading || !lookupID.trim()} variant="outline" onClick={() => void onLookup(lookupID.trim())}>Lookup</Button></div> : null}{creating && access.manage ? <div className="space-y-3 rounded-lg border p-4"><div className="font-medium">Create managed tunnel</div><div className="grid gap-3 md:grid-cols-2"><Input placeholder="Name" value={createName} onChange={(event) => setCreateName(event.target.value)} /><Input placeholder="Description" value={createDescription} onChange={(event) => setCreateDescription(event.target.value)} /><Input placeholder="Organization IDs, comma separated" value={createOrganizations} onChange={(event) => setCreateOrganizations(event.target.value)} /><Input placeholder="Workspace IDs, comma separated" value={createWorkspaces} onChange={(event) => setCreateWorkspaces(event.target.value)} /><Input placeholder="Tenant IDs, comma separated" value={createTenants} onChange={(event) => setCreateTenants(event.target.value)} /></div><div className="flex justify-end"><Button disabled={mutationBusy || !createName.trim() || !createDescription.trim() || (!createOrganizations.trim() && !createWorkspaces.trim())} onClick={() => void createTunnel()}>{mutationBusy ? "Creating..." : "Create"}</Button></div></div> : null}{!runtimeKeyConfigured && access.read ? <Alert><KeyRound /><AlertDescription>No runtime key is configured. Selecting a tunnel can generate a dedicated Read + Use service-account key with a sufficiently privileged admin key, or you can enter one manually.</AlertDescription></Alert> : null}{loading && items.length === 0 ? <PageLoading rows={3} /> : items.length === 0 ? <div className="text-sm text-muted-foreground">{access.manage ? "No managed tunnels are available in this scope." : "Enter a tunnel ID to read its metadata."}</div> : <div className="divide-y rounded-lg border">{items.map((item) => { const selected = currentID === item.id; const editing = editID === item.id; return <div key={item.id} className="space-y-3 p-4">{editing ? <><div className="grid gap-3 md:grid-cols-2"><Input value={editName} onChange={(event) => setEditName(event.target.value)} /><Input value={editDescription} onChange={(event) => setEditDescription(event.target.value)} /><Input placeholder="Organization IDs" value={editOrganizations} onChange={(event) => setEditOrganizations(event.target.value)} /><Input placeholder="Workspace IDs" value={editWorkspaces} onChange={(event) => setEditWorkspaces(event.target.value)} /><Input placeholder="Tenant IDs" value={editTenants} onChange={(event) => setEditTenants(event.target.value)} /></div><div className="flex justify-end gap-2"><Button variant="outline" onClick={() => setEditID("")}>Cancel</Button><Button disabled={mutationBusy || !editName.trim() || !editDescription.trim()} onClick={() => void updateTunnel()}>{mutationBusy ? "Saving..." : "Save changes"}</Button></div></> : <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><div className="font-medium">{item.name || "Unnamed tunnel"}</div>{selected ? <Badge variant="secondary">Current</Badge> : null}</div><div className="mt-1 break-all font-mono text-xs text-muted-foreground">{item.id}</div>{item.description ? <div className="mt-1 text-sm text-muted-foreground">{item.description}</div> : null}</div><div className="flex flex-wrap gap-2">{access.manage ? <><Button disabled={mutationBusy} size="sm" variant="outline" onClick={() => beginEdit(item)}>Edit</Button><Button disabled={mutationBusy || selected} size="sm" variant="outline" onClick={() => setDeleteID(item.id)}>Delete</Button></> : null}{access.read || access.manage ? <Button disabled={selected || Boolean(busyID)} size="sm" variant={selected ? "outline" : "default"} onClick={() => selectTunnel(item.id)}>{busyID === item.id ? <><Spinner className="size-3" />Switching...</> : selected ? "In use" : "Use tunnel"}</Button> : null}</div></div>}</div> })}</div>}</>}</CardContent><AlertDialog open={Boolean(useID)} onOpenChange={(open) => { if (!open && !busyID) setUseID("") }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Use managed tunnel?</AlertDialogTitle><AlertDialogDescription>No runtime key is configured. Choose how cm should obtain the Read + Use credential. The tunnel is enabled automatically when selected.</AlertDialogDescription></AlertDialogHeader><div className="space-y-4"><ConfigField label="Runtime credential" description="Automatic generation uses the stored admin key to create a dedicated project service-account API key."><Select value={runtimeMode} onValueChange={(value) => setRuntimeMode(value as "auto" | "manual")}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="auto">Auto generate runtime key</SelectItem><SelectItem value="manual">Enter runtime key manually</SelectItem></SelectContent></Select></ConfigField>{runtimeMode === "auto" ? <ConfigField label="OpenAI project ID" description="Optional. Blank uses the only active project or the Default project."><Input placeholder="proj_..." value={projectID} onChange={(event) => setProjectID(event.target.value)} /></ConfigField> : <ConfigField label="Runtime API key" description="OpenAI key with Tunnels Read + Use permissions."><Input autoComplete="off" type="password" value={runtimeAPIKey} onChange={(event) => setRuntimeAPIKey(event.target.value)} /></ConfigField>}</div><AlertDialogFooter><AlertDialogCancel disabled={Boolean(busyID)}>Cancel</AlertDialogCancel><AlertDialogAction disabled={Boolean(busyID) || (runtimeMode === "manual" && !runtimeAPIKey.trim())} onClick={confirmUse}>Use tunnel</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog><AlertDialog open={Boolean(deleteID)} onOpenChange={(open) => { if (!open && !mutationBusy) setDeleteID("") }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Delete managed tunnel?</AlertDialogTitle><AlertDialogDescription>This permanently deletes {deleteID}. The currently selected runtime tunnel cannot be deleted from this screen.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel disabled={mutationBusy}>Cancel</AlertDialogCancel><AlertDialogAction disabled={mutationBusy} variant="destructive" onClick={() => void deleteTunnel()}>{mutationBusy ? "Deleting..." : "Delete tunnel"}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog></Card>
}

function MetadataPanel({ config, status }: { config: TunnelConfig; status: TunnelStatus | null }) {
  const metadata = status?.metadata
  return <div className="grid gap-4 xl:grid-cols-2"><Card><CardHeader><CardTitle>Connection identity</CardTitle><CardDescription>Current runtime and control-plane identity.</CardDescription></CardHeader><CardContent className="divide-y"><DetailRow label="Provider" value={status?.provider ?? "-"} mono /><DetailRow label="Tunnel ID" value={<CopyValue value={metadata?.id ?? status?.id ?? config.id ?? "-"} />} /><DetailRow label="Name" value={metadata?.name || "-"} /><DetailRow label="Description" value={metadata?.description || "-"} /><DetailRow label="Creator" value={metadata?.creator || "-"} mono /><DetailRow label="Request ID" value={<CopyValue value={metadata?.request_id || "-"} />} /><DetailRow label="Fetched" value={metadata?.fetched_at ? formatDate(metadata.fetched_at) : "-"} /><DetailRow label="Control plane" value={<CopyValue value={status?.control_plane_base_url ?? config.control_plane_base_url ?? "Default"} />} /><DetailRow label="Runtime organization" value={<CopyValue value={status?.organization_id ?? config.organization_id ?? "-"} />} /></CardContent></Card><Card><CardHeader><CardTitle>Tunnel scope</CardTitle><CardDescription>Organizations, workspaces, and tenants attached to the current tunnel metadata.</CardDescription></CardHeader><CardContent className="divide-y"><DetailRow label="Organizations" value={<CopyList values={metadata?.organization_ids} />} /><DetailRow label="Workspaces" value={<CopyList values={metadata?.workspace_ids} />} /><DetailRow label="Tenants" value={<CopyList values={metadata?.tenant_ids} />} /><DetailRow label="Admin scope" value={status?.admin_key_configured ? formatAdminScope(status.admin_scope) : "Not configured"} /></CardContent></Card></div>
}

function SummaryLine({ label, value }: { label: string; value: string }) { return <div><div className="text-xs text-muted-foreground">{label}</div><div className="mt-1 break-all text-sm font-medium">{value}</div></div> }
function ConfigField({ label, description, children }: { label: string; description: string; children: ReactNode }) { return <Field><FieldLabel>{label}</FieldLabel>{children}<FieldDescription>{description}</FieldDescription></Field> }
function CopyValue({ value }: { value: string }) { return <div className="flex min-w-0 items-start gap-1"><span className="min-w-0 flex-1 break-all font-mono text-sm font-normal">{value}</span>{value !== "-" && value !== "Default" ? <CopyButton value={value} /> : null}</div> }
function CopyList({ values }: { values?: string[] }) { return values?.length ? <div className="space-y-1">{values.map((value) => <CopyValue key={value} value={value} />)}</div> : "-" }
function runtimeConfig(config: TunnelConfig): TunnelConfig { return { enabled: config.enabled, id: config.id, api_key: config.api_key, control_plane_base_url: config.control_plane_base_url, organization_id: config.organization_id } }
function adminRequest(key: string, scope: AdminScopeKind, id: string): TunnelAdminKeyRequest { const request: TunnelAdminKeyRequest = { admin_key: key.trim() || undefined }; if (scope === "organization") request.organization_id = id.trim(); else if (scope === "workspace") request.workspace_id = id.trim(); else request.tenant_id = id.trim(); return request }
function adminScopeValue(scope?: TunnelAdminScope): [AdminScopeKind | undefined, string] { if (scope?.organization_id) return ["organization", scope.organization_id]; if (scope?.workspace_id) return ["workspace", scope.workspace_id]; if (scope?.tenant_id) return ["tenant", scope.tenant_id]; return [undefined, ""] }
function formatAdminScope(scope?: TunnelAdminScope) { const [kind, id] = adminScopeValue(scope); return kind && id ? `${kind}:${id}` : "scope unavailable" }
function adminResultMessage(message: string, result: TunnelAdminKeyStatus) { return result.tunnels === undefined ? `${message}.` : `${message} · ${result.tunnels} tunnel${result.tunnels === 1 ? "" : "s"} accessible.` }
function scopeLabel(scope: AdminScopeKind) { return scope[0].toUpperCase() + scope.slice(1) }
function scopePlaceholder(scope: AdminScopeKind) { return scope === "organization" ? "org_..." : scope === "workspace" ? "Workspace ID" : "Tenant ID" }
function formatDate(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function splitIDs(value: string) { return [...new Set(value.split(",").map((item) => item.trim()).filter(Boolean))] }
function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }
