import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { ArrowLeft, CircleDot, RefreshCw, TerminalSquare } from "lucide-react"
import { Link, useNavigate } from "react-router-dom"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { TextViewer } from "@/components/text-viewer"
import { TruncatedText } from "@/components/truncated-text"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemHeader, ItemTitle } from "@/components/ui/item"
import { ScrollableTabsList, Tabs, TabsContent, TabsTrigger } from "@/components/ui/tabs"
import { streamExecution, streamWorkspaceExecutions } from "@/lib/execution-stream"
import { adminApi, type ExecutionEvent, type ExecutionFeedEvent, type ExecutionInfo, type ExecutionSnapshot } from "@/lib/api"

const refreshInterval = 1500
const reconnectDelay = 750
const maxCombinedFeedEvents = 4000

export function WorkspaceExecutions({ workspaceID }: { workspaceID: string }) {
  return <Tabs defaultValue="executions"><ScrollableTabsList><TabsTrigger value="executions">Executions</TabsTrigger><TabsTrigger value="combined">Combined stream</TabsTrigger></ScrollableTabsList><TabsContent className="mt-4" value="executions"><WorkspaceExecutionList workspaceID={workspaceID} /></TabsContent><TabsContent className="mt-4" value="combined"><WorkspaceExecutionFeed workspaceID={workspaceID} /></TabsContent></Tabs>
}

function WorkspaceExecutionList({ workspaceID }: { workspaceID: string }) {
  const navigate = useNavigate()
  const [items, setItems] = useState<ExecutionInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")

  const load = useCallback(async (refresh = false) => {
    if (refresh) setRefreshing(true)
    try { setItems(await adminApi.workspaceExecutions(workspaceID)); setError("") } catch (value) { setError(errorText(value)) } finally { setLoading(false); setRefreshing(false) }
  }, [workspaceID])

  useEffect(() => {
    let active = true
    void adminApi.workspaceExecutions(workspaceID).then((value) => { if (active) { setItems(value); setError("") } }).catch((value) => { if (active) setError(errorText(value)) }).finally(() => { if (active) setLoading(false) })
    const timer = window.setInterval(() => void load(), refreshInterval)
    return () => { active = false; window.clearInterval(timer) }
  }, [load, workspaceID])

  function openExecution(item: ExecutionInfo) { navigate(`/workspaces/${encodeURIComponent(workspaceID)}/activity/${encodeURIComponent(item.id)}`) }

  return <div className="space-y-4"><div className="flex flex-wrap items-center justify-between gap-2"><div><div className="text-sm font-medium">Command executions</div><div className="text-xs text-muted-foreground">Live run_command output is kept in a bounded in-memory buffer and scoped to this workspace.</div></div><Button disabled={refreshing} size="sm" variant="outline" onClick={() => void load(true)}><RefreshCw className={refreshing ? "animate-spin" : ""} />Refresh</Button></div><PageError message={error} />{loading ? <PageLoading rows={5} /> : items.length === 0 ? <PageEmpty icon={TerminalSquare} title="No command executions" description="run_command executions for this workspace will appear here." /> : <ItemGroup>{items.map((item) => <Item className="cursor-pointer" key={item.id} role="button" tabIndex={0} variant="outline" onClick={() => openExecution(item)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") openExecution(item) }}><ItemContent className="min-w-0"><ItemHeader><ItemTitle className="min-w-0"><TruncatedText lines={1}>{item.command}</TruncatedText></ItemTitle><ExecutionStatusBadge status={item.status} /></ItemHeader><ItemDescription>{item.tool} · {item.cwd}{item.source ? ` · ${item.source}` : ""}</ItemDescription><div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground"><span className="font-mono">{item.id}</span><span>{formatDateTime(item.started_at)}</span>{item.exit_code !== undefined ? <span>exit {item.exit_code}</span> : null}</div></ItemContent></Item>)}</ItemGroup>}</div>
}

function WorkspaceExecutionFeed({ workspaceID }: { workspaceID: string }) {
  const [events, setEvents] = useState<ExecutionFeedEvent[]>([])
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState("")
  const [streamVersion, setStreamVersion] = useState(0)
  const retryTimer = useRef<number | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const stickToBottomRef = useRef(true)

  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    async function connect() {
      try {
        await streamWorkspaceExecutions(workspaceID, controller.signal, {
          onSnapshot: (snapshot) => { setEvents(snapshot.events.slice(-maxCombinedFeedEvents)); setConnected(true); setError("") },
          onEvent: (event) => { setConnected(true); setError(""); setEvents((current) => appendFeedEvent(current, event)) },
        })
      } catch (value) {
        if (controller.signal.aborted || stopped) return
        setConnected(false)
        setError(errorText(value))
        retryTimer.current = window.setTimeout(() => void connect(), reconnectDelay)
      }
    }
    void connect()
    return () => { stopped = true; controller.abort(); if (retryTimer.current !== null) window.clearTimeout(retryTimer.current) }
  }, [streamVersion, workspaceID])

  useEffect(() => {
    function updateStickToBottom() {
      const root = document.documentElement
      stickToBottomRef.current = root.scrollHeight - window.scrollY - window.innerHeight <= 96
    }
    updateStickToBottom()
    window.addEventListener("scroll", updateStickToBottom, { passive: true })
    return () => window.removeEventListener("scroll", updateStickToBottom)
  }, [])

  const latestSequence = events.at(-1)?.sequence ?? 0
  useLayoutEffect(() => {
    if (!latestSequence || !stickToBottomRef.current) return
    bottomRef.current?.scrollIntoView({ block: "end" })
  }, [latestSequence])

  const log = formatExecutionFeed(events)
  return <div className="space-y-4"><div className="flex flex-wrap items-center justify-between gap-2"><div><div className="text-sm font-medium">Combined command stream</div><div className="text-xs text-muted-foreground">All run_command output for this workspace is merged in event order. Each execution starts with its execution ID.</div></div><div className="flex flex-wrap items-center gap-2"><Badge variant={connected ? "secondary" : "outline"}><CircleDot className="size-3" />{connected ? "Live" : "Reconnecting"}</Badge><Button size="sm" variant="outline" onClick={() => { setConnected(false); setError(""); setStreamVersion((value) => value + 1) }}><RefreshCw />Reconnect</Button><Button size="sm" variant="outline" onClick={() => setEvents([])}>Clear view</Button></div></div><PageError message={error} />{events.length === 0 ? <PageEmpty icon={TerminalSquare} title="Waiting for command output" description="Recent and future run_command output will stream here automatically." /> : <TextViewer maxHeight={null} value={log} />}<div ref={bottomRef} /></div>
}

export function WorkspaceExecutionDetail({ workspaceID, executionID }: { workspaceID: string; executionID: string }) {
  const [snapshot, setSnapshot] = useState<ExecutionSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState("")
  const retryTimer = useRef<number | null>(null)
  const streamExecutionID = snapshot?.execution.id
  const streamExecutionStatus = snapshot?.execution.status

  useEffect(() => {
    let active = true
    void adminApi.workspaceExecution(workspaceID, executionID).then((value) => { if (active) { setSnapshot(value); setConnected(value.execution.status === "running"); setError("") } }).catch((value) => { if (active) setError(errorText(value)) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [executionID, workspaceID])

  useEffect(() => {
    if (!streamExecutionID || streamExecutionStatus !== "running") return
    const executionID = streamExecutionID
    const controller = new AbortController()
    let stopped = false
    async function connect() {
      try {
        await streamExecution(workspaceID, executionID, controller.signal, {
          onSnapshot: (next) => { setSnapshot(next); setConnected(next.execution.status === "running"); setError("") },
          onEvent: (event) => setSnapshot((current) => current ? applyExecutionEvent(current, event) : current),
        })
        setConnected(false)
      } catch (value) {
        if (controller.signal.aborted || stopped) return
        setConnected(false)
        setError(errorText(value))
        retryTimer.current = window.setTimeout(() => void connect(), reconnectDelay)
      }
    }
    void connect()
    return () => { stopped = true; controller.abort(); if (retryTimer.current !== null) window.clearTimeout(retryTimer.current) }
  }, [streamExecutionID, streamExecutionStatus, workspaceID])

  if (loading) return <PageLoading rows={6} />
  if (!snapshot) return <div className="space-y-4"><PageError message={error || "Command execution not found."} /><Button asChild size="sm" variant="outline"><Link to={`/workspaces/${encodeURIComponent(workspaceID)}/activity`}><ArrowLeft />Activity</Link></Button></div>

  return <div className="space-y-4"><div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><TruncatedText className="text-base font-semibold" lines={2}>{snapshot.execution.command}</TruncatedText><div className="mt-1 break-all font-mono text-xs text-muted-foreground">{snapshot.execution.id} · {snapshot.execution.cwd}</div></div><Button asChild size="sm" variant="outline"><Link to={`/workspaces/${encodeURIComponent(workspaceID)}/activity`}><ArrowLeft />Activity</Link></Button></div><div className="flex flex-wrap items-center gap-2"><ExecutionStatusBadge status={snapshot.execution.status} />{snapshot.execution.status === "running" ? <Badge variant={connected ? "secondary" : "outline"}><CircleDot className="size-3" />{connected ? "Live" : "Reconnecting"}</Badge> : null}{snapshot.execution.exit_code !== undefined ? <Badge variant="outline">exit {snapshot.execution.exit_code}</Badge> : null}</div><PageError message={error} /><Tabs defaultValue="stdout"><ScrollableTabsList><TabsTrigger value="stdout">stdout</TabsTrigger><TabsTrigger value="stderr">stderr</TabsTrigger></ScrollableTabsList><TabsContent className="mt-4" value="stdout"><TextViewer maxHeight={null} value={snapshot.stdout || "(no stdout)"} /></TabsContent><TabsContent className="mt-4" value="stderr"><TextViewer maxHeight={null} value={snapshot.stderr || "(no stderr)"} /></TabsContent></Tabs></div>
}

function applyExecutionEvent(snapshot: ExecutionSnapshot, event: ExecutionEvent): ExecutionSnapshot {
  if (event.execution_id !== snapshot.execution.id || event.sequence <= snapshot.latest_sequence) return snapshot
  if (event.type === "output") return { ...snapshot, stdout: event.stream === "stdout" ? snapshot.stdout + (event.data ?? "") : snapshot.stdout, stderr: event.stream === "stderr" ? snapshot.stderr + (event.data ?? "") : snapshot.stderr, latest_sequence: event.sequence }
  if (event.type === "completed") return { ...snapshot, latest_sequence: event.sequence, execution: { ...snapshot.execution, status: event.status ?? snapshot.execution.status, exit_code: event.exit_code, timed_out: event.timed_out } }
  return { ...snapshot, latest_sequence: event.sequence }
}

function appendFeedEvent(events: ExecutionFeedEvent[], event: ExecutionFeedEvent) {
  if ((events.at(-1)?.sequence ?? 0) >= event.sequence) return events
  const next = [...events, event]
  return next.length > maxCombinedFeedEvents ? next.slice(-maxCombinedFeedEvents) : next
}

function formatExecutionFeed(events: ExecutionFeedEvent[]) {
  let output = ""
  let currentExecutionID = ""
  for (const event of events) {
    if (event.execution_id !== currentExecutionID || event.type === "started") {
      if (output && !output.endsWith("\n")) output += "\n"
      if (output) output += "\n"
      output += `===== exec_id=${event.execution_id} =====\n`
      if (event.execution?.command) output += `$ ${event.execution.command}\n`
      if (event.execution?.cwd) output += `cwd: ${event.execution.cwd}\n`
      currentExecutionID = event.execution_id
    }
    if (event.type === "output") output += event.data ?? ""
    if (event.type === "completed") {
      if (output && !output.endsWith("\n")) output += "\n"
      const exit = event.exit_code === undefined ? "" : `, exit ${event.exit_code}`
      output += `[${event.status || "completed"}${exit}]\n`
    }
  }
  return output
}

function ExecutionStatusBadge({ status }: { status: string }) { return <Badge variant={status === "failed" || status === "timed_out" ? "destructive" : status === "running" || status === "success" ? "secondary" : "outline"}>{status}</Badge> }
function formatDateTime(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }
