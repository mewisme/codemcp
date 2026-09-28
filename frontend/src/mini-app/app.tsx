import { useCallback, useEffect, useMemo, useState } from "react"
import type { FormEvent, ReactNode } from "react"
import { Activity, CircleDot, Filter, RefreshCw, Search, SlidersHorizontal, TerminalSquare } from "lucide-react"

import { JsonViewer } from "@/components/json-viewer"
import { PageEmpty, PageError } from "@/components/page-state"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { authenticateTelegramSession, loadLogsSnapshot, snapshotEvents, type LogEvent, type LogsSnapshot } from "@/mini-app/api"
import { initializeTelegramMiniApp } from "@/mini-app/telegram"

type ConnectionState = "authenticating" | "loading" | "ready" | "error"

export function MiniApp() {
  const [snapshot, setSnapshot] = useState<LogsSnapshot | null>(null)
  const [connection, setConnection] = useState<ConnectionState>("authenticating")
  const [level, setLevel] = useState("all")
  const [component, setComponent] = useState("")
  const [grep, setGrep] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [selected, setSelected] = useState<LogEvent | null>(null)

  const load = useCallback(async (filters?: { level?: string; component?: string; grep?: string }) => {
    setBusy(true)
    try {
      const next = await loadLogsSnapshot({
        tail: 200,
        level: filters?.level && filters.level !== "all" ? filters.level : undefined,
        components: filters?.component?.trim() || undefined,
        grep: filters?.grep?.trim() || undefined,
      })
      setSnapshot(next)
      setConnection("ready")
      setError("")
    } catch (value) {
      setConnection("error")
      setError(errorText(value))
    } finally {
      setBusy(false)
    }
  }, [])

  useEffect(() => {
    let active = true
    let disposeTelegram: (() => void) | undefined
    void (async () => {
      try {
        const telegram = initializeTelegramMiniApp()
        disposeTelegram = telegram.dispose
        await authenticateTelegramSession(telegram.webApp.initData)
        if (!active) return
        setConnection("loading")
        const next = await loadLogsSnapshot({ tail: 200 })
        if (!active) return
        setSnapshot(next)
        setConnection("ready")
      } catch (value) {
        if (!active) return
        setConnection("error")
        setError(errorText(value))
      }
    })()
    return () => {
      active = false
      disposeTelegram?.()
    }
  }, [])

  const events = useMemo(() => snapshotEvents(snapshot).slice().reverse(), [snapshot])
  const total = snapshot?.total ?? snapshot?.Total ?? events.length
  const truncated = snapshot?.truncated ?? snapshot?.Truncated ?? false
  const session = snapshot?.session ?? snapshot?.Session ?? ""

  function submit(event: FormEvent) {
    event.preventDefault()
    void load({ level, component, grep })
  }

  if (connection === "authenticating" || connection === "loading") {
    return <MiniAppShell><LoadingState /></MiniAppShell>
  }

  return (
    <MiniAppShell>
      <header className="sticky top-0 z-20 border-b bg-background/92 backdrop-blur-xl supports-[backdrop-filter]:bg-background/80">
        <div className="mx-auto flex h-14 max-w-3xl items-center gap-3 px-3 sm:px-4">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground"><TerminalSquare className="size-4" /></div>
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2"><h1 className="truncate text-[15px] font-semibold tracking-tight">Logs</h1><Badge variant="outline" className="h-5 gap-1 px-1.5 text-[10px] font-medium"><CircleDot className="size-2.5 fill-current" />Snapshot</Badge></div>
            <p className="truncate text-[11px] text-muted-foreground">CodeMCP Mini App</p>
          </div>
          <Button aria-label="Refresh logs" disabled={busy} size="icon-sm" variant="ghost" onClick={() => void load({ level, component, grep })}><RefreshCw className={busy ? "animate-spin" : ""} /></Button>
        </div>
      </header>

      <main className="mx-auto max-w-3xl space-y-3 px-3 py-3 sm:px-4 sm:py-4">
        {error ? <div className="space-y-2"><PageError message={error} title="Logs unavailable" /><Button size="sm" variant="outline" onClick={() => void load({ level, component, grep })}>Retry</Button></div> : null}

        <section className="grid grid-cols-3 overflow-hidden rounded-xl border bg-card shadow-xs">
          <Metric label="Matched" value={String(total)} />
          <Metric label="Loaded" value={String(events.length)} divider />
          <Metric label="Session" value={session ? compact(session, 10) : "current"} mono divider />
        </section>

        <Card className="gap-0 py-0 shadow-xs">
          <CardContent className="space-y-2.5 p-3">
            <form className="space-y-2.5" onSubmit={submit}>
              <div className="relative">
                <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input aria-label="Search logs" className="h-10 rounded-xl bg-muted/45 pl-9 pr-20 shadow-none" placeholder="Search messages, tools, events…" value={grep} onChange={(event) => setGrep(event.target.value)} />
                <Button className="absolute right-1 top-1 h-8 rounded-lg px-3" disabled={busy} size="sm" type="submit">Search</Button>
              </div>
              <div className="grid grid-cols-[8.5rem_1fr_auto] gap-2">
                <Select value={level} onValueChange={setLevel}>
                  <SelectTrigger aria-label="Log level" className="h-9 rounded-lg"><Filter className="size-3.5" /><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All levels</SelectItem>
                    <SelectItem value="debug">Debug</SelectItem>
                    <SelectItem value="info">Info</SelectItem>
                    <SelectItem value="warn">Warn</SelectItem>
                    <SelectItem value="error">Error</SelectItem>
                  </SelectContent>
                </Select>
                <div className="relative"><SlidersHorizontal className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input aria-label="Component filter" className="h-9 rounded-lg pl-9" placeholder="Component" value={component} onChange={(event) => setComponent(event.target.value)} /></div>
                <Button aria-label="Apply filters" disabled={busy} size="icon-sm" type="submit" variant="outline"><Filter className="size-3.5" /></Button>
              </div>
            </form>
          </CardContent>
        </Card>

        <div className="flex min-h-5 items-center justify-between gap-3 px-0.5 text-[11px] text-muted-foreground"><span>{events.length ? `${events.length} newest events` : "No events loaded"}</span>{truncated ? <span>Bounded snapshot</span> : <span>Current snapshot</span>}</div>

        {events.length === 0 ? (
          <PageEmpty icon={Activity} title="No matching log events" description="Adjust the filters or refresh after runtime activity." />
        ) : (
          <section className="overflow-hidden rounded-xl border bg-card shadow-xs">
            {events.map((event, index) => <div key={`${String(event.sequence ?? "event")}-${index}`}><LogEventRow event={event} onOpen={() => setSelected(event)} />{index < events.length - 1 ? <Separator /> : null}</div>)}
          </section>
        )}
      </main>

      <ResponsiveDialog open={selected !== null} onOpenChange={(open) => { if (!open) setSelected(null) }} title={selected ? eventTitle(selected) : "Log event"} description={selected ? eventSubtitle(selected) : undefined} wide>
        {selected ? <div className="space-y-4"><div className="flex flex-wrap gap-2"><LevelBadge level={String(selected.level ?? "info")} />{selected.component ? <Badge variant="outline">{String(selected.component)}</Badge> : null}{selected.workspace_id ? <Badge variant="outline">{compact(String(selected.workspace_id), 24)}</Badge> : null}</div><JsonViewer value={selected} maxHeight="65vh" /></div> : null}
      </ResponsiveDialog>
    </MiniAppShell>
  )
}

function MiniAppShell({ children }: { children: ReactNode }) {
  return <div className="min-h-[100dvh] bg-background pb-[max(1rem,env(safe-area-inset-bottom))] pt-[max(0px,env(safe-area-inset-top))] text-foreground">{children}</div>
}

function LoadingState() {
  return <div className="mx-auto max-w-3xl space-y-3 px-3 py-3 sm:px-4"><div className="flex h-11 items-center gap-3"><Skeleton className="size-8 rounded-lg" /><div className="space-y-1.5"><Skeleton className="h-3.5 w-20" /><Skeleton className="h-2.5 w-28" /></div></div><Skeleton className="h-16 w-full rounded-xl" /><Skeleton className="h-24 w-full rounded-xl" /><div className="space-y-0 overflow-hidden rounded-xl border">{Array.from({ length: 6 }).map((_, index) => <div key={index}><div className="space-y-2 p-3"><Skeleton className="h-3 w-24" /><Skeleton className="h-4 w-4/5" /><Skeleton className="h-2.5 w-2/5" /></div>{index < 5 ? <Separator /> : null}</div>)}</div></div>
}

function Metric({ label, value, mono = false, divider = false }: { label: string; value: string; mono?: boolean; divider?: boolean }) {
  return <div className={`min-w-0 px-2 py-3 text-center ${divider ? "border-l" : ""}`}><div className={`truncate text-[13px] font-semibold ${mono ? "font-mono text-[11px]" : ""}`}>{value}</div><div className="mt-0.5 text-[10px] text-muted-foreground">{label}</div></div>
}

function LogEventRow({ event, onOpen }: { event: LogEvent; onOpen: () => void }) {
  const message = String(event.message ?? event.name ?? "runtime event")
  const level = String(event.level ?? "info").toLowerCase()
  return (
    <button className="group w-full min-w-0 px-3 py-3 text-left transition-colors hover:bg-accent/45 active:bg-accent/60 sm:px-4" type="button" onClick={onOpen}>
      <div className="flex min-w-0 items-start gap-2.5">
        <span className={`mt-1.5 size-2 shrink-0 rounded-full ${levelDotClass(level)}`} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2 text-[11px] text-muted-foreground"><span className="shrink-0 tabular-nums">{formatTimestamp(event.timestamp)}</span>{event.component ? <><span>·</span><span className="truncate font-medium">{String(event.component)}</span></> : null}{event.sequence !== undefined ? <span className="ml-auto shrink-0 font-mono text-[10px]">#{event.sequence}</span> : null}</div>
          <p className="mt-1 line-clamp-2 break-words text-[13px] font-medium leading-[1.35rem] text-foreground">{message}</p>
          {(event.tool || event.workspace_id || event.status) ? <div className="mt-1.5 flex min-w-0 items-center gap-2 text-[10px] text-muted-foreground">{event.tool ? <span className="truncate rounded bg-muted px-1.5 py-0.5 font-mono">{String(event.tool)}</span> : null}{event.status ? <span className="shrink-0">{String(event.status)}</span> : null}{event.workspace_id ? <span className="truncate font-mono">{compact(String(event.workspace_id), 22)}</span> : null}</div> : null}
        </div>
      </div>
    </button>
  )
}

function LevelBadge({ level }: { level: string }) {
  const normalized = level.toLowerCase()
  const variant = normalized === "error" ? "destructive" : normalized === "warn" ? "secondary" : "outline"
  return <Badge variant={variant}>{normalized}</Badge>
}

function eventTitle(event: LogEvent) { return String(event.name ?? event.message ?? "Runtime event") }
function eventSubtitle(event: LogEvent) { return [event.timestamp, event.component, event.sequence !== undefined ? `#${event.sequence}` : ""].filter(Boolean).map(String).join(" · ") }
function compact(value: string, limit: number) { return value.length > limit ? `${value.slice(0, Math.max(1, limit - 1))}…` : value }
function formatTimestamp(value: unknown) {
  if (!value) return ""
  const date = new Date(String(value))
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })
}
function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }
function levelDotClass(level: string) {
  if (level === "error") return "bg-destructive"
  if (level === "warn" || level === "warning") return "bg-amber-500"
  if (level === "debug") return "bg-muted-foreground/50"
  return "bg-emerald-500"
}
