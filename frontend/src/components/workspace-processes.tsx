import { useEffect, useState } from "react"
import { RefreshCw, Trash2 } from "lucide-react"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { adminApi, type ProcessInfo } from "@/lib/api"

export function WorkspaceProcesses({ workspaceID }: { workspaceID: string }) {
  const [items, setItems] = useState<ProcessInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")

  async function load() {
    try {
      setItems(await adminApi.workspaceProcesses(workspaceID))
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
      .workspaceProcesses(workspaceID)
      .then((nextItems) => {
        if (active) {
          setItems(nextItems)
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

  async function clear(id: string) {
    setBusy(id)
    try {
      await adminApi.clearWorkspaceProcess(workspaceID, id)
      await load()
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy("")
    }
  }

  if (loading) return <PageLoading rows={4} />
  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button size="sm" variant="outline" onClick={() => void load()}>
          <RefreshCw />
          Refresh processes
        </Button>
      </div>
      <PageError message={error} />
      {items.length === 0 ? (
        <PageEmpty
          title="No background processes"
          description="Background commands for this workspace will appear here."
        />
      ) : (
        items.map((item) => (
          <Card key={item.id}>
            <CardHeader>
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <CardTitle className="font-mono text-sm">
                    {item.command}
                  </CardTitle>
                  <CardDescription>
                    {item.id} · PID {item.pid}
                  </CardDescription>
                </div>
                <Badge variant={item.running ? "secondary" : "outline"}>
                  {item.running
                    ? "running"
                    : item.exit_code == null
                      ? "finished"
                      : `exit ${item.exit_code}`}
                </Badge>
              </div>
            </CardHeader>
            <CardContent className="flex flex-wrap items-center justify-between gap-3">
              <div className="text-xs text-muted-foreground">
                {item.cwd} · {item.started_at}
              </div>
              <Button
                disabled={item.running || busy === item.id}
                size="sm"
                variant="outline"
                onClick={() => void clear(item.id)}
              >
                <Trash2 />
                Clear
              </Button>
            </CardContent>
          </Card>
        ))
      )}
    </div>
  )
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
