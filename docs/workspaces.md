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
├── prompts/
└── plans/
```

Memory, rewind/checkpoint history, shell session history, native CodeMCP rules/skills, prompts, and persisted agent plans are resolved through this workspace-local state service. `.cm/prompts/` is the canonical workspace Prompt root; `.cm/plans/` is the canonical durable plan root. Runtime-only execution feeds that are currently in memory are not duplicated into global workspace state. The global registry remains an ID/root lookup and does not become the primary store for these workspace-owned domains.

### Persisted agent plans

Each plan is a regular Markdown file at `.cm/plans/<name>.md`. The name is its stable cross-session identity; the content ID identifies a particular revision. Plan status and next-phase metadata are derived from the file itself rather than from a separate active-plan pointer or progress database.

CodeMCP owns mutations inside `.cm/plans/`: `create_plan` creates and updates canonical plan documents, while generic Agent filesystem mutation tools are blocked from writing, editing, moving, or deleting that managed subtree. Normal reads remain available so an Agent can inspect a selected plan before implementing it.

Plan files move with the workspace-local `.cm/` tree during a project move/relocation and are removed by workspace purge. Duplicate-workspace reconciliation unions distinct plan names and accepts equivalent same-name content, but divergent same-name documents are treated as a durable-state conflict instead of choosing a winner.

Files outside `.cm/plans/` are not CodeMCP plan state: ad-hoc planning notes elsewhere in a repository are not discovered, migrated, or required for product behavior.

### Git hygiene and local concealment

CodeMCP keeps workspace-local `.cm/` out of normal Git changes without editing the project's committed root `.gitignore`.

- `<workspace>/.cm/.gitignore` contains `*` as defense in depth.
- Git repositories receive a local `.cm/` rule in repository metadata `info/exclude`. Linked worktrees resolve through the common Git metadata directory.
- Registration, runtime activation, and runtime reload repair these rules idempotently.
- Nested repositories are not traversed or modified while repairing the parent workspace.
- On hosts with a native hidden-file attribute, CodeMCP applies it to `.cm/` on a best-effort basis. Correctness does not depend on visual concealment.

Ignore rules cannot retroactively untrack files already present in the Git index. `cm workspace list`, `cm workspace show`, and runtime diagnostics detect tracked `.cm` state and report the manual remediation command:

```bash
git rm -r --cached .cm
```

Review the resulting Git change before committing it. CodeMCP never runs that index mutation automatically.

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

Relocate preserves the existing stable `ws_*` identity. Move or rename the project directory first so its local `.cm/` state moves with it, then rebind the registry to the new root. The destination must contain the same `.cm/workspace.json` identity; a different identity, a missing identity, or copies of the same identity at both source and destination are rejected as ambiguous. Container membership remains attached to the same workspace ID, and workspace access roots nested under the old project root are rebased.

Relocate does **not** move project files. It is a trusted local control-plane operation available through CLI, TUI, and Admin surfaces rather than an Agent filesystem tool.

If a registered root disappears or its local identity becomes invalid, the registry entry remains present with the same ID but is reported as unavailable. Healthy sibling workspaces remain usable. Restore the original project root/local identity or relocate the workspace after moving the project; do not treat temporary filesystem unavailability as deletion of workspace identity.

Unregister removes only the registry handle and container membership. It does not delete project files or workspace-local `.cm/` state:

```bash
cm workspace unregister ws_...
```

Deleting local CodeMCP state is a separate destructive operation and requires explicit confirmation:

```bash
cm workspace purge ws_... --yes
```

Purge unregisters the workspace when necessary and removes only the verified `<workspace>/.cm/` tree. It does not remove project files outside `.cm/`; symlinked or mismatched local state is rejected instead of followed.

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

## Runtime ownership and synchronization

An active CodeMCP runtime holds one exclusive advisory lock at `<workspace>/.cm/runtime/lock` for every workspace it owns. A second runtime cannot acquire the same workspace state concurrently. Lock metadata records the owning process and instance for diagnostics, while lock authority comes from the operating-system file lock rather than the metadata text.

Global registry mutation serialization is separate from workspace runtime ownership. Safe registry-only changes, such as container metadata and workspace access metadata, use a global mutation lock and do not transfer or steal workspace runtime ownership. Runtime-aware reload reconciles newly registered or removed workspaces while retaining valid existing ownership locks.

If `.cm/workspace.json` or the runtime lock file is removed or replaced while active, workspace access fails closed as a state-ownership conflict instead of silently adopting the replacement. An owning runtime may relocate only after the project and its existing `.cm/` state have physically moved together, so the held runtime lock is still the same file at the destination. Unregistration by a different process is rejected while another runtime owns the workspace; an owning runtime can unregister its own workspace and releases that lock only after the registry mutation succeeds.

Cross-process lock acquisition follows a fixed order: runtime ownership coordination, global registry mutation serialization, in-process registry state, then a workspace runtime file lock. This keeps registry mutations and activation/reload ownership changes from taking the same locks in opposite order.

Workspace registry changes made through supported control surfaces are synchronized with a running runtime so subsequent workspace reads can see the new registry state without a full runtime restart. Runtime ownership diagnostics expose whether the manager is active, which workspaces it owns, each runtime lock path/state, and the global registry mutation lock path.

If an operation reports a synchronization or ownership conflict, inspect runtime status/logs before assuming the live process adopted the change.

## Recommended practice

- Register only project roots ChatGPT actually needs.
- Prefer workspace-specific extra directories over broad global roots.
- Keep different trust domains in separate workspaces or separate runtime instances where appropriate.
- Use containers for grouping/orchestration, never as permission shortcuts.
- Relocate an existing workspace after a project moves instead of registering a duplicate.

See [Security](security.md) for the trust model and [Configuration](configuration.md) for persistent access settings.
