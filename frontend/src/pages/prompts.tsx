import { useEffect, useState } from "react"
import { PageError, PageLoading } from "@/components/page-state"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
  Item,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemHeader,
  ItemTitle,
} from "@/components/ui/item"
import { Textarea } from "@/components/ui/textarea"
import {
  adminApi,
  type PromptDefinition,
  type ScopedPrompt,
  type Workspace,
} from "@/lib/api"

const template = JSON.stringify(
  {
    version: 1,
    name: "",
    description: "",
    arguments: [],
    messages: [{ role: "user", content: { type: "text", text: "" } }],
  },
  null,
  2
)

export function PromptsPage() {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [workspaceID, setWorkspaceID] = useState("")
  const [prompts, setPrompts] = useState<ScopedPrompt[] | null>(null)
  const [selected, setSelected] = useState("")
  const [inherited, setInherited] = useState(false)
  const [draft, setDraft] = useState(template)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")

  useEffect(() => {
    void adminApi
      .workspaces()
      .then(setWorkspaces)
      .catch((value) => setError(String(value)))
  }, [])
  useEffect(() => {
    let active = true
    void adminApi
      .prompts(workspaceID)
      .then((items) => {
        if (active) {
          setPrompts(items)
          setError("")
        }
      })
      .catch((value) => {
        if (active) setError(String(value))
      })
    return () => {
      active = false
    }
  }, [workspaceID])

  async function reload() {
    setPrompts(await adminApi.prompts(workspaceID))
  }
  async function choose(item: ScopedPrompt) {
    try {
      const current = await adminApi.prompt(item.definition.name, workspaceID)
      setSelected(current.definition.name)
      setInherited(Boolean(workspaceID && current.scope === "global"))
      setDraft(JSON.stringify(current.definition, null, 2))
      setError("")
    } catch (value) {
      setError(String(value))
    }
  }
  async function save() {
    setBusy(true)
    setError("")
    setMessage("")
    try {
      const definition = JSON.parse(draft) as PromptDefinition
      if (
        !definition ||
        typeof definition.name !== "string" ||
        !definition.name
      )
        throw new Error("Prompt name is required")
      if (selected && definition.name !== selected)
        throw new Error("Prompt name cannot change during update")
      const scope = workspaceID ? "workspace" : "global"
      const result =
        selected && !inherited
          ? await adminApi.updatePrompt(selected, workspaceID, definition)
          : await adminApi.createPrompt(scope, workspaceID, definition)
      setSelected(result.definition.name)
      setInherited(false)
      setDraft(JSON.stringify(result.definition, null, 2))
      await reload()
      setMessage("Prompt saved")
    } catch (value) {
      setError(String(value))
    } finally {
      setBusy(false)
    }
  }
  async function remove() {
    if (
      !selected ||
      !window.confirm(
        `Delete ${selected} from ${workspaceID ? "workspace" : "global"} Prompts?`
      )
    )
      return
    setBusy(true)
    setError("")
    setMessage("")
    try {
      await adminApi.deletePrompt(selected, workspaceID)
      setSelected("")
      setInherited(false)
      setDraft(template)
      await reload()
      setMessage("Prompt deleted")
    } catch (value) {
      setError(String(value))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title="Prompts"
        description="Global definitions are available to all workspaces; workspace definitions override names locally."
      />
      <PageError message={error} />
      {message && (
        <p role="status" className="text-sm text-muted-foreground">
          {message}
        </p>
      )}
      <label className="block space-y-2 text-sm">
        Scope
        <select
          className="block w-full max-w-md rounded-md border bg-background px-3 py-2"
          value={workspaceID}
          onChange={(event) => {
            setWorkspaceID(event.target.value)
            setPrompts(null)
            setSelected("")
            setInherited(false)
            setDraft(template)
          }}
        >
          <option value="">Global</option>
          {workspaces.map((item) => (
            <option key={item.id} value={item.id}>
              {item.id} · {item.path}
            </option>
          ))}
        </select>
      </label>
      {prompts === null ? (
        <PageLoading rows={3} />
      ) : (
        <div className="grid gap-5 lg:grid-cols-[minmax(15rem,1fr)_minmax(25rem,2fr)]">
          <Card>
            <CardHeader>
              <CardTitle>Definitions</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              <Button
                variant="outline"
                onClick={() => {
                  setSelected("")
                  setInherited(false)
                  setDraft(template)
                }}
              >
                New Prompt
              </Button>
              {prompts.length === 0 && (
                <p className="text-sm text-muted-foreground">
                  No Prompts in this view.
                </p>
              )}
              <ItemGroup>
                {prompts.map((item) => (
                  <Item
                    interactive
                    key={item.definition.name}
                    role="button"
                    selected={selected === item.definition.name}
                    tabIndex={0}
                    variant="outline"
                    onClick={() => void choose(item)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ")
                        void choose(item)
                    }}
                  >
                    <ItemContent className="min-w-0">
                      <ItemHeader>
                        <ItemTitle className="min-w-0 break-all">
                          {item.definition.name}
                        </ItemTitle>
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {item.scope}
                        </span>
                      </ItemHeader>
                      <ItemDescription className="break-words">
                        {item.definition.description}
                      </ItemDescription>
                    </ItemContent>
                  </Item>
                ))}
              </ItemGroup>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>
                {selected
                  ? `${inherited ? "Override" : "Edit"} ${selected}`
                  : "New Prompt"}
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <Input
                readOnly
                aria-label="Selected scope"
                value={workspaceID || "global"}
              />
              <Textarea
                aria-label="Prompt definition JSON"
                className="min-h-80 font-mono text-xs"
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
              />
              <div className="flex gap-2">
                <Button disabled={busy} onClick={() => void save()}>
                  {busy ? "Saving..." : "Save"}
                </Button>
                {selected && !inherited && (
                  <Button
                    disabled={busy}
                    variant="destructive"
                    onClick={() => void remove()}
                  >
                    Delete
                  </Button>
                )}
              </div>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  )
}
