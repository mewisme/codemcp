import { useEffect, useMemo, useRef, useState } from "react"
import { createColumnHelper } from "@tanstack/react-table"
import { ArrowLeft, CircleDot, Pause, Play, Radio, RefreshCw, Search, Trash2 } from "lucide-react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { DataTable } from "@/components/data-table"
import { DataTableColumnHeader } from "@/components/data-table-column-header"
import type { DataTableFeatures } from "@/components/data-table-features"
import { DetailRow } from "@/components/detail-row"
import { JsonViewer } from "@/components/json-viewer"
import { PageError, PageEmpty, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { TruncatedText } from "@/components/truncated-text"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemHeader, ItemTitle } from "@/components/ui/item"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useIsMobile } from "@/hooks/use-mobile"
import { adminApi, authHeaders, type ActivityEvent } from "@/lib/api"

type ActivityStreamHandlers = { onReady: () => void; onEvent: (event: ActivityEvent) => void; onGap: (from: number, to: number) => void }

const columnHelper = createColumnHelper<DataTableFeatures, ActivityEvent>()
const columns = columnHelper.columns([
  columnHelper.accessor("timestamp", { header: ({ column }) => <DataTableColumnHeader column={column} title="Time" />, cell: ({ getValue }) => <span className="whitespace-nowrap text-xs text-muted-foreground">{formatTime(getValue())}</span> }),
  columnHelper.display({ id: "event", header: "Event", cell: ({ row }) => <div className="min-w-0"><TruncatedText className="font-mono text-sm font-medium" lines={1}>{activityTitle(row.original)}</TruncatedText><TruncatedText className="mt-1 text-xs text-muted-foreground" lines={1}>{row.original.kind}</TruncatedText></div> }),
  columnHelper.accessor("workspace_id", { header: "Workspace", cell: ({ getValue }) => getValue() ? <TruncatedText className="text-xs text-muted-foreground" lines={1} mono>{getValue()!}</TruncatedText> : <span className="text-muted-foreground">-</span> }),
  columnHelper.accessor("status", { header: "Status", cell: ({ getValue }) => getValue() ? <StatusBadge status={getValue()!} /> : <span className="text-muted-foreground">-</span> }),
  columnHelper.accessor("duration_ms", { header: ({ column }) => <DataTableColumnHeader column={column} title="Duration" />, cell: ({ getValue }) => <span className="whitespace-nowrap text-xs text-muted-foreground">{getValue() === undefined ? "-" : formatDuration(getValue()!)}</span> }),
  columnHelper.accessor("source", { header: "Source", cell: ({ getValue }) => <span className="text-xs text-muted-foreground">{getValue() || "-"}</span> }),
])

export function ActivityPage() {
  const mobile = useIsMobile()
  const navigate = useNavigate()
  const [events, setEvents] = useState<ActivityEvent[]>([])
  const [pending, setPending] = useState<ActivityEvent[]>([])
  const [selected, setSelected] = useState<ActivityEvent | null>(null)
  const [kind, setKind] = useState("all")
  const [status, setStatus] = useState("all")
  const [source, setSource] = useState("all")
  const [query, setQuery] = useState("")
  const [paused, setPaused] = useState(false)
  const pausedRef = useRef(false)
  const [connected, setConnected] = useState(false)
  const [connecting, setConnecting] = useState(true)
  const [streamVersion, setStreamVersion] = useState(0)
  const [error, setError] = useState("")

  useEffect(() => { pausedRef.current = paused }, [paused])
  useEffect(() => {
    const controller = new AbortController()
    void streamActivity(controller.signal, {
      onReady: () => { setConnected(true); setConnecting(false) },
      onEvent: (event) => { setConnected(true); setConnecting(false); if (pausedRef.current) setPending((items) => prependActivity(items, event)); else setEvents((items) => prependActivity(items, event)) },
      onGap: (from, to) => setError(`Activity stream skipped ${to - from - 1} event(s) between sequence ${from} and ${to}.`),
    }).then(() => {
      if (!controller.signal.aborted) { setConnected(false); setConnecting(false); setError("Activity stream closed; use Refresh to reconnect.") }
    }).catch((value) => {
      if (!controller.signal.aborted) { setConnected(false); setConnecting(false); setError(errorText(value)) }
    })
    return () => controller.abort()
  }, [streamVersion])

  const kinds = useMemo(() => unique(events, pending, (event) => event.kind), [events, pending])
  const statuses = useMemo(() => unique(events, pending, (event) => event.status), [events, pending])
  const sources = useMemo(() => unique(events, pending, (event) => event.source), [events, pending])
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return events.filter((event) => {
      if (kind !== "all" && event.kind !== kind) return false
      if (status !== "all" && event.status !== status) return false
      if (source !== "all" && event.source !== source) return false
      if (!needle) return true
      return [event.kind, event.method, event.source, event.tool, event.workspace_id, event.session_hash, event.session_access, event.session_workspace_count?.toString(), event.received_by_instance_id, event.executed_by_instance_id, event.status, event.message].some((value) => value?.toLowerCase().includes(needle))
    })
  }, [events, kind, query, source, status])

  function resume() { setEvents((items) => mergeActivity(pending, items)); setPending([]); setPaused(false) }
  function refresh() { setConnected(false); setConnecting(true); setError(""); setStreamVersion((value) => value + 1) }
  function clear() { setEvents([]); setPending([]) }
  function openEvent(event: ActivityEvent) { if (event.call_id) navigate(`/activity/${encodeURIComponent(event.call_id)}`); else setSelected(event) }

  return <div className="space-y-6"><PageHeader title="Activity" description="Live MCP requests, tool calls, and runtime lifecycle events. Tool calls open as addressable child routes." actions={<><Badge variant={connected ? "secondary" : "outline"}><CircleDot className="size-3" />{connected ? "Live" : connecting ? "Connecting" : "Disconnected"}</Badge><Button size="sm" variant="outline" onClick={refresh}><RefreshCw className={connecting ? "animate-spin" : ""} />Refresh</Button><Button size="sm" variant="outline" onClick={() => paused ? resume() : setPaused(true)}>{paused ? <Play /> : <Pause />}{paused ? `Resume${pending.length ? ` (${pending.length})` : ""}` : "Pause"}</Button><Button size="sm" variant="outline" onClick={clear}><Trash2 />Clear</Button></>} /><PageError message={error} /><div className="flex flex-col gap-2 lg:flex-row"><div className="relative min-w-0 flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input className="pl-9" placeholder="Search tool, workspace, source, message..." value={query} onChange={(event) => setQuery(event.target.value)} /></div><FilterSelect label="All event types" value={kind} values={kinds} onValueChange={setKind} /><FilterSelect label="All statuses" value={status} values={statuses} onValueChange={setStatus} /><FilterSelect label="All sources" value={source} values={sources} onValueChange={setSource} /></div>{paused && pending.length ? <div className="rounded-lg border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">{pending.length} new event{pending.length === 1 ? "" : "s"} waiting while paused.</div> : null}{filtered.length === 0 ? <PageEmpty icon={Radio} title="No matching activity" description={events.length ? "Adjust the filters or resume the live stream." : "Activity will appear here as MCP requests and tools run."} /> : mobile ? <ActivityMobileList events={filtered} onSelect={openEvent} /> : <DataTable columns={columns} data={filtered} onRowClick={openEvent} pageSize={20} />}{selected ? <ActivityDetail event={selected} open onOpenChange={(open) => { if (!open) setSelected(null) }} /> : null}</div>
}

export function ActivityCallPage() {
  const { callID = "" } = useParams<{ callID: string }>()
  const [event, setEvent] = useState<ActivityEvent | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  useEffect(() => {
    let active = true
    void adminApi.activityCall(callID).then((value) => { if (active) { setEvent(value); setError("") } }).catch((value) => { if (active) setError(errorText(value)) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [callID])

  if (loading) return <PageLoading rows={6} />
  return <div className="space-y-6"><PageHeader title={event ? activityTitle(event) : "Tool Call"} description={event ? `${event.call_id} · ${formatDateTime(event.timestamp)}` : callID} actions={<Button asChild size="sm" variant="outline"><Link to="/activity"><ArrowLeft />Activity</Link></Button>} /><PageError message={error} />{event ? <ActivityDetailContent event={event} unboundedRaw /> : null}</div>
}

function ActivityMobileList({ events, onSelect }: { events: ActivityEvent[]; onSelect: (event: ActivityEvent) => void }) {
  return <ItemGroup>{events.map((event, index) => <Item className="cursor-pointer" key={`${event.sequence ?? event.timestamp}-${event.kind}-${index}`} role="button" tabIndex={0} variant="outline" onClick={() => onSelect(event)} onKeyDown={(value) => { if (value.key === "Enter" || value.key === " ") onSelect(event) }}><ItemContent><ItemHeader><ItemTitle className="min-w-0 font-mono">{activityTitle(event)}</ItemTitle>{event.status ? <StatusBadge status={event.status} /> : null}</ItemHeader><ItemDescription>{event.message || [event.kind, event.source, event.workspace_id].filter(Boolean).join(" · ")}</ItemDescription><div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground"><span>{formatTime(event.timestamp)}</span><span>{event.kind}</span>{event.duration_ms !== undefined ? <span>{formatDuration(event.duration_ms)}</span> : null}</div></ItemContent></Item>)}</ItemGroup>
}

function ActivityDetail({ event, open, onOpenChange }: { event: ActivityEvent; open: boolean; onOpenChange: (open: boolean) => void }) {
  return <ResponsiveDialog open={open} onOpenChange={onOpenChange} title={activityTitle(event)} description={`${event.kind} · ${formatDateTime(event.timestamp)}`}><ActivityDetailContent event={event} /></ResponsiveDialog>
}

function ActivityDetailContent({ event, unboundedRaw = false }: { event: ActivityEvent; unboundedRaw?: boolean }) {
  return <Tabs defaultValue="overview"><TabsList className="w-full"><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="metadata">Metadata</TabsTrigger><TabsTrigger value="raw">Raw</TabsTrigger></TabsList><TabsContent className="mt-4 divide-y" value="overview"><DetailRow label="Status" value={event.status ? <StatusBadge status={event.status} /> : "-"} /><DetailRow label="Tool" value={event.tool || "-"} mono /><DetailRow label="Method" value={event.method || "-"} mono /><DetailRow label="Message" value={event.message || "-"} /><DetailRow label="Duration" value={event.duration_ms === undefined ? "-" : formatDuration(event.duration_ms)} /></TabsContent><TabsContent className="mt-4 divide-y" value="metadata"><DetailRow label="Call ID" value={event.call_id || "-"} mono /><DetailRow label="Sequence" value={event.sequence ?? "-"} mono /><DetailRow label="Timestamp" value={formatDateTime(event.timestamp)} /><DetailRow label="Source" value={event.source || "-"} mono /><DetailRow label="Workspace" value={event.workspace_id || "-"} mono /><DetailRow label="Session" value={event.session_hash || "-"} mono /><DetailRow label="Session access" value={event.session_access || "-"} mono /><DetailRow label="Session workspaces" value={event.session_workspace_count ?? "-"} mono /><DetailRow label="Received by" value={event.received_by_instance_id || "-"} mono /><DetailRow label="Executed by" value={event.executed_by_instance_id || "-"} mono /><DetailRow label="Kind" value={event.kind} mono /></TabsContent><TabsContent className="mt-4" value="raw"><JsonViewer maxHeight={unboundedRaw ? null : undefined} value={event.raw ?? event} /></TabsContent></Tabs>
}

function FilterSelect({ label, value, values, onValueChange }: { label: string; value: string; values: string[]; onValueChange: (value: string) => void }) {
  return <Select value={value} onValueChange={onValueChange}><SelectTrigger className="w-full lg:w-44"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">{label}</SelectItem>{values.map((item) => <SelectItem key={item} value={item}>{item}</SelectItem>)}</SelectContent></Select>
}

function StatusBadge({ status }: { status: string }) {
  const destructive = status === "error" || status === "degraded" || status === "failed"
  return <Badge variant={destructive ? "destructive" : status === "success" || status === "completed" || status === "ok" ? "secondary" : "outline"}>{status}</Badge>
}

function activityKey(event: ActivityEvent) { return event.kind === "tool_call" && event.call_id ? `tool:${event.call_id}` : event.sequence ? `seq:${event.sequence}` : `${event.kind}:${event.timestamp}:${event.method || ""}` }
function prependActivity(items: ActivityEvent[], event: ActivityEvent) { const key = activityKey(event); return [event, ...items.filter((item) => activityKey(item) !== key)].slice(0, 200) }
function mergeActivity(head: ActivityEvent[], tail: ActivityEvent[]) { return [...head, ...tail].reduce<ActivityEvent[]>((items, event) => { const key = activityKey(event); return items.some((item) => activityKey(item) === key) ? items : [...items, event] }, []).slice(0, 200) }
function unique(events: ActivityEvent[], pending: ActivityEvent[], pick: (event: ActivityEvent) => string | undefined) { return [...new Set([...events, ...pending].map(pick).filter((value): value is string => Boolean(value)))].sort() }
function activityTitle(event: ActivityEvent) { return event.tool || event.method || event.kind }
function formatDuration(value: number) { return value < 1000 ? `${value} ms` : `${(value / 1000).toFixed(value < 10000 ? 1 : 0)} s` }
function formatTime(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString() }
function formatDateTime(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }

async function streamActivity(signal: AbortSignal, handlers: ActivityStreamHandlers) {
  const response = await fetch("/api/activity/stream?history=100", { headers: authHeaders(), signal })
  if (!response.ok || !response.body) { const message = await response.text().catch(() => ""); throw new Error(message.trim() || `Activity stream ${response.status}`) }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  let lastSequence = 0
  while (true) {
    const { value, done } = await reader.read()
    if (done) return
    buffer += decoder.decode(value, { stream: true })
    let boundary = buffer.indexOf("\n\n")
    while (boundary >= 0) {
      const packet = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      let eventType = "message"
      let data = ""
      for (const line of packet.split("\n")) { if (line.startsWith("event: ")) eventType = line.slice(7).trim(); if (line.startsWith("data: ")) data += line.slice(6) }
      if (eventType === "ready" || eventType === "heartbeat") handlers.onReady()
      else if (eventType === "overflow") throw new Error("Activity stream subscriber overflowed; reconnect to resync recent events.")
      else if (eventType === "activity" && data) {
        try {
          const event = JSON.parse(data) as ActivityEvent
          const sequence = event.sequence ?? 0
          if (lastSequence > 0 && sequence > lastSequence + 1) handlers.onGap(lastSequence, sequence)
          if (sequence > 0) lastSequence = sequence
          handlers.onEvent(event)
        } catch (value) { if (value instanceof SyntaxError) continue; throw value }
      }
      boundary = buffer.indexOf("\n\n")
    }
  }
}
