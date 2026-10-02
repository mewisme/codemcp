# MCP clients and Upstreams

For ChatGPT, the recommended/default transport is **OpenAI Secure MCP Tunnel**. Use this guide when you need a generic MCP client such as Cursor, or when `CodeMCP` should aggregate tools from an Upstream.

For the ChatGPT setup, start with [OpenAI + ChatGPT](openai-chatgpt.md).

## Generic local MCP clients

`cm mcp` starts an MCP-only transport without the normal Admin server, Secure MCP Tunnel lifecycle, or managed runtime service.

### stdio

```bash
cm mcp stdio
```

Bind the session to one already registered workspace:

```bash
cm mcp stdio --workspace ~/projects/my-project
cm mcp stdio --workspace ws_...
```

Binding does not register or relocate a workspace. The target must already exist in the workspace registry.

A Cursor project configuration can therefore use:

```json
{
  "mcpServers": {
    "CodeMCP": {
      "type": "stdio",
      "command": "cm",
      "args": ["mcp", "stdio", "--workspace", "${workspaceFolder}"]
    }
  }
}
```

### Streamable HTTP

Run the dedicated loopback MCP HTTP server:

```bash
cm mcp http
```

It exposes the current Streamable HTTP endpoint and legacy SSE compatibility. Disable SSE compatibility when unnecessary:

```bash
cm mcp http --no-sse
```

Bind it to one registered workspace:

```bash
cm mcp http --workspace ws_...
```

The dedicated generic-client HTTP transport is intentionally separate from the tunnel-first ChatGPT path.

## Authentication

`stdio` uses the local child-process boundary and does not require transport OAuth.

Protected `cm mcp http` uses OAuth as the canonical client authentication flow. Static managed MCP bearer compatibility can be controlled with:

```bash
cm config set http.mcp.auth.legacy_bearer false
```

The OpenAI Secure MCP Tunnel runtime API key is unrelated to generic MCP client authentication.

See [Configuration](configuration.md#authentication) and [Security](security.md).

## Workspace binding

Without transport-level binding, workspace-scoped tools target explicit registered `ws_*` IDs. With `--workspace`, the generic transport binds the session to that one concrete workspace and removes redundant workspace selection from the client-facing surface where applicable.

Workspace containers (`wsc_*`) remain orchestration groups and are never substituted for a concrete filesystem workspace.

See [Workspaces](workspaces.md) for the canonical workspace model.

## Agent Plan Mode

Compatible agents can enter CodeMCP Plan Mode when the user request contains the exact standalone token `/plan`. The token must be whitespace-delimited; text such as `/planner`, `/plan/foo`, or a URL/path fragment is not Plan Mode. Ordinary requests that an agent internally breaks into a few steps are not Plan Mode unless that directive is present.

Plan Mode is intentionally planning-only:

1. Resolve the target workspace and load `project_context`.
2. Inspect applicable rules/skills plus the relevant source and any existing persisted plan state.
3. Create or update the plan through `create_plan`.
4. Stop before implementation, even if the same request also asks to implement the work.

`create_plan` is the only plan mutation Tool. It writes one canonical Markdown artifact under the workspace-owned `.cm/plans/` tree. Agents may read the selected plan with normal filesystem tools, and later implementation continues through the normal filesystem, Git, shell, and runtime Tools; there is no separate `implement_plan` Tool.

Each persisted plan contains two synchronized parts in one file:

```markdown
# Plan title

## Goal
...

## Architecture contract
...

## Phase <ID> - <title>
- [ ] ...

## Acceptance
...

---

# Implementation order

## Execution rules
...

## Why this order
...

## Ordered phases
- [ ] Phase <ID> - <title>

## Terminal acceptance
- [ ] ...
```

Progress stays inside that document: phase task checklists and the matching `Ordered phases` row must agree, and terminal acceptance determines whether an otherwise-finished plan is fully completed.

Create uses a stable semantic `name` made from lowercase letters, digits, and hyphens. Update keeps that name and must include the latest `expected_content_id`; if another session changed the plan first, the stale update fails instead of overwriting newer progress. `dry_run: true` performs the same target/format/conflict checks without mutating state.

`project_context` exposes bounded plan metadata such as name, relative path, content ID, status, phase counts, and next incomplete phase. Pass `plan_name` for deterministic exact selection. Without an exact name, an agent may infer a target only when discovery is complete and exactly one valid non-completed plan exists.

The `/plan` directive is communicated through canonical server instructions. Generic MCP transports do not necessarily receive the original user prompt, so the MCP server itself does not claim to parse or hard-block arbitrary Tool calls based on `/plan`; prompt interpretation belongs to the compatible host agent.

## Upstream aggregation

`CodeMCP` can connect to remote MCP endpoints as Upstreams and expose selected tools through its own catalog.

Start with:

```bash
cm upstream --help
cm upstream server --help
```

Common operations:

```bash
cm upstream server list
cm upstream server show <id>
cm upstream server status <id>
cm upstream server tools <id>
cm upstream server enable <id>
cm upstream server disable <id>
cm upstream server remove <id>
```

### HTTP upstream

```bash
cm upstream server add example \
  --transport http \
  --url https://mcp.example.com/mcp \
  --auth auto \
  --expose all
```

### stdio upstream

```bash
cm upstream server add local-tools \
  --transport stdio \
  --command node \
  --arg /path/to/server.mjs \
  --cwd /path/to/project \
  --expose all
```

Tool exposure can be narrowed with prefixes, allowlists, disabled-tool lists, or exposure modes. Use `cm upstream server add --help` and `configure --help` for the installed version's exact fields.

The former `cm mcp server ...` management path has been removed; use `cm upstream server ...`.

## Upstream OAuth

HTTP upstreams can use managed OAuth:

```bash
cm upstream server auth login <id>
cm upstream server auth status <id>
cm upstream server auth logout <id>
```

Managed access/refresh tokens and client secrets are stored through the selected config root's secret store rather than ordinary structured configuration.

## Outbound network policy

HTTP upstreams are subject to outbound URL and redirect validation. Public non-loopback targets normally require HTTPS; private/link-local/metadata destinations are rejected unless that upstream explicitly opts into private-network access.

Use `--allow-private-network` only for upstreams you intentionally expect to reach on loopback/private networks.

See [Security](security.md#upstream-http-outbound-policy) for the exact boundary.

## Tool catalog changes

The visible tool catalog can change when Upstreams are added, removed, enabled, disabled, or rediscovered, or when local integration/configuration state changes the available tool surface.

Replacement discovery is applied as a complete catalog update rather than intentionally exposing a partially refreshed Upstream.

## Protocol profile

The integrated ChatGPT runtime follows the project's current stateless MCP profile and OpenAI tunnel requirements. Generic `cm mcp stdio` / `cm mcp http` transports provide standards-compatible client lifecycles for ordinary MCP clients.

The current binary is the authoritative source for its supported transport/command surface:

```bash
cm mcp --help
cm mcp stdio --help
cm mcp http --help
```

Protocol-specific implementation details such as the exact revision, method/header validation, and compatibility behavior are intentionally kept out of the normal setup path because most users do not need them to connect or operate the runtime.

### Base and OpenAI profiles

The base profile is generic MCP. Direct Streamable HTTP can opt into OpenAI-compatible projection when needed:

```bash
cm mcp http --profile openai
```

The OpenAI profile changes presentation/compatibility metadata only. Canonical tool schemas, effects, authentication requirements, workspace scope, control guards, and approval authority remain the same. OpenAI Secure MCP Tunnel always uses this profile, so direct HTTP and tunnel transport reach the same CodeMCP tool/runtime authority.

Per-tool authentication metadata is projected from the canonical auth contract, including compatibility metadata used by OpenAI clients. Authentication/linking never substitutes for CodeMCP authorization or local approval. When reauthorization is required, CodeMCP preserves the MCP `mcp/www_authenticate` result metadata expected by compatible clients.

Streamable HTTP is the primary remote transport. Legacy SSE remains an optional compatibility endpoint for older clients and is not an authority source for OpenAI-specific behavior; disable it with `cm mcp http --no-sse` when it is unnecessary.

### Skills and client snapshots

CodeMCP remains the live Skill authority. Clients that support the MCP Skills extension can import the OpenAI-profile projection; clients that do not still use the canonical `list_skills` and `load_skill` tools.

OpenAI-profile import is intentionally bounded: at most five Skills per scan, 100 files per Skill, 256 KiB for `SKILL.md`, 1 MiB per supporting file, 5 MiB per Skill, and 8 MiB aggregate raw resource data. For this projection, the directory containing `SKILL.md` must match the Skill name.

A client import is a snapshot. After changing a Skill, rescan/reconnect the client if its imported copy needs to change.

### Background work and continuation

Process completion and model continuation are separate capabilities. Background processes complete event-driven inside CodeMCP and remain recoverable through the normal process/execution surfaces; agents should not repeatedly poll.

CodeMCP advertises automatic model continuation only when a concrete client adapter has demonstrated that capability. For ordinary base/OpenAI stdio or HTTP clients and Secure MCP Tunnel, do not assume host-side continuation merely because background work completed. Return control after starting background work and use the explicit recovery/completion surfaces when the client does not provide continuation.
