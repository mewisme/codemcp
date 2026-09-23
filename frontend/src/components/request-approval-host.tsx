import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ShieldCheck } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { DetailRow } from "@/components/detail-row"
import { JsonViewer } from "@/components/json-viewer"
import { adminApi, type ApprovalRequest } from "@/lib/api"
import { streamApprovals } from "@/lib/approval-stream"

const reconnectDelay = 1000

export function RequestApprovalHost() {
  const [requests, setRequests] = useState<ApprovalRequest[]>([])
  const [selected, setSelected] = useState<ApprovalRequest | null>(null)
  const [busy, setBusy] = useState<"approve" | "deny" | "">("")
  const [error, setError] = useState("")
  const [now, setNow] = useState(() => Date.now())
  const retryTimer = useRef<number | null>(null)
  const notified = useRef(new Set<string>())

  const applyRequests = useCallback((next: ApprovalRequest[]) => {
    setRequests(next)
    setSelected((current) => {
      const currentID = current?.id
      if (currentID)
        return next.find((item) => item.id === currentID) ?? next[0] ?? null
      return next[0] ?? null
    })
  }, [])

  const refresh = useCallback(async () => {
    try {
      const next = await adminApi.approvalRequests("pending")
      applyRequests(next)
      setError("")
    } catch (value) {
      setError(errorText(value))
    }
  }, [applyRequests])

  const focusRequest = useCallback(async (id: string) => {
    try {
      const detail = await adminApi.approvalRequest(id)
      if (detail.status !== "pending") return
      setRequests((current) => current.some((item) => item.id === detail.id) ? current.map((item) => item.id === detail.id ? detail : item) : [detail, ...current])
      setSelected(detail)
    } catch {
      await refresh()
    }
  }, [refresh])

  useEffect(() => {
    let active = true
    void adminApi
      .approvalRequests("pending")
      .then((next) => {
        if (active) applyRequests(next)
      })
      .catch((value) => {
        if (active) setError(errorText(value))
      })
    return () => {
      active = false
    }
  }, [applyRequests])
  useEffect(() => {
    if (!selected?.id) return
    let active = true
    void adminApi
      .approvalRequest(selected.id)
      .then((detail) => {
        if (active && detail.status === "pending") setSelected(detail)
      })
      .catch(() => {
        if (active) void refresh()
      })
    return () => {
      active = false
    }
  }, [refresh, selected?.id])
  useEffect(() => {
    const timer = window.setInterval(() => {
      const nextNow = Date.now()
      setNow(nextNow)
      const expiry = selected
        ? new Date(selected.expires_at).getTime()
        : Number.NaN
      if (Number.isFinite(expiry) && expiry <= nextNow) void refresh()
    }, 1000)
    return () => window.clearInterval(timer)
  }, [refresh, selected])
  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    async function connect() {
      try {
        await streamApprovals(controller.signal, {
          onReady: () => void refresh(),
          onEvent: (event) => {
            if (event.name === "approval.requested" && !notified.current.has(event.request_id)) {
              notified.current.add(event.request_id)
              toast.warning("Control approval requested", {
                description: `${event.title || event.target_tool} · ${event.workspace_id}`,
                duration: 10000,
                action: { label: "Review", onClick: () => void focusRequest(event.request_id) },
              })
            }
            void refresh()
          },
        })
      } catch (value) {
        if (controller.signal.aborted || stopped) return
        setError(errorText(value))
        retryTimer.current = window.setTimeout(
          () => void connect(),
          reconnectDelay
        )
      }
    }
    void connect()
    return () => {
      stopped = true
      controller.abort()
      if (retryTimer.current !== null) window.clearTimeout(retryTimer.current)
    }
  }, [focusRequest, refresh])

  const position = useMemo(
    () =>
      selected ? requests.findIndex((item) => item.id === selected.id) + 1 : 0,
    [requests, selected]
  )
  const remaining = selected ? remainingSeconds(selected.expires_at, now) : 0

  async function resolve(action: "approve" | "deny") {
    if (!selected || busy) return
    setBusy(action)
    setError("")
    try {
      if (action === "approve") await adminApi.approveRequest(selected.id)
      else await adminApi.denyRequest(selected.id)
      await refresh()
    } catch (value) {
      setError(errorText(value))
      await refresh()
    } finally {
      setBusy("")
    }
  }

  if (!selected) return null
  return (
    <ResponsiveDialog
      open
      onOpenChange={(open) => {
        if (!open && !busy)
          setSelected(requests.length > 1 ? requests[1] : null)
      }}
      title={selected.title || `Allow ${selected.target_tool}?`}
      description={`Control approval request · ${position} of ${requests.length}`}
      footer={
        <>
          <Button
            disabled={Boolean(busy)}
            variant="outline"
            onClick={() => void resolve("deny")}
          >
            {busy === "deny" ? "Denying..." : "Deny"}
          </Button>
          <Button
            disabled={Boolean(busy) || remaining <= 0}
            onClick={() => void resolve("approve")}
          >
            <ShieldCheck />
            {busy === "approve" ? "Approving..." : "Approve"}
          </Button>
        </>
      }
    >
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Badge variant={remaining <= 10 ? "destructive" : "secondary"}>
          {remaining}s remaining
        </Badge>
        <Badge variant="outline">{selected.status}</Badge>
      </div>
      {error ? (
        <div className="mb-3 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
          {error}
        </div>
      ) : null}
      <Tabs defaultValue="overview">
        <TabsList className="w-full">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="details">Details</TabsTrigger>
        </TabsList>
        <TabsContent className="mt-4 divide-y" value="overview">
          <DetailRow label="Workspace" value={selected.workspace_id} mono />
          <DetailRow label="Request" value={selected.id} mono />
          <DetailRow label="Tool" value={selected.target_tool} mono />
          <DetailRow label="Source" value={selected.source || "-"} mono />
          <DetailRow
            label="Session"
            value={selected.session_hash || "-"}
            mono
          />
          <DetailRow
            label="Guard"
            value={
              selected.guard_code ||
              selected.guard_reason ||
              "control-plane mutation"
            }
            mono
          />
          <DetailRow
            label="Expires"
            value={formatDateTime(selected.expires_at)}
          />
        </TabsContent>
        <TabsContent className="mt-4 space-y-3" value="details">
          <div>
            <div className="mb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">
              Exact arguments
            </div>
            <JsonViewer value={selected.arguments ?? {}} />
          </div>
          {selected.guard_reason ? (
            <div className="rounded-lg border p-3 text-sm">
              <div className="mb-1 font-medium">Guard reason</div>
              <div className="text-muted-foreground">
                {selected.guard_reason}
              </div>
            </div>
          ) : null}
        </TabsContent>
      </Tabs>
    </ResponsiveDialog>
  )
}

function remainingSeconds(expiresAt: string, now: number) {
  const expiry = new Date(expiresAt).getTime()
  if (!Number.isFinite(expiry)) return 0
  return Math.max(0, Math.ceil((expiry - now) / 1000))
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
