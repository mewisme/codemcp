import { useEffect, useState } from "react"
import {
  Boxes,
  FolderGit2,
  Network,
  RefreshCw,
  Server,
  ShieldCheck,
  Wrench,
} from "lucide-react"
import { CopyButton } from "@/components/copy-button"
import { DashboardCard } from "@/components/dashboard-card"
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
import { Separator } from "@/components/ui/separator"
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
          if (active) {
            setData(next)
            setError("")
          }
        })
        .catch((value) => {
          if (active) setError(errorText(value))
        })
    update()
    const timer = window.setInterval(update, 5000)
    return () => {
      active = false
      window.clearInterval(timer)
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
  return (
    <div className="space-y-6">
      <PageHeader
        title="Overview"
        description="Runtime health, exposure, authentication, and registered resources at a glance."
        actions={
          <>
            <Badge variant={data?.tunnel === "Ready" ? "default" : "secondary"}>
              {data?.tunnel === "Ready"
                ? "Tunnel ready"
                : data?.tunnel === "Connecting"
                  ? "Tunnel connecting"
                  : "Tunnel stopped"}
            </Badge>
            <Button
              disabled={busy}
              size="sm"
              variant="outline"
              onClick={() => void refresh()}
            >
              <RefreshCw className={busy ? "animate-spin" : ""} />
              Refresh
            </Button>
          </>
        }
      />
      <div className="text-xs text-muted-foreground">
        {data
          ? `Updated ${data.updatedAt.toLocaleTimeString()}`
          : "Loading runtime status..."}
      </div>
      <PageError message={error} />
      {data?.cleartextHTTP ? (
        <Alert variant="destructive">
          <AlertDescription>
            Network exposure is enabled (server.expose is not none). Bearer
            tokens and request contents travel on cleartext HTTP —
            CodeMCP has no built-in TLS. Prefer Secure MCP Tunnel, a TLS
            reverse proxy, or a trusted encrypted network.
          </AlertDescription>
        </Alert>
      ) : null}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <DashboardCard
          title="Workspaces"
          value={data?.workspaces ?? "-"}
          description="Registered project roots"
          icon={FolderGit2}
        />
        <DashboardCard
          title="Tools"
          value={data?.tools ?? "-"}
          description="Currently exposed tools"
          icon={Wrench}
        />
        <DashboardCard
          title="MCP Servers"
          value={data ? `${data.enabledServers}/${data.servers}` : "-"}
          description="Enabled upstream servers"
          icon={Server}
        />
        <DashboardCard
          title="Tunnel"
          value={data?.tunnel ?? "-"}
          description={data?.tunnelName || "OpenAI Secure MCP Tunnel"}
          icon={Network}
        />
      </div>
      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Boxes className="size-4" />
              Runtime
            </CardTitle>
            <CardDescription>
              Active runtime listeners and endpoints.
            </CardDescription>
          </CardHeader>
          <CardContent className="divide-y">
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
            <CardTitle className="flex items-center gap-2">
              <ShieldCheck className="size-4" />
              Authentication
            </CardTitle>
            <CardDescription>
              Protection applied to the local MCP and Admin listeners.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-0">
            <AuthState label="MCP authentication" enabled={data?.mcpAuth} />
            <Separator />
            <AuthState label="Admin authentication" enabled={data?.adminAuth} />
          </CardContent>
        </Card>
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
    <div className="flex items-center justify-between gap-3 py-3">
      <span className="text-sm text-muted-foreground">{label}</span>
      <Badge variant={enabled ? "secondary" : "outline"}>
        {enabled === undefined ? "-" : enabled ? "Enabled" : "Disabled"}
      </Badge>
    </div>
  )
}

async function loadDashboard(): Promise<DashboardData> {
  const [workspaces, tools, servers, tunnel, config] =
    await Promise.all([
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
    mcpEndpoint: `http://${host}:${config.server.port}/mcp`,
    adminEndpoint: config.admin.enabled
      ? `http://${host}:${config.admin.port}`
      : "Disabled",
    mcpAuth: config.auth.mcp_enabled,
    adminAuth: config.auth.admin_enabled,
    cleartextHTTP: config.server.expose.mode !== "none",
    updatedAt: new Date(),
  }
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
