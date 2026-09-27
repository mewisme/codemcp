import { useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { JsonViewer } from "@/components/json-viewer"
import { PageError, PageLoading } from "@/components/page-state"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { adminApi, type CodeGraphWorkspaceStatus } from "@/lib/api"

export function WorkspaceCodeGraph({ workspaceID }: { workspaceID: string }) {
  const [status, setStatus] = useState<CodeGraphWorkspaceStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")
  async function load() {
    try {
      setStatus(await adminApi.codeGraphWorkspace(workspaceID))
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    let active = true
    void adminApi
      .codeGraphWorkspace(workspaceID)
      .then((nextStatus) => {
        if (active) {
          setStatus(nextStatus)
          setError("")
        }
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
  }, [workspaceID])
  async function act(action: "init" | "sync") {
    setBusy(action)
    try {
      setStatus(await adminApi.codeGraphWorkspaceAction(workspaceID, action))
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }
  if (loading) return <PageLoading rows={4} />
  return (
    <div className="space-y-4">
      <PageError message={error} />
      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <CardTitle>CodeGraph workspace</CardTitle>
              <CardDescription>
                Canonical index lifecycle for this registered workspace.
              </CardDescription>
            </div>
            <Button size="sm" variant="outline" onClick={() => void load()}>
              <RefreshCw />
              Refresh
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <JsonViewer value={status} />
          <div className="flex gap-2">
            <Button disabled={Boolean(busy)} onClick={() => void act("init")}>
              Initialize
            </Button>
            <Button
              disabled={Boolean(busy)}
              variant="outline"
              onClick={() => void act("sync")}
            >
              Sync
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
