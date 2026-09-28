import { useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { JsonViewer } from "@/components/json-viewer"
import { PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  adminApi,
  type IntegrationStatus,
  type TypeSafeStatus,
} from "@/lib/api"

export function IntegrationsPage() {
  const [rtk, setRTK] = useState<IntegrationStatus | null>(null)
  const [codeGraph, setCodeGraph] = useState<IntegrationStatus | null>(null)
  const [typeSafe, setTypeSafe] = useState<TypeSafeStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")

  async function load() {
    try {
      const [nextRTK, nextCodeGraph, nextTypeSafe] = await Promise.all([
        adminApi.rtkStatus(),
        adminApi.codeGraphStatus(),
        adminApi.typeSafeStatus(),
      ])
      setRTK(nextRTK)
      setCodeGraph(nextCodeGraph)
      setTypeSafe(nextTypeSafe)
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    let active = true
    void Promise.all([
      adminApi.rtkStatus(),
      adminApi.codeGraphStatus(),
      adminApi.typeSafeStatus(),
    ])
      .then(([nextRTK, nextCodeGraph, nextTypeSafe]) => {
        if (!active) return
        setRTK(nextRTK)
        setCodeGraph(nextCodeGraph)
        setTypeSafe(nextTypeSafe)
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

  async function act(name: string, action: () => Promise<unknown>) {
    setBusy(name)
    try {
      await action()
      await load()
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  if (loading) return <PageLoading rows={6} />
  return (
    <div className="space-y-6">
      <PageHeader
        title="Integrations"
        description="Canonical diagnostics and lifecycle actions for first-party integrations."
        actions={
          <Button size="sm" variant="outline" onClick={() => void load()}>
            <RefreshCw />
            Refresh
          </Button>
        }
      />
      <PageError message={error} />
      <IntegrationCard
        title="RTK"
        description="Command rewriting and managed executable resolution."
        value={rtk}
        actions={
          <>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              onClick={() =>
                void act("rtk-probe", () => adminApi.rtkAction("probe"))
              }
            >
              Probe
            </Button>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="outline"
              onClick={() =>
                void act("rtk-install", () => adminApi.rtkAction("install"))
              }
            >
              Install
            </Button>
            <Button disabled={Boolean(busy)} size="sm" variant="outline" onClick={() => void act("rtk-install-global", () => adminApi.rtkAction("install/global"))}>Install globally</Button>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="outline"
              onClick={() =>
                void act("rtk-enable", () => adminApi.rtkAction("enable"))
              }
            >
              Enable
            </Button>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="outline"
              onClick={() =>
                void act("rtk-disable", () => adminApi.rtkAction("disable"))
              }
            >
              Disable
            </Button>
          </>
        }
      />
      <IntegrationCard
        title="CodeGraph"
        description="Native code graph exploration runtime."
        value={codeGraph}
        actions={
          <>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              onClick={() =>
                void act("codegraph-probe", () =>
                  adminApi.codeGraphAction("probe")
                )
              }
            >
              Probe
            </Button>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="outline"
              onClick={() =>
                void act("codegraph-install", () =>
                  adminApi.codeGraphAction("install")
                )
              }
            >
              Install
            </Button>
            <Button disabled={Boolean(busy)} size="sm" variant="outline" onClick={() => void act("codegraph-install-global", () => adminApi.codeGraphAction("install/global"))}>Install globally</Button>
          </>
        }
      />
      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <CardTitle>TypeSafe</CardTitle>
              <CardDescription>
                Semantic provider state comes directly from the application read
                model.
              </CardDescription>
            </div>
            <Badge
              variant={typeSafe?.state === "ready" ? "secondary" : "outline"}
            >
              {typeSafe?.state ?? "unknown"}
            </Badge>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap gap-2">
            <Badge variant="outline">Model {typeSafe?.model || "-"}</Badge>
            <Badge variant="outline">
              API key {typeSafe?.api_key_configured ? "configured" : "missing"}
            </Badge>
            <Badge variant="outline">{typeSafe?.timeout_ms ?? 0} ms</Badge>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              disabled={Boolean(busy)}
              size="sm"
              onClick={() =>
                void act("typesafe-probe", () =>
                  adminApi.typeSafeAction("probe")
                )
              }
            >
              Probe
            </Button>
            <Button
              disabled={Boolean(busy) || typeSafe?.enabled}
              size="sm"
              variant="outline"
              onClick={() =>
                void act("typesafe-enable", () =>
                  adminApi.typeSafeAction("enable")
                )
              }
            >
              Enable
            </Button>
            <Button
              disabled={Boolean(busy) || !typeSafe?.enabled}
              size="sm"
              variant="outline"
              onClick={() =>
                void act("typesafe-disable", () =>
                  adminApi.typeSafeAction("disable")
                )
              }
            >
              Disable
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

function IntegrationCard({
  title,
  description,
  value,
  actions,
}: {
  title: string
  description: string
  value: IntegrationStatus | null
  actions: React.ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <JsonViewer value={value} maxHeight="18rem" />
        <div className="flex flex-wrap gap-2">{actions}</div>
      </CardContent>
    </Card>
  )
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
