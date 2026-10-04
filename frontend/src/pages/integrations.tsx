import { useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { JsonViewer } from "@/components/json-viewer"
import { PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
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
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  adminApi,
  type CFTunnelStatus,
  type GlobalExecutableResolution,
  type IntegrationStatus,
  type PublicConfig,
  type TypeSafeStatus,
} from "@/lib/api"

export function IntegrationsPage() {
  const [rtk, setRTK] = useState<IntegrationStatus | null>(null)
  const [codeGraph, setCodeGraph] = useState<IntegrationStatus | null>(null)
  const [cf, setCF] = useState<CFTunnelStatus | null>(null)
  const [cfVersion, setCFVersion] = useState("")
  const [removeCFOpen, setRemoveCFOpen] = useState(false)
  const [typeSafe, setTypeSafe] = useState<TypeSafeStatus | null>(null)
  const [config, setConfig] = useState<PublicConfig | null>(null)
  const [savedConfig, setSavedConfig] = useState<PublicConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")

  async function load() {
    try {
      const [nextRTK, nextCodeGraph, nextCF, nextTypeSafe, nextConfig] =
        await Promise.all([
          adminApi.rtkStatus(),
          adminApi.codeGraphStatus(),
          adminApi.cfTunnel(),
          adminApi.typeSafeStatus(),
          adminApi.config(),
        ])
      setRTK(nextRTK)
      setCodeGraph(nextCodeGraph)
      setCF(nextCF)
      setTypeSafe(nextTypeSafe)
      setConfig(nextConfig)
      setSavedConfig(nextConfig)
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
      adminApi.cfTunnel(),
      adminApi.typeSafeStatus(),
      adminApi.config(),
    ])
      .then(([nextRTK, nextCodeGraph, nextCF, nextTypeSafe, nextConfig]) => {
        if (!active) return
        setRTK(nextRTK)
        setCodeGraph(nextCodeGraph)
        setCF(nextCF)
        setTypeSafe(nextTypeSafe)
        setConfig(nextConfig)
        setSavedConfig(nextConfig)
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
    setNotice("")
    try {
      await action()
      await load()
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  async function checkGlobal(
    name: string,
    action: () => Promise<GlobalExecutableResolution>,
    managedCommand: string
  ) {
    setBusy(`${name.toLowerCase()}-global`)
    try {
      const result = await action()
      if (name === "RTK") setRTK(result.status)
      if (name === "CodeGraph") setCodeGraph(result.status)
      if (result.available) {
        setNotice(
          `${name} global executable detected${result.path ? ` at ${result.path}` : ""}.`
        )
      } else if (result.managed_recommended) {
        setNotice(
          `${name} is not installed globally. Use the verified managed asset instead: ${managedCommand}.`
        )
      } else {
        setNotice(
          `${name} is not installed globally; a managed asset is already available.`
        )
      }
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  async function cfAct(action: "probe" | "install" | "update" | "remove") {
    setBusy(`cf-${action}`)
    try {
      if (action === "probe") {
        const result = await adminApi.probeCFTunnel()
        setCF(result.status)
        setCFVersion(result.version ?? "")
      }
      if (action === "install") {
        const result = await adminApi.installCFTunnel()
        setCF(result.status)
        setCFVersion(result.version ?? "")
      }
      if (action === "update") {
        const result = await adminApi.updateCFTunnel()
        setCF(result.status)
        setCFVersion(result.version ?? "")
      }
      if (action === "remove") {
        const result = await adminApi.removeCFTunnel()
        setCF(result.status)
        setCFVersion("")
        setRemoveCFOpen(false)
      }
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  const configDirty = Boolean(
    config &&
    savedConfig &&
    JSON.stringify(config.integrations) !==
      JSON.stringify(savedConfig.integrations)
  )

  async function saveIntegrationConfig() {
    if (!config) return
    setBusy("integration-config")
    try {
      const next = await adminApi.saveConfig({
        integrations: config.integrations,
      })
      setConfig(next)
      setSavedConfig(next)
      setNotice(
        "Integration defaults saved from the dedicated Integrations owner."
      )
      setError("")
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
      {notice ? (
        <p role="status" className="text-sm text-muted-foreground">
          {notice}
        </p>
      ) : null}
      {config ? (
        <Card>
          <CardHeader>
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <CardTitle>Integration defaults</CardTitle>
                <CardDescription className="mt-1">
                  Global Ponytail/Caveman behavior and RTK/CodeGraph runtime
                  resolution live here rather than in Settings.
                </CardDescription>
              </div>
              <Badge variant={configDirty ? "outline" : "secondary"}>
                {configDirty ? "Unsaved changes" : "Saved"}
              </Badge>
            </div>
          </CardHeader>
          <CardContent className="grid gap-5 lg:grid-cols-2">
            <IntegrationConfigBlock
              title="Ponytail"
              description="Default response-behavior integration for new workspace mode state."
            >
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm">Active</span>
                <Switch
                  checked={config.integrations.ponytail.active}
                  onCheckedChange={(active) =>
                    setConfig({
                      ...config,
                      integrations: {
                        ...config.integrations,
                        ponytail: { ...config.integrations.ponytail, active },
                      },
                    })
                  }
                />
              </div>
              <Select
                value={config.integrations.ponytail.mode}
                onValueChange={(mode) =>
                  setConfig({
                    ...config,
                    integrations: {
                      ...config.integrations,
                      ponytail: {
                        ...config.integrations.ponytail,
                        mode: mode as PublicConfig["integrations"]["ponytail"]["mode"],
                      },
                    },
                  })
                }
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="lite">Lite</SelectItem>
                  <SelectItem value="full">Full</SelectItem>
                  <SelectItem value="ultra">Ultra</SelectItem>
                </SelectContent>
              </Select>
            </IntegrationConfigBlock>
            <IntegrationConfigBlock
              title="Caveman"
              description="Default Caveman level for new workspace mode state."
            >
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm">Active</span>
                <Switch
                  checked={config.integrations.caveman.active}
                  onCheckedChange={(active) =>
                    setConfig({
                      ...config,
                      integrations: {
                        ...config.integrations,
                        caveman: { ...config.integrations.caveman, active },
                      },
                    })
                  }
                />
              </div>
              <Select
                value={config.integrations.caveman.mode}
                onValueChange={(mode) =>
                  setConfig({
                    ...config,
                    integrations: {
                      ...config.integrations,
                      caveman: {
                        ...config.integrations.caveman,
                        mode: mode as PublicConfig["integrations"]["caveman"]["mode"],
                      },
                    },
                  })
                }
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="lite">Lite</SelectItem>
                  <SelectItem value="full">Full</SelectItem>
                  <SelectItem value="ultra">Ultra</SelectItem>
                  <SelectItem value="wenyan-lite">Wenyan Lite</SelectItem>
                  <SelectItem value="wenyan-full">Wenyan Full</SelectItem>
                  <SelectItem value="wenyan-ultra">Wenyan Ultra</SelectItem>
                </SelectContent>
              </Select>
            </IntegrationConfigBlock>
            <IntegrationConfigBlock
              title="RTK runtime"
              description="Enable RTK rewriting and optionally pin an executable path."
            >
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm">Enabled</span>
                <Switch
                  checked={config.integrations.rtk.enabled}
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      integrations: {
                        ...config.integrations,
                        rtk: { ...config.integrations.rtk, enabled },
                      },
                    })
                  }
                />
              </div>
              <Input
                placeholder="Optional RTK executable path"
                value={config.integrations.rtk.path}
                onChange={(event) =>
                  setConfig({
                    ...config,
                    integrations: {
                      ...config.integrations,
                      rtk: {
                        ...config.integrations.rtk,
                        path: event.target.value,
                      },
                    },
                  })
                }
              />
            </IntegrationConfigBlock>
            <IntegrationConfigBlock
              title="CodeGraph runtime"
              description="Enable native code graph support and optionally pin an executable path."
            >
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm">Enabled</span>
                <Switch
                  checked={config.integrations.codegraph.enabled}
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      integrations: {
                        ...config.integrations,
                        codegraph: {
                          ...config.integrations.codegraph,
                          enabled,
                        },
                      },
                    })
                  }
                />
              </div>
              <Input
                placeholder="Optional CodeGraph executable path"
                value={config.integrations.codegraph.path}
                onChange={(event) =>
                  setConfig({
                    ...config,
                    integrations: {
                      ...config.integrations,
                      codegraph: {
                        ...config.integrations.codegraph,
                        path: event.target.value,
                      },
                    },
                  })
                }
              />
            </IntegrationConfigBlock>
          </CardContent>
          <CardFooter className="justify-end border-t">
            <Button
              disabled={!configDirty || busy === "integration-config"}
              onClick={() => void saveIntegrationConfig()}
            >
              {busy === "integration-config"
                ? "Saving..."
                : "Save integration defaults"}
            </Button>
          </CardFooter>
        </Card>
      ) : null}
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
              Install managed
            </Button>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="outline"
              onClick={() =>
                void checkGlobal(
                  "RTK",
                  () => adminApi.rtkGlobal(),
                  "cm integration rtk install"
                )
              }
            >
              Check global
            </Button>
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
              Install managed
            </Button>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="outline"
              onClick={() =>
                void checkGlobal(
                  "CodeGraph",
                  () => adminApi.codeGraphGlobal(),
                  "cm integration codegraph install"
                )
              }
            >
              Check global
            </Button>
          </>
        }
      />
      <CFTunnelCard
        status={cf}
        reportedVersion={cfVersion}
        busy={busy}
        removeOpen={removeCFOpen}
        setRemoveOpen={setRemoveCFOpen}
        act={cfAct}
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

function CFTunnelCard({
  status,
  reportedVersion,
  busy,
  removeOpen,
  setRemoveOpen,
  act,
}: {
  status: CFTunnelStatus | null
  reportedVersion: string
  busy: string
  removeOpen: boolean
  setRemoveOpen: (open: boolean) => void
  act: (action: "probe" | "install" | "update" | "remove") => Promise<void>
}) {
  const available = status?.source !== "unavailable"
  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <CardTitle>Cloudflare Quick Tunnel</CardTitle>
            <CardDescription className="mt-1">
              cf-tunnel integration for ephemeral Telegram Activity Mini App
              ingress. OpenAI Secure MCP Tunnel remains the persistent MCP
              tunnel authority.
            </CardDescription>
          </div>
          <Badge variant={available ? "secondary" : "outline"}>
            {available ? status?.source : "Unavailable"}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {status ? (
          <div className="grid gap-3 md:grid-cols-2">
            <IntegrationField label="Platform" value={status.platform} />
            <IntegrationField label="Source" value={status.source} />
            <IntegrationField
              label="Version"
              value={reportedVersion || status.version || "-"}
            />
            <IntegrationField
              label="Verified"
              value={status.verified ? "Yes" : "No"}
            />
            <IntegrationField
              label="Managed asset"
              value={
                status.managed_installed
                  ? "Installed"
                  : status.managed_supported
                    ? "Available"
                    : "Unsupported"
              }
            />
            <IntegrationField
              label="Consumer"
              value={activityMiniAppConsumer(status.consumer)}
            />
            {status.path ? (
              <div className="md:col-span-2">
                <IntegrationField label="Executable" value={status.path} />
              </div>
            ) : null}
          </div>
        ) : (
          <PageLoading rows={3} />
        )}
      </CardContent>
      <CardFooter className="flex flex-wrap justify-end gap-2 border-t">
        <Button
          disabled={Boolean(busy) || !available}
          size="sm"
          variant="outline"
          onClick={() => void act("probe")}
        >
          {busy === "cf-probe" ? "Probing..." : "Probe"}
        </Button>
        {!available && status?.managed_supported ? (
          <Button
            disabled={Boolean(busy)}
            size="sm"
            onClick={() => void act("install")}
          >
            {busy === "cf-install" ? "Installing..." : "Install managed"}
          </Button>
        ) : null}
        {status?.managed_installed ? (
          <>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="outline"
              onClick={() => void act("update")}
            >
              {busy === "cf-update" ? "Updating..." : "Update managed"}
            </Button>
            <Button
              disabled={Boolean(busy)}
              size="sm"
              variant="destructive"
              onClick={() => setRemoveOpen(true)}
            >
              Remove managed
            </Button>
          </>
        ) : null}
      </CardFooter>
      <AlertDialog
        open={removeOpen}
        onOpenChange={(open) => {
          if (!busy) setRemoveOpen(open)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove managed cf-tunnel?</AlertDialogTitle>
            <AlertDialogDescription>
              This removes only CodeMCP's managed cf-tunnel asset. A
              user-installed global cf-tunnel is never removed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={Boolean(busy)}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={Boolean(busy)}
              variant="destructive"
              onClick={() => void act("remove")}
            >
              {busy === "cf-remove" ? "Removing..." : "Remove"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}

function IntegrationConfigBlock({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <div className="space-y-3 rounded-lg border p-4">
      <div>
        <div className="text-sm font-medium">{title}</div>
        <div className="mt-1 text-xs text-muted-foreground">{description}</div>
      </div>
      {children}
    </div>
  )
}

function IntegrationField({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-sm font-medium break-all">{value}</div>
    </div>
  )
}

function activityMiniAppConsumer(value?: string) {
  if (!value || value === "Telegram Logs Mini App")
    return "Telegram Activity Mini App"
  return value
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
