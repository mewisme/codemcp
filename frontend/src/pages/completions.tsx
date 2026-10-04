import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { CircleCheckBig, CircleDot, RefreshCw, Search } from "lucide-react"
import { DetailRow } from "@/components/detail-row"
import { JsonViewer } from "@/components/json-viewer"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { SemanticStatusBadge } from "@/components/semantic-status-badge"
import { TruncatedText } from "@/components/truncated-text"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Item,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemHeader,
  ItemTitle,
} from "@/components/ui/item"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { streamCompletions } from "@/lib/completion-stream"
import { adminApi, type CompletionRecord } from "@/lib/api"

const reconnectDelay = 1000
const statuses = ["completed", "partial", "blocked", "cancelled"]

export function CompletionsPage() {
  const [items, setItems] = useState<CompletionRecord[]>([])
  const [selected, setSelected] = useState<CompletionRecord | null>(null)
  const [status, setStatus] = useState("all")
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [connected, setConnected] = useState(false)
  const [doctor, setDoctor] = useState<Record<string, unknown> | null>(null)
  const [error, setError] = useState("")
  const retryTimer = useRef<number | null>(null)

  const load = useCallback(async (refresh = false) => {
    if (refresh) setRefreshing(true)
    try {
      const [records, health] = await Promise.all([
        adminApi.completions("", 100),
        adminApi.completionDoctor(),
      ])
      setItems(records)
      setSelected((current) =>
        current
          ? (records.find((record) => record.id === current.id) ?? current)
          : current
      )
      setDoctor(health)
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    async function connect() {
      try {
        await streamCompletions(
          controller.signal,
          {
            onReady: (snapshot) => {
              setItems(snapshot.records)
              setSelected((current) =>
                current
                  ? (snapshot.records.find(
                      (record) => record.id === current.id
                    ) ?? current)
                  : current
              )
              setConnected(true)
              setLoading(false)
              setError("")
            },
            onEvent: (event) => {
              setConnected(true)
              setItems((records) => upsertCompletion(records, event.record))
              setSelected((record) =>
                record?.id === event.record.id ? event.record : record
              )
            },
          },
          "",
          100
        )
      } catch (value) {
        if (controller.signal.aborted || stopped) return
        setConnected(false)
        setLoading(false)
        setError(errorText(value))
        retryTimer.current = window.setTimeout(
          () => void connect(),
          reconnectDelay
        )
      }
    }
    void adminApi
      .completionDoctor()
      .then((health) => setDoctor(health))
      .catch(() => undefined)
    void connect()
    return () => {
      stopped = true
      controller.abort()
      if (retryTimer.current !== null) window.clearTimeout(retryTimer.current)
    }
  }, [])

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return [...items].reverse().filter((record) => {
      if (status !== "all" && record.status !== status) return false
      if (!needle) return true
      return [
        record.id,
        record.agent_id,
        record.workspace_id,
        record.status,
        record.title,
        record.summary,
        record.source,
      ].some((value) => value?.toLowerCase().includes(needle))
    })
  }, [items, query, status])

  async function openCompletion(record: CompletionRecord) {
    try {
      setSelected(await adminApi.completion(record.id))
      setError("")
    } catch (value) {
      setError(errorText(value))
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Agent completions"
        description="Read-only durable completion history accepted by this runtime."
        actions={
          <>
            <Badge variant={connected ? "secondary" : "outline"}>
              <CircleDot className="size-3" />
              {connected ? "Live" : "Reconnecting"}
            </Badge>
            <Button
              disabled={refreshing}
              size="sm"
              variant="outline"
              onClick={() => void load(true)}
            >
              <RefreshCw className={refreshing ? "animate-spin" : ""} />
              Refresh
            </Button>
          </>
        }
      />
      <PageError message={error} />
      {doctor ? (
        <div className="rounded-xl border p-4">
          <div className="mb-2 flex items-center gap-2">
            <div className="font-medium">Completion health</div>
            <Badge variant="outline">doctor</Badge>
          </div>
          <JsonViewer maxHeight="12rem" value={doctor} />
        </div>
      ) : null}
      <div className="flex flex-col gap-2 lg:flex-row">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-9"
            placeholder="Search completion, workspace, agent, title..."
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <Select value={status} onValueChange={setStatus}>
          <SelectTrigger className="w-full lg:w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All statuses</SelectItem>
            {statuses.map((value) => (
              <SelectItem key={value} value={value}>
                {value}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {loading ? (
        <PageLoading rows={6} />
      ) : filtered.length === 0 ? (
        <PageEmpty
          icon={CircleCheckBig}
          title="No matching completions"
          description={
            items.length
              ? "Adjust the search or status filter."
              : "Accepted agent completions will appear here."
          }
        />
      ) : (
        <ItemGroup>
          {filtered.map((record) => (
            <Item
              interactive
              selected={selected?.id === record.id}
              key={record.id}
              role="button"
              tabIndex={0}
              variant="outline"
              onClick={() => void openCompletion(record)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ")
                  void openCompletion(record)
              }}
            >
              <ItemContent className="min-w-0">
                <ItemHeader>
                  <ItemTitle className="min-w-0">
                    <TruncatedText lines={1}>{record.title}</TruncatedText>
                  </ItemTitle>
                  <SemanticStatusBadge status={record.status} />
                </ItemHeader>
                <ItemDescription>
                  <TruncatedText lines={1}>
                    {record.summary || "No summary"}
                  </TruncatedText>
                </ItemDescription>
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span className="font-mono">{record.id}</span>
                  <span className="font-mono">{record.workspace_id}</span>
                  <span>{formatDateTime(record.created_at)}</span>
                </div>
              </ItemContent>
            </Item>
          ))}
        </ItemGroup>
      )}
      {selected ? (
        <CompletionDetail
          record={selected}
          onOpenChange={(open) => {
            if (!open) setSelected(null)
          }}
        />
      ) : null}
    </div>
  )
}

function CompletionDetail({
  record,
  onOpenChange,
}: {
  record: CompletionRecord
  onOpenChange: (open: boolean) => void
}) {
  return (
    <ResponsiveDialog
      open
      onOpenChange={onOpenChange}
      title={record.title}
      description={"Agent completion · " + record.id}
    >
      <div className="mb-3">
        <SemanticStatusBadge status={record.status} />
      </div>
      <div className="divide-y">
        <DetailRow label="Workspace" value={record.workspace_id} mono />
        <DetailRow label="Completion" value={record.id} mono />
        <DetailRow label="Agent" value={record.agent_id} mono />
        <DetailRow label="Sequence" value={record.sequence} mono />
        <DetailRow label="Source" value={record.source || "-"} mono />
        <DetailRow label="Created" value={formatDateTime(record.created_at)} />
        <DetailRow
          label="Supersedes"
          value={record.supersedes_id || "-"}
          mono
        />
        <DetailRow label="Summary" value={record.summary || "-"} />
      </div>
    </ResponsiveDialog>
  )
}

function upsertCompletion(
  records: CompletionRecord[],
  record: CompletionRecord
) {
  const next = [...records.filter((item) => item.id !== record.id), record]
  next.sort((left, right) => left.sequence - right.sequence)
  return next.slice(-100)
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
