import { useEffect, useMemo, useState } from "react"
import { createColumnHelper } from "@tanstack/react-table"
import { RefreshCw, Search, Wrench } from "lucide-react"
import { DataTable } from "@/components/data-table"
import { DataTableColumnHeader } from "@/components/data-table-column-header"
import type { DataTableFeatures } from "@/components/data-table-features"
import { DetailRow } from "@/components/detail-row"
import { JsonViewer } from "@/components/json-viewer"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { TruncatedText } from "@/components/truncated-text"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemHeader, ItemTitle } from "@/components/ui/item"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ScrollableTabsList, Tabs, TabsContent, TabsTrigger } from "@/components/ui/tabs"
import { useIsMobile } from "@/hooks/use-mobile"
import { adminApi, type Tool } from "@/lib/api"

const columnHelper = createColumnHelper<DataTableFeatures, Tool>()
const columns = columnHelper.columns([
  columnHelper.accessor("name", { header: ({ column }) => <DataTableColumnHeader column={column} title="Name" />, cell: ({ row }) => <div className="min-w-0"><TruncatedText className="font-mono text-sm font-medium" lines={1}>{row.original.name}</TruncatedText>{row.original.title && row.original.title !== row.original.name ? <TruncatedText className="mt-1 text-xs text-muted-foreground" lines={1}>{row.original.title}</TruncatedText> : null}</div> }),
  columnHelper.accessor("description", { header: "Description", cell: ({ getValue }) => <TruncatedText className="text-sm text-muted-foreground" lines={2}>{getValue() || "No description."}</TruncatedText> }),
  columnHelper.display({ id: "hints", header: "Hints", cell: ({ row }) => <ToolHints tool={row.original} /> }),
])

type ToolFilter = "all" | "read" | "write" | "destructive"

export function ToolsPage() {
  const mobile = useIsMobile()
  const [tools, setTools] = useState<Tool[]>([])
  const [selected, setSelected] = useState<Tool | null>(null)
  const [query, setQuery] = useState("")
  const [filter, setFilter] = useState<ToolFilter>("all")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  async function load(manual = false) { if (manual) setBusy(true); try { setTools(await adminApi.tools()); setError("") } catch (value) { setError(errorText(value)) } finally { setLoading(false); setBusy(false) } }
  useEffect(() => { let active = true; void adminApi.tools().then((next) => { if (active) { setTools(next); setError("") } }).catch((value) => { if (active) setError(errorText(value)) }).finally(() => { if (active) setLoading(false) }); return () => { active = false } }, [])

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return tools.filter((tool) => {
      const readOnly = tool.annotations?.readOnlyHint === true
      const destructive = tool.annotations?.destructiveHint === true
      if (filter === "read" && !readOnly) return false
      if (filter === "write" && readOnly) return false
      if (filter === "destructive" && !destructive) return false
      return !needle || [tool.name, tool.title, tool.description].some((value) => value?.toLowerCase().includes(needle))
    })
  }, [filter, query, tools])

  return <div className="space-y-6"><PageHeader title="Tools" description="Inspect every tool exposed by the local runtime and enabled upstream servers, including schemas and behavioral hints." actions={<><Badge variant="secondary">{tools.length} tools</Badge><Button disabled={busy} size="sm" variant="outline" onClick={() => void load(true)}><RefreshCw className={busy ? "animate-spin" : ""} />Refresh</Button></>} /><PageError message={error} /><div className="flex flex-col gap-2 sm:flex-row"><div className="relative min-w-0 flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input className="pl-9" placeholder="Search by name, title, or description..." value={query} onChange={(event) => setQuery(event.target.value)} /></div><Select value={filter} onValueChange={(value) => setFilter(value as ToolFilter)}><SelectTrigger className="w-full sm:w-44"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">All tools</SelectItem><SelectItem value="read">Read only</SelectItem><SelectItem value="write">Writes</SelectItem><SelectItem value="destructive">Destructive</SelectItem></SelectContent></Select></div>{loading ? <PageLoading rows={6} /> : filtered.length === 0 ? <PageEmpty icon={Wrench} title={tools.length ? "No matching tools" : "No tools exposed"} description={tools.length ? "Try another search term or filter." : "Register a workspace or enable an upstream server to expose tools."} /> : mobile ? <ToolMobileList tools={filtered} onSelect={setSelected} /> : <DataTable columns={columns} data={filtered} onRowClick={setSelected} pageSize={20} />}{selected ? <ToolDetail open tool={selected} onOpenChange={(open) => { if (!open) setSelected(null) }} /> : null}</div>
}

function ToolMobileList({ tools, onSelect }: { tools: Tool[]; onSelect: (tool: Tool) => void }) {
  return <ItemGroup>{tools.map((tool) => <Item className="cursor-pointer" key={tool.name} role="button" tabIndex={0} variant="outline" onClick={() => onSelect(tool)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") onSelect(tool) }}><ItemContent className="min-w-0"><ItemHeader><ItemTitle className="min-w-0 font-mono">{tool.name}</ItemTitle><ToolHints tool={tool} /></ItemHeader><ItemDescription>{tool.description || "No description."}</ItemDescription>{tool.title && tool.title !== tool.name ? <div className="text-xs text-muted-foreground">{tool.title}</div> : null}</ItemContent></Item>)}</ItemGroup>
}

function ToolDetail({ tool, open, onOpenChange }: { tool: Tool; open: boolean; onOpenChange: (open: boolean) => void }) {
  return <ResponsiveDialog open={open} onOpenChange={onOpenChange} title={tool.title || tool.name} description={tool.name} scrollbars="both"><Tabs defaultValue="overview"><ScrollableTabsList><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="input">Input schema</TabsTrigger><TabsTrigger value="output">Output schema</TabsTrigger><TabsTrigger value="annotations">Annotations</TabsTrigger></ScrollableTabsList><TabsContent className="mt-4 divide-y" value="overview"><DetailRow label="Name" value={tool.name} mono /><DetailRow label="Title" value={tool.title || "-"} /><DetailRow label="Description" value={<span className="whitespace-pre-wrap">{tool.description || "No description."}</span>} /><DetailRow label="Hints" value={<ToolHints tool={tool} />} /></TabsContent><TabsContent className="mt-4" value="input"><JsonViewer value={tool.inputSchema ?? {}} /></TabsContent><TabsContent className="mt-4" value="output"><JsonViewer value={tool.outputSchema ?? {}} /></TabsContent><TabsContent className="mt-4" value="annotations"><JsonViewer value={tool.annotations ?? {}} /></TabsContent></Tabs></ResponsiveDialog>
}

function ToolHints({ tool }: { tool: Tool }) {
  const readOnly = tool.annotations?.readOnlyHint === true
  const destructive = tool.annotations?.destructiveHint === true
  return <div className="flex flex-wrap gap-1">{readOnly ? <Badge variant="outline">Read only</Badge> : <Badge variant="secondary">Writes</Badge>}{destructive ? <Badge variant="destructive">Destructive</Badge> : null}</div>
}

function errorText(value: unknown) { return value instanceof Error ? value.message : String(value) }
