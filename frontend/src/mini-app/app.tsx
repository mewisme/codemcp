import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ArrowLeftIcon,
  CirclePauseIcon,
  CirclePlayIcon,
  Maximize2Icon,
  RefreshCwIcon,
  SearchIcon,
  Settings2Icon,
  Trash2Icon,
  XIcon,
} from "lucide-react"

import { JsonViewer } from "@/components/json-viewer"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { TextViewer } from "@/components/text-viewer"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Switch } from "@/components/ui/switch"
import { Tabs, ScrollableTabsList, TabsContent, TabsTrigger } from "@/components/ui/tabs"
import { useIsMobile } from "@/hooks/use-mobile"
import type { ActivityEvent, ExecutionFeedEvent, ExecutionInfo, ExecutionSnapshot as ExecutionDetailSnapshot, LogEvent, ToolCallDetail } from "@/lib/api"
import { cn } from "@/lib/utils"
import { authenticateTelegramSession, loadExecutionDetail, loadToolCallDetail } from "@/mini-app/api"
import {
  applyExecutionEvent,
  applyToolEvent,
  boundedAppend,
  parseStreamFrame,
  streamURL,
  type ExecutionSnapshot,
  type MiniAppFeed,
  type RuntimeSnapshot,
  type StreamState,
  type ToolRecord,
  type ToolSnapshot,
} from "@/mini-app/stream"
import {
  bindBackButton,
  bindMainButtonState,
  bindSecondaryButton,
  bindSettingsButton,
  bindTelegramActivity,
  confirmTelegramAction,
  hideTelegramKeyboard,
  initializeTelegramMiniApp,
  loadMiniAppPreferences,
  saveMiniAppPreferences,
  setTelegramVerticalSwipesEnabled,
  telegramHaptic,
  telegramNativeControls,
  telegramWebApp,
  toggleTelegramFullscreen,
  type MiniAppPreferences,
} from "@/mini-app/telegram"

type SelectedItem = { feed: MiniAppFeed; key: string }
type FilterMode = "all" | "running" | "success" | "warn" | "error" | "cancelled"

const defaultPreferences: MiniAppPreferences = { density: "comfortable", autoFollow: true, defaultFeed: "runtime" }

export function MiniApp() {
  const isMobile = useIsMobile()
  const [initData] = useState(() => telegramWebApp()?.initData?.trim() || "")
  const [authenticated, setAuthenticated] = useState(false)
  const [authError, setAuthError] = useState(() => initData ? "" : "Telegram Mini App context is unavailable")
  const [activeFeed, setActiveFeed] = useState<MiniAppFeed>("runtime")
  const [telegramActive, setTelegramActive] = useState(() => telegramWebApp()?.isActive !== false)
  const [connection, setConnection] = useState<StreamState>(() => initData ? (telegramWebApp()?.isActive === false ? "suspended" : "connecting") : "disconnected")
  const [streamError, setStreamError] = useState("")
  const [reconnectKey, setReconnectKey] = useState(0)
  const [paused, setPaused] = useState(false)
  const [pauseCursor, setPauseCursor] = useState(0)
  const [query, setQuery] = useState("")
  const [filter, setFilter] = useState<FilterMode>("all")
  const [scope, setScope] = useState("all")
  const [selected, setSelected] = useState<SelectedItem | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [preferences, setPreferences] = useState<MiniAppPreferences>(defaultPreferences)
  const [preferencesLoaded, setPreferencesLoaded] = useState(false)
  const [runtimeEvents, setRuntimeEvents] = useState<LogEvent[]>([])
  const [executions, setExecutions] = useState<ExecutionInfo[]>([])
  const [executionEvents, setExecutionEvents] = useState<ExecutionFeedEvent[]>([])
  const [toolRecords, setToolRecords] = useState<ToolRecord[]>([])
  const [toolEvents, setToolEvents] = useState<ActivityEvent[]>([])
  const [clearCursor, setClearCursor] = useState<Record<MiniAppFeed, number>>({ runtime: 0, executions: 0, tools: 0 })
  const cursorRef = useRef<Record<MiniAppFeed, number>>({ runtime: 0, executions: 0, tools: 0 })
  const retryRef = useRef(0)
  const listTopRef = useRef<HTMLDivElement | null>(null)
  const nativeControls = telegramNativeControls()

  const applySnapshot = useCallback((feed: MiniAppFeed, payload: unknown) => {
    if (feed === "runtime") {
      const snapshot = payload as RuntimeSnapshot
      setRuntimeEvents(Array.isArray(snapshot?.events) ? snapshot.events : [])
      return
    }
    if (feed === "executions") {
      const snapshot = payload as ExecutionSnapshot
      setExecutions(Array.isArray(snapshot?.executions) ? snapshot.executions : [])
      setExecutionEvents(Array.isArray(snapshot?.events) ? snapshot.events : [])
      return
    }
    const snapshot = payload as ToolSnapshot
    setToolRecords(Array.isArray(snapshot?.records) ? snapshot.records : [])
    setToolEvents(Array.isArray(snapshot?.events) ? snapshot.events : [])
  }, [])

  const applyEvent = useCallback((feed: MiniAppFeed, payload: unknown) => {
    if (feed === "runtime") {
      setRuntimeEvents((items) => boundedAppend(items, payload as LogEvent))
      return
    }
    if (feed === "executions") {
      const event = payload as ExecutionFeedEvent
      setExecutionEvents((items) => boundedAppend(items, event))
      setExecutions((items) => applyExecutionEvent(items, event))
      return
    }
    const event = payload as ActivityEvent
    setToolEvents((items) => boundedAppend(items, event))
    setToolRecords((items) => applyToolEvent(items, event))
  }, [])

  useEffect(() => initializeTelegramMiniApp(), [])

  useEffect(() => bindTelegramActivity((active) => {
    setTelegramActive(active)
    if (active) {
      retryRef.current = 0
      setConnection("reconnecting")
    } else {
      setConnection("suspended")
    }
  }), [])

  useEffect(() => {
    let cancelled = false
    void loadMiniAppPreferences().then((loaded) => {
      if (cancelled) return
      setPreferences(loaded)
      setActiveFeed(loaded.defaultFeed)
      setPreferencesLoaded(true)
    })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (!preferencesLoaded) return
    void saveMiniAppPreferences(preferences)
  }, [preferences, preferencesLoaded])

  useEffect(() => {
    if (!initData) return
    let cancelled = false
    void authenticateTelegramSession(initData)
      .then(() => {
        if (!cancelled) setAuthenticated(true)
      })
      .catch((error) => {
        if (!cancelled) {
          setAuthError(error instanceof Error ? error.message : "Telegram Mini App authentication failed")
          setConnection("disconnected")
        }
      })
    return () => { cancelled = true }
  }, [initData])

  useEffect(() => {
    if (!authenticated || !telegramActive) return
    if (typeof WebSocket === "undefined") return
    let disposed = false
    let reconnectTimer = 0
    const socket = new WebSocket(streamURL(activeFeed))

    socket.onmessage = (message) => {
      if (disposed || typeof message.data !== "string") return
      const frame = parseStreamFrame(message.data)
      if (!frame || frame.feed !== activeFeed) return
      if (frame.type === "snapshot") {
        const latest = frame.latest_sequence || 0
        cursorRef.current[activeFeed] = latest
        applySnapshot(activeFeed, frame.payload)
        retryRef.current = 0
        setConnection("live")
        setStreamError("")
        return
      }
      if (frame.type === "heartbeat") {
        cursorRef.current[activeFeed] = Math.max(cursorRef.current[activeFeed], frame.latest_sequence || 0)
        return
      }
      if (frame.type === "resync") {
        setConnection("reconnecting")
        setStreamError("Live history changed; resyncing")
        socket.close()
        return
      }
      if (frame.type === "error") {
        setStreamError(frame.reason === "snapshot_unavailable" ? "Runtime snapshot is unavailable" : "Live feed is temporarily unavailable")
        socket.close()
        return
      }
      const sequence = frame.sequence || 0
      if (sequence && sequence <= cursorRef.current[activeFeed]) return
      if (sequence) cursorRef.current[activeFeed] = sequence
      applyEvent(activeFeed, frame.payload)
      setConnection("live")
    }

    socket.onerror = () => {
      if (!disposed) setConnection("reconnecting")
    }

    socket.onclose = () => {
      if (disposed) return
      setConnection("reconnecting")
      const attempt = Math.min(retryRef.current + 1, 4)
      retryRef.current = attempt
      const delay = Math.min(800 * Math.pow(2, attempt - 1), 5000)
      reconnectTimer = window.setTimeout(() => setReconnectKey((value) => value + 1), delay)
    }

    return () => {
      disposed = true
      if (reconnectTimer) window.clearTimeout(reconnectTimer)
      socket.close()
    }
  }, [authenticated, telegramActive, activeFeed, reconnectKey, applySnapshot, applyEvent])

  const changeFeed = useCallback((feed: MiniAppFeed) => {
    if (feed === activeFeed) return
    setPaused(false)
    setPauseCursor(0)
    setFilter("all")
    setScope("all")
    setQuery("")
    setSelected(null)
    setConnection("connecting")
    setStreamError("")
    retryRef.current = 0
    setActiveFeed(feed)
  }, [activeFeed])

  const visibleCursor = paused ? pauseCursor : Number.MAX_SAFE_INTEGER

  const executionSequenceByID = useMemo(() => {
    const result = new Map<string, number>()
    for (const event of executionEvents) result.set(event.execution_id, Math.max(result.get(event.execution_id) || 0, event.sequence || 0))
    return result
  }, [executionEvents])

  const scopes = useMemo(() => {
    const values = new Set<string>()
    if (activeFeed === "runtime") for (const event of runtimeEvents) if (event.workspace_id) values.add(String(event.workspace_id))
    if (activeFeed === "executions") for (const item of executions) if (item.workspace_id) values.add(item.workspace_id)
    if (activeFeed === "tools") for (const record of toolRecords) if (record.latest.workspace_id) values.add(record.latest.workspace_id)
    return Array.from(values).sort()
  }, [activeFeed, runtimeEvents, executions, toolRecords])

  const filteredRuntime = useMemo(() => {
    const lower = query.trim().toLowerCase()
    return runtimeEvents
      .filter((event) => (event.sequence || 0) > clearCursor.runtime && (event.sequence || 0) <= visibleCursor)
      .filter((event) => scope === "all" || event.workspace_id === scope)
      .filter((event) => filterRuntime(event, filter))
      .filter((event) => !lower || runtimeHaystack(event).includes(lower))
      .slice()
      .reverse()
  }, [runtimeEvents, clearCursor.runtime, visibleCursor, scope, filter, query])

  const filteredExecutions = useMemo(() => {
    const lower = query.trim().toLowerCase()
    return executions
      .filter((item) => {
        const sequence = executionSequenceByID.get(item.id) || 0
        return sequence > clearCursor.executions && sequence <= visibleCursor
      })
      .filter((item) => scope === "all" || item.workspace_id === scope)
      .filter((item) => filterExecution(item, filter))
      .filter((item) => !lower || executionHaystack(item).includes(lower))
      .slice()
      .sort((a, b) => Date.parse(b.started_at || "") - Date.parse(a.started_at || ""))
  }, [executions, executionSequenceByID, clearCursor.executions, visibleCursor, scope, filter, query])

  const filteredTools = useMemo(() => {
    const lower = query.trim().toLowerCase()
    return toolRecords
      .filter((record) => (record.latest.sequence || 0) > clearCursor.tools && (record.latest.sequence || 0) <= visibleCursor)
      .filter((record) => scope === "all" || record.latest.workspace_id === scope)
      .filter((record) => filterTool(record, filter))
      .filter((record) => !lower || toolHaystack(record).includes(lower))
      .slice()
      .sort((a, b) => (b.latest.sequence || 0) - (a.latest.sequence || 0))
  }, [toolRecords, clearCursor.tools, visibleCursor, scope, filter, query])

  const clearView = useCallback(() => {
    const feed = activeFeed
    const cursor = cursorRef.current[feed]
    setClearCursor((value) => ({ ...value, [feed]: cursor }))
    setSelected(null)
    telegramHaptic("medium")
  }, [activeFeed])

  const requestClearView = useCallback(() => {
    void confirmTelegramAction("Clear the current Mini App log view? New events will continue to appear.").then((confirmed) => {
      if (confirmed) clearView()
    })
  }, [clearView])

  const togglePause = useCallback(() => {
    if (paused) {
      setPaused(false)
      setPauseCursor(0)
      requestAnimationFrame(() => listTopRef.current?.scrollIntoView?.({ block: "start", behavior: "smooth" }))
    } else {
      setPauseCursor(cursorRef.current[activeFeed])
      setPaused(true)
    }
    telegramHaptic()
  }, [activeFeed, paused])

  const reconnect = useCallback(() => {
    retryRef.current = 0
    setConnection("reconnecting")
    setReconnectKey((value) => value + 1)
  }, [])

  const mainAction = useCallback(() => {
    if (connection === "suspended") return
    if (connection === "live") togglePause()
    else reconnect()
  }, [connection, togglePause, reconnect])

  const closeOverlay = useCallback(() => {
    if (settingsOpen) setSettingsOpen(false)
    else if (selected) setSelected(null)
  }, [selected, settingsOpen])

  const nativeListActionsVisible = authenticated && !(isMobile && Boolean(selected || settingsOpen))

  useEffect(() => bindBackButton(Boolean(selected || settingsOpen), closeOverlay), [selected, settingsOpen, closeOverlay])
  useEffect(() => bindSettingsButton(() => setSettingsOpen(true), !(isMobile && Boolean(selected))), [isMobile, selected])
  useEffect(() => bindMainButtonState(
    connection === "live"
      ? (paused ? "Back to live" : "Pause live")
      : connection === "suspended"
        ? "Mini App inactive"
        : "Reconnect",
    nativeListActionsVisible,
    mainAction,
    {
      active: connection !== "suspended",
      progress: connection === "connecting" || connection === "reconnecting",
      shine: connection === "disconnected",
    }
  ), [connection, paused, nativeListActionsVisible, mainAction])
  useEffect(() => bindSecondaryButton("Clear view", nativeListActionsVisible, requestClearView), [nativeListActionsVisible, requestClearView])

  useEffect(() => {
    const overlayOpen = Boolean((isMobile && selected) || settingsOpen)
    setTelegramVerticalSwipesEnabled(!overlayOpen)
    return () => setTelegramVerticalSwipesEnabled(true)
  }, [isMobile, selected, settingsOpen])

  useEffect(() => {
    if (!preferences.autoFollow || paused || connection !== "live") return
    listTopRef.current?.scrollIntoView?.({ block: "start" })
  }, [runtimeEvents.length, executionEvents.length, toolEvents.length, preferences.autoFollow, paused, connection])

  const openDetail = (item: SelectedItem) => {
    setSelected(item)
    telegramHaptic()
  }

  const submitFilters = (event: React.FormEvent) => {
    event.preventDefault()
    hideTelegramKeyboard()
  }

  if (authError) return <Unavailable message={authError} />
  if (!authenticated) return <LoadingShell />
  if (typeof WebSocket === "undefined") return <Unavailable message="Realtime streaming is unavailable in this Telegram client" />

  const count = activeFeed === "runtime" ? filteredRuntime.length : activeFeed === "executions" ? filteredExecutions.length : filteredTools.length
  const selectedDetail = selected ? resolveSelected(selected, runtimeEvents, executions, executionEvents, toolRecords) : null

  if (isMobile && selected) {
    return (
      <main
        data-mini-app-detail-page
        className="mx-auto flex h-[var(--tg-viewport-height,100dvh)] min-h-0 w-full max-w-6xl flex-col overflow-hidden bg-background text-foreground"
        style={{
          paddingTop: "max(8px, var(--tg-content-safe-area-inset-top, 0px))",
          paddingRight: "max(12px, var(--tg-content-safe-area-inset-right, 0px))",
          paddingBottom: "max(8px, var(--tg-content-safe-area-inset-bottom, 0px))",
          paddingLeft: "max(12px, var(--tg-content-safe-area-inset-left, 0px))",
        }}
      >
        <header className="flex shrink-0 min-w-0 items-start gap-2 border-b pb-3">
          {!nativeControls.backButton ? (
            <Button size="icon-sm" variant="ghost" aria-label="Back to logs" onClick={() => setSelected(null)}>
              <ArrowLeftIcon />
            </Button>
          ) : null}
          <div className="min-w-0 flex-1">
            <div className="break-words text-base font-semibold">{detailTitle(selectedDetail)}</div>
            {detailDescription(selectedDetail) ? <div className="mt-0.5 break-words text-xs text-muted-foreground">{detailDescription(selectedDetail)}</div> : null}
          </div>
        </header>
        <div className="min-h-0 flex-1 pt-2">
          <DetailBody key={selected.key} detail={selectedDetail} executionEvents={executionEvents} fill />
        </div>
      </main>
    )
  }

  return (
    <main
      className="mx-auto min-h-[var(--tg-viewport-stable-height,100dvh)] w-full max-w-6xl bg-background text-foreground"
      style={{
        paddingTop: "max(12px, var(--tg-content-safe-area-inset-top, 0px))",
        paddingRight: "max(12px, var(--tg-content-safe-area-inset-right, 0px))",
        paddingBottom: "max(24px, var(--tg-content-safe-area-inset-bottom, 0px))",
        paddingLeft: "max(12px, var(--tg-content-safe-area-inset-left, 0px))",
      }}
    >
      <div ref={listTopRef} />
      <header className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h1 className="text-lg font-semibold tracking-tight">Logs</h1>
            <ConnectionBadge state={connection} paused={paused} />
          </div>
          <p className="mt-0.5 break-words text-xs text-muted-foreground">CodeMCP Mini App · realtime read-only view</p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {!nativeControls.mainButton ? (
            <Button size="icon-sm" variant="ghost" aria-label={connection === "live" ? (paused ? "Back to live" : "Pause live") : "Reconnect"} onClick={mainAction} disabled={connection === "suspended"}>
              {connection === "live" ? (paused ? <CirclePlayIcon /> : <CirclePauseIcon />) : <RefreshCwIcon className={cn(connection === "reconnecting" && "animate-spin")} />}
            </Button>
          ) : null}
          {!nativeControls.secondaryButton ? <Button size="icon-sm" variant="ghost" aria-label="Clear view" onClick={requestClearView}><Trash2Icon /></Button> : null}
          {!nativeControls.settingsButton ? <Button size="icon-sm" variant="ghost" aria-label="Settings" onClick={() => setSettingsOpen(true)}><Settings2Icon /></Button> : null}
        </div>
      </header>

      {streamError ? (
        <div className="mb-3 flex items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">
          <span>{streamError}</span>
          <Button size="xs" variant="outline" onClick={reconnect}>Retry</Button>
        </div>
      ) : null}

      <Tabs value={activeFeed} onValueChange={(value) => changeFeed(value as MiniAppFeed)} className="gap-3">
        <ScrollableTabsList className="justify-start">
          <TabsTrigger className="flex-none px-3" value="runtime">Runtime</TabsTrigger>
          <TabsTrigger className="flex-none px-3" value="executions">Command Execute</TabsTrigger>
          <TabsTrigger className="flex-none px-3" value="tools">Tool Call/MCP</TabsTrigger>
        </ScrollableTabsList>
      </Tabs>

      <form onSubmit={submitFilters} className="mt-3 rounded-xl border bg-card p-2.5 shadow-xs">
        <div className="flex min-w-0 items-center gap-2">
          <div className="relative min-w-0 flex-1">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={searchPlaceholder(activeFeed)} className="pl-8" />
          </div>
          {(query || filter !== "all" || scope !== "all") ? (
            <Button type="button" variant="ghost" size="sm" onClick={() => { setQuery(""); setFilter("all"); setScope("all") }}>Reset</Button>
          ) : null}
        </div>
        <div className="mt-2 flex min-w-0 flex-wrap items-center gap-2">
          <Select value={filter} onValueChange={(value) => setFilter(value as FilterMode)}>
            <SelectTrigger size="sm" className="max-w-[10rem]"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All states</SelectItem>
              {activeFeed !== "runtime" ? <SelectItem value="running">Running</SelectItem> : null}
              {activeFeed === "runtime" ? <SelectItem value="warn">Warnings</SelectItem> : null}
              <SelectItem value="success">{activeFeed === "runtime" ? "Info" : "Success"}</SelectItem>
              <SelectItem value="error">Errors</SelectItem>
              {activeFeed !== "runtime" ? <SelectItem value="cancelled">Cancelled</SelectItem> : null}
            </SelectContent>
          </Select>
          <Select value={scope} onValueChange={setScope}>
            <SelectTrigger size="sm" className="max-w-[13rem]"><SelectValue placeholder="All workspaces" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All workspaces</SelectItem>
              {scopes.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}
            </SelectContent>
          </Select>
          <span className="ml-auto text-xs tabular-nums text-muted-foreground">{count} visible</span>
        </div>
      </form>

      <div className={cn("mt-3 grid min-w-0 gap-3", selected && !isMobile && "md:grid-cols-[minmax(0,1.35fr)_minmax(22rem,0.85fr)]")}>
        <section className={cn("min-w-0 overflow-hidden rounded-xl border bg-card", preferences.density === "compact" && "text-[13px]")}>
          {connection === "connecting" && count === 0 ? <LoadingRows /> : null}
          {activeFeed === "runtime" && filteredRuntime.map((event) => (
            <RuntimeRow key={runtimeKey(event)} event={event} compact={preferences.density === "compact"} selected={selected?.feed === "runtime" && selected.key === runtimeKey(event)} onClick={() => openDetail({ feed: "runtime", key: runtimeKey(event) })} />
          ))}
          {activeFeed === "executions" && filteredExecutions.map((execution) => (
            <ExecutionRow key={execution.id} execution={execution} compact={preferences.density === "compact"} selected={selected?.feed === "executions" && selected.key === execution.id} onClick={() => openDetail({ feed: "executions", key: execution.id })} />
          ))}
          {activeFeed === "tools" && filteredTools.map((record) => (
            <ToolRow key={record.call_id} record={record} compact={preferences.density === "compact"} selected={selected?.feed === "tools" && selected.key === record.call_id} onClick={() => openDetail({ feed: "tools", key: record.call_id })} />
          ))}
          {connection !== "connecting" && count === 0 ? (
            <div className="px-4 py-12 text-center">
              <p className="text-sm font-medium">No events in this view</p>
              <p className="mt-1 text-xs text-muted-foreground">Filters and Clear view are local to this Mini App session.</p>
            </div>
          ) : null}
        </section>

        {selected && !isMobile ? (
          <aside className="sticky top-3 hidden h-[calc(var(--tg-viewport-stable-height,100dvh)-1.5rem)] min-h-0 min-w-0 self-start overflow-hidden rounded-xl border bg-card md:flex md:flex-col">
            <div className="flex min-w-0 items-start gap-3 border-b px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="break-words text-sm font-semibold">{detailTitle(selectedDetail)}</div>
                {detailDescription(selectedDetail) ? <div className="mt-0.5 break-words text-xs text-muted-foreground">{detailDescription(selectedDetail)}</div> : null}
              </div>
              <Button size="icon-sm" variant="ghost" aria-label="Close detail" onClick={() => setSelected(null)}><XIcon /></Button>
            </div>
            <div className="min-h-0 flex-1 p-3"><DetailBody key={selected.key} detail={selectedDetail} executionEvents={executionEvents} fill /></div>
          </aside>
        ) : null}
      </div>

      <ResponsiveDialog
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
        title="View settings"
        description="Presentation preferences only. No authentication data or secrets are stored."
      >
        <div className="space-y-4 pb-1">
          <SettingRow title="Auto-follow live" description="Keep the newest event in view while the stream is live.">
            <Switch checked={preferences.autoFollow} onCheckedChange={(checked) => setPreferences((value) => ({ ...value, autoFollow: checked }))} />
          </SettingRow>
          <SettingRow title="Density" description="Controls row spacing only.">
            <Select value={preferences.density} onValueChange={(density) => setPreferences((value) => ({ ...value, density: density as MiniAppPreferences["density"] }))}>
              <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="comfortable">Comfortable</SelectItem><SelectItem value="compact">Compact</SelectItem></SelectContent>
            </Select>
          </SettingRow>
          <SettingRow title="Default tab" description="Used the next time the Mini App opens.">
            <Select value={preferences.defaultFeed} onValueChange={(defaultFeed) => setPreferences((value) => ({ ...value, defaultFeed: defaultFeed as MiniAppFeed }))}>
              <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="runtime">Runtime</SelectItem><SelectItem value="executions">Command Execute</SelectItem><SelectItem value="tools">Tool Call/MCP</SelectItem></SelectContent>
            </Select>
          </SettingRow>
          <Button variant="outline" className="w-full" onClick={toggleTelegramFullscreen}><Maximize2Icon /> Toggle fullscreen</Button>
        </div>
      </ResponsiveDialog>
    </main>
  )
}

function ConnectionBadge({ state, paused }: { state: StreamState; paused: boolean }) {
  if (paused && state === "live") return <Badge variant="secondary">Paused</Badge>
  if (state === "live") return <Badge variant="outline">Live</Badge>
  if (state === "suspended") return <Badge variant="secondary">Inactive</Badge>
  if (state === "reconnecting") return <Badge variant="secondary">Reconnecting</Badge>
  if (state === "disconnected") return <Badge variant="destructive">Offline</Badge>
  return <Badge variant="secondary">Connecting</Badge>
}

function RuntimeRow({ event, compact, selected, onClick }: { event: LogEvent; compact: boolean; selected: boolean; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={cn("grid w-full min-w-0 gap-1 border-b px-3 text-left last:border-b-0 hover:bg-muted/50 md:grid-cols-[7rem_6rem_minmax(0,1fr)_9rem] md:items-center md:gap-3", selected && "bg-muted/70", compact ? "py-2" : "py-3")}>
      <div className="flex min-w-0 items-center gap-2 md:block"><StatusDot status={String(event.level || "")} /><span className="text-xs tabular-nums text-muted-foreground">{formatTime(event.timestamp || String(event.time || ""))}</span></div>
      <div className="break-words text-xs font-medium uppercase text-muted-foreground">{String(event.component || "runtime")}</div>
      <div className="min-w-0"><div className="break-words font-medium">{String(event.message || event.event || "Runtime event")}</div><div className="mt-0.5 break-words text-xs text-muted-foreground">{String(event.event || event.name || event.kind || "")}</div></div>
      <div className="break-all text-xs text-muted-foreground md:text-right">{String(event.workspace_id || event.status || "")}</div>
    </button>
  )
}

function ExecutionRow({ execution, compact, selected, onClick }: { execution: ExecutionInfo; compact: boolean; selected: boolean; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={cn("grid w-full min-w-0 gap-1 border-b px-3 text-left last:border-b-0 hover:bg-muted/50 md:grid-cols-[7rem_7rem_minmax(0,1fr)_11rem] md:items-center md:gap-3", selected && "bg-muted/70", compact ? "py-2" : "py-3")}>
      <div className="flex items-center gap-2"><StatusDot status={execution.status} /><span className="text-xs tabular-nums text-muted-foreground">{formatTime(execution.started_at)}</span></div>
      <Badge variant={badgeForStatus(execution.status)} className="max-w-full">{execution.status}</Badge>
      <div className="min-w-0"><div className="break-all whitespace-normal font-mono text-[13px]">{execution.command}</div><div className="mt-0.5 break-all text-xs text-muted-foreground">{execution.cwd}</div></div>
      <div className="break-all text-xs text-muted-foreground md:text-right">{execution.workspace_id}</div>
    </button>
  )
}

function ToolRow({ record, compact, selected, onClick }: { record: ToolRecord; compact: boolean; selected: boolean; onClick: () => void }) {
  const event = record.latest
  const status = event.status || event.phase || "event"
  return (
    <button type="button" onClick={onClick} className={cn("grid w-full min-w-0 gap-1 border-b px-3 text-left last:border-b-0 hover:bg-muted/50 md:grid-cols-[7rem_7rem_minmax(0,1fr)_11rem] md:items-center md:gap-3", selected && "bg-muted/70", compact ? "py-2" : "py-3")}>
      <div className="flex items-center gap-2"><StatusDot status={status} /><span className="text-xs tabular-nums text-muted-foreground">{formatTime(event.timestamp)}</span></div>
      <Badge variant={badgeForStatus(status)} className={statusBadgeClass(status)}>{status}</Badge>
      <div className="min-w-0"><div className="break-words font-medium">{event.tool || event.method || "Tool call"}</div><div className="mt-0.5 break-all font-mono text-xs text-muted-foreground">{record.call_id}</div></div>
      <div className="break-all text-xs text-muted-foreground md:text-right">{event.workspace_id || event.source || ""}</div>
    </button>
  )
}

function DetailBody({ detail, executionEvents, fill = false }: { detail: ReturnType<typeof resolveSelected> | null; executionEvents: ExecutionFeedEvent[]; fill?: boolean }) {
  const [canonical, setCanonical] = useState<ToolCallDetail | ExecutionDetailSnapshot | null>(null)
  const [canonicalError, setCanonicalError] = useState("")
  const detailFeed = detail?.feed
  const detailID = detail?.value ? detail.feed === "tools" ? (detail.value as ToolRecord).call_id : detail.feed === "executions" ? (detail.value as ExecutionInfo).id : "" : ""
  const revision = detail?.value ? detail.feed === "tools" ? ((detail.value as ToolRecord).latest.sequence ?? 0) : detail.feed === "executions" ? executionEvents.filter((event) => event.execution_id === (detail.value as ExecutionInfo).id).reduce((latest, event) => Math.max(latest, event.sequence || 0), 0) : 0 : 0
  useEffect(() => {
    let active = true
    if (!detailID || !detailFeed || detailFeed === "runtime") return () => { active = false }
    const request = detailFeed === "tools" ? loadToolCallDetail(detailID) : loadExecutionDetail(detailID)
    void request.then((value) => { if (active) { setCanonical(value); setCanonicalError("") } }).catch((value) => { if (active) setCanonicalError(value instanceof Error ? value.message : String(value)) })
    return () => { active = false }
  }, [detailFeed, detailID, revision])
  if (!detail?.value) return <p className="text-sm text-muted-foreground">This event is no longer in the retained view.</p>
  const sections = buildDetailSections(detail, executionEvents, canonical, canonicalError)
  return (
    <Tabs defaultValue="overview" className={cn("gap-3", fill && "h-full min-h-0")}>
      <ScrollableTabsList className="shrink-0 justify-start" variant="line">
        <TabsTrigger className="flex-none px-2.5" value="overview">Overview</TabsTrigger>
        <TabsTrigger className="flex-none px-2.5" value="request">Request</TabsTrigger>
        <TabsTrigger className="flex-none px-2.5" value="response">Response</TabsTrigger>
        <TabsTrigger className="flex-none px-2.5" value="metadata">Metadata</TabsTrigger>
        <TabsTrigger className="flex-none px-2.5" value="raw">Raw</TabsTrigger>
      </ScrollableTabsList>
      <DetailTab value="overview" fill={fill}>{sections.overview}</DetailTab>
      <DetailTab value="request" fill={fill}><DetailSection value={sections.request} empty="No request payload is available for this event." /></DetailTab>
      <DetailTab value="response" fill={fill}><DetailSection value={sections.response} empty="No response payload is available for this event." /></DetailTab>
      <DetailTab value="metadata" fill={fill}><DetailSection value={sections.metadata} empty="No additional metadata is available." /></DetailTab>
      <DetailTab value="raw" fill={fill}><DetailSection value={sections.raw} empty="No safe raw projection is available." /></DetailTab>
    </Tabs>
  )
}

function DetailTab({ value, fill, children }: { value: string; fill: boolean; children: React.ReactNode }) {
  if (!fill) return <TabsContent value={value}>{children}</TabsContent>
  return (
    <TabsContent value={value} className="min-h-0 flex-1 overflow-hidden">
      <ScrollArea className="h-full min-h-0 min-w-0 max-w-full" scrollbars="both">
        <div className="min-h-full min-w-0 max-w-full pb-3 pr-2">{children}</div>
      </ScrollArea>
    </TabsContent>
  )
}

function buildDetailSections(detail: NonNullable<ReturnType<typeof resolveSelected>>, executionEvents: ExecutionFeedEvent[], canonical: ToolCallDetail | ExecutionDetailSnapshot | null = null, canonicalError = "") {
  if (detail.feed === "runtime") {
    const event = detail.value as LogEvent
    return {
      overview: <Overview values={[["Level", String(event.level || "—")], ["Component", String(event.component || "—")], ["Workspace", String(event.workspace_id || "—")], ["Time", formatTime(event.timestamp || String(event.time || ""))]]} />,
      request: valueOrNull((event as Record<string, unknown>).fields),
      response: compactObject({ message: event.message, error: event.error, status: event.status, duration_ms: event.duration_ms }),
      metadata: compactObject({ sequence: event.sequence, run_id: event.run_id, pid: event.pid, kind: event.kind, event: event.event || event.name, component: event.component, workspace_id: event.workspace_id, tool: event.tool, method: event.method, source: event.source, service_id: event.service_id, service_scope: event.service_scope }),
      raw: event,
    }
  }
  if (detail.feed === "tools") {
    const record = detail.value as ToolRecord
    const first = record.first as ActivityEvent & { raw?: unknown }
    const latest = record.latest as ActivityEvent & { raw?: unknown }
    const resolved = canonical && "kind" in canonical ? canonical as ToolCallDetail : null
    return {
      overview: <Overview values={[["Tool", latest.tool || "—"], ["Status", latest.status || latest.phase || "—"], ["Workspace", latest.workspace_id || "—"], ["Duration", latest.duration_ms ? latest.duration_ms + " ms" : "—"]]} />,
      request: resolved?.request ?? (canonicalError ? { error: canonicalError } : toolRequestView(first)),
      response: resolved?.error ?? resolved?.response ?? (canonicalError ? { error: canonicalError } : toolResponseView(latest)),
      metadata: compactObject({ call_id: record.call_id, kind: latest.kind, tool: latest.tool, method: latest.method, source: latest.source, workspace_id: latest.workspace_id, first_timestamp: first.timestamp, latest_timestamp: latest.timestamp, diagnostic: resolved?.diagnostic }),
      raw: resolved ?? record,
    }
  }
  const fallbackExecution = detail.value as ExecutionInfo & { requested_command?: string; effective_command?: string }
  const resolved = canonical && "execution" in canonical ? canonical as ExecutionDetailSnapshot : null
  const execution = resolved?.execution ?? fallbackExecution
  const related = executionEvents.filter((event) => event.execution_id === execution.id)
  const output = related.filter((event) => event.type === "output").map((event) => event.data || "").join("")
  const stdout = resolved?.stdout ?? output
  const stderr = resolved?.stderr ?? ""
  const response = stdout || stderr ? <div className="space-y-3">{stdout ? <div><div className="mb-1.5 text-xs font-medium text-muted-foreground">stdout</div><TextViewer value={stdout} maxHeight={null} /></div> : null}{stderr ? <div><div className="mb-1.5 text-xs font-medium text-muted-foreground">stderr</div><TextViewer value={stderr} maxHeight={null} /></div> : null}<JsonViewer value={compactObject({ status: execution.status, exit_code: execution.exit_code, timed_out: execution.timed_out, finished_at: execution.finished_at })} maxHeight={null} /></div> : canonicalError ? { error: canonicalError } : compactObject({ status: execution.status, exit_code: execution.exit_code, timed_out: execution.timed_out, finished_at: execution.finished_at })
  return {
    overview: <Overview values={[["Status", execution.status], ["Workspace", execution.workspace_id], ["Tool", execution.tool], ["Exit code", execution.exit_code == null ? "—" : String(execution.exit_code)]]} />,
    request: <div className="space-y-3"><TextViewer value={execution.command} maxHeight={null} /><DetailSection value={compactObject({ requested_command: execution.requested_command, effective_command: execution.effective_command, cwd: execution.cwd, source: execution.source })} empty="" /></div>,
    response,
    metadata: compactObject({ id: execution.id, workspace_id: execution.workspace_id, tool: execution.tool, source: execution.source, started_at: execution.started_at, finished_at: execution.finished_at }),
    raw: resolved ?? { execution, events: related },
  }
}

function DetailSection({ value, empty }: { value: React.ReactNode | unknown; empty: string }) {
  if (value == null || value === "") return empty ? <p className="py-3 text-sm text-muted-foreground">{empty}</p> : null
  if (isReactNode(value)) return value
  return <JsonViewer value={value} maxHeight={null} />
}

function isReactNode(value: unknown): value is React.ReactNode {
  return Boolean(value && typeof value === "object" && "$$typeof" in (value as Record<string, unknown>))
}

function valueOrNull(value: unknown) {
  if (value == null) return null
  if (Array.isArray(value) && value.length === 0) return null
  if (typeof value === "object" && !Array.isArray(value) && Object.keys(value as Record<string, unknown>).length === 0) return null
  return value
}

function compactObject(value: Record<string, unknown>) {
  const entries = Object.entries(value).filter(([, item]) => item !== undefined && item !== null && item !== "")
  return entries.length ? Object.fromEntries(entries) : null
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function toolRequestView(event: ActivityEvent & { raw?: unknown }) {
  const raw = asRecord(event.raw)
  if (!raw) return compactObject({ method: event.method, tool: event.tool, source: event.source, message: event.message })
  return valueOrNull(raw.request)
    || (valueOrNull(raw.params) ? compactObject({ method: raw.method ?? event.method, params: raw.params }) : null)
    || (valueOrNull(raw.arguments) ? compactObject({ tool: raw.tool ?? event.tool, arguments: raw.arguments }) : null)
    || compactObject({ method: event.method, tool: event.tool, source: event.source, message: event.message })
}

function toolResponseView(event: ActivityEvent & { raw?: unknown }) {
  const raw = asRecord(event.raw)
  return compactObject({
    status: raw?.status ?? event.status,
    result_type: raw?.result_type,
    result: raw?.result,
    error: raw?.error ?? (event.status && event.status !== "ok" ? event.message : undefined),
  }) || compactObject({ status: event.status || event.phase, message: event.message, duration_ms: event.duration_ms })
}

function Overview({ values }: { values: [string, string][] }) {
  return <div className="grid grid-cols-2 gap-2 md:grid-cols-1">{values.map(([label, value]) => <div key={label} className="min-w-0 rounded-lg border bg-muted/20 p-2.5 md:flex md:items-center md:justify-between md:gap-3"><div className="text-[11px] text-muted-foreground">{label}</div><div className="mt-1 break-all text-sm font-medium md:mt-0 md:text-right">{value}</div></div>)}</div>
}

function SettingRow({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return <div className="flex items-center justify-between gap-4"><div className="min-w-0"><div className="text-sm font-medium">{title}</div><div className="mt-0.5 text-xs text-muted-foreground">{description}</div></div><div className="shrink-0">{children}</div></div>
}

function StatusDot({ status }: { status: string }) {
  const normalized = status.toLowerCase()
  const className = normalized.includes("error") || normalized.includes("fail")
    ? "bg-destructive"
    : isSuccessfulStatus(normalized)
      ? "bg-emerald-500"
    : normalized.includes("running") || normalized.includes("start")
      ? "bg-primary"
      : normalized.includes("warn")
        ? "bg-amber-500"
        : "bg-muted-foreground/60"
  return <span className={cn("size-1.5 shrink-0 rounded-full", className)} aria-hidden />
}

function LoadingRows() {
  return <div className="space-y-px">{Array.from({ length: 6 }).map((_, index) => <div key={index} className="flex animate-pulse gap-3 border-b px-3 py-3 last:border-b-0"><div className="h-3 w-16 rounded bg-muted" /><div className="h-3 w-20 rounded bg-muted" /><div className="h-3 flex-1 rounded bg-muted" /></div>)}</div>
}

function LoadingShell() {
  return <main className="flex min-h-[var(--tg-viewport-stable-height,100dvh)] items-center justify-center bg-background p-6 text-foreground"><div className="text-center"><RefreshCwIcon className="mx-auto mb-3 size-5 animate-spin text-muted-foreground" /><p className="text-sm font-medium">Opening Logs</p><p className="mt-1 text-xs text-muted-foreground">Authenticating Telegram session…</p></div></main>
}

function Unavailable({ message }: { message: string }) {
  return <main className="flex min-h-[var(--tg-viewport-stable-height,100dvh)] items-center justify-center bg-background p-6 text-foreground"><div className="max-w-sm rounded-xl border bg-card p-5 text-center shadow-sm"><h1 className="text-base font-semibold">Logs unavailable</h1><p className="mt-2 text-sm text-muted-foreground">{message}</p></div></main>
}

function resolveSelected(selected: SelectedItem, runtimeEvents: LogEvent[], executions: ExecutionInfo[], executionEvents: ExecutionFeedEvent[], toolRecords: ToolRecord[]) {
  if (selected.feed === "runtime") return { feed: "runtime" as const, value: runtimeEvents.find((event) => runtimeKey(event) === selected.key) }
  if (selected.feed === "executions") return { feed: "executions" as const, value: executions.find((item) => item.id === selected.key), events: executionEvents.filter((event) => event.execution_id === selected.key) }
  return { feed: "tools" as const, value: toolRecords.find((item) => item.call_id === selected.key) }
}

function detailTitle(detail: ReturnType<typeof resolveSelected> | null) {
  if (!detail?.value) return "Event detail"
  if (detail.feed === "runtime") return String((detail.value as LogEvent).event || (detail.value as LogEvent).name || "Runtime event")
  if (detail.feed === "executions") return "Command execution"
  return String((detail.value as ToolRecord).latest.tool || "Tool call")
}

function detailDescription(detail: ReturnType<typeof resolveSelected> | null) {
  if (!detail?.value) return undefined
  if (detail.feed === "runtime") return "Safe runtime projection"
  if (detail.feed === "executions") return (detail.value as ExecutionInfo).id
  return (detail.value as ToolRecord).call_id
}

function runtimeKey(event: LogEvent) {
  return String(event.sequence || [event.run_id, event.timestamp || event.time, event.event || event.name].filter(Boolean).join(":"))
}

function filterRuntime(event: LogEvent, filter: FilterMode) {
  if (filter === "all") return true
  const level = String(event.level || "").toLowerCase()
  if (filter === "warn") return level === "warn" || level === "warning"
  if (filter === "error") return level === "error"
  if (filter === "success") return level === "info" || level === "debug"
  return true
}

function filterExecution(item: ExecutionInfo, filter: FilterMode) {
  if (filter === "all") return true
  const status = item.status.toLowerCase()
  if (filter === "error") return status === "failed" || status === "timed_out" || status === "error"
  if (filter === "success") return status === "success" || status === "completed" || status === "ok"
  return status === filter
}

function filterTool(record: ToolRecord, filter: FilterMode) {
  if (filter === "all") return true
  const status = String(record.latest.status || record.latest.phase || "").toLowerCase()
  if (filter === "running") return status === "running" || status === "start" || status === "progress"
  if (filter === "error") return status === "error" || status === "failed"
  if (filter === "success") return status === "ok" || status === "success" || status === "finish" || status === "completed"
  return status === filter
}

function runtimeHaystack(event: LogEvent) {
  return [event.event, event.name, event.message, event.component, event.workspace_id, event.status, event.error, event.tool, event.method, event.source].filter(Boolean).join(" ").toLowerCase()
}

function executionHaystack(item: ExecutionInfo) {
  return [item.command, item.cwd, item.workspace_id, item.tool, item.status, item.source, item.id].filter(Boolean).join(" ").toLowerCase()
}

function toolHaystack(record: ToolRecord) {
  const event = record.latest
  return [record.call_id, event.tool, event.method, event.workspace_id, event.status, event.source, event.message].filter(Boolean).join(" ").toLowerCase()
}

function searchPlaceholder(feed: MiniAppFeed) {
  if (feed === "executions") return "Search command, cwd, workspace…"
  if (feed === "tools") return "Search tool, call ID, workspace…"
  return "Search event, component, message…"
}

function badgeForStatus(status: string): "default" | "secondary" | "destructive" | "outline" {
  const value = status.toLowerCase()
  if (value.includes("error") || value.includes("fail") || value.includes("timed")) return "destructive"
  if (value.includes("running") || value.includes("start") || value.includes("progress")) return "secondary"
  return "outline"
}

function statusBadgeClass(status: string) {
  return isSuccessfulStatus(status.toLowerCase())
    ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
    : undefined
}

function isSuccessfulStatus(status: string) {
  return status === "ok" || status === "success" || status === "completed" || status === "finish"
}

function formatTime(value?: string) {
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(date)
}
