# Workspaces

A workspace is the concrete project boundary that `CodeMCP` uses for filesystem, shell, Git, process, project-context, memory, rule, skill, and checkpoint operations.

The important distinction is:

```text
ws_*  = concrete execution/filesystem workspace
wsc_* = logical orchestration group of workspaces
```

A workspace container never becomes a filesystem permission boundary and never replaces a concrete `ws_*` target.

## Register a workspace

```bash
cm workspace register ~/projects/my-project
```

Inspect it:

```bash
cm workspace list
cm workspace show ws_...
```

New registrations receive a stable `ws_*` identity persisted in `<workspace>/.cm/workspace.json`. Equivalent canonical spellings of the same project root reuse that identity instead of deriving a new ID from the input path string. The global registry maps the workspace ID to its registered root; existing legacy IDs may remain usable as aliases after migration or relocation.

The workspace-local `.cm/` root is CodeMCP-owned state. Registration creates its ownership marker lazily. A symlinked `.cm`, an invalid marker, or a non-empty unowned `.cm` is rejected rather than claimed. The effective global CodeMCP root (normally `$HOME/.cm`, or the selected `CM_CONFIG_DIR`) is never accepted as workspace-local state.

Workspace-owned durable state follows one local layout:

```text
<workspace>/.cm/
├── workspace.json
├── memory/
│   └── MEMORY.md
├── checkpoints/
├── state/
│   └── shell.json
├── rules/
├── skills/
└── prompts/
```

Memory, rewind/checkpoint history, shell session history, and native CodeMCP rules/skills are resolved through this workspace-local state service. `.cm/prompts/` is the canonical workspace Prompt root for Prompt storage. Runtime-only execution feeds that are currently in memory are not duplicated into global workspace state. The global registry remains an ID/root lookup and does not become the primary store for these workspace-owned domains.

## Effective filesystem scope

A workspace can reach:

```text
registered workspace root
+ global permissions.allow_dirs
+ workspace-specific access directories
```

Add a narrow workspace-specific directory when a project genuinely needs files outside its root:

```bash
cm workspace access add ws_... /path/to/build-cache
cm workspace access list ws_...
cm workspace access remove ws_... /path/to/build-cache
```

Global extra roots apply to every workspace and should be used more carefully:

```bash
cm config set permissions.allow_dirs /path/one,/path/two
```

Paths are canonicalized and symlink escapes are rejected. Read [Security](security.md#workspace-boundary) for the full boundary.

## Explicit targeting

A single MCP session may work with multiple registered projects. Each workspace-scoped operation still names the intended `workspace_id`; the runtime does not maintain a hidden global “current workspace” and does not silently fall back to another project.

That means an Agent can move between projects safely by targeting different `ws_*` IDs while workspace-specific state remains isolated.

## What stays isolated

Switching between workspaces does not merge their:

- filesystem roots and extra access directories
- shell working directory and process state
- Git working context
- project instructions and context
- workspace memory
- rules and skills
- checkpoints/rewind state
- approval state

This isolation is why concrete `ws_*` targets remain required even when several projects belong to the same workspace container.

## Relocate a moved project

If the project directory has already been renamed or moved, relocate the existing workspace instead of registering the destination as an unrelated project:

```bash
cm workspace relocate ws_... /new/path/to/project
```

The current relocation compatibility flow updates the trusted root and derives a replacement path-based workspace ID. The previous ID is retained as a legacy alias, workspace-scoped persistent state follows the project, and container membership is preserved.

Relocate does **not** move project files. It is a trusted local control-plane operation available through CLI, TUI, and Admin surfaces rather than an Agent filesystem tool.

## Workspace containers

Containers group registered workspaces for orchestration:

```bash
cm workspace container list
cm workspace container create "Backend projects"
cm workspace container add wsc_... ws_... ws_...
cm workspace container show wsc_...
```

A `wsc_*` ID answers “which workspaces belong together?”, not “which filesystem may this tool access?”.

Agent-facing container tools can discover the members of a group, but substantial work still targets one or more concrete member `ws_*` IDs individually. Resolving a container does not merge member context, memory, permissions, shell state, or checkpoints.

## Runtime synchronization

Workspace registry changes made through supported control surfaces are synchronized with a running runtime so subsequent workspace reads can see the new registry state without a full runtime restart.

If an operation reports a synchronization failure, inspect runtime status/logs before assuming the live process adopted the change.

## Recommended practice

- Register only project roots ChatGPT actually needs.
- Prefer workspace-specific extra directories over broad global roots.
- Keep different trust domains in separate workspaces or separate runtime instances where appropriate.
- Use containers for grouping/orchestration, never as permission shortcuts.
- Relocate an existing workspace after a project moves instead of registering a duplicate.

See [Security](security.md) for the trust model and [Configuration](configuration.md) for persistent access settings.
