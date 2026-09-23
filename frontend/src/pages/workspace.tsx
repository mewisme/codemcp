import { useEffect, useState } from "react"
import { ArrowLeft, FolderGit2, RefreshCw } from "lucide-react"
import { Link, NavLink, Outlet, useOutletContext, useParams } from "react-router-dom"
import { CopyButton } from "@/components/copy-button"
import { DetailRow } from "@/components/detail-row"
import { PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { WorkspaceContext } from "@/components/workspace-context"
import { WorkspaceExecutionDetail, WorkspaceExecutions } from "@/components/workspace-executions"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { RequestsPage } from "@/pages/requests"
import { adminApi, type Workspace } from "@/lib/api"
import { cn } from "@/lib/utils"

type WorkspaceOutletContext = { workspace: Workspace }

export function WorkspaceLayout() {
  const { workspaceID = "" } = useParams<{ workspaceID: string }>()
  const [workspace, setWorkspace] = useState<Workspace | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")

  async function load(refresh = false) {
    if (!workspaceID) return
    if (refresh) setRefreshing(true)
    try { setWorkspace(await adminApi.workspace(workspaceID)); setError("") } catch (value) { setError(errorText(value)) } finally { setLoading(false); setRefreshing(false) }
  }

  useEffect(() => { let active = true; if (!workspaceID) return; void adminApi.workspace(workspaceID).then((value) => { if (active) { setWorkspace(value); setError("") } }).catch((value) => { if (active) setError(errorText(value)) }).finally(() => { if (active) setLoading(false) }); return () => { active = false } }, [workspaceID])

  return <div className="space-y-6"><PageHeader title="Workspace" description={workspace?.path || workspaceID} actions={<><Button asChild size="sm" variant="outline"><Link to="/workspaces"><ArrowLeft />Workspaces</Link></Button><Button disabled={refreshing} size="sm" variant="outline" onClick={() => void load(true)}><RefreshCw className={refreshing ? "animate-spin" : ""} />Refresh</Button></>} /><PageError message={error} />{loading ? <PageLoading rows={4} /> : workspace ? <><WorkspaceNav workspaceID={workspace.id} /><Outlet context={{ workspace } satisfies WorkspaceOutletContext} /></> : null}</div>
}

export function WorkspaceOverviewPage() {
  const { workspace } = useWorkspaceContext()
  return <div className="rounded-xl border bg-card"><div className="flex items-center gap-3 border-b p-4"><div className="flex size-9 items-center justify-center rounded-lg bg-muted"><FolderGit2 className="size-4 text-muted-foreground" /></div><div className="min-w-0"><div className="truncate text-sm font-medium">{workspace.path}</div><div className="font-mono text-xs text-muted-foreground">{workspace.id}</div></div></div><div className="divide-y px-4"><DetailRow label="Path" value={<CopyValue value={workspace.path} />} /><DetailRow label="Workspace ID" value={<CopyValue value={workspace.id} />} /><DetailRow label="Allowed directories" value={workspace.allow_dirs?.length ? <div className="space-y-1">{workspace.allow_dirs.map((value) => <CopyValue key={value} value={value} />)}</div> : "None"} /></div></div>
}

export function WorkspaceContextPage() { const { workspace } = useWorkspaceContext(); return <WorkspaceContext workspaceID={workspace.id} /> }
export function WorkspaceRequestsPage() { const { workspace } = useWorkspaceContext(); return <RequestsPage workspaceID={workspace.id} /> }
export function WorkspaceActivityPage() { const { workspace } = useWorkspaceContext(); return <WorkspaceExecutions workspaceID={workspace.id} /> }
export function WorkspaceExecutionPage() { const { workspace } = useWorkspaceContext(); const { executionID = "" } = useParams<{ executionID: string }>(); return <WorkspaceExecutionDetail workspaceID={workspace.id} executionID={executionID} /> }

function WorkspaceNav({ workspaceID }: { workspaceID: string }) {
  const base = `/workspaces/${encodeURIComponent(workspaceID)}`
  return <ScrollArea className="w-full" scrollbars="horizontal"><div aria-label="Workspace sections" className="inline-flex h-8 w-max min-w-full items-center rounded-lg bg-muted p-[3px] text-muted-foreground" role="tablist"><WorkspaceNavLink end label="Overview" to={base} /><WorkspaceNavLink label="Context" to={`${base}/context`} /><WorkspaceNavLink label="Requests" to={`${base}/requests`} /><WorkspaceNavLink label="Activity" to={`${base}/activity`} /></div></ScrollArea>
}

function WorkspaceNavLink({ to, label, end = false }: { to: string; label: string; end?: boolean }) {
  return <NavLink end={end} role="tab" to={to} className={({ isActive }) => cn("inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center rounded-md px-2 py-0.5 text-sm font-medium whitespace-nowrap text-foreground/60 transition-all hover:text-foreground focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50", isActive && "bg-background text-foreground shadow-sm dark:bg-input/30")}>{label}</NavLink>
}

function useWorkspaceContext() { return useOutletContext<WorkspaceOutletContext>() }
function CopyValue({ value }: { value: string }) { return <div className="flex min-w-0 items-start gap-1"><span className="min-w-0 flex-1 break-all font-mono text-sm">{value}</span><CopyButton value={value} /></div> }
function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }
