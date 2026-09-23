import { createBrowserRouter, Navigate, type RouteObject } from "react-router-dom"
import { App } from "@/App"
import { PageLoading } from "@/components/page-state"
import { navItems, type AdminRouteHandle } from "@/lib/admin-navigation"

function navHandle(id: string): AdminRouteHandle {
  const item = navItems.find((value) => value.id === id)
  if (!item) throw new Error(`Unknown admin navigation item: ${id}`)
  return { title: item.title, description: item.description }
}

export const adminRoutes: RouteObject[] = [{
  path: "/",
  element: <App />,
  hydrateFallbackElement: <div className="p-6"><PageLoading rows={6} /></div>,
  children: [
    { index: true, element: <Navigate replace to="/overview" /> },
    { path: "index.html", element: <Navigate replace to="/overview" /> },
    { path: "overview", lazy: () => import("@/pages/overview").then((module) => ({ Component: module.OverviewPage })), handle: navHandle("overview") },
    { path: "workspaces", lazy: () => import("@/pages/workspaces").then((module) => ({ Component: module.WorkspacesPage })), handle: navHandle("workspaces") },
    { path: "instructions", lazy: () => import("@/pages/global-instructions").then((module) => ({ Component: module.GlobalInstructionsPage })), handle: navHandle("instructions") },
    { path: "workspaces/global", element: <Navigate replace to="/instructions" /> },
    {
      path: "workspaces/:workspaceID",
      lazy: () => import("@/pages/workspace").then((module) => ({ Component: module.WorkspaceLayout })),
      handle: { title: "Workspace", description: "Inspect this registered workspace and its workspace-scoped tools." } satisfies AdminRouteHandle,
      children: [
        { index: true, lazy: () => import("@/pages/workspace").then((module) => ({ Component: module.WorkspaceOverviewPage })) },
        { path: "context", lazy: () => import("@/pages/workspace").then((module) => ({ Component: module.WorkspaceContextPage })), handle: { title: "Project Context", description: "Preview the effective instruction context for this workspace." } satisfies AdminRouteHandle },
        { path: "requests", lazy: () => import("@/pages/workspace").then((module) => ({ Component: module.WorkspaceRequestsPage })), handle: { title: "Workspace Requests", description: "Review control approval requests scoped to this workspace." } satisfies AdminRouteHandle },
        { path: "activity", lazy: () => import("@/pages/workspace").then((module) => ({ Component: module.WorkspaceActivityPage })), handle: { title: "Workspace Activity", description: "Inspect activity and command executions scoped to this workspace." } satisfies AdminRouteHandle },
        { path: "activity/:executionID", lazy: () => import("@/pages/workspace").then((module) => ({ Component: module.WorkspaceExecutionPage })), handle: { title: "Command Execution", description: "Inspect one run_command execution and its live output." } satisfies AdminRouteHandle },
      ],
    },
    { path: "tools", lazy: () => import("@/pages/tools").then((module) => ({ Component: module.ToolsPage })), handle: navHandle("tools") },
    { path: "servers", lazy: () => import("@/pages/servers").then((module) => ({ Component: module.ServersPage })), handle: navHandle("servers") },
    { path: "tunnel", lazy: () => import("@/pages/tunnel").then((module) => ({ Component: module.TunnelPage })), handle: navHandle("tunnel") },
    { path: "activity", lazy: () => import("@/pages/activity").then((module) => ({ Component: module.ActivityPage })), handle: navHandle("activity") },
    { path: "activity/:callID", lazy: () => import("@/pages/activity").then((module) => ({ Component: module.ActivityCallPage })), handle: { title: "Tool Call", description: "Inspect one tool call and its complete runtime metadata." } satisfies AdminRouteHandle },
    { path: "settings", lazy: () => import("@/pages/settings").then((module) => ({ Component: module.SettingsPage })), handle: navHandle("settings") },
    { path: "*", element: <Navigate replace to="/overview" /> },
  ],
}]

export function createAdminRouter() { return createBrowserRouter(adminRoutes) }
