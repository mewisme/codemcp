# MCP Server Fields

This page documents every field in the MCP server editor. The editor is split into **General**, **Connection**, **Authentication**, and **Tools** sections. Values belonging to the inactive transport are preserved, so switching HTTP/stdio does not silently destroy the other transport draft.

## General

### Server ID

Only shown when creating a server. This is the stable identifier used by configuration, routes, commands, and references to the upstream server. It is required and must not collide with an existing server ID. Editing an existing server does not expose this field because the ID is the resource identity.

### Display name

Optional human-readable name for the server. It is presentation metadata; the Server ID remains the stable identifier.

### Transport

Selects how `cm` connects to the upstream server.

- **HTTP** uses an MCP HTTP endpoint and exposes the HTTP-specific connection/authentication fields.
- **stdio** launches a local process and communicates over standard input/output.

Inactive transport values remain in the draft so changing transport and changing back does not erase them.

### Enabled

Persistent on/off state for the upstream server. Disabled servers remain configured but are not treated as active upstream connections.

## Connection — HTTP

### HTTP MCP URL

The MCP endpoint used when Transport is HTTP. This should identify the upstream MCP HTTP endpoint that `cm` connects to.

### Non-sensitive headers

Multiline `KEY=VALUE` input, one header assignment per line. Use this only for values safe to keep in ordinary configuration. Secret-bearing headers belong in **Sensitive headers JSON**.

### Sensitive headers JSON

Password-style input for sensitive HTTP header values. The value is not rendered as ordinary visible text. When editing an existing server, leaving this field blank keeps the existing sensitive header data rather than replacing it with an empty object.

### Bearer token environment variable

Name of an environment variable whose value supplies a bearer token for the HTTP connection. Store the secret in the environment/secret workflow rather than writing the token itself into this field.

## Connection — stdio

### Command

Executable/command used to launch the stdio MCP server.

### Arguments

Multiline argument list, one process argument per line. Each line becomes one argv entry; this is not a shell command string.

### Working directory

Directory used as the launched process working directory. The field is path-aware: it can use the directory picker and can fall back to manual input. Missing paths are allowed in the draft because a path may be created later or be meaningful in another runtime environment.

### Non-sensitive environment

Multiline `KEY=VALUE` environment assignments, one per line, for values safe to persist normally.

### Sensitive environment JSON

Password-style input for sensitive environment data. When editing, blank keeps the existing sensitive environment values.

## Authentication

### Auth mode

HTTP authentication strategy.

- **Auto** lets the MCP/OAuth client choose the appropriate behavior from server metadata and configured credentials.
- **OAuth** explicitly selects OAuth behavior.
- **None** disables MCP OAuth/auth handling for the server.

For new HTTP servers the default is Auto. For stdio servers the default is None. Authentication values are preserved when stdio is selected even though they are inactive there.

### OAuth scope

Scope string requested for OAuth authorization when the server/auth flow uses OAuth. Leave empty when the server defaults are sufficient.

## Tools

### Tool prefix

Optional prefix applied to tools exposed from this upstream. Use it to avoid collisions or make the upstream origin obvious in aggregated tool names.

### Expose

Controls which upstream tools are exposed.

- **All** exposes all eligible tools except explicitly disabled tools.
- **Allowlist** exposes only tools listed in **Allowlisted tools**.
- **Metadata only** keeps server/tool metadata available without exposing normal tool execution.
- **None** exposes no tools from the upstream.

### Allowlisted tools

Multiline tool-name list, one tool per line. It is used when Expose is **Allowlist**.

### Disabled tools

Multiline deny list, one tool per line. These tools remain disabled even when the broader exposure mode would otherwise include them.

### Idle timeout (seconds)

Positive integer controlling the upstream idle timeout. New forms default to `600` seconds when the server has no positive stored timeout. Zero/negative/non-numeric values are rejected by the editor validation.
