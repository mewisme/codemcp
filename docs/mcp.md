# MCP clients and upstreams

For ChatGPT, the recommended/default transport is **OpenAI Secure MCP Tunnel**. Use this guide when you need a generic MCP client such as Cursor, or when `CodeMCP` should aggregate tools from another MCP server.

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
cm config set auth.mcp_legacy_bearer false
```

The OpenAI Secure MCP Tunnel runtime API key is unrelated to generic MCP client authentication.

See [Configuration](configuration.md#authentication) and [Security](security.md).

## Workspace binding

Without transport-level binding, workspace-scoped tools target explicit registered `ws_*` IDs. With `--workspace`, the generic transport binds the session to that one concrete workspace and removes redundant workspace selection from the client-facing surface where applicable.

Workspace containers (`wsc_*`) remain orchestration groups and are never substituted for a concrete filesystem workspace.

See [Workspaces](workspaces.md) for the canonical workspace model.

## Upstream MCP aggregation

`CodeMCP` can connect to other MCP servers and expose selected upstream tools through its own catalog.

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

`cm mcp server ...` is a deprecated compatibility path; new automation should use `cm upstream server ...`.

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

The visible tool catalog can change when upstream servers are added, removed, enabled, disabled, or rediscovered, or when local feature/configuration state changes the available tool surface.

Replacement discovery is applied as a complete catalog update rather than intentionally exposing a partially refreshed upstream.

## Protocol profile

The integrated ChatGPT runtime follows the project's current stateless MCP profile and OpenAI tunnel requirements. Generic `cm mcp stdio` / `cm mcp http` transports provide standards-compatible client lifecycles for ordinary MCP clients.

The current binary is the authoritative source for its supported transport/command surface:

```bash
cm mcp --help
cm mcp stdio --help
cm mcp http --help
```

Protocol-specific implementation details such as the exact revision, method/header validation, MRTR support, and compatibility behavior are intentionally kept out of the normal setup path because most users do not need them to connect or operate the runtime.
