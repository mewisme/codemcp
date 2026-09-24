import { Activity, Cloud, FileText, FolderGit2, Home, Server, Settings, Wrench, type LucideIcon } from "lucide-react"

export type NavItem = { id: string; path: string; title: string; description: string; icon: LucideIcon; parent?: string }
export type AdminRouteHandle = Pick<NavItem, "title" | "description">

export const adminAppTitle = "CodeMCP"

export function adminDocumentTitle(title: string) { return `${title} | ${adminAppTitle}` }

export const navItems: NavItem[] = [
  { id: "overview", path: "/overview", title: "Overview", description: "Runtime health and configuration at a glance.", icon: Home },
  { id: "workspaces", path: "/workspaces", title: "Workspaces", description: "Manage canonical project roots and workspace handles.", icon: FolderGit2 },
  { id: "instructions", path: "/instructions", title: "Global Instructions", description: "Manage global context, rules, and detected user instruction sources.", icon: FileText },
  { id: "tools", path: "/tools", title: "Tools", description: "Inspect the tools currently exposed by this runtime.", icon: Wrench },
  { id: "upstreams", path: "/upstreams", title: "Upstreams", description: "Configure Upstream servers, health, tools, and OAuth.", icon: Server },
  { id: "tunnel", path: "/tunnel", title: "Tunnel", description: "Configure and monitor the OpenAI Secure MCP Tunnel.", icon: Cloud },
  { id: "activity", path: "/activity", title: "Activity", description: "Watch live MCP requests and tool execution events.", icon: Activity },
  { id: "settings", path: "/settings", title: "Settings", description: "Configure listeners, runtime behavior, and authentication.", icon: Settings },
]
