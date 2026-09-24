import { useEffect, useMemo, useState } from "react"
import { Save, Undo2 } from "lucide-react"
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
import { adminApi, type NetworkInterface, type PublicConfig } from "@/lib/api"

export function SettingsPage() {
  const [config, setConfig] = useState<PublicConfig | null>(null)
  const [savedConfig, setSavedConfig] = useState<PublicConfig | null>(null)
  const [tunnelEnabled, setTunnelEnabled] = useState(false)
  const [interfaces, setInterfaces] = useState<NetworkInterface[]>([])
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")

  useEffect(() => {
    void Promise.all([
      adminApi.config(),
      adminApi.networkInterfaces(),
      adminApi.tunnelConfig(),
    ])
      .then(([nextConfig, nextInterfaces, nextTunnel]) => {
        const normalized = normalizeConfig(nextConfig)
        setConfig(normalized)
        setSavedConfig(normalized)
        setInterfaces(nextInterfaces)
        setTunnelEnabled(nextTunnel.enabled)
      })
      .catch((value) => setError(errorText(value)))
  }, [])

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

  function setExposureMode(mode: PublicConfig["server"]["expose"]["mode"]) {
    if (!config) return
    const current = config.server.expose.interfaces
    const exposed = mode !== "none"
    setConfig({
      ...config,
      server: {
        ...config.server,
        expose: { mode, interfaces: mode === "interfaces" ? current : [] },
      },
      auth: exposed
        ? {
            ...config.auth,
            mcp_enabled: config.server.enabled ? true : config.auth.mcp_enabled,
            admin_enabled: config.admin.enabled
              ? true
              : config.auth.admin_enabled,
          }
        : config.auth,
    })
  }

  function toggleInterface(name: string, checked: boolean) {
    if (!config) return
    const selected = new Set(config.server.expose.interfaces)
    if (checked) selected.add(name)
    else selected.delete(name)
    setConfig({
      ...config,
      server: {
        ...config.server,
        expose: { mode: "interfaces", interfaces: [...selected].sort() },
      },
    })
  }

  if (!config || !savedConfig)
    return (
      <div className="text-sm text-muted-foreground">
        {error || "Loading settings..."}
      </div>
    )
  const selectedInterfaces = new Set(config.server.expose.interfaces)
  const exposed = config.server.expose.mode !== "none"
  const exposureAuthReady =
    !exposed ||
    ((!config.server.enabled ||
      (config.auth.mcp_enabled && config.auth.mcp_token_configured)) &&
      (!config.admin.enabled ||
        (config.auth.admin_enabled && config.auth.admin_token_configured)))
  const saveDisabled =
    busy ||
    (!config.server.enabled && !tunnelEnabled) ||
    (config.server.expose.mode === "interfaces" &&
      config.server.expose.interfaces.length === 0) ||
    !exposureAuthReady ||
    (exposed && !config.server.allow_insecure_http)

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
                  checked={config.server.enabled}
                  disabled={config.server.enabled && !tunnelEnabled}
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      server: { ...config.server, enabled },
                    })
                  }
                />
                <div className="flex flex-wrap gap-2">
                  <Badge
                    variant={config.server.enabled ? "secondary" : "outline"}
                  >
                    MCP HTTP {config.server.enabled ? "enabled" : "disabled"}
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
                      disabled={!config.server.enabled}
                      max={65535}
                      min={1}
                      type="number"
                      value={config.server.port}
                      onChange={(event) =>
                        setConfig({
                          ...config,
                          server: {
                            ...config.server,
                            port: Number(event.target.value),
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
                      value={config.admin.port}
                      onChange={(event) =>
                        setConfig({
                          ...config,
                          admin: {
                            ...config.admin,
                            port: Number(event.target.value),
                          },
                        })
                      }
                    />
                  </SettingField>
                </div>
                <Toggle
                  label="Admin enabled"
                  description="Serve the local Admin API and dashboard."
                  checked={config.admin.enabled}
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      admin: { ...config.admin, enabled },
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
                value={config.server.expose.mode}
                onValueChange={(value) =>
                  setExposureMode(
                    value as PublicConfig["server"]["expose"]["mode"]
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
              {config.server.expose.mode === "interfaces" ? (
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
                    checked={config.server.allow_insecure_http}
                    onCheckedChange={(allow_insecure_http) =>
                      setConfig({
                        ...config,
                        server: { ...config.server, allow_insecure_http },
                      })
                    }
                  />
                  {!config.server.allow_insecure_http ? (
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
        <TabsContent className="mt-6" value="authentication">
          <Card>
            <CardHeader>
              <CardTitle>Authentication</CardTitle>
              <CardDescription>
                Authentication is mandatory for direct network exposure; Admin
                authentication is mandatory when its endpoint is exposed.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <AuthToggle
                  locked={exposed && config.server.enabled}
                  label="MCP authentication"
                  configured={config.auth.mcp_token_configured}
                  checked={config.auth.mcp_enabled}
                  command="cm auth mcp create"
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      auth: { ...config.auth, mcp_enabled: enabled },
                    })
                  }
                />
                <AuthToggle
                  locked={exposed && config.admin.enabled}
                  label="Admin authentication"
                  configured={config.auth.admin_token_configured}
                  checked={config.auth.admin_enabled}
                  command="cm auth admin create"
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      auth: { ...config.auth, admin_enabled: enabled },
                    })
                  }
                />
              </FieldGroup>
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
function AuthToggle({
  label,
  configured,
  command,
  checked,
  locked = false,
  onCheckedChange,
}: {
  label: string
  configured: boolean
  command: string
  checked: boolean
  locked?: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <Field orientation="horizontal">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <FieldLabel>{label}</FieldLabel>
          <Badge variant={configured ? "secondary" : "outline"}>
            {configured ? "Token configured" : "Token missing"}
          </Badge>
        </div>
        {!configured ? (
          <FieldDescription className="font-mono">{command}</FieldDescription>
        ) : null}
      </div>
      <Switch
        checked={checked}
        disabled={locked || (!configured && !checked)}
        onCheckedChange={onCheckedChange}
      />
    </Field>
  )
}
function normalizeConfig(value: PublicConfig): PublicConfig {
  return {
    ...value,
    server: { ...value.server, enabled: value.server?.enabled ?? true },
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
