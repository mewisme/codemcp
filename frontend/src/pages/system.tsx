import { type FormEvent, useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { JsonViewer } from "@/components/json-viewer"
import { PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { adminApi, type StatusOverview, type TelemetryStatus } from "@/lib/api"

export function SystemPage() {
  const [status, setStatus] = useState<StatusOverview | null>(null)
  const [telemetry, setTelemetry] = useState<TelemetryStatus | null>(null)
  const [doctor, setDoctor] = useState<unknown>(null)
  const [about, setAbout] = useState<unknown>(null)
  const [notificationStatus, setNotificationStatus] = useState<Record<
    string,
    unknown
  > | null>(null)
  const [telegramToken, setTelegramToken] = useState("")
  const [telegramUserID, setTelegramUserID] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")

  async function load() {
    try {
      const [
        nextStatus,
        nextTelemetry,
        nextDoctor,
        nextAbout,
        nextNotifications,
      ] = await Promise.all([
        adminApi.status(),
        adminApi.telemetry(),
        adminApi.doctor(),
        adminApi.about(),
        adminApi.notificationStatus(),
      ])
      setStatus(nextStatus)
      setTelemetry(nextTelemetry)
      setDoctor(nextDoctor)
      setAbout(nextAbout)
      setNotificationStatus(nextNotifications)
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
      adminApi.status(),
      adminApi.telemetry(),
      adminApi.doctor(),
      adminApi.about(),
      adminApi.notificationStatus(),
    ])
      .then(
        ([
          nextStatus,
          nextTelemetry,
          nextDoctor,
          nextAbout,
          nextNotifications,
        ]) => {
          if (!active) return
          setStatus(nextStatus)
          setTelemetry(nextTelemetry)
          setDoctor(nextDoctor)
          setAbout(nextAbout)
          setNotificationStatus(nextNotifications)
          setError("")
        }
      )
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

  async function runtime(action: "up" | "down" | "restart") {
    setBusy(action)
    try {
      await adminApi.runtimeAction(action)
      await load()
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  async function toggleTelemetry() {
    if (!telemetry) return
    setBusy("telemetry")
    try {
      setTelemetry(await adminApi.setTelemetry(!telemetry.persisted_enabled))
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  async function setupTelegram(event: FormEvent) {
    event.preventDefault()
    const token = telegramToken.trim()
    const userID = Number(telegramUserID)
    if (!token || !Number.isSafeInteger(userID) || userID <= 0) return
    setBusy("telegram-setup")
    try {
      await adminApi.telegramSetup(token, userID)
      setTelegramToken("")
      setTelegramUserID("")
      await load()
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  if (loading) return <PageLoading rows={6} />
  const telemetrySource = telemetry?.environment_override
    ? `Environment override · ${telemetry.source}`
    : `Persisted setting · ${telemetry?.source ?? "-"}`
  return (
    <div className="space-y-6">
      <PageHeader
        title="System"
        description="Canonical runtime, Telegram, telemetry, version, and diagnostic state."
        actions={
          <Button size="sm" variant="outline" onClick={() => void load()}>
            <RefreshCw />
            Refresh
          </Button>
        }
      />
      <PageError message={error} />
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <StateCard
          title="Runtime"
          active={status?.runtime_running}
          detail={status?.runtime_running ? "Running" : "Stopped"}
        />
        <StateCard
          title="MCP HTTP"
          active={status?.mcp_http_enabled}
          detail={status?.mcp_http_enabled ? "Enabled" : "Disabled"}
        />
        <StateCard
          title="Secure tunnel"
          active={status?.tunnel_ready}
          detail={
            !status?.tunnel_enabled
              ? "Disabled"
              : !status.tunnel_configured
                ? "Enabled, not configured"
                : status.tunnel_ready
                  ? "Ready"
                  : status.tunnel_running
                    ? "Running"
                    : "Configured, inactive"
          }
        />
        <StateCard
          title="Telegram"
          active={status?.telegram_healthy}
          detail={
            !status?.telegram_enabled
              ? "Disabled"
              : status.telegram_healthy
                ? "Healthy"
                : status.telegram_running
                  ? "Running, degraded"
                  : status.telegram_configured
                    ? "Configured, inactive"
                    : "Enabled, not configured"
          }
        />
        <StateCard
          title="Telegram topics"
          active={status?.telegram_topics_effective}
          detail={
            !status?.telegram_topics_enabled
              ? "Disabled"
              : !status.telegram_topics_supported
                ? "Enabled, unsupported"
                : status.telegram_topics_repairing
                  ? "Repairing"
                  : status.telegram_topics_error
                    ? "Degraded"
                    : status.telegram_topics_effective
                      ? "Effective"
                      : "Available, inactive"
          }
        />
        <StateCard
          title="Activity Mini App"
          active={status?.logs_mini_app_effective}
          detail={
            !status?.logs_mini_app_enabled
              ? "Disabled"
              : !status.logs_mini_app_available
                ? "Enabled, unavailable"
                : status.logs_mini_app_effective
                  ? "Effective"
                  : "Available, inactive"
          }
        />
        <StateCard
          title="TypeSafe"
          active={status?.typesafe_available}
          detail={
            !status?.typesafe_enabled
              ? "Disabled"
              : !status.typesafe_configured
                ? "Enabled, not configured"
                : status.typesafe_available
                  ? "Available"
                  : "Configured, unavailable"
          }
        />
        <StateCard
          title="Semantic approval"
          active={status?.semantic_approval_effective}
          detail={
            !status?.semantic_approval_enabled
              ? "Disabled"
              : status.semantic_approval_effective
                ? "Effective"
                : "Enabled, unavailable"
          }
        />
      </div>
      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Telegram setup</CardTitle>
            <CardDescription>
              Store the bot token through protected input and authorize the
              first user through the canonical Telegram owner.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="grid gap-3 md:grid-cols-[1fr_14rem_auto]"
              onSubmit={setupTelegram}
            >
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
              <Button
                disabled={
                  busy === "telegram-setup" ||
                  !telegramToken.trim() ||
                  !telegramUserID.trim()
                }
                type="submit"
              >
                {busy === "telegram-setup" ? "Configuring..." : "Configure"}
              </Button>
            </form>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Notifications</CardTitle>
            <CardDescription>
              Current notification readiness from the canonical notification
              owner.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <JsonViewer value={notificationStatus ?? {}} />
          </CardContent>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Managed runtime</CardTitle>
          <CardDescription>
            Actions use the canonical managed runtime lifecycle.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          <Button
            disabled={Boolean(busy) || status?.runtime_running}
            onClick={() => void runtime("up")}
          >
            Start
          </Button>
          <Button
            disabled={Boolean(busy) || !status?.runtime_running}
            variant="outline"
            onClick={() => void runtime("restart")}
          >
            Restart
          </Button>
          <Button
            disabled={Boolean(busy) || !status?.runtime_running}
            variant="outline"
            onClick={() => void runtime("down")}
          >
            Stop
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Product telemetry</CardTitle>
          <CardDescription>
            Effective state comes from the server read model, including
            environment overrides.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3">
          <Badge
            variant={telemetry?.effective_enabled ? "secondary" : "outline"}
          >
            {telemetry?.effective_enabled ? "Enabled" : "Disabled"}
          </Badge>
          <span className="text-sm text-muted-foreground">
            {telemetrySource}
          </span>
          <Button
            disabled={busy === "telemetry" || telemetry?.environment_override}
            size="sm"
            variant="outline"
            onClick={() => void toggleTelemetry()}
          >
            {telemetry?.persisted_enabled ? "Disable" : "Enable"}
          </Button>
        </CardContent>
      </Card>
      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>About</CardTitle>
          </CardHeader>
          <CardContent>
            <JsonViewer value={about} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Doctor</CardTitle>
          </CardHeader>
          <CardContent>
            <JsonViewer value={doctor} />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function StateCard({
  title,
  active,
  detail,
}: {
  title: string
  active?: boolean
  detail: string
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardDescription>{title}</CardDescription>
        <CardTitle className="text-base">{detail}</CardTitle>
      </CardHeader>
      <CardContent>
        <Badge variant={active ? "secondary" : "outline"}>
          {active ? "Active" : "Inactive"}
        </Badge>
      </CardContent>
    </Card>
  )
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
