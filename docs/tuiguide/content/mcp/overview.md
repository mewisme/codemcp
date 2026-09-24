# Upstreams

The Upstreams area manages remote Model Context Protocol endpoints that `CodeMCP` can expose or proxy.

## Server list and details

Each row shows the server name/ID, transport, endpoint/command, and enabled state. A server detail page provides child views for Health, Tools, and OAuth when applicable. Sensitive header values are redacted before rendering.

## Create: Form or JSON

**Add server** opens a routed editor with `Form / JSON` modes.

Form mode is split into `General / Connection / Authentication / Tools`. It supports HTTP and stdio configuration, enabled state, expose policy, allow/disable tool lists, environment values, headers, working directory, OAuth settings, and related transport fields.

JSON mode accepts the canonical MCP configuration shape:

```json
{
  "mcpServers": {
    "docs": {
      "url": "https://example.test/mcp"
    },
    "local": {
      "command": "node",
      "args": ["server.js"]
    }
  }
}
```

JSON mode can create multiple servers atomically. The entire batch is validated before persistence; duplicate/existing IDs or an invalid server prevent partial creation. Form ↔ JSON synchronization is automatic when there is exactly one server and the target draft has not independently changed. If both drafts diverge, the TUI preserves both instead of silently overwriting one.

## Transports

An HTTP server uses a URL and may use authentication headers, bearer environment variables, or OAuth. A stdio server uses a command, arguments, environment variables, and optional working directory. Working directory uses the shared path field with picker/manual entry.

Changing transport does not intentionally erase inactive transport draft values, so switching while configuring a server is reversible before save.

## Health and tools

Health refresh connects to the server and records connection/tool status. The Tools child page loads the upstream tool catalog and shows which proxy names are exposed after prefix/expose policy is applied.

Long tool refreshes are cancellable. The page retains clear cancellation state rather than allowing a late result to overwrite a newer operation.

## OAuth

HTTP servers with OAuth-capable auth can open the OAuth Login editor. It accepts issuer/settings and a switch controlling whether the authorization URL should be opened in a browser. The authorization URL is surfaced while login is in progress; browser-open failure does not necessarily abort a login that can continue manually.

Credentials are stored through the managed OAuth store and tokens are never rendered in the TUI. Logout removes managed OAuth credentials while preserving the Upstream configuration.
