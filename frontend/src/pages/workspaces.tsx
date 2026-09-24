import { useEffect, useState } from "react"
import {
  Boxes,
  FolderGit2,
  FolderMinus,
  FolderPlus,
  MoreHorizontal,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react"
import { useNavigate } from "react-router-dom"
import { PageEmpty, PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { ResponsiveDialog } from "@/components/responsive-dialog"
import { TruncatedText } from "@/components/truncated-text"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import { Label } from "@/components/ui/label"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { adminApi, type Workspace, type WorkspaceContainer } from "@/lib/api"

type MembershipMode = "add" | "remove"
type MembershipState = {
  workspace: Workspace
  mode: MembershipMode
  current: string[]
}
type ConfirmAction =
  | {
      kind: "membership"
      workspace: Workspace
      mode: MembershipMode
      containerIDs: string[]
    }
  | { kind: "create-container"; name: string }
  | { kind: "rename-container"; container: WorkspaceContainer; name: string }
  | { kind: "delete-container"; container: WorkspaceContainer }
  | { kind: "unregister"; workspace: Workspace }
  | { kind: "purge"; workspace: Workspace }

export function WorkspacesPage() {
  const navigate = useNavigate()
  const [items, setItems] = useState<Workspace[]>([])
  const [containers, setContainers] = useState<WorkspaceContainer[]>([])
  const [path, setPath] = useState("")
  const [containerName, setContainerName] = useState("")
  const [renameName, setRenameName] = useState("")
  const [registerOpen, setRegisterOpen] = useState(false)
  const [createContainerOpen, setCreateContainerOpen] = useState(false)
  const [renameTarget, setRenameTarget] = useState<WorkspaceContainer | null>(
    null
  )
  const [membership, setMembership] = useState<MembershipState | null>(null)
  const [selectedContainerIDs, setSelectedContainerIDs] = useState<string[]>([])
  const [confirmAction, setConfirmAction] = useState<ConfirmAction | null>(null)
  const [loading, setLoading] = useState(true)
  const [registering, setRegistering] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")

  async function load(manual = false) {
    if (manual) setRefreshing(true)
    try {
      const [nextItems, nextContainers] = await Promise.all([
        adminApi.workspaces(),
        adminApi.workspaceContainers(),
      ])
      setItems(nextItems)
      setContainers(nextContainers)
      setError("")
    } catch (value) {
      setError(errorText(value))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    let active = true
    void Promise.all([adminApi.workspaces(), adminApi.workspaceContainers()])
      .then(([nextItems, nextContainers]) => {
        if (!active) return
        setItems(nextItems)
        setContainers(nextContainers)
        setError("")
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
  }, [])

  async function register(event: React.FormEvent) {
    event.preventDefault()
    const value = path.trim()
    if (!value) return
    setRegistering(true)
    try {
      await adminApi.registerWorkspace(value)
      setPath("")
      setRegisterOpen(false)
      await load()
    } catch (value) {
      setError(errorText(value))
    } finally {
      setRegistering(false)
    }
  }

  function requestCreateContainer(event: React.FormEvent) {
    event.preventDefault()
    const name = containerName.trim()
    if (!name) return
    setCreateContainerOpen(false)
    setConfirmAction({ kind: "create-container", name })
  }

  function requestRenameContainer(event: React.FormEvent) {
    event.preventDefault()
    const name = renameName.trim()
    if (!renameTarget || !name) return
    const container = renameTarget
    setRenameTarget(null)
    setConfirmAction({ kind: "rename-container", container, name })
  }

  function openMembership(workspace: Workspace, mode: MembershipMode) {
    const current = containers
      .filter((container) => container.workspace_ids?.includes(workspace.id))
      .map((container) => container.id)
    setSelectedContainerIDs([])
    setMembership({ workspace, mode, current })
  }

  function requestMembership() {
    if (!membership || selectedContainerIDs.length === 0) return
    setConfirmAction({
      kind: "membership",
      workspace: membership.workspace,
      mode: membership.mode,
      containerIDs: [...selectedContainerIDs],
    })
    setMembership(null)
    setSelectedContainerIDs([])
  }

  async function confirm() {
    if (!confirmAction) return
    const action = confirmAction
    setBusy(true)
    try {
      if (action.kind === "create-container") {
        await adminApi.createWorkspaceContainer(action.name)
        setContainerName("")
      } else if (action.kind === "rename-container") {
        await adminApi.renameWorkspaceContainer(
          action.container.id,
          action.name
        )
      } else if (action.kind === "delete-container") {
        await adminApi.removeWorkspaceContainer(action.container.id)
      } else if (action.kind === "unregister") {
        await adminApi.removeWorkspace(action.workspace.id)
      } else if (action.kind === "purge") {
        await adminApi.purgeWorkspace(action.workspace.id)
      } else if (action.mode === "add") {
        await adminApi.addWorkspaceContainers(
          action.workspace.id,
          action.containerIDs
        )
      } else {
        await adminApi.removeWorkspaceContainers(
          action.workspace.id,
          action.containerIDs
        )
      }
      setConfirmAction(null)
      await load()
    } catch (value) {
      setError(errorText(value))
    } finally {
      setBusy(false)
    }
  }

  const membershipOptions = !membership
    ? []
    : membership.mode === "remove"
      ? containers.filter((container) =>
          membership.current.includes(container.id)
        )
      : containers

  return (
    <div className="space-y-6">
      <PageHeader
        title="Workspaces"
        description="Manage canonical project roots and logical workspace containers."
        actions={
          <>
            <Button
              disabled={refreshing}
              size="sm"
              variant="outline"
              onClick={() => void load(true)}
            >
              <RefreshCw className={refreshing ? "animate-spin" : ""} />
              Refresh
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setCreateContainerOpen(true)}
            >
              <Boxes />
              Create container
            </Button>
            <Button size="sm" onClick={() => setRegisterOpen(true)}>
              <Plus />
              Register workspace
            </Button>
          </>
        }
      />
      <PageError message={error} />
      <Tabs defaultValue="workspaces">
        <TabsList>
          <TabsTrigger value="workspaces">Workspaces</TabsTrigger>
          <TabsTrigger value="containers">Containers</TabsTrigger>
        </TabsList>
        <TabsContent className="mt-4" value="workspaces">
          {loading ? (
            <PageLoading rows={4} />
          ) : items.length === 0 ? (
            <PageEmpty
              icon={FolderGit2}
              title="No workspaces registered"
              description="Register a project root to start using workspace-scoped tools."
              action={
                <Button onClick={() => setRegisterOpen(true)}>
                  <Plus />
                  Register workspace
                </Button>
              }
            />
          ) : (
            <ItemGroup>
              {items.map((item) => (
                <WorkspaceRow
                  key={item.id}
                  item={item}
                  containers={containers}
                  onOpen={() =>
                    navigate(`/workspaces/${encodeURIComponent(item.id)}`)
                  }
                  onAdd={() => openMembership(item, "add")}
                  onRemove={() => openMembership(item, "remove")}
                  onUnregister={() =>
                    setConfirmAction({ kind: "unregister", workspace: item })
                  }
                  onPurge={() =>
                    setConfirmAction({ kind: "purge", workspace: item })
                  }
                />
              ))}
            </ItemGroup>
          )}
        </TabsContent>
        <TabsContent className="mt-4" value="containers">
          {loading ? (
            <PageLoading rows={4} />
          ) : containers.length === 0 ? (
            <PageEmpty
              icon={Boxes}
              title="No workspace containers"
              description="Create a container to group related workspaces without merging their state or filesystem scope."
              action={
                <Button onClick={() => setCreateContainerOpen(true)}>
                  <Plus />
                  Create container
                </Button>
              }
            />
          ) : (
            <ItemGroup>
              {containers.map((container) => (
                <Item key={container.id} variant="outline">
                  <ItemMedia variant="icon">
                    <Boxes className="text-muted-foreground" />
                  </ItemMedia>
                  <ItemContent className="min-w-0">
                    <ItemTitle className="w-full">
                      <TruncatedText lines={1}>{container.name}</TruncatedText>
                    </ItemTitle>
                    <ItemDescription className="font-mono">
                      {container.id}
                    </ItemDescription>
                    <div className="mt-1">
                      <Badge variant="outline">
                        {container.workspace_ids?.length ?? 0} workspace
                        {(container.workspace_ids?.length ?? 0) === 1
                          ? ""
                          : "s"}
                      </Badge>
                    </div>
                  </ItemContent>
                  <ItemActions>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          aria-label={`Actions for ${container.name}`}
                          size="icon-sm"
                          variant="ghost"
                        >
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          onClick={() => {
                            setRenameName(container.name)
                            setRenameTarget(container)
                          }}
                        >
                          <Pencil />
                          Rename
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() =>
                            setConfirmAction({
                              kind: "delete-container",
                              container,
                            })
                          }
                        >
                          <Trash2 />
                          Delete container
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </ItemActions>
                </Item>
              ))}
            </ItemGroup>
          )}
        </TabsContent>
      </Tabs>
      <ResponsiveDialog
        open={registerOpen}
        onOpenChange={setRegisterOpen}
        title="Register workspace"
        description="Register one canonical project root."
        footer={
          <>
            <Button variant="outline" onClick={() => setRegisterOpen(false)}>
              Cancel
            </Button>
            <Button
              form="register-workspace-form"
              disabled={registering || !path.trim()}
              type="submit"
            >
              {registering ? "Registering..." : "Register workspace"}
            </Button>
          </>
        }
      >
        <form
          className="space-y-3"
          id="register-workspace-form"
          onSubmit={register}
        >
          <Label htmlFor="workspace-path">Project root</Label>
          <Input
            autoComplete="off"
            autoFocus
            id="workspace-path"
            placeholder="/home/mew/projects/example"
            value={path}
            onChange={(event) => setPath(event.target.value)}
          />
          <p className="text-xs text-muted-foreground">
            Use an absolute path. The server resolves symlinks and validates the
            canonical root.
          </p>
        </form>
      </ResponsiveDialog>
      <ResponsiveDialog
        open={createContainerOpen}
        onOpenChange={setCreateContainerOpen}
        title="Create workspace container"
        description="Only a name is required. Membership can be assigned from each workspace."
        footer={
          <>
            <Button
              variant="outline"
              onClick={() => setCreateContainerOpen(false)}
            >
              Cancel
            </Button>
            <Button
              form="create-workspace-container-form"
              disabled={!containerName.trim()}
              type="submit"
            >
              Continue
            </Button>
          </>
        }
      >
        <form
          className="space-y-3"
          id="create-workspace-container-form"
          onSubmit={requestCreateContainer}
        >
          <Label htmlFor="workspace-container-name">Name</Label>
          <Input
            autoComplete="off"
            autoFocus
            id="workspace-container-name"
            placeholder="Backend projects"
            value={containerName}
            onChange={(event) => setContainerName(event.target.value)}
          />
        </form>
      </ResponsiveDialog>
      <ResponsiveDialog
        open={Boolean(renameTarget)}
        onOpenChange={(open) => {
          if (!open) setRenameTarget(null)
        }}
        title="Rename workspace container"
        description={renameTarget?.id}
        footer={
          <>
            <Button variant="outline" onClick={() => setRenameTarget(null)}>
              Cancel
            </Button>
            <Button
              form="rename-workspace-container-form"
              disabled={!renameName.trim()}
              type="submit"
            >
              Continue
            </Button>
          </>
        }
      >
        <form
          className="space-y-3"
          id="rename-workspace-container-form"
          onSubmit={requestRenameContainer}
        >
          <Label htmlFor="workspace-container-rename">Name</Label>
          <Input
            autoComplete="off"
            autoFocus
            id="workspace-container-rename"
            value={renameName}
            onChange={(event) => setRenameName(event.target.value)}
          />
        </form>
      </ResponsiveDialog>
      <ResponsiveDialog
        open={Boolean(membership)}
        onOpenChange={(open) => {
          if (!open) {
            setMembership(null)
            setSelectedContainerIDs([])
          }
        }}
        title={
          membership?.mode === "remove"
            ? "Remove workspace container"
            : "Select workspace container"
        }
        description={
          membership
            ? `${membership.workspace.path} · ${membership.workspace.id}`
            : undefined
        }
        footer={
          <>
            <Button
              variant="outline"
              onClick={() => {
                setMembership(null)
                setSelectedContainerIDs([])
              }}
            >
              Cancel
            </Button>
            <Button
              disabled={selectedContainerIDs.length === 0}
              onClick={requestMembership}
            >
              Continue
            </Button>
          </>
        }
      >
        <div className="space-y-2">
          {membershipOptions.length === 0 ? (
            <div className="rounded-lg border p-4 text-sm text-muted-foreground">
              {membership?.mode === "remove"
                ? "This workspace is not assigned to any container."
                : "No workspace containers exist yet."}
            </div>
          ) : (
            membershipOptions.map((container) => {
              const existing =
                membership?.mode === "add" &&
                membership.current.includes(container.id)
              const checked =
                existing || selectedContainerIDs.includes(container.id)
              return (
                <label
                  className={`flex items-start gap-3 rounded-lg border p-3 ${existing ? "cursor-not-allowed opacity-60" : "cursor-pointer"}`}
                  key={container.id}
                >
                  <Checkbox
                    checked={checked}
                    disabled={existing}
                    onCheckedChange={(value) =>
                      setSelectedContainerIDs((current) =>
                        value
                          ? [
                              ...current.filter((id) => id !== container.id),
                              container.id,
                            ]
                          : current.filter((id) => id !== container.id)
                      )
                    }
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">
                      {container.name}
                    </span>
                    <span className="block truncate font-mono text-xs text-muted-foreground">
                      {container.id}
                    </span>
                    {existing ? (
                      <span className="mt-1 block text-xs text-muted-foreground">
                        Already added
                      </span>
                    ) : null}
                  </span>
                </label>
              )
            })
          )}
        </div>
      </ResponsiveDialog>
      <AlertDialog
        open={Boolean(confirmAction)}
        onOpenChange={(open) => {
          if (!open && !busy) setConfirmAction(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmTitle(confirmAction)}</AlertDialogTitle>
            <AlertDialogDescription className="break-words">
              {confirmDescription(confirmAction, containers)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={busy}
              variant={
                confirmAction?.kind === "delete-container" ||
                confirmAction?.kind === "unregister" ||
                confirmAction?.kind === "purge" ||
                (confirmAction?.kind === "membership" &&
                  confirmAction.mode === "remove")
                  ? "destructive"
                  : "default"
              }
              onClick={() => void confirm()}
            >
              {busy ? "Applying..." : confirmLabel(confirmAction)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function WorkspaceRow({
  item,
  containers,
  onOpen,
  onAdd,
  onRemove,
  onUnregister,
  onPurge,
}: {
  item: Workspace
  containers: WorkspaceContainer[]
  onOpen: () => void
  onAdd: () => void
  onRemove: () => void
  onUnregister: () => void
  onPurge: () => void
}) {
  const count = containers.filter((container) =>
    container.workspace_ids?.includes(item.id)
  ).length
  return (
    <Item
      className="cursor-pointer"
      role="button"
      tabIndex={0}
      variant="outline"
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") onOpen()
      }}
    >
      <ItemMedia variant="icon">
        <FolderGit2 className="text-muted-foreground" />
      </ItemMedia>
      <ItemContent className="min-w-0">
        <ItemTitle className="w-full">
          <TruncatedText lines={1}>{item.path}</TruncatedText>
        </ItemTitle>
        <ItemDescription className="font-mono">{item.id}</ItemDescription>
        <div className="mt-1 flex flex-wrap gap-1">
          {item.allow_dirs?.length ? (
            <Badge variant="outline">
              {item.allow_dirs.length} extra path
              {item.allow_dirs.length === 1 ? "" : "s"}
            </Badge>
          ) : null}
          {count ? (
            <Badge variant="outline">
              {count} container{count === 1 ? "" : "s"}
            </Badge>
          ) : null}
          {item.available === false ? (
            <Badge variant="destructive">Unavailable</Badge>
          ) : null}
        </div>
      </ItemContent>
      <ItemActions onClick={(event) => event.stopPropagation()}>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              aria-label={`Actions for ${item.path}`}
              size="icon-sm"
              variant="ghost"
            >
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={onOpen}>
              <FolderGit2 />
              View workspace
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onAdd}>
              <FolderPlus />
              Select workspace container
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onRemove}>
              <FolderMinus />
              Remove workspace container
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={onUnregister}>
              <Trash2 />
              Unregister workspace
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" onClick={onPurge}>
              <Trash2 />
              Purge local .cm state
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </ItemActions>
    </Item>
  )
}

function confirmTitle(action: ConfirmAction | null) {
  if (!action) return "Confirm action"
  if (action.kind === "create-container") return "Create workspace container?"
  if (action.kind === "rename-container") return "Rename workspace container?"
  if (action.kind === "delete-container") return "Delete workspace container?"
  if (action.kind === "unregister") return "Unregister workspace?"
  if (action.kind === "purge") return "Purge workspace local state?"
  return action.mode === "add"
    ? "Add workspace to containers?"
    : "Remove workspace from containers?"
}

function confirmDescription(
  action: ConfirmAction | null,
  containers: WorkspaceContainer[]
) {
  if (!action) return ""
  if (action.kind === "create-container") return `Create ${action.name}?`
  if (action.kind === "rename-container")
    return `${action.container.name} will be renamed to ${action.name}.`
  if (action.kind === "delete-container")
    return `${action.container.name} (${action.container.id}) will be deleted. Registered workspaces and project files remain unchanged.`
  if (action.kind === "unregister")
    return `${action.workspace.path} will be removed from CodeMCP. Project files and local .cm state will not be deleted.`
  if (action.kind === "purge")
    return `${action.workspace.path} will be unregistered and its local .cm state will be permanently deleted. Project files outside .cm remain unchanged.`
  const names = action.containerIDs
    .map(
      (id) => containers.find((container) => container.id === id)?.name ?? id
    )
    .join(", ")
  return action.mode === "add"
    ? `${action.workspace.id} will be added to: ${names}.`
    : `${action.workspace.id} will be removed from: ${names}.`
}

function confirmLabel(action: ConfirmAction | null) {
  if (!action) return "Confirm"
  if (action.kind === "create-container") return "Create"
  if (action.kind === "rename-container") return "Rename"
  if (action.kind === "delete-container") return "Delete"
  if (action.kind === "unregister") return "Unregister"
  if (action.kind === "purge") return "Purge"
  return action.mode === "add" ? "Add" : "Remove"
}

function errorText(value: unknown) {
  return value instanceof Error ? value.message : String(value)
}
