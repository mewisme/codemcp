import { useEffect, useMemo, useState } from "react"
import { RefreshCw, Trash2 } from "lucide-react"
import { JsonViewer } from "@/components/json-viewer"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  adminApi,
  type LogEvent,
  type LogsInfo,
  type LogsSnapshot,
} from "@/lib/api"

export function LogsPage() {
  const [snapshot, setSnapshot] = useState<LogsSnapshot | null>(null)
  const [info, setInfo] = useState<LogsInfo | null>(null)
  const [level, setLevel] = useState("all")
  const [grep, setGrep] = useState("")
  const [workspace, setWorkspace] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [viewCleared, setViewCleared] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [error, setError] = useState("")

  async function load() {
    setBusy(true)
    try {
      const [nextSnapshot, nextInfo] = await Promise.all([
        adminApi.followLogs({
          tail: 200,
          level: level === "all" ? undefined : level,
          grep: grep.trim() || undefined,
          workspace: workspace.trim() || undefined,
        }),
        adminApi.logsInfo(),
      ])
      setSnapshot(nextSnapshot)
      setInfo(nextInfo)
      setViewCleared(false)
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy(false)
      setLoading(false)
    }
  }

  useEffect(() => {
    let active = true
    void Promise.all([adminApi.followLogs({ tail: 200 }), adminApi.logsInfo()])
      .then(([nextSnapshot, nextInfo]) => {
        if (!active) return
        setSnapshot(nextSnapshot)
        setInfo(nextInfo)
        setError("")
      })
      .catch((value) => {
        if (active) setError(errorText(value))
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [])

  async function clear() {
    setBusy(true)
    try {
      await adminApi.clearLogs()
      setDeleteOpen(false)
      await load()
    } catch (value) {
      setError(errorText(value))
      setBusy(false)
    }
  }

  const events = useMemo(
    () => (viewCleared ? [] : (snapshot?.events ?? snapshot?.Events ?? [])),
    [snapshot, viewCleared]
  )
  const path = info?.path ?? info?.Path ?? "-"
  const files = info?.files ?? info?.Files ?? 0
  const bytes = info?.bytes ?? info?.Bytes ?? 0
  if (loading) return <PageLoading rows={6} />
  return (
    <div className="space-y-6">
      <PageHeader
        title="Logs"
        description="Read and clear the canonical runtime event journal."
        actions={
          <>
            <Button
              disabled={busy}
              size="sm"
              variant="outline"
              onClick={() => void load()}
            >
              <RefreshCw className={busy ? "animate-spin" : ""} />
              Refresh
            </Button>
            <Button
              disabled={busy || events.length === 0}
              size="sm"
              variant="outline"
              onClick={() => setViewCleared(true)}
            >
              <Trash2 />
              Clear view
            </Button>
            <Button
              disabled={busy}
              size="sm"
              variant="destructive"
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 />
              Delete journal
            </Button>
          </>
        }
      />
      <PageError message={error} />
      <Card>
        <CardHeader>
          <CardTitle>Journal</CardTitle>
          <CardDescription className="font-mono break-all">
            {path}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          <Badge variant="outline">{files} files</Badge>
          <Badge variant="outline">{bytes} bytes</Badge>
          <Badge variant="outline">
            {snapshot?.total ?? snapshot?.Total ?? events.length} matched
          </Badge>
        </CardContent>
      </Card>
      <Card>
        <CardContent className="grid gap-3 p-4 md:grid-cols-[12rem_1fr_1fr_auto]">
          <Select value={level} onValueChange={setLevel}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All levels</SelectItem>
              <SelectItem value="debug">Debug</SelectItem>
              <SelectItem value="info">Info</SelectItem>
              <SelectItem value="warn">Warn</SelectItem>
              <SelectItem value="error">Error</SelectItem>
            </SelectContent>
          </Select>
          <Input
            aria-label="Workspace filter"
            placeholder="Workspace ID or path"
            value={workspace}
            onChange={(event) => setWorkspace(event.target.value)}
          />
          <Input
            aria-label="Search logs"
            placeholder="Search journal"
            value={grep}
            onChange={(event) => setGrep(event.target.value)}
          />
          <Button disabled={busy} onClick={() => void load()}>
            Apply
          </Button>
        </CardContent>
      </Card>
      <AlertDialog
        open={deleteOpen}
        onOpenChange={(open) => {
          if (!busy) setDeleteOpen(open)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete runtime log journal?</AlertDialogTitle>
            <AlertDialogDescription>
              Current and rotated runtime logs will be permanently removed. This
              cannot be undone. Clearing this browser view does not delete
              retained logs.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={busy}
              variant="destructive"
              onClick={() => void clear()}
            >
              {busy ? "Deleting..." : "Delete logs permanently"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {events.length === 0 ? (
        <PageEmpty
          title="No matching log events"
          description="Adjust the filters or refresh after runtime activity."
        />
      ) : (
        <div className="space-y-3">
          {events
            .slice()
            .reverse()
            .map((event, index) => (
              <LogCard
                key={`${String(event.sequence ?? "event")}-${index}`}
                event={event}
              />
            ))}
        </div>
      )}
    </div>
  )
}

function LogCard({ event }: { event: LogEvent }) {
  const title = String(event.name ?? event.message ?? "runtime event")
  const level = String(event.level ?? "info")
  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <CardTitle className="text-sm">{title}</CardTitle>
            <CardDescription>{String(event.timestamp ?? "")}</CardDescription>
          </div>
          <div className="flex gap-2">
            <Badge variant="outline">{level}</Badge>
            {event.component ? (
              <Badge variant="outline">{String(event.component)}</Badge>
            ) : null}
          </div>
        </div>
      </CardHeader>
      <CardContent>
        <JsonViewer value={event} maxHeight="16rem" />
      </CardContent>
    </Card>
  )
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
