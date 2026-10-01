import { useCallback, useEffect, useState } from "react"
import { Loader2, RefreshCw, Sparkles } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import {
  adminApi,
  type ApprovalExplainStatus,
  type ApprovalExplanationResult,
} from "@/lib/api"

export function RequestExplanation({ requestID, revision = 0 }: { requestID: string; revision?: number }) {
  const [status, setStatus] = useState<ApprovalExplainStatus | null>(null)
  const [result, setResult] = useState<ApprovalExplanationResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const load = useCallback(async () => {
    try {
      const [nextStatus, nextResult] = await Promise.all([
        adminApi.approvalExplainStatus(),
        adminApi.approvalExplanation(requestID),
      ])
      setStatus(nextStatus)
      setResult(nextResult)
      setError("")
      return { status: nextStatus, result: nextResult }
    } catch (value) {
      setError(errorText(value))
      return null
    } finally {
      setLoading(false)
    }
  }, [requestID])

  useEffect(() => {
    let active = true
    void Promise.all([
      adminApi.approvalExplainStatus(),
      adminApi.approvalExplanation(requestID),
    ])
      .then(([nextStatus, nextResult]) => {
        if (!active) return
        setStatus(nextStatus)
        setResult(nextResult)
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
  }, [requestID, revision])

  async function trigger(retry: boolean) {
    setBusy(true)
    setError("")
    try {
      const next = await adminApi.explainApproval(requestID, retry)
      setResult(next)
      await load()
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy(false)
    }
  }

  const explanation = result?.explanation
  return (
    <section className="space-y-3 rounded-xl border bg-muted/20 p-4" aria-labelledby={`explain-${requestID}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 id={`explain-${requestID}`} className="flex items-center gap-2 font-medium"><Sparkles className="size-4" />AI explanation</h3>
          <p className="text-xs text-muted-foreground">Informational only. The exact request details remain authoritative.</p>
        </div>
        <div className="flex flex-wrap gap-1.5">
          {status?.mode ? <Badge variant="outline">{status.mode}</Badge> : null}
          {result?.state ? <Badge variant={result.state === "failed" ? "destructive" : result.state === "ready" ? "default" : "secondary"}>{result.state}</Badge> : null}
        </div>
      </div>
      {loading ? <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />Loading explanation state…</div> : null}
      {error ? <div role="status" className="text-sm text-destructive">{error}</div> : null}
      {status && !status.available ? <p className="text-sm text-muted-foreground">{status.reason || "AI explanation is unavailable."}</p> : null}
      {status?.mode === "manual" && status.available && (!result || result.state === "none") ? (
        <Button size="sm" variant="outline" disabled={busy} onClick={() => void trigger(false)}>{busy ? <Loader2 className="animate-spin" /> : <Sparkles />}Explain command</Button>
      ) : null}
      {result?.state === "pending" ? <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />Generating explanation…</div> : null}
      {result?.state === "failed" ? (
        <div className="space-y-2">
          <p className="text-sm text-destructive">{result.failure || "Explanation generation failed."}</p>
          {status?.available ? <Button size="sm" variant="outline" disabled={busy} onClick={() => void trigger(true)}><RefreshCw className={busy ? "animate-spin" : ""} />Retry explanation</Button> : null}
        </div>
      ) : null}
      {explanation ? (
        <div className="space-y-3 text-sm">
          <Separator />
          <div className="grid gap-2 sm:grid-cols-3">
            <Info label="Provider" value={explanation.provider_id} />
            <Info label="Model" value={explanation.model} />
            <Info label="Generated" value={formatTimestamp(explanation.generated_at)} />
          </div>
          <div><div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Summary</div><p>{explanation.summary}</p></div>
          <ExplanationList title="Steps" values={explanation.steps} />
          <ExplanationList title="Effects" values={explanation.effects} />
          <ExplanationList title="Risk notes" values={explanation.risk_notes} />
          <ExplanationList title="Unknowns" values={explanation.unknowns} />
        </div>
      ) : null}
    </section>
  )
}

function Info({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0"><div className="text-xs text-muted-foreground">{label}</div><div className="break-all">{value || "—"}</div></div>
}

function ExplanationList({ title, values }: { title: string; values?: string[] }) {
  if (!values?.length) return null
  return <div><div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{title}</div><ul className="list-disc space-y-1 pl-5">{values.map((value, index) => <li key={`${title}-${index}`}>{value}</li>)}</ul></div>
}

function formatTimestamp(value: string) {
  if (!value) return "—"
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
