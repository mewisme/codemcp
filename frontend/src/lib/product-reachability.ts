import { browserOperationBindings } from "@/lib/operations"
import { canonicalPresentationContract } from "@/lib/operation-presentation.generated"

export type BrowserAdapterState = "live" | "gap"

export type BrowserConfirmationEvidence = {
  consumer: string
  source: string
  marker: string
}

export type CanonicalOperationPresentation = {
  operation: string
  title: string
  subject: string
  category: "read" | "create" | "change" | "delete" | "run" | "review"
  danger: "none" | "caution" | "destructive"
  confirmation: "none" | "recommended" | "required" | "review-decision"
  input: "none" | "resource" | "form" | "setting" | "protected-secret" | "decision"
  secret_policy?: "protected-input" | "masked-read" | "one-time-output"
  secret_recovery?: string
  unavailable_reason?: string
}

const canonicalOperationPresentation = canonicalPresentationContract.operations as Record<
  string,
  CanonicalOperationPresentation
>

export type BrowserAdapterDescriptor = {
  operation: string
  state: BrowserAdapterState
  route: string
  component: string
  action: string
  api: { method: string; pattern: string }[]
  presentation: CanonicalOperationPresentation
  confirmation?: BrowserConfirmationEvidence
  secretPolicy?: "protected-input" | "masked-read" | "one-time-output"
  secretRecovery?: string
  gap?: string
}

type BrowserSurface = {
  route: string
  component: string
}

const browserSurfaces: { prefix: string; surface: BrowserSurface }[] = [
  {
    prefix: "integration.codegraph.workspace.",
    surface: { route: "/workspaces/:workspaceID/codegraph", component: "WorkspaceCodeGraphPage" },
  },
  {
    prefix: "execution.",
    surface: { route: "/workspaces/:workspaceID/activity", component: "WorkspaceActivityPage" },
  },
  {
    prefix: "process.",
    surface: { route: "/workspaces/:workspaceID/processes", component: "WorkspaceProcessesPage" },
  },
  {
    prefix: "project.context.",
    surface: { route: "/workspaces/:workspaceID/context", component: "WorkspaceContextPage" },
  },
  {
    prefix: "request.",
    surface: { route: "/workspaces/:workspaceID/requests", component: "WorkspaceRequestsPage" },
  },
  { prefix: "workspace.", surface: { route: "/workspaces", component: "WorkspacesPage" } },
  { prefix: "upstream.", surface: { route: "/upstreams", component: "UpstreamsPage" } },
  { prefix: "tunnel.", surface: { route: "/tunnel", component: "TunnelPage" } },
  { prefix: "integration.", surface: { route: "/integrations", component: "IntegrationsPage" } },
  { prefix: "llm.", surface: { route: "/llm", component: "LLMPage" } },
  { prefix: "logs.", surface: { route: "/logs", component: "LogsPage" } },
  { prefix: "activity.", surface: { route: "/activity", component: "ActivityPage" } },
  { prefix: "completion.", surface: { route: "/completions", component: "CompletionsPage" } },
  { prefix: "instructions.", surface: { route: "/instructions", component: "GlobalInstructionsPage" } },
  { prefix: "prompt.", surface: { route: "/prompts", component: "PromptsPage" } },
  { prefix: "tools.", surface: { route: "/tools", component: "ToolsPage" } },
  { prefix: "config.", surface: { route: "/settings", component: "SettingsPage" } },
  { prefix: "auth.", surface: { route: "/settings", component: "SettingsPage" } },
  { prefix: "notification.", surface: { route: "/settings", component: "SettingsPage" } },
  { prefix: "telegram.", surface: { route: "/settings", component: "SettingsPage" } },
  { prefix: "telemetry.", surface: { route: "/settings", component: "SettingsPage" } },
  { prefix: "network.", surface: { route: "/system", component: "SystemPage" } },
  { prefix: "runtime.", surface: { route: "/system", component: "SystemPage" } },
  { prefix: "install.", surface: { route: "/system", component: "SystemPage" } },
  { prefix: "update.", surface: { route: "/system", component: "SystemPage" } },
  { prefix: "doctor.", surface: { route: "/system", component: "SystemPage" } },
  { prefix: "health.", surface: { route: "/overview", component: "OverviewPage" } },
  { prefix: "status.", surface: { route: "/overview", component: "OverviewPage" } },
  { prefix: "version.", surface: { route: "/overview", component: "OverviewPage" } },
]

export const browserDestructiveOperations = new Set(
  Object.values(canonicalOperationPresentation)
    .filter((presentation) => presentation.danger === "destructive")
    .map((presentation) => presentation.operation)
)

export const browserConfirmationEvidence: Record<
  string,
  BrowserConfirmationEvidence
> = {
  "logs.clear": {
    consumer: "LogsPage confirmation",
    source: "../pages/logs.tsx",
    marker: "Delete runtime log journal?",
  },
  "process.clear": {
    consumer: "WorkspaceProcesses confirmation",
    source: "../components/workspace-processes.tsx",
    marker: 'window.confirm("Clear this finished process record?")',
  },
  "request.grant.revoke": {
    consumer: "RequestsPage grant confirmation",
    source: "../pages/requests.tsx",
    marker: 'window.confirm("Revoke this runtime grant?")',
  },
  "prompt.delete": {
    consumer: "PromptsPage confirmation",
    source: "../pages/prompts.tsx",
    marker: "window.confirm(",
  },
  "workspace.container.delete": {
    consumer: "WorkspacesPage confirmation",
    source: "../pages/workspaces.tsx",
    marker: 'kind: "delete-container"',
  },
  "workspace.purge": {
    consumer: "WorkspacesPage confirmation",
    source: "../pages/workspaces.tsx",
    marker: 'kind: "purge"',
  },
  "upstream.server.remove": {
    consumer: "UpstreamsPage confirmation",
    source: "../pages/servers.tsx",
    marker: "<AlertDialog open={Boolean(removeTarget)}",
  },
  "tunnel.delete": {
    consumer: "TunnelPage confirmation",
    source: "../pages/tunnel.tsx",
    marker: "<AlertDialog open={Boolean(deleteID)}",
  },
  "llm.provider.remove": {
    consumer: "LLMPage confirmation",
    source: "../pages/llm.tsx",
    marker: "<AlertDialog open={Boolean(removeTarget)}",
  },
  "integration.cf.remove": {
    consumer: "IntegrationsPage confirmation",
    source: "../pages/integrations.tsx",
    marker: "<AlertDialog open={removeOpen}",
  },
}

export function browserProductReachability(): BrowserAdapterDescriptor[] {
  const grouped = new Map<string, BrowserAdapterDescriptor>()
  for (const binding of browserOperationBindings()) {
    const surface = browserSurface(binding.operation)
    if (!surface) continue
    const presentation = canonicalOperationPresentation[binding.operation]
    if (!presentation) continue
    const confirmation = browserConfirmationEvidence[binding.operation]
    const destructive = presentation.danger === "destructive"
    const gap =
      destructive && !confirmation
        ? "destructive adapter does not consume canonical confirmation"
        : undefined
    const adapterPresentation = gap
      ? { ...presentation, unavailable_reason: gap }
      : presentation
    const current = grouped.get(binding.operation) ?? {
      operation: binding.operation,
      state: destructive && !confirmation ? "gap" : "live",
      route: surface.route,
      component: surface.component,
      action: binding.operation,
      api: [],
      presentation: adapterPresentation,
      confirmation,
      secretPolicy: presentation.secret_policy,
      secretRecovery: presentation.secret_recovery,
      gap,
    }
    current.api.push({ method: binding.method, pattern: binding.pattern })
    grouped.set(binding.operation, current)
  }
  return [...grouped.values()].sort((left, right) =>
    left.operation.localeCompare(right.operation)
  )
}

export function browserSurface(operation: string): BrowserSurface | undefined {
  return browserSurfaces.find(({ prefix }) => operation.startsWith(prefix))?.surface
}
