import {
  Activity,
  BrainCircuit,
  CheckCircle2,
  Cloud,
  FileText,
  FolderGit2,
  Home,
  Plug,
  ScrollText,
  Server,
  Settings,
  Wrench,
  MonitorCog,
  type LucideIcon,
} from "lucide-react"

export type NavItem = {
  id: string
  path: string
  title: string
  description: string
  icon: LucideIcon
  parent?: string
}
export type AdminRouteHandle = Pick<NavItem, "title" | "description"> & {
  component?: string
}

export const adminAppTitle = "CodeMCP"

export function adminDocumentTitle(title: string) {
  return `${title} | ${adminAppTitle}`
}

export const navItems: NavItem[] = [
  {
    id: "overview",
    path: "/overview",
    title: "Overview",
    description: "Runtime health and configuration at a glance.",
    icon: Home,
  },
  {
    id: "system",
    path: "/system",
    title: "System",
    description:
      "Runtime lifecycle, Telegram health, telemetry, and diagnostics.",
    icon: MonitorCog,
  },
  {
    id: "logs",
    path: "/logs",
    title: "Logs",
    description: "Inspect and clear the canonical runtime event journal.",
    icon: ScrollText,
  },
  {
    id: "integrations",
    path: "/integrations",
    title: "Integrations",
    description:
      "Manage first-party RTK, CodeGraph, and TypeSafe integrations.",
    icon: Plug,
  },
  {
    id: "llm",
    path: "/llm",
    title: "LLM",
    description: "Manage inference providers, models, credentials, and readiness.",
    icon: BrainCircuit,
  },
  {
    id: "workspaces",
    path: "/workspaces",
    title: "Workspaces",
    description: "Manage canonical project roots and workspace handles.",
    icon: FolderGit2,
  },
  {
    id: "prompts",
    path: "/prompts",
    title: "Prompts",
    description: "Manage global and workspace Prompt definitions.",
    icon: FileText,
  },
  {
    id: "tools",
    path: "/tools",
    title: "Tools",
    description: "Inspect the tools currently exposed by this runtime.",
    icon: Wrench,
  },
  {
    id: "upstreams",
    path: "/upstreams",
    title: "Upstreams",
    description: "Configure Upstream servers, health, tools, and OAuth.",
    icon: Server,
  },
  {
    id: "tunnel",
    path: "/tunnel",
    title: "Tunnel",
    description: "Configure and monitor the OpenAI Secure MCP Tunnel.",
    icon: Cloud,
  },
  {
    id: "activity",
    path: "/activity",
    title: "Activity",
    description: "Watch live MCP requests and tool execution events.",
    icon: Activity,
  },
  {
    id: "completions",
    path: "/completions",
    title: "Completions",
    description:
      "Inspect durable agent completion history and live accepted completion events.",
    icon: CheckCircle2,
  },
  {
    id: "settings",
    path: "/settings",
    title: "Settings",
    description: "Configure listeners, runtime behavior, and authentication.",
    icon: Settings,
  },
]
