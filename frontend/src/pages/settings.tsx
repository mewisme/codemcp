import { useEffect, useMemo, useState } from "react"
import { Copy, Download, Save, Search, Undo2 } from "lucide-react"
import { PageError } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  ScrollableTabsList,
  Tabs,
  TabsContent,
  TabsTrigger,
} from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import {
  adminApi,
  type AuthStatus,
  type NetworkInterface,
  type PublicConfig,
  type SettingResult,
} from "@/lib/api"

export function SettingsPage() {
  const [config, setConfig] = useState<PublicConfig | null>(null)
  const [savedConfig, setSavedConfig] = useState<PublicConfig | null>(null)
  const [tunnelEnabled, setTunnelEnabled] = useState(false)
  const [interfaces, setInterfaces] = useState<NetworkInterface[]>([])
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null)
  const [authBusy, setAuthBusy] = useState("")
  const [revealedCredential, setRevealedCredential] = useState("")
  const [settings, setSettings] = useState<SettingResult[]>([])
  const [settingQuery, setSettingQuery] = useState("")
  const [selectedSetting, setSelectedSetting] = useState<SettingResult | null>(null)
  const [settingValue, setSettingValue] = useState("")
  const [notificationStatus, setNotificationStatus] = useState<Record<string, unknown> | null>(null)
  const [telegramToken, setTelegramToken] = useState("")
  const [telegramUserID, setTelegramUserID] = useState("")
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")

  useEffect(() => {
    let active = true
    void Promise.all([
      adminApi.config(),
      adminApi.networkInterfaces(),
      adminApi.tunnelConfig(),
      adminApi.authStatus(),
      adminApi.settings("", ""),
      adminApi.notificationStatus(),
    ])
      .then(([nextConfig, nextInterfaces, nextTunnel, nextAuth, nextSettings, nextNotifications]) => {
        if (!active) return
        const normalized = normalizeConfig(nextConfig)
        setConfig(normalized)
        setSavedConfig(normalized)
        setInterfaces(nextInterfaces)
        setTunnelEnabled(nextTunnel.enabled)
        setAuthStatus(nextAuth)
        setSettings(nextSettings)
        setNotificationStatus(nextNotifications)
      })
      .catch((value) => {
        if (active) setError(errorText(value))
      })
    return () => {
      active = false
    }
  }, [])

  useEffect(() => {
    if (!revealedCredential) return
    const timer = window.setTimeout(() => setRevealedCredential(""), 60_000)
    return () => window.clearTimeout(timer)
  }, [revealedCredential])

  const dirty = useMemo(
    () =>
      Boolean(
        config &&
        savedConfig &&
        JSON.stringify(config) !== JSON.stringify(savedConfig)
      ),
    [config, savedConfig]
  )

  async function save() {
    if (!config) return
    setBusy(true)
    try {
      const next = normalizeConfig(await adminApi.saveConfig(config))
      setConfig(next)
      setSavedConfig(next)
      setMessage(
        "Saved. Runtime, transport, listener, integration, auth, filesystem, and shell execution changes were applied live."
      )
      setError("")
    } catch (value) {
      setError(errorText(value))
      setMessage("")
    } finally {
      setBusy(false)
    }
  }

  async function authAction(
    scope: "mcp" | "admin",
    action: "rotate" | "enable" | "disable"
  ) {
    const key = `${scope}:${action}`
    setAuthBusy(key)
    setError("")
    try {
      const result = await adminApi.authAction(scope, action)
      if ("token" in result) {
        setRevealedCredential(result.token)
        setAuthStatus(result.status)
      } else {
        setAuthStatus(result)
      }
      const normalized = normalizeConfig(await adminApi.config())
      setConfig(normalized)
      setSavedConfig(normalized)
      setMessage(
        action === "rotate"
          ? "Credential rotated. Copy the one-time value now; it will be hidden automatically."
          : `${scope.toUpperCase()} authentication ${action === "enable" ? "enabled" : "disabled"}.`
      )
    } catch (value) {
      setError(errorText(value))
    } finally {
      setAuthBusy("")
    }
  }

  async function loadSettings(query = settingQuery) {
    try {
      setSettings(await adminApi.settings("", query))
      setError("")
    } catch (value) {
      setError(errorText(value))
    }
  }

  async function openSetting(item: SettingResult) {
    try {
      const next = await adminApi.setting(item.Spec.Key)
      setSelectedSetting(next)
      setSettingValue(next.Spec.Secret ? "" : next.Value)
      setError("")
    } catch (value) {
      setError(errorText(value))
    }
  }

  async function updateSelectedSetting(action = "set") {
    if (!selectedSetting) return
    setBusy(true)
    try {
      await adminApi.setSetting(selectedSetting.Spec.Key, settingValue, action)
      const next = await adminApi.setting(selectedSetting.Spec.Key)
      setSelectedSetting(next)
      setSettingValue(next.Spec.Secret ? "" : next.Value)
      await loadSettings()
      setMessage(`${selectedSetting.Spec.Label || selectedSetting.Spec.Key} updated from canonical settings.`)
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy(false)
    }
  }

  async function exportCanonicalSettings() {
    try {
      const document = await adminApi.exportSettings()
      const bytes = Uint8Array.from(atob(document.Data), (char) => char.charCodeAt(0))
      const url = URL.createObjectURL(new Blob([bytes], { type: "application/json" }))
      const anchor = window.document.createElement("a")
      anchor.href = url
      anchor.download = document.FileName || "codemcp-config.json"
      anchor.click()
      URL.revokeObjectURL(url)
      setError("")
    } catch (value) {
      setError(errorText(value))
    }
  }

  async function setupTelegram(event: React.FormEvent) {
    event.preventDefault()
    const token = telegramToken.trim()
    const userID = Number(telegramUserID)
    if (!token || !Number.isSafeInteger(userID) || userID <= 0) return
    setBusy(true)
    try {
      const result = await adminApi.telegramSetup(token, userID)
      setTelegramToken("")
      setMessage(
        `Telegram configured for ${result.authorized_users.length} authorized user(s); token ${result.token.Value || "stored securely"}.`
      )
      await loadSettings()
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy(false)
    }
  }

  function setExposureMode(mode: PublicConfig["http"]["exposure"]["mode"]) {
    if (!config) return
    const current = config.http.exposure.interfaces
    setConfig({
      ...config,
      http: {
        ...config.http,
        exposure: { mode, interfaces: mode === "interfaces" ? current : [] },
      },
    })
  }

  function toggleInterface(name: string, checked: boolean) {
    if (!config) return
    const selected = new Set(config.http.exposure.interfaces)
    if (checked) selected.add(name)
    else selected.delete(name)
    setConfig({
      ...config,
      http: {
        ...config.http,
        exposure: { mode: "interfaces", interfaces: [...selected].sort() },
      },
    })
  }

  if (!config || !savedConfig)
    return (
      <div className="text-sm text-muted-foreground">
        {error || "Loading settings..."}
      </div>
    )
  const selectedInterfaces = new Set(config.http.exposure.interfaces)
  const exposed = config.http.exposure.mode !== "none"
  const exposureAuthReady =
    !exposed ||
    ((!config.http.mcp.enabled ||
      (config.http.mcp.auth.enabled && config.http.mcp.auth.token_configured)) &&
      (!config.http.admin.enabled ||
        (config.http.admin.auth.enabled && config.http.admin.auth.token_configured)))
  const saveDisabled =
    busy ||
    (!config.http.mcp.enabled && !tunnelEnabled) ||
    (config.http.exposure.mode === "interfaces" &&
      config.http.exposure.interfaces.length === 0) ||
    !exposureAuthReady ||
    (exposed && !config.http.security.allow_insecure)

  return (
    <div className="space-y-6">
      <PageHeader
        title="Settings"
        description="Configure runtime listeners, security, filesystem access, first-party integrations, and the managed execution environment."
      />
      <PageError message={error} />
      {message ? (
        <Alert>
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      ) : null}
      <Tabs defaultValue="general">
        <ScrollableTabsList className="justify-start">
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="network">Network</TabsTrigger>
          <TabsTrigger value="permissions">Permissions</TabsTrigger>
          <TabsTrigger value="integrations">Integrations</TabsTrigger>
          <TabsTrigger value="authentication">Authentication</TabsTrigger>
          <TabsTrigger value="operations">Operations</TabsTrigger>
          <TabsTrigger value="environment">Environment</TabsTrigger>
        </ScrollableTabsList>
        <TabsContent className="mt-6 space-y-6" value="general">
          <Card>
            <CardHeader>
              <CardTitle>Runtime</CardTitle>
              <CardDescription>
                MCP transports, listener ports, and Admin availability.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Toggle
                  label="MCP HTTP"
                  description={
                    tunnelEnabled
                      ? "Serve MCP directly over HTTP. Secure MCP Tunnel remains available if this transport is disabled."
                      : "Serve MCP directly over HTTP. This transport is required while Secure MCP Tunnel is disabled."
                  }
                  checked={config.http.mcp.enabled}
                  disabled={config.http.mcp.enabled && !tunnelEnabled}
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      http: {
                        ...config.http,
                        mcp: { ...config.http.mcp, enabled },
                      },
                    })
                  }
                />
                <div className="flex flex-wrap gap-2">
                  <Badge
                    variant={config.http.mcp.enabled ? "secondary" : "outline"}
                  >
                    MCP HTTP {config.http.mcp.enabled ? "enabled" : "disabled"}
                  </Badge>
                  <Badge variant={tunnelEnabled ? "secondary" : "outline"}>
                    Secure MCP Tunnel {tunnelEnabled ? "enabled" : "disabled"}
                  </Badge>
                </div>
                <div className="grid gap-5 md:grid-cols-2">
                  <SettingField
                    label="MCP HTTP port"
                    description="Direct MCP HTTP listener port."
                  >
                    <Input
                      disabled={!config.http.mcp.enabled}
                      max={65535}
                      min={1}
                      type="number"
                      value={config.http.mcp.port}
                      onChange={(event) =>
                        setConfig({
                          ...config,
                          http: {
                            ...config.http,
                            mcp: {
                              ...config.http.mcp,
                              port: Number(event.target.value),
                            },
                          },
                        })
                      }
                    />
                  </SettingField>
                  <SettingField
                    label="Admin port"
                    description="Admin API and dashboard port."
                  >
                    <Input
                      max={65535}
                      min={1}
                      type="number"
                      value={config.http.admin.port}
                      onChange={(event) =>
                        setConfig({
                          ...config,
                          http: {
                            ...config.http,
                            admin: {
                              ...config.http.admin,
                              port: Number(event.target.value),
                            },
                          },
                        })
                      }
                    />
                  </SettingField>
                </div>
                <Toggle
                  label="Admin enabled"
                  description="Serve the local Admin API and dashboard."
                  checked={config.http.admin.enabled}
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      http: {
                        ...config.http,
                        admin: { ...config.http.admin, enabled },
                      },
                    })
                  }
                />
              </FieldGroup>
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent className="mt-6" value="network">
          <Card>
            <CardHeader>
              <CardTitle>Network exposure</CardTitle>
              <CardDescription>
                Choose which interfaces receive enabled direct MCP HTTP and
                Admin listeners.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-5">
              <RadioGroup
                value={config.http.exposure.mode}
                onValueChange={(value) =>
                  setExposureMode(
                    value as PublicConfig["http"]["exposure"]["mode"]
                  )
                }
              >
                <ExposureOption
                  value="none"
                  title="Local only"
                  description="Bind enabled MCP HTTP and Admin listeners only to 127.0.0.1."
                />
                <ExposureOption
                  value="all"
                  title="All active interfaces"
                  description="Keep loopback and bind every currently active eligible IPv4 and IPv6 address."
                />
                <ExposureOption
                  value="interfaces"
                  title="Selected interfaces"
                  description="Keep loopback and bind eligible IPv4 and IPv6 addresses only on selected interfaces."
                />
                <ExposureOption
                  value="0.0.0.0"
                  title="Wildcard 0.0.0.0"
                  description="Bind every IPv4 interface, including interfaces that appear later."
                />
              </RadioGroup>
              {config.http.exposure.mode === "interfaces" ? (
                <div className="space-y-2 rounded-lg border p-3">
                  {interfaces.length === 0 ? (
                    <div className="text-sm text-muted-foreground">
                      No eligible active network interfaces detected.
                    </div>
                  ) : (
                    interfaces.map((iface) => (
                      <label
                        className="flex cursor-pointer items-start gap-3 rounded-md px-2 py-2 hover:bg-muted/50"
                        key={iface.name}
                      >
                        <Checkbox
                          checked={selectedInterfaces.has(iface.name)}
                          onCheckedChange={(checked) =>
                            toggleInterface(iface.name, checked === true)
                          }
                        />
                        <div className="min-w-0 flex-1">
                          <div className="font-mono text-sm">{iface.name}</div>
                          <div className="mt-1 flex flex-wrap gap-2">
                            {iface.addresses.map((address) => (
                              <Badge key={address.address} variant="outline">
                                {address.address} · {address.scope}
                              </Badge>
                            ))}
                          </div>
                        </div>
                      </label>
                    ))
                  )}
                </div>
              ) : null}
              {exposed && !exposureAuthReady ? (
                <Alert variant="destructive">
                  <AlertDescription>
                    Direct network exposure requires configured MCP
                    authentication when MCP HTTP is enabled and, when Admin is
                    enabled, configured Admin authentication.
                  </AlertDescription>
                </Alert>
              ) : null}
              {exposed ? (
                <div className="space-y-3">
                  <Alert variant="destructive">
                    <AlertDescription>
                      Bearer tokens and request contents travel on cleartext
                      HTTP. CodeMCP has no built-in TLS — use a trusted or
                      already encrypted network, terminate TLS in a reverse
                      proxy, or prefer Secure MCP Tunnel.
                    </AlertDescription>
                  </Alert>
                  <Toggle
                    label="Allow authenticated HTTP beyond loopback"
                    description="Acknowledge that direct non-loopback listeners are unencrypted HTTP."
                    checked={config.http.security.allow_insecure}
                    onCheckedChange={(allow_insecure) =>
                      setConfig({
                        ...config,
                        http: {
                          ...config.http,
                          security: { ...config.http.security, allow_insecure },
                        },
                      })
                    }
                  />
                  {!config.http.security.allow_insecure ? (
                    <Alert variant="destructive">
                      <AlertDescription>
                        Use this only on a trusted or encrypted network, or
                        prefer Secure MCP Tunnel / a TLS reverse proxy.
                      </AlertDescription>
                    </Alert>
                  ) : null}
                </div>
              ) : null}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent className="mt-6" value="permissions">
          <Card>
            <CardHeader>
              <CardTitle>Filesystem access</CardTitle>
              <CardDescription>
                Directories available to every registered workspace in addition
                to its own canonical root.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <SettingField
                label="Allowed directories"
                description="One absolute existing directory per line."
              >
                <Textarea
                  className="min-h-40 font-mono"
                  placeholder={"/tmp\n/var/tmp/codemcp"}
                  value={config.permissions.allow_dirs.join("\n")}
                  onChange={(event) =>
                    setConfig({
                      ...config,
                      permissions: {
                        allow_dirs: parseLines(event.target.value),
                      },
                    })
                  }
                />
              </SettingField>
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent className="mt-6" value="integrations">
          <Card>
            <CardHeader>
              <CardTitle>First-party integrations</CardTitle>
              <CardDescription>
                Configure first-party CodeMCP integrations. Ponytail and Caveman
                control response behavior; RTK and CodeGraph manage
                executable-backed coding workflows.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Toggle
                  label="Ponytail"
                  description="Keep the Ponytail coding integration active by default."
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
                <SettingField
                  label="Ponytail intensity"
                  description="Default intensity for new workspace mode state. Review remains session-only."
                >
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
                    <SelectTrigger className="w-full sm:w-64">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="lite">Lite</SelectItem>
                      <SelectItem value="full">Full</SelectItem>
                      <SelectItem value="ultra">Ultra</SelectItem>
                    </SelectContent>
                  </Select>
                </SettingField>
                <Toggle
                  label="Caveman"
                  description="Keep Caveman mode active by default."
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
                <SettingField
                  label="Caveman intensity"
                  description="Default Caveman level for new workspace mode state. Wenyan levels use classical Chinese compression."
                >
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
                    <SelectTrigger className="w-full sm:w-64">
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
                </SettingField>
                <Toggle
                  label="RTK"
                  description="Enable RTK command rewriting and executable resolution."
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
                <SettingField
                  label="RTK executable"
                  description="Optional absolute executable path. Leave blank for system or verified managed resolution."
                >
                  <Input
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
                </SettingField>
                <Toggle
                  label="CodeGraph"
                  description="Enable CodeGraph runtime resolution and native codegraph_explore support."
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
                <SettingField
                  label="CodeGraph executable"
                  description="Optional absolute executable path. Leave blank for system or verified managed resolution."
                >
                  <Input
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
                </SettingField>
              </FieldGroup>
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent className="mt-6 space-y-6" value="authentication">
          <Card>
            <CardHeader>
              <CardTitle>Authentication</CardTitle>
              <CardDescription>
                Manage MCP and Admin credentials directly. Rotation returns a
                one-time credential that is never written to browser storage.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-5">
              <AuthControl
                label="MCP authentication"
                configured={authStatus?.mcp_configured ?? config.http.mcp.auth.token_configured}
                enabled={authStatus?.mcp_enabled ?? config.http.mcp.auth.enabled}
                busy={authBusy.startsWith("mcp:")}
                locked={exposed && config.http.mcp.enabled}
                onRotate={() => void authAction("mcp", "rotate")}
                onToggle={(enabled) =>
                  void authAction("mcp", enabled ? "enable" : "disable")
                }
              />
              <AuthControl
                label="Admin authentication"
                configured={authStatus?.admin_configured ?? config.http.admin.auth.token_configured}
                enabled={authStatus?.admin_enabled ?? config.http.admin.auth.enabled}
                busy={authBusy.startsWith("admin:")}
                locked={exposed && config.http.admin.enabled}
                onRotate={() => void authAction("admin", "rotate")}
                onToggle={(enabled) =>
                  void authAction("admin", enabled ? "enable" : "disable")
                }
              />
            </CardContent>
          </Card>
          {revealedCredential ? (
            <Alert>
              <AlertDescription className="space-y-3">
                <div>
                  <div className="font-medium">One-time credential</div>
                  <div className="text-xs text-muted-foreground">
                    Copy it now. It is hidden automatically after one minute and
                    cannot be recovered without rotating again.
                  </div>
                </div>
                <div className="flex min-w-0 items-center gap-2">
                  <code className="min-w-0 flex-1 overflow-x-auto rounded bg-muted px-2 py-1 text-xs">
                    {revealedCredential}
                  </code>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => void navigator.clipboard.writeText(revealedCredential)}
                  >
                    <Copy />
                    Copy
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setRevealedCredential("")}>
                    Hide
                  </Button>
                </div>
              </AlertDescription>
            </Alert>
          ) : null}
        </TabsContent>
        <TabsContent className="mt-6 space-y-6" value="operations">
          <Card>
            <CardHeader>
              <CardTitle>Canonical settings</CardTitle>
              <CardDescription>
                Search, inspect, update, clear, or export typed settings through
                the canonical settings service.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex flex-col gap-2 sm:flex-row">
                <div className="relative min-w-0 flex-1">
                  <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    className="pl-9"
                    placeholder="Search setting key, label, owner..."
                    value={settingQuery}
                    onChange={(event) => setSettingQuery(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") void loadSettings()
                    }}
                  />
                </div>
                <Button variant="outline" onClick={() => void loadSettings()}>
                  Search
                </Button>
                <Button variant="outline" onClick={() => void exportCanonicalSettings()}>
                  <Download />
                  Export
                </Button>
              </div>
              <div className="max-h-80 space-y-2 overflow-y-auto">
                {settings.map((item) => (
                  <button
                    className="flex w-full items-start justify-between gap-3 rounded-lg border p-3 text-left hover:bg-muted/40"
                    key={item.Spec.Key}
                    type="button"
                    onClick={() => void openSetting(item)}
                  >
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium">{item.Spec.Label || item.Spec.Key}</div>
                      <div className="truncate font-mono text-xs text-muted-foreground">{item.Spec.Key}</div>
                    </div>
                    <Badge variant={item.Configured === false ? "outline" : "secondary"}>
                      {item.Value || (item.Configured === false ? "not configured" : "empty")}
                    </Badge>
                  </button>
                ))}
              </div>
              {selectedSetting ? (
                <div className="space-y-3 rounded-lg border p-4">
                  <div>
                    <div className="font-medium">{selectedSetting.Spec.Label || selectedSetting.Spec.Key}</div>
                    <div className="font-mono text-xs text-muted-foreground">{selectedSetting.Spec.Key}</div>
                    {selectedSetting.Spec.Description ? (
                      <div className="mt-1 text-sm text-muted-foreground">
                        {selectedSetting.Spec.Description}
                      </div>
                    ) : null}
                  </div>
                  <Input
                    type={selectedSetting.Spec.Secret ? "password" : "text"}
                    placeholder={selectedSetting.Spec.Secret ? "Enter replacement secret" : "Value"}
                    value={settingValue}
                    onChange={(event) => setSettingValue(event.target.value)}
                  />
                  <div className="flex flex-wrap gap-2">
                    <Button
                      disabled={busy || !selectedSetting.Spec.Writable}
                      onClick={() => void updateSelectedSetting("set")}
                    >
                      Save setting
                    </Button>
                    {selectedSetting.Spec.Clearable ? (
                      <Button
                        disabled={busy}
                        variant="outline"
                        onClick={() => void updateSelectedSetting("unset")}
                      >
                        Clear
                      </Button>
                    ) : null}
                  </div>
                </div>
              ) : null}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Notifications</CardTitle>
              <CardDescription>
                Current notification readiness. Notification policy settings are
                editable above through their canonical keys.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <pre className="max-h-48 overflow-auto rounded-lg border bg-muted/30 p-3 text-xs">
                {JSON.stringify(notificationStatus ?? {}, null, 2)}
              </pre>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Telegram setup</CardTitle>
              <CardDescription>
                Store the bot token through protected input and authorize the
                first user through the canonical Telegram owner.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form className="grid gap-3 md:grid-cols-[1fr_14rem_auto]" onSubmit={setupTelegram}>
                <Input
                  autoComplete="new-password"
                  placeholder="Bot token"
                  type="password"
                  value={telegramToken}
                  onChange={(event) => setTelegramToken(event.target.value)}
                />
                <Input
                  inputMode="numeric"
                  placeholder="Authorized user ID"
                  value={telegramUserID}
                  onChange={(event) => setTelegramUserID(event.target.value)}
                />
                <Button disabled={busy || !telegramToken.trim() || !telegramUserID.trim()} type="submit">
                  Configure
                </Button>
              </form>
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent className="mt-6" value="environment">
          <Card>
            <CardHeader>
              <CardTitle>Managed execution environment</CardTitle>
              <CardDescription>
                Shell commands inherit the runtime environment. Additional
                executable search paths are prepended to PATH for foreground and
                background execution.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <SettingField
                label="Additional PATH entries"
                description="One absolute directory per line. These entries are prepended to the inherited process PATH."
              >
                <Textarea
                  className="min-h-40 font-mono"
                  placeholder={"/opt/custom/bin\n/usr/local/cuda/bin"}
                  value={config.shell.path.join("\n")}
                  onChange={(event) =>
                    setConfig({
                      ...config,
                      shell: {
                        ...config.shell,
                        path: parseLines(event.target.value),
                      },
                    })
                  }
                />
              </SettingField>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
      {dirty ? (
        <div className="sticky bottom-4 z-10 mx-auto flex max-w-2xl flex-col gap-3 rounded-xl border bg-background/95 p-3 shadow-lg backdrop-blur sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm">
            <div className="font-medium">Unsaved changes</div>
            <div className="text-xs text-muted-foreground">
              Review and save the current settings before leaving this page.
            </div>
          </div>
          <ButtonGroup className="self-end sm:self-auto">
            <Button
              disabled={busy}
              variant="outline"
              onClick={() => {
                setConfig(savedConfig)
                setMessage("")
                setError("")
              }}
            >
              <Undo2 />
              Reset
            </Button>
            <Button disabled={saveDisabled} onClick={() => void save()}>
              <Save />
              {busy ? "Saving..." : "Save"}
            </Button>
          </ButtonGroup>
        </div>
      ) : null}
    </div>
  )
}

function SettingField({
  label,
  description,
  children,
}: {
  label: string
  description?: string
  children: React.ReactNode
}) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      {children}
      {description ? <FieldDescription>{description}</FieldDescription> : null}
    </Field>
  )
}
function Toggle({
  label,
  description,
  checked,
  disabled = false,
  onCheckedChange,
}: {
  label: string
  description: string
  checked: boolean
  disabled?: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <Field orientation="horizontal">
      <div className="min-w-0 flex-1">
        <FieldLabel>{label}</FieldLabel>
        <FieldDescription>{description}</FieldDescription>
      </div>
      <Switch
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
      />
    </Field>
  )
}
function ExposureOption({
  value,
  title,
  description,
}: {
  value: string
  title: string
  description: string
}) {
  return (
    <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-muted/40">
      <RadioGroupItem className="mt-0.5" value={value} />
      <div>
        <div className="text-sm font-medium">{title}</div>
        <div className="text-sm text-muted-foreground">{description}</div>
      </div>
    </label>
  )
}
function AuthControl({
  label,
  configured,
  enabled,
  busy,
  locked = false,
  onRotate,
  onToggle,
}: {
  label: string
  configured: boolean
  enabled: boolean
  busy: boolean
  locked?: boolean
  onRotate: () => void
  onToggle: (enabled: boolean) => void
}) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <div className="text-sm font-medium">{label}</div>
          <Badge variant={configured ? "secondary" : "outline"}>
            {configured ? "Credential configured" : "Credential missing"}
          </Badge>
          <Badge variant={enabled ? "secondary" : "outline"}>
            {enabled ? "Enabled" : "Disabled"}
          </Badge>
        </div>
        <div className="mt-1 text-xs text-muted-foreground">
          Rotation reveals a new credential once. Existing credentials are never
          returned by ordinary status reads.
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button disabled={busy} size="sm" variant="outline" onClick={onRotate}>
          Rotate
        </Button>
        <Switch
          checked={enabled}
          disabled={busy || locked || (!configured && !enabled)}
          onCheckedChange={onToggle}
        />
      </div>
    </div>
  )
}
function normalizeConfig(value: PublicConfig): PublicConfig {
  return {
    ...value,
    http: {
      ...value.http,
      mcp: { ...value.http.mcp, enabled: value.http.mcp?.enabled ?? true },
    },
    shell: { path: value.shell?.path ?? [] },
  }
}
function parseLines(value: string) {
  return value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
}
function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
