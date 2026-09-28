import { useCallback, useEffect, useMemo, useState } from "react"
import type { FormEvent, ReactNode } from "react"
import { Activity, Filter, RefreshCw, Search, TerminalSquare } from "lucide-react"

import { JsonViewer } from "@/components/json-viewer"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { authenticateTelegramSession, loadLogsSnapshot, snapshotEvents, type LogEvent, type LogsSnapshot } from "@/logs/api"
import { initializeTelegramLogsApp } from "@/logs/telegram"

type ConnectionState = "authenticating" | "loading" | "ready" | "error"

export function LogsMiniApp() {
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
        const telegram = initializeTelegramLogsApp()
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
    return <LogsShell><PageLoading rows={7} /></LogsShell>
  }

  return (
    <LogsShell>
      <header className="sticky top-0 z-20 -mx-3 border-b bg-background/90 px-3 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/75 sm:-mx-5 sm:px-5">
        <div className="mx-auto flex max-w-5xl items-center gap-3">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-xl border bg-card shadow-sm"><TerminalSquare className="size-4" /></div>
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-base font-semibold tracking-tight">CodeMCP Logs</h1>
            <p className="truncate text-xs text-muted-foreground">Read-only runtime journal</p>
          </div>
          <Badge variant={connection === "ready" ? "secondary" : "outline"} className="shrink-0"><Activity className="size-3" />{connection === "ready" ? "Live snapshot" : "Offline"}</Badge>
          <Button aria-label="Refresh logs" disabled={busy} size="icon-sm" variant="outline" onClick={() => void load({ level, component, grep })}><RefreshCw className={busy ? "animate-spin" : ""} /></Button>
        </div>
      </header>

      <main className="mx-auto max-w-5xl space-y-3 py-4 sm:space-y-4">
        <PageError message={error} title="Logs unavailable" />

        <Card className="gap-0 overflow-hidden py-0">
          <CardContent className="grid grid-cols-3 divide-x p-0 text-center">
            <Metric label="Matched" value={String(total)} />
            <Metric label="Loaded" value={String(events.length)} />
            <Metric label="Session" value={session ? compact(session, 12) : "current"} mono />
          </CardContent>
        </Card>

        <Card>
          <CardContent className="p-3 sm:p-4">
            <form className="grid gap-2 sm:grid-cols-[10rem_1fr_1fr_auto]" onSubmit={submit}>
              <Select value={level} onValueChange={setLevel}>
                <SelectTrigger aria-label="Log level"><Filter className="size-4" /><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All levels</SelectItem>
                  <SelectItem value="debug">Debug</SelectItem>
                  <SelectItem value="info">Info</SelectItem>
                  <SelectItem value="warn">Warn</SelectItem>
                  <SelectItem value="error">Error</SelectItem>
                </SelectContent>
              </Select>
              <Input aria-label="Component filter" placeholder="Component" value={component} onChange={(event) => setComponent(event.target.value)} />
              <div className="relative"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input aria-label="Search logs" className="pl-9" placeholder="Search logs" value={grep} onChange={(event) => setGrep(event.target.value)} /></div>
              <Button disabled={busy} type="submit">Apply</Button>
            </form>
          </CardContent>
        </Card>

        {truncated ? <p className="px-1 text-xs text-muted-foreground">Showing a bounded snapshot. Refine filters to inspect older matching events.</p> : null}

        {events.length === 0 ? (
          <PageEmpty icon={Activity} title="No matching log events" description="Adjust the filters or refresh after runtime activity." />
        ) : (
          <div className="space-y-2">
            {events.map((event, index) => <LogEventCard key={`${String(event.sequence ?? "event")}-${index}`} event={event} onOpen={() => setSelected(event)} />)}
          </div>
        )}
      </main>

      <ResponsiveDialog open={selected !== null} onOpenChange={(open) => { if (!open) setSelected(null) }} title={selected ? eventTitle(selected) : "Log event"} description={selected ? eventSubtitle(selected) : undefined} wide>
        {selected ? <div className="space-y-4"><div className="flex flex-wrap gap-2"><LevelBadge level={String(selected.level ?? "info")} />{selected.component ? <Badge variant="outline">{String(selected.component)}</Badge> : null}{selected.workspace_id ? <Badge variant="outline">{compact(String(selected.workspace_id), 24)}</Badge> : null}</div><JsonViewer value={selected} maxHeight="65vh" /></div> : null}
      </ResponsiveDialog>
    </LogsShell>
  )
}

function LogsShell({ children }: { children: ReactNode }) {
  return <div className="min-h-[100dvh] bg-muted/20 px-3 pb-[max(1rem,env(safe-area-inset-bottom))] pt-[max(0px,env(safe-area-inset-top))] text-foreground sm:px-5">{children}</div>
}

function Metric({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="min-w-0 px-2 py-3"><div className={`truncate text-sm font-semibold ${mono ? "font-mono" : ""}`}>{value}</div><div className="mt-0.5 text-[11px] text-muted-foreground">{label}</div></div>
}

function LogEventCard({ event, onOpen }: { event: LogEvent; onOpen: () => void }) {
  const message = String(event.message ?? event.name ?? "runtime event")
  return (
    <Card className="gap-0 py-0 transition-colors hover:bg-accent/40">
      <button className="w-full min-w-0 p-3 text-left sm:p-4" type="button" onClick={onOpen}>
        <div className="flex min-w-0 items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 flex-wrap items-center gap-2"><LevelBadge level={String(event.level ?? "info")} />{event.component ? <span className="truncate text-xs font-medium text-muted-foreground">{String(event.component)}</span> : null}</div>
            <p className="mt-2 line-clamp-2 break-words text-sm font-medium leading-5">{message}</p>
            <div className="mt-2 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground"><span>{formatTimestamp(event.timestamp)}</span>{event.tool ? <span className="truncate font-mono">{String(event.tool)}</span> : null}{event.workspace_id ? <span className="truncate font-mono">{compact(String(event.workspace_id), 26)}</span> : null}</div>
          </div>
          {event.sequence !== undefined ? <span className="shrink-0 font-mono text-[10px] text-muted-foreground">#{event.sequence}</span> : null}
        </div>
      </button>
    </Card>
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
