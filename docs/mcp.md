# MCP profiles and transports

CodeMCP has one canonical runtime authority. A profile only changes the client-facing MCP projection; it does not change tool effects, schemas, authentication requirements, workspace scope, control guards, or approval authority.

## OpenAI profile

The OpenAI profile comes first because it is the normal ChatGPT path.

### Secure MCP Tunnel

Create or manage the tunnel directly in [OpenAI Platform → Tunnels](https://platform.openai.com/settings/organization/tunnels), then configure CodeMCP with that tunnel ID.

```bash
cm tunnel configure --enabled --id tunnel_...
cm tunnel key set
cm up
```

The tunnel uses the OpenAI profile automatically. The runtime initiates outbound traffic, so the local MCP server does not need a public inbound endpoint.

Use **Tunnels Read + Use** for the runtime key. Creating/editing tunnels can require **Tunnels Read + Manage**. OpenAI Platform tunnel permissions and ChatGPT developer-mode workspace access are separate.

Reference: <https://developers.openai.com/api/docs/guides/secure-mcp-tunnels>.

### Direct HTTP with OpenAI projection

```bash
cm mcp http --profile openai
cm mcp http --profile openai --workspace ws_...
```

This changes compatibility/presentation metadata only. CodeMCP remains the authorization authority.

## Base profile

Base is the default generic MCP projection.

### stdio

```bash
cm mcp stdio
cm mcp stdio --workspace ~/projects/my-project
cm mcp stdio --workspace ws_...
```

Workspace binding requires an already registered workspace.

Example:

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

```bash
cm mcp http
cm mcp http --workspace ws_...
cm mcp http --no-sse
```

Base is the default profile. Legacy SSE is optional compatibility.

## Authentication

- stdio uses the local child-process boundary.
- protected HTTP uses CodeMCP MCP authentication.
- the OpenAI tunnel runtime key authenticates tunnel transport to OpenAI; it does not replace CodeMCP authorization.
- authentication/linking never bypasses workspace scope, control guards, or approvals.

## Upstream MCP servers

```bash
cm upstream server list
cm upstream server show <id>
cm upstream server status <id>
cm upstream server tools <id>
cm upstream server enable <id>
cm upstream server disable <id>
cm upstream server remove <id>
```

HTTP upstream:

```bash
cm upstream server add example \
  --transport http \
  --url https://mcp.example.com/mcp \
  --auth auto \
  --expose all
```

Managed OAuth:

```bash
cm upstream server auth login <id>
cm upstream server auth status <id>
cm upstream server auth logout <id>
```

The retired `cm mcp server ...` path is replaced by `cm upstream server ...`.

## Agent Plan Mode

Compatible agents may treat the exact standalone token `/plan` as planning-only intent. `create_plan` is the canonical plan mutation tool; later implementation uses the normal workspace, filesystem, Git, and execution tools.
