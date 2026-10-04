import { useEffect, useState } from "react"
import {
  FolderGit2,
  Network,
  RefreshCw,
  Server,
  ShieldAlert,
  ShieldCheck,
  Wrench,
} from "lucide-react"
import { CopyButton } from "@/components/copy-button"
import { DetailRow } from "@/components/detail-row"
import { PageError } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { adminApi } from "@/lib/api"

type DashboardData = {
  workspaces: number
  tools: number
  servers: number
  enabledServers: number
  tunnel: "Ready" | "Connecting" | "Stopped"
  tunnelName: string
  mcpEndpoint: string
  adminEndpoint: string
  mcpAuth: boolean
  adminAuth: boolean
  cleartextHTTP: boolean
  updatedAt: Date
}

export function OverviewPage() {
  const [data, setData] = useState<DashboardData | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    let active = true
    const update = () =>
      void loadDashboard()
        .then((next) => {
          if (!active) return
          setData(next)
          setError("")
        })
        .catch((value) => {
          if (active) setError(errorText(value))
        })
    update()
    window.addEventListener("focus", update)
    return () => {
      active = false
      window.removeEventListener("focus", update)
    }
  }, [])

  async function refresh() {
    setBusy(true)
    try {
      setData(await loadDashboard())
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy(false)
    }
  }

  const attentionCount = data?.cleartextHTTP ? 1 : 0

  return (
    <div className="space-y-6">
      <PageHeader
        title="Overview"
        description="Current operator state, connectivity, inventory, and listener protection."
        actions={
          <Button
            disabled={busy}
            size="sm"
            variant="outline"
            onClick={() => void refresh()}
          >
            <RefreshCw className={busy ? "animate-spin" : ""} />
            Refresh
          </Button>
        }
      />
      <PageError message={error} />

      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <CardTitle className="flex items-center gap-2">
                {attentionCount ? (
                  <ShieldAlert className="size-4" />
                ) : (
                  <ShieldCheck className="size-4" />
                )}
                Needs attention
              </CardTitle>
              <CardDescription className="mt-1">
                This view surfaces the explicit cleartext-exposure warning from
                configuration; it does not infer aggregate health.
              </CardDescription>
            </div>
            <Badge variant={attentionCount ? "destructive" : "secondary"}>
              {data
                ? attentionCount
                  ? `${attentionCount} item`
                  : "No exposure warning"
                : "Loading"}
            </Badge>
          </div>
        </CardHeader>
        {data?.cleartextHTTP ? (
          <CardContent>
            <Alert variant="destructive">
              <AlertDescription>
                Direct network exposure is enabled over cleartext HTTP. Bearer
                tokens and request contents are not protected by built-in TLS.
                Prefer Secure MCP Tunnel, a TLS reverse proxy, or a trusted
                encrypted network.
              </AlertDescription>
            </Alert>
          </CardContent>
        ) : null}
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Runtime and connectivity</CardTitle>
            <CardDescription>
              Observed tunnel state and active listener endpoints.
            </CardDescription>
          </CardHeader>
          <CardContent className="divide-y">
            <DetailRow
              label="Secure MCP Tunnel"
              value={
                <div className="flex flex-wrap items-center justify-end gap-2">
                  <Badge
                    variant={data?.tunnel === "Ready" ? "secondary" : "outline"}
                  >
                    {data?.tunnel ?? "-"}
                  </Badge>
                  {data?.tunnelName ? (
                    <span className="text-sm text-muted-foreground">
                      {data.tunnelName}
                    </span>
                  ) : null}
                </div>
              }
            />
            <EndpointDetail
              label="MCP endpoint"
              value={data?.mcpEndpoint ?? "-"}
            />
            <EndpointDetail
              label="Admin endpoint"
              value={data?.adminEndpoint ?? "-"}
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Listener protection</CardTitle>
            <CardDescription>
              Authentication state reported by the current public configuration.
            </CardDescription>
          </CardHeader>
          <CardContent className="divide-y">
            <AuthState label="MCP authentication" enabled={data?.mcpAuth} />
            <AuthState label="Admin authentication" enabled={data?.adminAuth} />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Inventory</CardTitle>
          <CardDescription>
            Registered resources available to the current runtime.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-3">
          <InventoryItem
            icon={FolderGit2}
            label="Workspaces"
            value={data?.workspaces}
          />
          <InventoryItem icon={Wrench} label="Tools" value={data?.tools} />
          <InventoryItem
            icon={Server}
            label="Upstreams"
            value={
              data
                ? `${data.enabledServers}/${data.servers} enabled`
                : undefined
            }
          />
        </CardContent>
      </Card>

      <div className="text-xs text-muted-foreground">
        {data
          ? `Updated ${data.updatedAt.toLocaleTimeString()}`
          : "Loading runtime status..."}
      </div>
    </div>
  )
}

function InventoryItem({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Network
  label: string
  value?: string | number
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border p-3">
      <Icon className="size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="mt-0.5 text-sm font-medium">{value ?? "-"}</div>
      </div>
    </div>
  )
}

function EndpointDetail({ label, value }: { label: string; value: string }) {
  return (
    <DetailRow
      label={label}
      value={
        <div className="flex min-w-0 items-start justify-end gap-1">
          <span className="min-w-0 font-mono text-sm font-normal break-all">
            {value}
          </span>
          {value !== "-" && value !== "Disabled" ? (
            <CopyButton label={`Copy ${label}`} value={value} />
          ) : null}
        </div>
      }
    />
  )
}

function AuthState({ label, enabled }: { label: string; enabled?: boolean }) {
  return (
    <DetailRow
      label={label}
      value={
        <Badge variant={enabled ? "secondary" : "outline"}>
          {enabled === undefined ? "-" : enabled ? "Enabled" : "Disabled"}
        </Badge>
      }
    />
  )
}

async function loadDashboard(): Promise<DashboardData> {
  const [workspaces, tools, servers, tunnel, config] = await Promise.all([
    adminApi.workspaces(),
    adminApi.tools(),
    adminApi.upstream(),
    adminApi.tunnel(),
    adminApi.config(),
  ])
  const host = window.location.hostname || "127.0.0.1"
  return {
    workspaces: workspaces.length,
    tools: tools.length,
    servers: servers.length,
    enabledServers: servers.filter((server) => server.enabled).length,
    tunnel: tunnel.running
      ? tunnel.ready
        ? "Ready"
        : "Connecting"
      : "Stopped",
    tunnelName: tunnel.metadata?.name ?? "",
    mcpEndpoint: `http://${host}:${config.http.mcp.port}/mcp`,
    adminEndpoint: config.http.admin.enabled
      ? `http://${host}:${config.http.admin.port}`
      : "Disabled",
    mcpAuth: config.http.mcp.auth.enabled,
    adminAuth: config.http.admin.auth.enabled,
    cleartextHTTP: config.http.exposure.mode !== "none",
    updatedAt: new Date(),
  }
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
