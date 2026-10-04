import { LogOut } from "lucide-react"
import { NavLink, useMatch } from "react-router-dom"
import { ThemeMenu } from "@/components/theme-menu"
import { Button } from "@/components/ui/button"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarRail,
  SidebarSeparator,
  useSidebar,
} from "@/components/ui/sidebar"
import {
  navGroups,
  navItemByID,
  navItems,
  type NavItem,
} from "@/lib/admin-navigation"

export function AppSidebar({
  authRequired,
  onSignOut,
}: {
  authRequired: boolean
  onSignOut: () => void
}) {
  const { isMobile, setOpenMobile } = useSidebar()

  function closeMobile() {
    if (isMobile) setOpenMobile(false)
  }

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader className="p-3">
        <div className="flex items-center gap-2 overflow-hidden rounded-md px-1 py-1">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-xs font-semibold text-primary-foreground">
            CM
          </div>
          <div className="min-w-0 group-data-[collapsible=icon]:hidden">
            <div className="truncate text-sm font-semibold">CodeMCP</div>
            <div className="truncate text-[11px] text-muted-foreground">
              Admin dashboard
            </div>
          </div>
        </div>
      </SidebarHeader>
      <SidebarSeparator />
      <SidebarContent>
        {navGroups.map((group) => {
          const items = group.items
            .map(navItemByID)
            .filter((item): item is NavItem => Boolean(item))
          if (!items.length) return null

          return (
            <SidebarGroup
              key={group.id}
              className={group.label ? undefined : "py-1"}
            >
              {group.label ? (
                <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
              ) : null}
              <SidebarGroupContent>
                <SidebarMenu>
                  {items.map((item) => (
                    <SidebarRootItem
                      closeMobile={closeMobile}
                      item={item}
                      key={item.id}
                    />
                  ))}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          )
        })}
      </SidebarContent>
      <SidebarSeparator />
      <SidebarFooter>
        <div className="flex items-center justify-between gap-1 group-data-[collapsible=icon]:flex-col">
          <div className="flex min-w-0 items-center gap-2 px-2 group-data-[collapsible=icon]:px-0">
            <span className="size-2 shrink-0 rounded-full bg-emerald-500" />
            <span className="truncate text-xs text-muted-foreground group-data-[collapsible=icon]:hidden">
              Admin connected
            </span>
          </div>
          <div className="flex items-center">
            <ThemeMenu />
            {authRequired ? (
              <Button
                aria-label="Sign out"
                size="icon-sm"
                variant="ghost"
                onClick={onSignOut}
              >
                <LogOut />
              </Button>
            ) : null}
          </div>
        </div>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

function SidebarRootItem({
  item,
  closeMobile,
}: {
  item: NavItem
  closeMobile: () => void
}) {
  const children = navItems.filter((child) => child.parent === item.id)
  const active = Boolean(
    useMatch(
      item.path === "/overview"
        ? { path: item.path, end: true }
        : { path: `${item.path}/*`, end: true }
    )
  )

  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={item.title}>
        <NavLink to={item.path} onClick={closeMobile}>
          <item.icon />
          <span>{item.title}</span>
        </NavLink>
      </SidebarMenuButton>
      {children.length ? (
        <SidebarMenuSub>
          {children.map((child) => (
            <SidebarChildItem
              closeMobile={closeMobile}
              item={child}
              key={child.id}
            />
          ))}
        </SidebarMenuSub>
      ) : null}
    </SidebarMenuItem>
  )
}

function SidebarChildItem({
  item,
  closeMobile,
}: {
  item: NavItem
  closeMobile: () => void
}) {
  const active = Boolean(useMatch({ path: item.path, end: true }))

  return (
    <SidebarMenuSubItem>
      <SidebarMenuSubButton asChild isActive={active}>
        <NavLink to={item.path} onClick={closeMobile}>
          <item.icon />
          <span>{item.title}</span>
        </NavLink>
      </SidebarMenuSubButton>
    </SidebarMenuSubItem>
  )
}
