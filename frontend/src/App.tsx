import { lazy, Suspense, useEffect, useState } from "react"
import { LoaderCircle } from "lucide-react"
import { Outlet, useMatches } from "react-router-dom"
import { AppSidebar } from "@/components/app-sidebar"
import { RequestApprovalHost } from "@/components/request-approval-host"
import { PageLoading } from "@/components/page-state"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Toaster } from "@/components/ui/sonner"
import { adminDocumentTitle, type AdminRouteHandle } from "@/lib/admin-navigation"
import { adminApi, adminToken, ApiError } from "@/lib/api"

const LoginPage = lazy(() => import("@/pages/login").then((module) => ({ default: module.LoginPage })))
const fallbackHandle: AdminRouteHandle = { title: "Overview", description: "Runtime health and configuration at a glance." }

export function App() {
  const matches = useMatches()
  const route = [...matches].reverse().map((match) => match.handle).find(isAdminRouteHandle) ?? fallbackHandle
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [authRequired, setAuthRequired] = useState(true)

  useEffect(() => {
    void adminApi.health().then((health) => { setAuthRequired(health.auth_enabled); setAuthenticated(true) }).catch((value) => {
      if (value instanceof ApiError && value.status === 401) { adminToken.clear(); setAuthRequired(true); setAuthenticated(false); return }
      setAuthenticated(false)
    })
  }, [])

  useEffect(() => { document.title = adminDocumentTitle(authenticated === null ? "Connecting" : authenticated ? route.title : "Login") }, [authenticated, route.title])

  if (authenticated === null) return <div className="flex min-h-screen items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />Connecting to admin API...</div>
  if (!authenticated) return <Suspense fallback={<FullPageLoading label="Loading sign in..." />}><LoginPage onAuthenticated={() => { setAuthRequired(true); setAuthenticated(true) }} /></Suspense>

  function signOut() { adminToken.clear(); setAuthenticated(false) }

  return <TooltipProvider><SidebarProvider><AppSidebar authRequired={authRequired} onSignOut={signOut} /><SidebarInset className="min-w-0"><header className="sticky top-0 z-20 flex min-h-14 items-center gap-3 border-b bg-background/95 px-3 backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4"><SidebarTrigger /><div className="min-w-0 flex-1"><div className="truncate text-sm font-semibold">{route.title}</div><div className="hidden truncate text-xs text-muted-foreground sm:block">{route.description}</div></div></header><div className="min-w-0 flex-1 bg-muted/20"><div className="mx-auto w-full max-w-[1400px] p-4 sm:p-6 lg:p-8"><Suspense fallback={<PageLoading rows={6} />}><Outlet /></Suspense></div></div></SidebarInset><RequestApprovalHost /><Toaster /></SidebarProvider></TooltipProvider>
}

function isAdminRouteHandle(value: unknown): value is AdminRouteHandle { return Boolean(value && typeof value === "object" && "title" in value && "description" in value) }
function FullPageLoading({ label }: { label: string }) { return <div className="flex min-h-screen items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{label}</div> }

export default App
