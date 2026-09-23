import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { CircleDot, RefreshCw, Search, ShieldCheck } from "lucide-react"
import { DetailRow } from "@/components/detail-row"
import { JsonViewer } from "@/components/json-viewer"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { TruncatedText } from "@/components/truncated-text"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemHeader, ItemTitle } from "@/components/ui/item"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { streamApprovals } from "@/lib/approval-stream"
import { adminApi, type ApprovalRequest } from "@/lib/api"

const statuses = ["pending", "approved", "denied", "expired", "cancelled", "consumed"]
const reconnectDelay = 1000

export function RequestsPage({ workspaceID = "" }: { workspaceID?: string }) {
  const [items, setItems] = useState<ApprovalRequest[]>([])
  const [selected, setSelected] = useState<ApprovalRequest | null>(null)
  const [status, setStatus] = useState("all")
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [busy, setBusy] = useState<"approve" | "deny" | "">("")
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState("")
  const retryTimer = useRef<number | null>(null)

  const load = useCallback(async (refresh = false) => {
    if (refresh) setRefreshing(true)
    try { setItems(await adminApi.approvalRequests("", workspaceID)); setError("") } catch (value) { setError(errorText(value)) } finally { setLoading(false); setRefreshing(false) }
  }, [workspaceID])

  useEffect(() => { let active = true; void adminApi.approvalRequests("", workspaceID).then((next) => { if (active) { setItems(next); setError("") } }).catch((value) => { if (active) setError(errorText(value)) }).finally(() => { if (active) setLoading(false) }); return () => { active = false } }, [workspaceID])
  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    async function connect() {
      try {
        await streamApprovals(controller.signal, { onReady: () => setConnected(true), onEvent: () => { setConnected(true); void load() } }, workspaceID)
      } catch (value) {
        if (controller.signal.aborted || stopped) return
        setConnected(false)
        setError(errorText(value))
        retryTimer.current = window.setTimeout(() => void connect(), reconnectDelay)
      }
    }
    void connect()
    return () => { stopped = true; controller.abort(); if (retryTimer.current !== null) window.clearTimeout(retryTimer.current) }
  }, [load, workspaceID])

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return items.filter((item) => {
      if (status !== "all" && item.status !== status) return false
      if (!needle) return true
      return [item.id, item.title, item.workspace_id, item.target_tool, item.source, item.status, item.session_hash].some((value) => value?.toLowerCase().includes(needle))
    })
  }, [items, query, status])
  const pendingCount = items.filter((item) => item.status === "pending").length

  async function openRequest(item: ApprovalRequest) {
    try { setSelected(await adminApi.approvalRequest(item.id)); setError("") } catch (value) { setError(errorText(value)) }
  }
  async function resolve(action: "approve" | "deny") {
    if (!selected || busy || selected.status !== "pending") return
    setBusy(action)
    try {
      const next = action === "approve" ? await adminApi.approveRequest(selected.id) : await adminApi.denyRequest(selected.id)
      setSelected(next)
      await load()
    } catch (value) { setError(errorText(value)); await load() } finally { setBusy("") }
  }

  return <div className="space-y-6"><PageHeader title="Approval requests" description={workspaceID ? `Review control approvals and resolved request history for ${workspaceID}.` : "Review control approvals and resolved request history."} actions={<><Badge variant={pendingCount ? "secondary" : "outline"}>{pendingCount} pending</Badge><Badge variant={connected ? "secondary" : "outline"}><CircleDot className="size-3" />{connected ? "Live" : "Reconnecting"}</Badge><Button disabled={refreshing} size="sm" variant="outline" onClick={() => void load(true)}><RefreshCw className={refreshing ? "animate-spin" : ""} />Refresh</Button></>} /><PageError message={error} /><div className="flex flex-col gap-2 lg:flex-row"><div className="relative min-w-0 flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input className="pl-9" placeholder="Search request, tool, source..." value={query} onChange={(event) => setQuery(event.target.value)} /></div><Select value={status} onValueChange={setStatus}><SelectTrigger className="w-full lg:w-44"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">All statuses</SelectItem>{statuses.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select></div>{loading ? <PageLoading rows={6} /> : filtered.length === 0 ? <PageEmpty icon={ShieldCheck} title="No matching approval requests" description={items.length ? "Adjust the search or filters." : "Control approval requests will appear here when guarded actions require a human grant."} /> : <ItemGroup>{filtered.map((item) => <Item className="cursor-pointer" key={item.id} role="button" tabIndex={0} variant="outline" onClick={() => void openRequest(item)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") void openRequest(item) }}><ItemContent className="min-w-0"><ItemHeader><ItemTitle className="min-w-0"><TruncatedText lines={1}>{item.title || item.target_tool}</TruncatedText></ItemTitle><ApprovalStatusBadge status={item.status} /></ItemHeader><ItemDescription>{item.workspace_id} · {item.target_tool}{item.source ? ` · ${item.source}` : ""}</ItemDescription><div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground"><span className="font-mono">{item.id}</span><span>{formatDateTime(item.created_at)}</span></div></ItemContent></Item>)}</ItemGroup>}{selected ? <RequestDetail request={selected} busy={busy} onOpenChange={(open) => { if (!open && !busy) setSelected(null) }} onResolve={resolve} /> : null}</div>
}

function RequestDetail({ request, busy, onOpenChange, onResolve }: { request: ApprovalRequest; busy: "approve" | "deny" | ""; onOpenChange: (open: boolean) => void; onResolve: (action: "approve" | "deny") => void }) {
  const pending = request.status === "pending"
  return <ResponsiveDialog open onOpenChange={onOpenChange} title={request.title || request.target_tool} description={`Control approval request · ${request.id}`} footer={pending ? <><Button disabled={Boolean(busy)} variant="outline" onClick={() => onResolve("deny")}>{busy === "deny" ? "Denying..." : "Deny"}</Button><Button disabled={Boolean(busy)} onClick={() => onResolve("approve")}><ShieldCheck />{busy === "approve" ? "Approving..." : "Approve"}</Button></> : undefined}><div className="mb-3"><ApprovalStatusBadge status={request.status} /></div><Tabs defaultValue="overview"><TabsList className="w-full"><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="details">Details</TabsTrigger></TabsList><TabsContent className="mt-4 divide-y" value="overview"><DetailRow label="Workspace" value={request.workspace_id} mono /><DetailRow label="Request" value={request.id} mono /><DetailRow label="Tool" value={request.target_tool} mono /><DetailRow label="Source" value={request.source || "-"} mono /><DetailRow label="Session" value={request.session_hash || "-"} mono /><DetailRow label="Created" value={formatDateTime(request.created_at)} /><DetailRow label="Expires" value={formatDateTime(request.expires_at)} />{request.resolved_at ? <DetailRow label="Resolved" value={formatDateTime(request.resolved_at)} /> : null}{request.resolved_by ? <DetailRow label="Resolved by" value={request.resolved_by} mono /> : null}{request.reason ? <DetailRow label="Reason" value={request.reason} /> : null}{request.retry_until ? <DetailRow label="Retry until" value={formatDateTime(request.retry_until)} /> : null}{request.consumed_at ? <DetailRow label="Consumed" value={formatDateTime(request.consumed_at)} /> : null}</TabsContent><TabsContent className="mt-4 space-y-3" value="details"><JsonViewer value={request.arguments ?? {}} />{request.guard_reason ? <div className="rounded-lg border p-3 text-sm"><div className="mb-1 font-medium">Guard reason</div><div className="text-muted-foreground">{request.guard_reason}</div></div> : null}</TabsContent></Tabs></ResponsiveDialog>
}

function ApprovalStatusBadge({ status }: { status: string }) { return <Badge variant={status === "denied" ? "destructive" : status === "pending" || status === "approved" || status === "consumed" ? "secondary" : "outline"}>{status}</Badge> }
function formatDateTime(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }
