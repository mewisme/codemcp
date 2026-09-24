# TUI Command Center

`cm tui` is the human-operated terminal interface for `CodeMCP`. The normal CLI remains the stable surface for scripts and automation.

```text
cm ...   scriptable CLI
cm tui   interactive Command Center
```

## Start

```bash
cm tui
```

The TUI requires a real terminal. Redirected/non-TTY invocation fails instead of writing alternate-screen output into a pipe.

Deep-link directly to a page or resource when useful:

```bash
cm tui workspace
cm tui workspace ws_...
cm tui upstream github
cm tui tunnel
cm tui requests
cm tui logs
cm tui config
cm tui runtime
cm tui guide
```

## Global navigation

| Key | Action |
| --- | --- |
| `Ctrl+K` | open Commands for actions, pages, resources, and Guide topics |
| `Alt+Left` / `Alt+Right` | cycle top-level pages |
| `Esc` | close the nearest overlay/child page, then navigate back/exit |
| `Backspace` | navigate back when an input is not consuming the key |

Page-local shortcuts are shown directly above the application footer. Press `?` where available to expand additional bindings.

Mouse clicks and scrolling follow the same actions and validation paths as keyboard input.

## Commands

Press `Ctrl+K` and search for an action, resource, page, ID, or canonical CLI path.

Examples:

```text
workspace register
upstream server add
request approve
config verify
restart
guide logs
```

Commands is the primary discovery surface. There is no separate Quick Open workflow.

## Main areas

The top-level navigation covers:

```text
Workspaces | Upstreams | Tunnel | Requests | Logs | Config | Instruction | Runtime
```

Child resources remain owned by their parent area. Additional resources such as workspace containers, managed tunnels, About/build information, and embedded Guide topics are reachable through Commands or deep links.

## Editors and confirmations

Create/edit/configure workflows use full-page editors so long forms remain usable in small terminals.

Common behavior:

- `Enter` advances structured fields/sections and performs the editor action on the final visible field.
- Multiline inputs keep `Enter` for newlines and use `Ctrl+Enter` for save/create/apply actions.
- Path fields use `Ctrl+O` to switch between picker and manual input where supported.
- Sensitive fields use password-style input and do not expose persisted secrets.
- Failed operations keep the current draft.
- Leaving an unsaved editor requires explicit discard confirmation.
- Destructive actions require confirmation.

## Workspaces

The Workspaces area manages concrete `ws_*` project roots, additional access directories, workspace containers, and Project Context previews.

A workspace detail can relocate a project after its directory has already moved. Relocation updates the trusted registered root; it does not move project files itself.

Workspace containers (`wsc_*`) are grouping/orchestration resources, not filesystem scopes. See [Workspaces](workspaces.md).

## Upstreams

The Upstreams area manages remote MCP endpoints, health/tool discovery, tool exposure, and OAuth where supported.

Server creation supports both form-driven setup and canonical JSON input. Sensitive environment/header values remain managed as secrets rather than being echoed into normal detail views.

See [MCP clients and Upstreams](mcp.md).

## Tunnel

Tunnel manages the local OpenAI Secure MCP Tunnel configuration and, when an appropriate verified admin credential is configured, managed tunnel resources.

The normal ChatGPT runtime credential is the restricted **Tunnels Read + Use** key. Administrative tunnel management remains separate. See [OpenAI + ChatGPT](openai-chatgpt.md).

## Requests

Requests is the approval inbox for guarded actions. Pending requests can also appear in the global live approval dialog so a local operator can review the exact action without leaving the current page.

Approval does not create a general shell bypass; it authorizes the runtime-defined action/retry scope. See [Security](security.md#control-guard-approvals-and-self-grant-prevention).

## Logs

Logs has three tabs:

```text
Runtime | Command Execution | Tool Calls
```

All three support two views:

- **Browser** — inspect individual records and open details.
- **Timeline** — follow the chronological stream.

Use:

| Key | Action |
| --- | --- |
| `v` | switch Browser / Timeline |
| `m` | choose Stream Mode |
| `Space` | pause/resume follow |
| `r` | refresh/reconnect where applicable |

Stream Mode controls the visible scope for Runtime, Command Execution, and Tool Calls. Command Execution can additionally select Process view for a workspace; the other tabs do not expose Process mode.

### Runtime

Runtime combines persistent journal history with the live runtime event stream. Tool-call lifecycle events are presented in the dedicated Tool Calls tab rather than duplicated into Runtime history.

### Command Execution

Command Execution follows the runtime execution feed and keeps stdout/stderr in event order. Browser view makes individual executions easy to inspect; Timeline is better for watching interleaved activity live.

### Tool Calls

Tool Calls shows authoritative tool-call lifecycle records. Browser detail renders the complete structured call body; Timeline renders request/result/error blocks in chronological order.

### Follow behavior

While a Logs page is active, follow keeps the selected view at the newest visible item/event. Navigating a Browser row away from the tail pauses follow so the selection does not get pulled out from under the user. Press `Space` to return to follow.

Leaving Logs for another top-level page closes its live feeds. Returning reconstructs the page from fresh history/snapshots and **resumes follow automatically** instead of restoring a stale paused stream.

## Config and Instruction

Config is schema-driven. Use it for typed configuration editing and storage/maintenance operations; exhaustive configuration semantics remain available through `cm config explain` and [Configuration](configuration.md).

Instruction manages Global Context, managed rules, and detected instruction sources used by Project Context assembly.

## Runtime

Runtime is the operational control surface for service state, authentication, install/update actions, and version/build information.

For scripts or remote automation, use the equivalent CLI commands instead. See [Runtime and operations](runtime.md).

## Embedded Guide

The Markdown tree under [`tuiguide/`](tuiguide/) is embedded into the binary as contextual help.

Open Commands (`Ctrl+K`) and search for **Guide**, or deep-link directly:

```bash
cm tui guide
cm tui guide logs
cm tui guide mcp
```

The embedded Guide intentionally explains the current page/editor instead of duplicating the complete public documentation.

## Scripting and automation

Do not automate the full-screen TUI. Use normal CLI commands and structured output instead:

```bash
cm workspace list --json
cm upstream server list --json
cm config verify
cm status
```

Use `cm <command> --help` and the [CLI reference](cli-reference.md) for the scriptable interface.
